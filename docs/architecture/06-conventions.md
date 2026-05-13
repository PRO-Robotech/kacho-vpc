# 06 — Conventions & Gotchas

VPC-specific правила, error mapping, top-10 уроков из истории фиксов.
Workspace-уровень — в `kacho-workspace/docs/architecture/07-conventions.md`.

## Validation layering

**Sync** (до создания Operation):
- Required: `folder_id`, `network_id` (для дочерних), `name` (где обязательно), `zone_id`.
- Format:
  - `corevalidate.NameVPC` — permissive (`^([a-zA-Z]([-_a-zA-Z0-9]{0,61}[a-zA-Z0-9])?)?$`, разрешает empty/uppercase/underscore).
  - `Description` ≤ 256.
  - `Labels` ≤ 64 пар, key regex.
  - `ZoneId` — required-only в `kacho-corelib/validate` (hardcoded whitelist убран).
    Existence-проверка `zone_id` — sync, в `SubnetService.validateZoneID` через
    порт `ZoneRegistry` (запрос к таблице `zones`); неизвестная зона → `InvalidArgument`.
- CIDR: `validateCIDRPrefix` — host-bits=0 (`netip.Prefix.Masked() == prefix`).
- DhcpOptions: `domain_name` RFC 1123, `domain_name_servers[]`/`ntp_servers[]` IP.
- UpdateMask: known-set + immutable check.
- DeletionProtection.
- Address spec: oneof external/internal — exactly one.

**Async** (внутри Operation worker):
- Folder existence через `folderClient.Exists` → `NotFound`.
- Network/Subnet existence для дочерних → `NotFound`.
- Repo Insert/Update — FK violations, EXCLUDE constraint (CIDR overlap),
  UNIQUE violation (name within folder, IP collision).
- Все маппятся через `mapRepoErr` в gRPC-status.

## Error mapping (sentinel → grpc)

`internal/service/network.go::mapRepoErr` — единая точка трансляции:

| Sentinel | gRPC code | Verbatim YC text source |
|---|---|---|
| `ErrNotFound` | `NOT_FOUND` | `"<Resource> {X} not found"` |
| `ErrAlreadyExists` | `ALREADY_EXISTS` | `"<resource> with name ... exists"` |
| `ErrFailedPrecondition` | `FAILED_PRECONDITION` | varies |
| `ErrInvalidArg` | `INVALID_ARGUMENT` | varies |
| `ErrInternal` | `INTERNAL` | `"internal database error"` (no leak) |

Specific:
- CIDR overlap (PG `23P01` от EXCLUDE) → `FailedPrecondition` `"Subnet CIDRs can not overlap"`.
- Malformed / нераспознанный resource-id (нет известного 3-char prefix `b1g/bpf/enp/e9b/epd/fd8`) → sync `InvalidArgument "invalid <res> id '<X>'"` (`corevalidate.ResourceID`, вызывается первым стейтментом в каждом id-берущем RPC; verbatim-YC, probe 2026-05-11). Well-formed-но-несуществующий id (известный prefix) → `NotFound` через `repo.Get`. Семантика family-agnostic (`enp...` как subnet-id проходит prefix-check → потом `repo.Get` → `NotFound`, как у реального YC). (`kacho-vpc#7` — закрыт; старый gotcha `ac61127` «не валидировать id sync» устарел и заменён.)
- Duplicate name (UNIQUE `23505`) → `ALREADY_EXISTS`.
- `addresses_external_pool_ip_uniq` violation → должна быть `RetryableInternal`, allocator её ловит и пытается заново.
- Dependency-chain `FailedPrecondition` (sync-prechecks, KAC-31/KAC-33/KAC-34): `Address.Delete` used-адреса → `"address ... is in use by network interface ...; detach it before deleting the address"`; `Subnet.Delete` с внутренними адресами (v4/v6) → `"Subnet has allocated internal addresses"`, с NIC'ами → `"subnet ... has N network interface(s) (...); delete them first"`; `Network.Delete` непустой → `"Network ... is not empty"`; CIDR-less подсеть при internal-v4-allocate → `"subnet ... has no IPv4 CIDR"`.

