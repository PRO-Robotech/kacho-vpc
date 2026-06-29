# 06 — Conventions & Gotchas

VPC-specific правила, error mapping, уроки из истории фиксов.
Общие конвенции API Kachō — `01-resources.md` и `04-api-surface.md`.

## Validation layering

**Sync** (до создания Operation):
- Required: `project_id`, `network_id` (для дочерних), `name` (где обязательно), `zone_id`.
- Format:
  - `corevalidate.NameVPC` — permissive (`^([a-zA-Z]([-_a-zA-Z0-9]{0,61}[a-zA-Z0-9])?)?$`, разрешает empty/uppercase/underscore).
  - `Description` ≤ 256.
  - `Labels` ≤ 64 пар, key regex.
  - `ZoneId` — required-only в `kacho-corelib/validate`.
    Existence-проверка `zone_id` — sync, в `SubnetService.validateZoneID` через
    порт `ZoneRegistry` (вызов `geo.v1.ZoneService.Get` в `kacho-geo`); неизвестная зона → `InvalidArgument`.
- CIDR: `validateCIDRPrefix` — host-bits=0 (`netip.Prefix.Masked == prefix`).
- DhcpOptions: `domain_name` RFC 1123, `domain_name_servers[]`/`ntp_servers[]` IP.
- UpdateMask: known-set + immutable check.
- DeletionProtection.
- Address spec: oneof external/internal — exactly one.

**Async** (внутри Operation worker):
- Project existence через `projectClient.Exists` → `NotFound`.
- Network/Subnet existence для дочерних → `NotFound`.
- Repo Insert/Update — FK violations, EXCLUDE constraint (CIDR overlap),
  UNIQUE violation (name within project, IP collision).
- Все маппятся через `mapRepoErr` в gRPC-status.

## Error mapping (sentinel → grpc)

`mapRepoErr` — трансляция sentinel → gRPC-status; теперь **per-resource** (своя копия в каждом `internal/apps/kacho/api/<resource>/helpers.go`, 8 копий; для не-ресурсных сервисов — общий `internal/apps/kacho/shared/serviceerr.MapRepoErr`):

| Sentinel | gRPC code | Текст сообщения |
|---|---|---|
| `ErrNotFound` | `NOT_FOUND` | `"<Resource> {X} not found"` |
| `ErrAlreadyExists` | `ALREADY_EXISTS` | `"<resource> with name ... exists"` |
| `ErrFailedPrecondition` | `FAILED_PRECONDITION` | varies |
| `ErrInvalidArg` | `INVALID_ARGUMENT` | varies |
| `ErrInternal` | `INTERNAL` | `"internal database error"` (no leak) |

Specific:
- CIDR overlap (PG `23P01` от EXCLUDE) → `FailedPrecondition` `"Subnet CIDRs can not overlap"`.
- Malformed / нераспознанный resource-id (нет известного 3-char prefix `net/sub/adr/rtb/sgr/gtw/nic/apl/enp`) → sync `InvalidArgument "invalid <res> id '<X>'"` (`corevalidate.ResourceID`, вызывается первым стейтментом в каждом id-берущем RPC). Well-formed-но-несуществующий id (известный prefix) → `NotFound` через `repo.Get`. Семантика family-agnostic: `enp...`, переданный как Operation-id, проходит prefix-check → затем `repo.Get` → `NotFound`.
- Duplicate name (UNIQUE `23505`) → `ALREADY_EXISTS`.
- `addresses_external_pool_ip_uniq` violation → должна быть `RetryableInternal`, allocator ее ловит и пытается заново.
- Dependency-chain `FailedPrecondition` (sync-prechecks): `Address.Delete` used-адреса → `"address ... is in use by network interface ...; detach it before deleting the address"`; `Subnet.Delete` с внутренними адресами (v4/v6) → `"Subnet has allocated internal addresses"`, с NIC'ами → `"subnet ... has N network interface(s) (...); delete them first"`; `Network.Delete` непустой → `"Network ... is not empty"`; CIDR-less подсеть при internal-v4-allocate → `"subnet ... has no IPv4 CIDR"`.

## Hard delete

`DELETE FROM <table> WHERE id = $1`. Никаких `deletion_timestamp` для tombstones.

## Flat schemas (без K8s envelope)

Все VPC-таблицы — flat: только domain-specific колонки + id/project_id/name/description/labels/created_at. **Нет** `resource_version`, `generation`, `deletion_timestamp`, `finalizers`, `spec`, `status` (как jsonb).

## Optimistic concurrency

Без отдельной колонки. Используем Postgres `xmin::text`:

