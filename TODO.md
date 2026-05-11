# TODO — outstanding tech-debt

После 10-раундового adversarial-ревью + IPAM Phase 2 + delete-restriction
fix + #23/#27/#37/#25 closures + перепаковка миграций (`5581316`) +
FINDING-005 fix (`0002_resource_name_unique.sql`).

Verified Newman regression: **686 кейсов / ~3120 assertions / 0 failures**
(v15; коллекции в `tests/newman/collections/`, генерятся из `tests/newman/cases/*.py`
через `tests/newman/scripts/gen.py`; прогон — `tests/newman/scripts/run.sh`). Требует
`KACHO_VPC_DEFAULT_SG_INLINE=true` (default) — иначе default-SG-кейсы краснеют.

## Status table

| #  | Title                                                | Severity | Status     |
|----|------------------------------------------------------|----------|------------|
| 3  | dns_record_specs не маппятся в Address               | High     | deferred   |
| 4  | Address.dns_records не заполняется в proto           | High     | deferred   |
| 9  | cloud_id/organization_id не в domain                 | Low      | deferred   |
| 12 | Дублирование моков                                   | Medium   | deferred   |
| 18 | Newman не в CI                                       | Medium   | partial    |
| 21 | Нет тестов на InternalWatchService                   | Low      | deferred   |
| 22 | Lost update в SG UpdateRules/UpdateRule              | High     | **done** (xmin OCC) |
| 23 | json.Unmarshal silent failures в repo                | Medium   | **done** (`unmarshalJSONB` helper применён в 3 оставшихся местах) |
| 24 | mustMarshalJSON panic → error-returning              | Low      | wontfix (см. ниже) |
| 25 | Operation worker без graceful shutdown               | Medium   | **done** (`operations.Wait` + `shutdownDone` channel в R7-R10) |
| 26 | UpdateSecurityGroup без mask whitelist               | Medium   | done       |
| 27 | subnet_repo Update обновляет immutable v4_cidr       | Low      | **done** (column убрана из UPDATE statement) |
| 28 | Тесты на concurrent UpdateRules                      | Low      | deferred (требует testcontainers) |
| 29 | Тесты на Delete response type = Empty                | Low      | done       |
| 30 | NetBox-integration убрать; IPAM встроить             | High     | done       |
| 31 | AddressPool resource (internal-only)                 | High     | done       |
| 32 | Pool selectors + cascade resolve + Check/Explain     | High     | done       |
| 33 | kachoctl-ipam admin CLI                              | Medium   | **removed** (`cmd/kachoctl-ipam` удалён; admin — curl/REST на api-gateway / UI) |
| 34 | Seed default-pool в dev-стенде                       | Low      | reverted (per #49 — admin создаёт пулы вручную через curl на api-gateway) |
| 35 | Newman regression: AddressPool RPC suite             | Medium   | wontfix — internal API, прогон через grpcurl |
| 36 | Inline allocation в request-path                     | Medium   | **done** (kacho-vpc-controllers упразднён в Phase 2; см. workspace CLAUDE.md) |
| 37 | SetInternalIP deprecation в proto                    | Low      | **done** (`option deprecated = true` на rpc + request/response messages) |
| 38 | E2E test для cascade resolve all 5 steps             | Medium   | pending |
| **39** | **AddressPool/Zone/Region delete без empty-check**     | **High**     | **done** (service-level `CountAddressesByPool` / `CountDependents` / `CountZones` + FailedPrecondition) |
| **40** | **Production fail-closed mode + AuthZ coverage** | **Critical** | **done** (`KACHO_VPC_AUTH_MODE=production`, `AssertFolderOwnership` во всех 7 публичных handler'ах включая List/ListOperations/child Lists/GetByValue) |
| **41** | **List(folder_id="") cross-folder enumeration**      | **Critical** | **done** (service-level `if f.FolderID == "" → InvalidArgument` во всех 7 List сервисах) |
| **42** | **IsAnonymous bypass через actor-only header**       | **Critical** | **done** (Actor — audit-only; AuthN признан только при Admin или non-empty FolderIDs) |
| **43** | **Internal-handler info-leak (`mapPoolErr` / `mapGeoErr`)** | **High** | **done** (унифицированы через `internalMapErr`, sentinel-only text) |
| **44** | **OperationHandler raw err leak**                    | **High**     | **done** (generic `Internal "operation get failed"`) |
| **45** | **Concurrency P0: operations.Wait + shutdownDone + cancel-on-Serve-return** | **Critical** | **done** (R7-R10 wiring) |
| **46** | **N+1 в `CountAddressesByPoolPerCIDR` + canonicalization** | **High** | **done** (single SQL с `unnest WITH ORDINALITY` + 1-based index) |
| **47** | **Allocator: 32-attempt random без memoization** + `/31` off-by-one | **High** | **done** (двухфазный allocator + switch hostBits) |
| **48** | **Watch: stream cap + connect timeout 2s + info-leak fix** | **Medium** | **done** (per-stream semaphore + `connectCtx 2s` + generic Unavailable) |
| **49** | **NetworkPolicy для :9091 + admin-only interceptor + mTLS opt-in + sslmode env** | **High** | **done** (kacho-deploy PR #2 + R7-R10 wiring) |
| **50** | **GetByValue: PermissionDenied → NotFound (не leak'ать существование IP)** | **Low** | **done** |
| **51** | **AuthMode whitelist (typo `prod` → fatal exit)**    | **Medium** | **done** (switch с unknown→error) |
| **52** | **production-strict sslmode whitelist (require/verify-ca/verify-full)** | **Medium** | **done** |
| **53** | **assertAdminAccess HasPrefix вместо Contains**      | **Low**    | **done** |
| **54** | **internal grpc Serve: filter ErrServerStopped в логе** | **Low** | **done** |
| **55** | **Unit-tests для tenant_interceptor**                | **High** | **done** (12 cases в `tenant_interceptor_test.go`) |

Status legend: pending / in_progress / done / partial / deferred / wontfix

---

## Carry-over в IAM design phase

- **OperationService AuthZ** — требует data-model change (`folder_id` на operations table или join через `metadata.resource_id`).
- **mTLS на :9091** — defense-in-depth поверх NetworkPolicy + admin-check + production-mode (4-й слой; основная защита уже есть).
- **SECURITY.md обновление** — документация устарела; будет переписано при IAM design phase.
- **IAM сам** — JWT-validating interceptor заменит metadata-based scaffolding (контракт `TenantFromCtx` / `AssertFolderOwnership` стабилен).

---

## Pending — конкретные задачи без deferred-обоснования

### 38. E2E test для cascade resolve all 5 steps

5 шагов cascade: address_override → network_default → cloud_pool_selector
→ zone_default → global_default. Сейчас покрытие unit-тестов на каждый
step + Newman smoke на global_default. Не хватает: одного полного e2e-
сценария где админ конфигурирует все 5 уровней и каждый Address получает
IP из правильного pool в зависимости от своих labels/folder/network.

**Когда поднимать**: когда добавится Newman suite для admin REST flow
(curl/REST на api-gateway).

---

## Deferred — обоснование

### 3. `dns_record_specs` не маппятся в Address Create/Update
### 4. `Address.dns_records` не заполняется в proto-ответе

Требует `kacho-dns` сервиса (которого пока нет в roadmap). Не блокирует
parity — YC API позволяет пустой `dns_record_specs: []`.

### 9. `cloud_id` / `organization_id` не используются в domain-моделях

Требует cross-service резолва из ResourceManager + расширения proto.
Не нужно для текущего folder-level scoping.

### 12. Дублирование моков между service и handler тестами

Циркулярная зависимость через service port-интерфейсы. Решение —
отдельный `internal/ports/` пакет, structural refactor.

### 18. Newman не в CI (partial)

Workflow есть, но `if: false` — нет `kacho-deploy/ci-up` target с
docker-compose-light (нужен mock RM). Открыто.

### 21. Нет тестов на `InternalWatchService`

Server-stream gRPC + LISTEN/NOTIFY + catchup — требует testcontainers
+ bufconn. Сложная обвязка, не блокирует функционал.

### 28. Тесты на concurrent `UpdateRules`

Lost-update fix #22 (xmin OCC) уже закрыт; тест добавится при следующем
прогоне testcontainers integration.

---

## #24 wontfix — обоснование

`mustMarshalJSON` паникует при `json.Marshal` error. Для текущих типов
(`map[string]string` labels, `[]Rule` rules, `*DhcpOptions`, `[]StaticRoute`,
ExternalIpv4Spec, InternalIpv4Spec, `*DnsOptions`) panic unreachable —
типы содержат только stdlib без channel/func/cyclic-ref. Если когда-то
добавится тип с потенциальной marshal-fail (cyclic proto через
`*anypb.Any`) — переделаем на error-returning форму. Пока — defensive
overhead без real benefit. См. invariant-comment в `internal/repo/jsonb.go`.

---

## Снапшоты Newman (audit trail)

- `tests/newman/out/*.json` — per-service JSON reporter последнего прогона
  `tests/newman/scripts/run.sh` (агрегируется в `tests/newman/out/summary.txt`).
- Текущий результат: 686 кейсов / ~3120 assertions / 0 fail (v15).
- История версий и mapping техник тестирования — `tests/newman/docs/RESULTS.md`;
  карта багов/наблюдений — `tests/newman/docs/BUG-MAP.md`; каталог паттернов — `tests/newman/docs/CASES-INDEX.md`.

---

## Архитектурные рекомендации (не блокирующие)

- Tight coupling через `replace ../kacho-corelib` в `go.mod` — переход на
  versioned modules + `go.work` для воспроизводимости релизов.
- Anti-corruption layer между handler и proto — отложить до увеличения
  поверхности API.
- Composition root в `run` (`cmd/vpc/main.go`, ~80 строк wiring) — decompose
  в отдельные factory-функции при следующем major refactor.
- Setter-DI `networkSvc.SetSGRepo` — temporal coupling; вызывается в `cmd/vpc/main.go`
  только при `KACHO_VPC_DEFAULT_SG_INLINE=true` (default). AddressAllocator уже
  inlined в `AddressService` (commit `5581316`), `SetAllocator` больше нет.
