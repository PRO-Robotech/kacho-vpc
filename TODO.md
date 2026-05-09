# TODO — outstanding tech-debt

Список после Phase 2 закрытия 14 пунктов исходного TODO. Verified Newman parity:
**283 assertions / 0 failures** (snap4-local-* в `newman/out/`).

## Status table

| #  | Title                                            | Severity | Status     |
|----|--------------------------------------------------|----------|------------|
| 3  | dns_record_specs не маппятся в Address           | High     | deferred   |
| 4  | Address.dns_records не заполняется в proto       | High     | deferred   |
| 9  | cloud_id/organization_id не в domain             | Low      | deferred   |
| 12 | Дублирование моков                               | Medium   | deferred   |
| 18 | Newman не в CI                                   | Medium   | partial    |
| 21 | Нет тестов на InternalWatchService               | Low      | deferred   |
| 22 | Lost update в SG UpdateRules/UpdateRule          | High     | done       |
| 23 | json.Unmarshal silent failures в repo            | Medium   | done       |
| 24 | mustMarshalJSON panic → error-returning          | Low      | deferred   |
| 25 | Operation worker без graceful shutdown           | Medium   | deferred   |
| 26 | UpdateSecurityGroup без mask whitelist           | Medium   | done       |
| 27 | subnet_repo Update обновляет immutable v4_cidr   | Low      | deferred   |
| 28 | Тесты на concurrent UpdateRules                  | Low      | deferred   |
| 29 | Тесты на Delete response type = Empty            | Low      | done       |
| 30 | NetBox-integration убрать; IPAM встроить в kacho-vpc | High | **done** |
| 31 | AddressPool resource (internal-only)              | High     | **done** (proto, migration 0015, service, repo, handler, wire-up) |
| 32 | Pool selectors + cascade resolve + Check/Explain  | High     | **done** (migration 0016, service ResolvePoolForAddress, internal RPC) |
| 33 | kachoctl-ipam admin CLI                           | Medium   | **done** (cmd/kachoctl-ipam/) |
| 34 | Seed default-pool в dev-стенде                    | Low      | **done** (kacho-deploy `make seed-ipam`) |
| 35 | Newman regression: AddressPool RPC suite          | Medium   | pending — internal API не маршрутизируется через api-gateway, прогон через grpcurl, не Newman |
| 36 | Inline allocation в request-path (упразднить controller) | Medium | deferred — open RFC; см. 16.6 в CLAUDE.md |
| 37 | SetInternalIP deprecation в proto comment        | Low      | pending |
| 38 | E2E test для cascade resolve all 5 steps          | Medium   | pending |

Status legend: pending / in_progress / done / partial / deferred / wontfix

---

## Deferred — обоснование

### 3. `dns_record_specs` не маппятся в Address Create/Update
### 4. `Address.dns_records` не заполняется в proto-ответе

Proto-контракт: `repeated DnsRecordSpec dns_record_specs` в Create/Update,
`repeated DnsRecord dns_records = 20` в Address. Сейчас handler не читает,
domain не содержит, в proto-ответе пустой.

**Why deferred**: требует расширения domain.Address, миграции БД (новая
колонка JSONB или отдельная таблица address_dns_records), интеграции с
DNS-сервисом (которого пока нет в Kachō scope). Не блокирует текущую
parity — YC API позволяет создавать Address без dns_record_specs (пустой
slice — `[]`).

**Когда поднимать**: когда появится `kacho-dns` сервис в roadmap или
конкретный e2e-сценарий, требующий PTR/A-records.

### 9. `cloud_id` / `organization_id` не используются в domain-моделях

Колонки добавлены в `internal/migrations/0002_*.sql`, но `domain/*.go`
их не содержит. Бизнес-логика фильтрует только по folder_id.

**Why deferred**: требует cross-service резолва cloud_id из folder_id
через ResourceManager (которое vpc уже вызывает только для Exists), и
обновления proto если фильтрация по cloud понадобится (`?cloudId=...`
в List). Текущий YC API использует только folder-level scoping.