## Hard delete

С Phase 1.0 — `DELETE FROM <table> WHERE id = $1`. Никаких `deletion_timestamp` для tombstones.

## Flat schemas (без K8s envelope)

Все VPC-таблицы — flat: только domain-specific колонки + id/folder_id/name/description/labels/created_at. **Нет** `resource_version`, `generation`, `deletion_timestamp`, `finalizers`, `spec`, `status` (как jsonb).

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
| Network | `enp` | `ids.PrefixNetwork` |
| Subnet | `e9b` | `ids.PrefixSubnet` |
| Address | `e9b` | `ids.PrefixAddress` |
| RouteTable | `enp` | `ids.PrefixRouteTable` |
| SecurityGroup | `enp` | `ids.PrefixSecurityGroup` |
| Gateway | `enp` | `ids.PrefixGateway` |
| PrivateEndpoint | `enp` | `ids.PrefixPrivateEndpoint` |
| NetworkInterface | `e9b` | **переиспользует `ids.PrefixSubnet`** — отдельного `PrefixNetworkInterface` нет (см. `network_interface.go::niResourceID` / `Create`) |
| AddressPool | `apl` | hardcoded в `address_pool_service.go` |
| Operation (VPC) | `enp` | `ids.PrefixOperationVPC == ids.PrefixNetwork` |