```sql
SELECT field, xmin::text FROM t WHERE id = $1;
UPDATE t SET field = $2 WHERE id = $1 AND xmin::text = $3 RETURNING ...;
```

Zero-overhead, миграция не нужна.

## ID format

| Resource | Prefix | Where |
|---|---|---|
| Network | `net` | `ids.PrefixNetwork` |
| Subnet | `sub` | `ids.PrefixSubnet` |
| Address | `adr` | `ids.PrefixAddress` |
| RouteTable | `rtb` | `ids.PrefixRouteTable` |
| SecurityGroup | `sgr` | `ids.PrefixSecurityGroup` |
| Gateway | `gtw` | `ids.PrefixGateway` |
| NetworkInterface | `nic` | `ids.PrefixNetworkInterface` |
| AddressPool | `apl` | `ids.PrefixAddressPool` |
| Operation (VPC) | `enp` | `ids.PrefixOperationVPC` |

3-char prefix + 17-char crockford-base32; тип ресурса читается по prefix.
Operation несет **отдельный** prefix `enp` (`ids.PrefixOperationVPC`): api-gateway
маршрутизирует `OperationService.Get(id)` по первым 3 символам, и все VPC-операции
должны идти в один backend. Все ID — `TEXT`.

## Subnet immutable fields & optional CIDR

`network_id`, `zone_id` — **hard-immutable** в UpdateMask → `InvalidArgument "<field> is immutable after Subnet.Create"`.
`v4_cidr_blocks`, `v6_cidr_blocks` — **soft-immutable**: в UpdateMask — не ошибка (no-op зеркало), в full-PATCH — silent ignore;
`UpdateSubnet` теперь принимает и `v6_cidr_blocks` (тоже no-op). Реальное изменение — verbs `:add/:remove-cidr-blocks`
(обе семьи: v6 — валидный IPv6-префикс, host-bits=0, intra-request disjoint, cross-subnet overlap → `FailedPrecondition`,
backstop — EXCLUDE `subnets_no_overlap_v6`).

`v4_cidr_blocks` / `v6_cidr_blocks` **необязательны на Create** (proto-`(required)` снят; миграция не нужна — `text[] DEFAULT '{}'`).
CIDR-less подсеть легальна; `Address.Create` с `internal_ipv4_address_spec` в нее / `AllocateInternalIP` →
`FailedPrecondition "subnet ... has no IPv4 CIDR"` — добавьте CIDR через `:add-cidr-blocks`.

## NetworkInterface ↔ Address referrer-convention

NIC ссылается на `Address`-ресурсы **по id** (`v4_address_ids[]`/`v6_address_ids[]`); один `Address`
может быть привязан **максимум к одному NIC** — enforced сервис-слоем через `addresses.used` + referrer-rows
в `address_references` (`referrer_type="network_interface"`, как `compute_instance`). `Address.Delete` для
`used`-адреса → `FailedPrecondition "address ... is in use by network interface ...; detach it before deleting the address"`.
NIC `used_by` (кто использует NIC) — денормализованное зеркало `Address.used_by`; владелец ставится
атомарным CAS на одной строке (flat-колонки `used_by_type`/`used_by_id`/`used_by_name`). Дерево удаления —
снизу вверх: NIC → Address → Subnet → Network,
все FK RESTRICT (`network_interfaces_subnet_id_fkey`).

## ListOperations переживает удаление ресурса (Network/Subnet/Address/NetworkInterface)

`ListOperations` для этих четырех ресурсов **не требует существования ресурса** — precondition `repo.Get`
убран из сервиса и из хэндлера. Handler best-effort: жив → проверка project-ownership; `NotFound` → пропуск,
отдаем накопленные операции; прочие ошибки пробрасываются. `operations`-строки без FK-каскада — история сохраняется.
(route_table/SG/gateway `ListOperations` по-прежнему гейтит на `repo.Get` — это существующее поведение.)

## Default Security Group (inline, опционально)

Управляется флагом `KACHO_VPC_DEFAULT_SG_INLINE` (default `true`).

При `true` — Network.Create:
1. SYNC создается Operation, возвращается клиенту.
2. ASYNC в worker:
   - `repo.Insert(network)`.
   - **Inline создается SG** `default-sg-{first-8-chars-of-net-id}` с правилами по умолчанию.
   - `UPDATE networks SET default_security_group_id = sg.id`.
3. Outbox emit для всех трех событий (Network.CREATED, SecurityGroup.CREATED, Network.UPDATED).

При `false` — Network.Create НЕ создает SG (`SetSGRepo` не вызывается в `cmd/vpc/main.go`),
`default_security_group_id` остается пустым; создание делегируется внешнему reconciler'у.
Убирает 2 INSERT + 1 UPDATE из hot-path (≈ +30-40% write-throughput) — для load-тестов.
В таком режиме newman-кейсы `*-LSG-CRUD-DEFAULT-SG` / `*-DEL-STATE-DEFAULT-SG` ожидаемо падают.