**Когда поднимать**: при добавлении cross-folder/cloud-level операций
(например, ListNetworksAcrossFolders).

### 12. Дублирование моков между service и handler тестами

`mockNetworkRepo`, `mockOpsRepo`, `mockFolderClient` в обоих:
`internal/service/mock_test.go` и `internal/handler/handler_test.go`.

**Why deferred**: вынесение в `internal/testutil/` создаёт циркулярную
зависимость через service port-интерфейсы (testutil → service →
testutil). Решение требует рефакторинга — отдельный пакет
`internal/ports/` с интерфейсами, чтобы и service, и testutil импортили
из одного места. Это структурный refactor, не точечная правка.

**Когда поднимать**: при следующем major refactoring пакета service.

### 18. Newman не в CI (partial)

В `.github/workflows/ci.yaml` добавлен job `newman` со скелетом, но шаг
`docker compose up` стоит TODO. Job сейчас не запускает прогон (`if: false`).

**Why partial**: требует `kacho-deploy/docker-compose.yaml` (которого нет
— стенд через kind). Альтернативы:
1. Поднимать kind-cluster в CI (медленно, требует privileged/dind).
2. Минимальный compose с только vpc + postgres (без api-gateway, без RM —
   тогда нужен заглушка для resource-manager.Exists).
3. Использовать `kacho-deploy/dev-up` как pre-step, кэшируя cluster.

**Когда поднимать**: когда добавится `kacho-deploy/ci-up` target с
docker-compose-light (только VPC + Postgres + mock RM).

### 21. Нет тестов на `InternalWatchService`

Server-stream gRPC handler с LISTEN/NOTIFY и catchup не покрыт тестами.

**Why deferred**: требует testcontainers + gRPC server setup +
streaming-client mock (или real grpc.NewClient на bufconn). Сложная
обвязка, не блокирует основной функционал.

**Когда поднимать**: при выявлении регрессии в watch-protocol (например,
missed event от kacho-vpc-controllers) или при добавлении нового
event_type.

---

## Новые пункты (из второго code review post-YC API)

### 22. Lost update в `SecurityGroupService.UpdateRules` / `UpdateRule`

`internal/repo/security_group_repo.go:207-237` (`UpdateRules`) и :259-302
(`UpdateRule`) делают read-modify-write внутри tx:
```sql
SELECT rules FROM security_groups WHERE id = $1     -- read
-- (Go: filter + append)
UPDATE security_groups SET rules = $2 WHERE id = $1  -- write
```

Нет CAS-условия в UPDATE. Два concurrent `UpdateRules` на одну SG →
последний wins, изменения первого теряются (lost update).

Fix: добавить optimistic concurrency через Postgres system column `xmin::text`
(zero-overhead, не требует миграции; envelope-колонок `resource_version`
в схеме нет — это legacy от envelope-эпохи до 1.0):
```sql
SELECT rules, xmin::text FROM security_groups WHERE id = $1;
UPDATE security_groups SET rules = $2
WHERE id = $1 AND xmin::text = $3
RETURNING ...
```
Если 0 rows affected → возвращать `ErrFailedPrecondition` `"concurrent
modification, please retry"`.

### 23. `json.Unmarshal` silent failures в repo scan-функциях

10 случаев `_ = json.Unmarshal(...)` в `internal/repo/*.go`:
- `network_repo.go:242`, `subnet_repo.go:365`, `route_table_repo.go:242, 245`,
  `gateway_repo.go:233`, `private_endpoint_repo.go:224, 227`,
  `security_group_repo.go:213, 265, 348, 351`.

При повреждённом JSONB (ручное вмешательство в DB, баг миграции) клиент
получит silent data loss — пустые labels/rules/dhcp_options без ошибки.

Fix: ввести helper `mustUnmarshalJSON(raw, &target)` или просто пробрасывать
ошибку через repo error. Минимум — логировать `slog.Warn` при unmarshal-fail.

### 24. `mustMarshalJSON` panic vs error-returning