3-char prefix + 17-char base32. Network/RouteTable/SecurityGroup/Gateway/PrivateEndpoint
**делят `enp`** — это умышленно: api-gateway маршрутизирует `OperationService.Get(id)`
по первым 3 символам id, и все VPC-операции должны идти в один backend (поэтому
`PrefixOperationVPC == PrefixNetwork`). Subnet/Address/**NetworkInterface** используют `e9b`
(NIC переиспользует `PrefixSubnet`). Все ID — `TEXT`
(в squashed baseline; исторически переход от UUID).

## Subnet immutable fields & optional CIDR

`network_id`, `zone_id` — **hard-immutable** в UpdateMask → `InvalidArgument "<field> is immutable after Subnet.Create"`.
`v4_cidr_blocks`, `v6_cidr_blocks` — **soft-immutable**: в UpdateMask — не ошибка (no-op зеркало), в full-PATCH — silent ignore;
`UpdateSubnet` теперь принимает и `v6_cidr_blocks` (тоже no-op). Реальное изменение — verbs `:add/:remove-cidr-blocks`
(обе семьи: v6 — валидный IPv6-префикс, host-bits=0, intra-request disjoint, cross-subnet overlap → `FailedPrecondition`,
backstop — EXCLUDE `subnets_no_overlap_v6`).

`v4_cidr_blocks` / `v6_cidr_blocks` **необязательны на Create** (proto-`(required)` снят; миграция не нужна — `text[] DEFAULT '{}'`).
CIDR-less подсеть легальна; `Address.Create` с `internal_ipv4_address_spec` в неё / `AllocateInternalIP` →
`FailedPrecondition "subnet ... has no IPv4 CIDR"` — добавьте CIDR через `:add-cidr-blocks`.

## NetworkInterface ↔ Address referrer-convention (KAC-2 / KAC-31)

NIC ссылается на `Address`-ресурсы **по id** (`v4_address_ids[]`/`v6_address_ids[]`); один `Address`
может быть привязан **максимум к одному NIC** — enforced сервис-слоем через `addresses.used` + referrer-rows
в `address_references` (`referrer_type="network_interface"`, как `compute_instance`). `Address.Delete` для
`used`-адреса → `FailedPrecondition "address ... is in use by network interface ...; detach it before deleting the address"`.
NIC `used_by` (кто приаттачил NIC) — зеркало `Address.used_by`: `AttachToInstance` ставит, `DetachFromInstance` чистит
(flat-колонки `used_by_type`/`used_by_id`/`used_by_name`). Дерево удаления — снизу вверх: NIC → Address → Subnet → Network,
все FK RESTRICT (`network_interfaces_subnet_id_fkey` — миграция 0012 откатила KAC-31's CASCADE из 0011).

## ListOperations переживает удаление ресурса (Network/Subnet/Address/NetworkInterface)

`ListOperations` для этих четырёх ресурсов **не требует существования ресурса** — precondition `repo.Get`
убран из сервиса и из хэндлера. Handler best-effort: жив → проверка folder-ownership; `NotFound` → пропуск,
отдаём накопленные операции; прочие ошибки пробрасываются. `operations`-строки без FK-каскада — история сохраняется.
(route_table/SG/gateway/private_endpoint `ListOperations` по-прежнему гейтит на `repo.Get` — это существующее поведение.)

## Default Security Group (inline, опционально)

Управляется флагом `KACHO_VPC_DEFAULT_SG_INLINE` (default `true`).

При `true` — Network.Create:
1. SYNC создаётся Operation, возвращается клиенту.
2. ASYNC в worker:
   - аллокация `vpn_id` (head `vpn_id_free`, иначе `nextval(vpn_id_seq)`) — internal-only, 24-bit (миграция 0005).
   - `repo.Insert(network)` (включая `vpn_id`).
   - **Inline создаётся SG** `default-sg-{first-8-chars-of-net-id}` с правилами по умолчанию.
   - `UPDATE networks SET default_security_group_id = sg.id`.
3. Outbox emit для всех трёх событий (Network.CREATED, SecurityGroup.CREATED, Network.UPDATED).

При `false` — Network.Create НЕ создаёт SG (`SetSGRepo` не вызывается в `cmd/vpc/main.go`),
`default_security_group_id` остаётся пустым; создание делегируется внешнему reconciler'у.
Убирает 2 INSERT + 1 UPDATE из hot-path (≈ +30-40% write-throughput) — для load-тестов.
В таком режиме newman-кейсы `*-LSG-CRUD-DEFAULT-SG` / `*-DEL-STATE-DEFAULT-SG` краснеют.

При Network.Delete worker сначала удаляет default SG (если есть), возвращает `vpn_id` в `vpn_id_free`, потом Network. Не-default SG / subnets / route tables препятствуют удалению (FK RESTRICT + sync-precheck) → клиент получает `FailedPrecondition "Network ... is not empty"`.

## Admin boundary

⚠️ **Внутренние служебные сущности не публиковать наружу** (workspace `CLAUDE.md` запрет 6, kacho-vpc `CLAUDE.md` §16.x):

- `Internal*Service`'ы могут быть зарегистрированы через api-gateway REST mux на cluster-internal listener — для UI/admin-tooling.
- На external TLS endpoint (`api.kacho.local:443`, advertised для `yc` CLI) эти paths **не должны** быть доступны.
- Список admin / data-plane paths (для будущего TLS-middleware фильтра):
  - `/vpc/v1/addressPools*`
  - `/vpc/v1/networks/*/addressPoolBinding`
  - `/vpc/v1/addresses/*/addressPoolOverride`
  - `/vpc/v1/clouds/*/poolSelector`
  - `/vpc/v1/networks/*/internal`  — `InternalNetworkService.GetNetwork` (содержит `vpn_id`)
  - `/vpc/v1/networkInterfaces/*/internal`  — `InternalNetworkInterfaceService.GetNetworkInterface` (data-plane-инфа)
  - `/vpc/v1/networkInterfaces/*` `ListByHypervisor` / `ReportNiDataplane` (write-back от kacho-vpc-implement)
  - (`/vpc/v1/regions*`, `/vpc/v1/zones*` — переехали в kacho-compute, в kacho-vpc их больше нет)

При добавлении нового admin-RPC обновлять этот список.

**Правило для новых admin-RPC**: добавлять **только** в `Internal*` сервис на `:9091`, регистрировать через `vpcInternalAddr` блок в `kacho-api-gateway/internal/restmux/mux.go`. **НЕ** расширять публичные сервисы для admin-нужд — это сломает verbatim-YC parity и засветит admin-функции на TLS endpoint.

## Top-10 gotchas (из истории фиксов)

1. **id sync-валидация** — malformed / нераспознанный resource-id (нет известного 3-char prefix `b1g/bpf/enp/e9b/epd/fd8`) → sync `InvalidArgument "invalid <res> id '<X>'"` (`corevalidate.ResourceID`, первым стейтментом в каждом id-берущем RPC; verbatim-YC, probe 2026-05-11). Well-formed-но-несуществующий id (известный prefix) → `NotFound` через `repo.Get`. Семантика family-agnostic — `enp...`, переданный как subnet-id, проходит prefix-check, затем `repo.Get` → `NotFound` (как у реального YC). (`kacho-vpc#7` — закрыт; старый gotcha «не валидировать id sync», `ac61127`, устарел и заменён.)
2. **NameVPC permissive, не strict** — empty/uppercase/underscore разрешены для Network/Subnet/Address/RouteTable/SG. Gateway — strict (`corevalidate.NameGateway`: lowercase, без uppercase/underscore — verbatim YC).
3. **CIDR overlap** = `FailedPrecondition`, не `InvalidArgument` (`e015191`).
4. **CIDR host-bits=0** обязательно, sync через `netip.Prefix.Masked()`.
5. **Subnet immutable**: `v4_cidr_blocks/v6_cidr_blocks/network_id/zone_id` — reject в mask, silent ignore в full-PATCH (`8158a84`).
6. **Hard-delete, не soft** (`4e3e7ec`).
7. **Default SG создаётся inline в NetworkService.doCreate** при `KACHO_VPC_DEFAULT_SG_INLINE=true` (default). Раньше был reconciler в `kacho-vpc-controllers` — упразднён в Phase 2; флаг `=false` возвращает «без inline-SG» поведение для load-тестов / внешнего reconciler'а.
8. **Timestamp truncate to seconds** в proto-ответе (`ac61127`, `YC-DIFF-TIMESTAMP-PRECISION`).
9. **DeletionProtection sync-check** перед Delete — `FailedPrecondition` `"... deletion_protection enabled"` (`333c535`).
10. **page_size валидируется**, garbage page_token → `InvalidArgument` (`5d16961`, `8de9366`).

## IPAM-specific gotchas

11. **`isUniqueViolation` распознаёт обе формы**: raw pgErr substring + обёртку `service.ErrAlreadyExists` через `errors.Is`. Без второй ветки allocator после `wrapPgErr` в `SetIPSpec` вылетал из retry-loop с raw "already exists" вместо `ResourceExhausted`.
12. **AddressPool.zone_id NULL = глобальный fallback**, не "ошибка". Cascade Step 5 ищет `WHERE zone_id IS NULL`.
13. **Cloud-selector inverse-containment**: `cloud_selector ⊆ pool.selector_labels` (pool — whitelist). Safe-by-default — лишний label у клиента уводит его в default-pool, не в премиум.
14. **При equal-equal в cascade resolve order undefined** — Postgres вернёт первую row. Используй `ipam check` для обнаружения ambiguous.
15. **CloudPoolSelector хранится в pg-vpc**, а Cloud — в pg-resource-manager. Кросс-DB FK нет; валидация только на момент `Set` (через FolderClient).

## Что нельзя делать

- НЕ менять public proto без обновления verbatim-YC parity registry (`PARITY.md`).
- НЕ редактировать применённые миграции — только новые.
- НЕ добавлять admin-нужное в публичный сервис — только в `Internal*`.
- НЕ возвращать ресурс синхронно из мутирующих RPC — все мутации через Operation.
- НЕ делать каскадное удаление через границу сервиса — только same-DB FK.
- НЕ использовать ORM (gorm/ent/bun) — только pgx + handwritten SQL.

## Ссылки в репо

- `../../CLAUDE.md` — operational правила, project-level subagents.
- GitHub Issues (`github.com/PRO-Robotech/kacho-vpc/issues`) — долги, баги, задачи, tech-debt
  (verbatim-YC: Delete RPC возвращают `google.protobuf.Empty` — сделано; см. `04-api-surface.md`).
- [07-known-divergences.md](07-known-divergences.md) — registry by-design расхождений с verbatim YC.
- `../../tests/newman/docs/TAXONOMY.md` — class taxonomy для regression-кейсов.
