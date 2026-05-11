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
- Garbage UUID format в id → **NE** sync InvalidArgument; async через `repo.Get` → `NotFound`.
- Duplicate name (UNIQUE `23505`) → `ALREADY_EXISTS`.
- `addresses_external_pool_ip_uniq` violation → должна быть `RetryableInternal`, allocator её ловит и пытается заново.

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
| AddressPool | `apl` | hardcoded в `address_pool_service.go` |
| Operation (VPC) | `enp` | `ids.PrefixOperationVPC == ids.PrefixNetwork` |

3-char prefix + 17-char base32. Network/RouteTable/SecurityGroup/Gateway/PrivateEndpoint
**делят `enp`** — это умышленно: api-gateway маршрутизирует `OperationService.Get(id)`
по первым 3 символам id, и все VPC-операции должны идти в один backend (поэтому
`PrefixOperationVPC == PrefixNetwork`). Subnet/Address используют `e9b`. Все ID — `TEXT`
(в squashed baseline; исторически переход от UUID — миграция 0009).

## Subnet immutable fields

`v4_cidr_blocks`, `v6_cidr_blocks`, `network_id`, `zone_id`:
- В UpdateMask → `InvalidArgument "<field> is immutable after Subnet.Create"`.
- В full-PATCH (mask пустой) → **silent ignore** (verbatim YC).

## Default Security Group (inline, опционально)

Управляется флагом `KACHO_VPC_DEFAULT_SG_INLINE` (default `true`).

При `true` — Network.Create:
1. SYNC создаётся Operation, возвращается клиенту.
2. ASYNC в worker:
   - `repo.Insert(network)`.
   - **Inline создаётся SG** `default-sg-{first-8-chars-of-net-id}` с правилами по умолчанию.
   - `UPDATE networks SET default_security_group_id = sg.id`.
3. Outbox emit для всех трёх событий (Network.CREATED, SecurityGroup.CREATED, Network.UPDATED).

При `false` — Network.Create НЕ создаёт SG (`SetSGRepo` не вызывается в `cmd/vpc/main.go`),
`default_security_group_id` остаётся пустым; создание делегируется внешнему reconciler'у.
Убирает 2 INSERT + 1 UPDATE из hot-path (≈ +30-40% write-throughput) — для load-тестов.
В таком режиме newman-кейсы `*-LSG-CRUD-DEFAULT-SG` / `*-DEL-STATE-DEFAULT-SG` краснеют.

При Network.Delete worker сначала удаляет default SG (если есть), потом Network. Не-default SG препятствуют удалению (FK RESTRICT) → клиент получает `FailedPrecondition "network is not empty"`.

## Admin boundary

⚠️ **Внутренние служебные сущности не публиковать наружу** (workspace `CLAUDE.md` запрет 6, kacho-vpc `CLAUDE.md` §16.x):

- `Internal*Service`'ы могут быть зарегистрированы через api-gateway REST mux на cluster-internal listener — для UI/admin-tooling.
- На external TLS endpoint (`api.kacho.local:443`, advertised для `yc` CLI) эти paths **не должны** быть доступны.
- Список admin paths (для будущего TLS-middleware фильтра):
  - `/vpc/v1/regions*`
  - `/vpc/v1/zones*`
  - `/vpc/v1/addressPools*`
  - `/vpc/v1/networks/*/addressPoolBinding`
  - `/vpc/v1/addresses/*/addressPoolOverride`
  - `/vpc/v1/clouds/*/poolSelector`

При добавлении нового admin-RPC обновлять этот список.

**Правило для новых admin-RPC**: добавлять **только** в `Internal*` сервис на `:9091`, регистрировать через `vpcInternalAddr` блок в `kacho-api-gateway/internal/restmux/mux.go`. **НЕ** расширять публичные сервисы для admin-нужд — это сломает verbatim-YC parity и засветит admin-функции на TLS endpoint.

## Top-10 gotchas (из истории фиксов)

1. **Не валидировать UUID/id sync** — garbage id даёт **async** NotFound, не sync InvalidArgument (verbatim YC, `ac61127`).
2. **NameVPC permissive, не strict** — empty/uppercase/underscore разрешены для Network/Subnet/Address/RouteTable/SG. Gateway — strict (TODO #6).
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
- `../../TODO.md` — долги (например, Delete RPC возвращают неправильный response).
- `../../tests/newman/docs/BUG-MAP.md` — registry verbatim-YC расхождений.
- `../../tests/newman/docs/TAXONOMY.md` — class taxonomy для regression-кейсов.