При Network.Delete worker сначала удаляет default SG (если есть), потом Network. Не-default SG / subnets / route tables препятствуют удалению (FK RESTRICT + sync-precheck) → клиент получает `FailedPrecondition "Network ... is not empty"`.

## Admin boundary

⚠️ **Внутренние служебные сущности не публиковать наружу:**

- `Internal*Service`'ы могут быть зарегистрированы через api-gateway REST mux на cluster-internal listener — для UI/admin-tooling.
- На external TLS endpoint (`api.kacho.local:443`, advertised для внешних клиентов) эти paths **не должны** быть доступны.
- Список admin paths (для TLS-middleware фильтра):
  - `/vpc/v1/addressPools*`
  - `/vpc/v1/networks/*/addressPoolBinding`
  - (Region/Zone — домен `kacho-geo`, в kacho-vpc их нет)

При добавлении нового admin-RPC обновлять этот список.

**Правило для новых admin-RPC**: добавлять **только** в `Internal*` сервис на `:9091`, регистрировать через `vpcInternalAddr` блок в `kacho-api-gateway/internal/restmux/mux.go`. **НЕ** расширять публичные сервисы для admin-нужд — это засветит admin-функции на TLS endpoint.

## Gotchas (из истории фиксов)

1. **id sync-валидация** — malformed / нераспознанный resource-id (нет известного 3-char prefix `net/sub/adr/rtb/sgr/gtw/nic/apl/enp`) → sync `InvalidArgument "invalid <res> id '<X>'"` (`corevalidate.ResourceID`, первым стейтментом в каждом id-берущем RPC). Well-formed-но-несуществующий id (известный prefix) → `NotFound` через `repo.Get`. Семантика family-agnostic — `enp...`, переданный как subnet-id, проходит prefix-check, затем `repo.Get` → `NotFound`.
2. **NameVPC permissive, не strict** — empty/uppercase/underscore разрешены для Network/Subnet/Address/RouteTable/SG. Gateway — strict (`corevalidate.NameGateway`: lowercase, без uppercase/underscore).
3. **CIDR overlap** = `FailedPrecondition`, не `InvalidArgument`.
4. **CIDR host-bits=0** обязательно, sync через `netip.Prefix.Masked`.
5. **Subnet immutable**: `v4_cidr_blocks/v6_cidr_blocks/network_id/zone_id` — reject в mask, silent ignore в full-PATCH.
6. **Hard-delete, не soft**.
7. **Default SG создается inline в NetworkService.doCreate** при `KACHO_VPC_DEFAULT_SG_INLINE=true` (default). Флаг `=false` отключает inline-SG (для load-тестов / внешнего reconciler'а).
8. **Timestamp truncate to seconds** в proto-ответе (БД хранит микросекунды).
9. **DeletionProtection sync-check** перед Delete — `FailedPrecondition` `"... deletion_protection enabled"`.
10. **page_size валидируется**, garbage page_token → `InvalidArgument`.

## IPAM-specific gotchas

11. **`isUniqueViolation` распознает обе формы**: raw pgErr substring + обертку `service.ErrAlreadyExists` через `errors.Is`. Без второй ветки allocator после `wrapPgErr` в `SetIPSpec` вылетал из retry-loop с raw "already exists" вместо `ResourceExhausted`.
12. **AddressPool.zone_id NULL = глобальный fallback**, не "ошибка". Cascade Step 3 (global-default) ищет `WHERE zone_id IS NULL`.
13. **Cascade family-aware**: на каждом шаге pool пропускается, если его CIDR-список для запрошенного family пуст (`poolHasFamily`). Cascade — 3 шага: network-default → zone-default → global-default.

## Что нельзя делать

- НЕ редактировать примененные миграции — только новые.
- НЕ добавлять admin-нужное в публичный сервис — только в `Internal*`.
- НЕ возвращать ресурс синхронно из мутирующих RPC — все мутации через Operation.
- НЕ делать каскадное удаление через границу сервиса — только same-DB FK.
- НЕ использовать ORM (gorm/ent/bun) — только pgx + handwritten SQL.

## Ссылки в репо

- GitHub Issues (`github.com/PRO-Robotech/kacho-vpc/issues`) — долги, баги, задачи, tech-debt.
- [07-known-divergences.md](07-known-divergences.md) — осознанные дизайн-решения.
- `tests/newman/docs/TAXONOMY.md` — class taxonomy для regression-кейсов.