Текущий helper в `internal/repo/jsonb.go` паникует при `json.Marshal` error.
Для текущих типов (`map[string]string`, `[]Rule`, `*DhcpOptions`) panic
unreachable, но defensive стиль требует error-returning формы.

**Why low**: panic'ит до commit'а tx → tx aborted, ничего не закоммичено.
Conn возвращается в пул через pgx defer (release auto-rollback'ит). Не
data corruption. Но нарушает `errcheck` linter.

Fix: переделать на `(b []byte, err error)`, прокинуть через wrapPgErr.

### 25. Operation worker без graceful shutdown

При SIGTERM `ctx.Done` → `grpcSrv.GracefulStop()` ждёт завершения in-flight
RPC, но worker-горутины внутри `operations.Run` (`kacho-corelib/operations`)
запущены в отдельной goroutine с background context — не отменяются.

В худшем случае: SIGTERM во время Network.Delete → gRPC закрывается, но
worker продолжает выполнять transaction. Если процесс убит до commit'а —
Operation остаётся в `done=false` (orphan).

**Why deferred**: требует context propagation через `operations.Run` и
recovery-механизма для orphan operations при старте. Сложный refactor,
требует изменений в `kacho-corelib`.

### 26. `UpdateSecurityGroup` без known-mask whitelist

`internal/service/security_group.go:204-216` — switch по mask полям без
`corevalidate.UpdateMask(...)` whitelist-check. Передача `update_mask`:
`["unknown_field"]` молча игнорируется (switch не ловит case).

В отличие от `validateNetworkUpdate` / `validateSubnetUpdate`, где есть
known-set + `corevalidate.UpdateMask` валидация, в SG это не сделано.

YC verbatim: unknown field в `update_mask` → `INVALID_ARGUMENT`.

Fix: добавить `validateSGUpdate(req)` с known-set
`{"name", "description", "labels", "rule_specs"}`.

### 27. `subnet_repo.Update` обновляет immutable v4_cidr_blocks на DB-уровне

`internal/repo/subnet_repo.go:171` —
`UPDATE subnets SET name=$2, description=$3, labels=$4, v4_cidr_blocks=$5,
route_table_id=$6, dhcp_options=$7`.

Service-layer `applySubnetMask` блокирует изменение `v4_cidr_blocks` через
mask, но если кто-то напрямую вызовет `repo.Update(sub)` с модифицированным
`sub.V4CidrBlocks` — БД позволит. Это bypass возможен только через bug в
своём же коде, не через API.

**Why deferred (low)**: defensive depth, не блокирует контракт. Service-layer
защита достаточна. Можно добавить trigger `subnets_immutable_check` или
исключить колонку из UPDATE statement.

### 28. Тесты на concurrent `UpdateRules`

Сейчас нет теста, демонстрирующего lost update (см. #22). Добавить
integration-тест с двумя goroutine-ами, делающими UpdateRules одновременно.
Покрытие ловит fix #22.

### 29. Тесты на Delete response type = Empty

Простой assertion: после `Delete*` operation `done=true`, проверить
`anypb.UnmarshalTo(op.Response, &emptypb.Empty{})` не возвращает ошибку.
Защищает от регрессии #1 (которая уже была: 6 мест с `metadata` в
response).

---

## Снапшоты Newman (audit trail)

- `newman/out/baseline-yc-*.json` — yc baseline (зафиксирован вне сессии)
- `newman/out/snap1-local-*.json` — first local прогон до фикса env (78/152 light failures)
- `newman/out/snap2-local-*.json` — local после фикса env (283/0 ✓)
- `newman/out/snap3-local-*.json` — local после redeploy (3 quota-related failures)
- `newman/out/snap4-local-*.json` — финальный local после cleanup БД (**283/0 ✓**)

---

## Архитектурные рекомендации (не блокирующие)

- Tight coupling через `replace ../kacho-corelib` в `go.mod` — рассмотреть
  переход на версионированные модули + `go.work` для воспроизводимости релизов.
- Anti-corruption layer между handler и proto — отложить до увеличения
  поверхности API.
