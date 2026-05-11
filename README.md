# kacho-vpc

VPC-сервис Kachō: control-plane для Network, Subnet, Address, RouteTable,
SecurityGroup, Gateway, PrivateEndpoint. Цель — verbatim parity с YC VPC API
(см. `CLAUDE.md` §1, sub-phase 0.3 в `../../docs/specs/`).

## Quick start (локальный стенд)

```bash
# 1. Поднять полный стенд (kind + helm + Postgres + все сервисы)
cd ../kacho-deploy && make dev-up

# 2. Прокинуть api-gateway наружу
kubectl -n kacho port-forward svc/api-gateway 18080:8080 &

# 3. Smoke-проверка
curl 'http://localhost:18080/vpc/v1/networks?folderId=&pageSize=5'
```

Перезапуск только VPC после изменений в коде:
```bash
cd ../kacho-deploy && make reload-svc SVC=vpc
make logs-svc SVC=vpc        # tail логов
make psql SVC=vpc            # psql kacho_vpc
```

## Архитектура

Clean Architecture (`domain → service → handler/repo/clients`); `cmd/vpc/main.go` —
единственный composition root. Подробности по слоям и паттернам — в
`CLAUDE.md` §4. Service возвращает `Operation` для всех мутаций (LRO),
выполнение worker'ом через `kacho-corelib/operations.Run`. Outbox + LISTEN/NOTIFY
дают event stream через `InternalWatchService` (для admin-tooling / UI; раньше
его потреблял `kacho-vpc-controllers` — упразднён в Phase 2, IPAM-allocate и
default-SG теперь inline в service-слое).

### Dual gRPC ports

| Порт   | Сервисы                                                                           | Кто использует                       |
|--------|-----------------------------------------------------------------------------------|--------------------------------------|
| `9090` | NetworkService, SubnetService, AddressService, RouteTableService, SecurityGroupService, GatewayService, PrivateEndpointService, OperationService | api-gateway → внешние клиенты        |
| `9091` | InternalWatchService, InternalAddressService (allocate int/ext IP), InternalAddressPoolService, InternalRegionService, InternalZoneService, InternalNetworkService, InternalCloudService | admin-tooling (curl/REST на api-gateway internal mux), UI (через api-gateway internal mux), in-process inline-allocate; в будущем kacho-compute |

`Internal*` сервисы не маршрутизируются через external TLS endpoint api-gateway
(запрет #6 из workspace `CLAUDE.md`); часть проброшена на cluster-internal listener
для UI/admin (`/vpc/v1/{regions,zones,addressPools,...}`).

## Контракт ошибок

Sync-валидация (до Operation) — формат полей, regex, whitelist. Async (внутри
worker) — existence checks, FK, EXCLUDE constraints. Маппинг через
`mapRepoErr` + verbatim YC text. Полная таблица: `CLAUDE.md` §6.

Ключевые case'ы:
- CIDR overlap → `FAILED_PRECONDITION "Subnet CIDRs can not overlap"`
- garbage id → async `NOT_FOUND` (НЕ sync `INVALID_ARGUMENT`)
- duplicate name within folder → `ALREADY_EXISTS`
- folder not found → async `NOT_FOUND "Folder with id %s not found"`
- deletion_protection → sync `FAILED_PRECONDITION` перед Delete

## Тестирование

Уровни (детали — `CLAUDE.md` §14):

```bash
make test-short                          # unit (моки, без Docker)
make test                                # unit + integration (testcontainers)
# E2E newman (нужен port-forward api-gateway → localhost:18080, KACHO_VPC_DEFAULT_SG_INLINE=true):
python3 tests/newman/scripts/gen.py      # перегенерить коллекции из cases/*.py
tests/newman/scripts/run.sh              # все сервисы; --service network для одного
# Нагрузочные (k6 + ghz):
tests/k6/run-all.sh                      # быстрый набор сценариев; см. tests/k6/README.md
```

`tests/newman/` — главная regression-сьюта: декларативные `cases/*.py` → `gen.py` →
Postman-коллекции по сервису; black-box покрытие всех публичных RPC. Документооборот —
`tests/newman/docs/` (TAXONOMY / TEST-PLAN / CASES-INDEX / REQUIREMENTS / RESULTS; баги — в TODO.md).
Подробности — `tests/newman/README.md` и `vpc-newman-author` агент.
`tests/k6/` — нагрузочные сценарии (k6 HTTP + ghz gRPC, in-cluster Jobs); baseline —
`tests/k6/results/BASELINE.md`. См. `vpc-load-testing` агент/скил.

## Migrations

Боевые: `internal/migrations/*.sql`, embed FS — `0001_initial.sql` (squashed
baseline схемы; 22 исторические миграции свёрнуты в один файл) + `0002_resource_name_unique.sql`
(partial UNIQUE `(folder_id, name)` для subnet/RT/SG/GW/PE/Address). `migrations/`
в корне репо — staging для `make sync-migrations` (только `0001_operations.sql`
от corelib). **Не редактировать применённые миграции** — только новый файл.

```bash
KACHO_VPC_DB_PASSWORD=secret bin/kacho-vpc migrate up
KACHO_VPC_DB_PASSWORD=secret bin/kacho-vpc migrate status
```

## Spec & decision records

- **Архитектурный документ (итоговый)**: [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) — самодостаточное описание от C0 до пошагового воспроизведения
- Detailed arch docs: [`docs/architecture/`](docs/architecture/) (00-06 + 09)
- Acceptance: `../../docs/specs/sub-phase-0.3-vpc-acceptance.md`
- Roadmap: `../../docs/specs/04-roadmap-and-phasing.md`
- Workspace правила: `../../CLAUDE.md`
- Outstanding tech-debt: `./TODO.md`

## Subagents (project-level в `.claude/agents/`)

13 общих (workspace) + VPC-специализированные:
- `vpc-yc-parity-auditor` — verbatim YC checks (texts/regex/codes/timestamps)
- `vpc-cidr-specialist` — CIDR (host-bits, EXCLUDE, overlap, internal IP)
- `vpc-outbox-watch-engineer` — outbox + LISTEN/NOTIFY + Internal services
- `vpc-newman-author` — Postman/Newman regression suites
- `testing-code-coach` — эталонные практики тестирования кода (TESTING.md)
- `testing-product-coach` — black-box product testing техники (TESTING-PRODUCT.md)
- `vpc-load-testing` — k6/ghz нагрузочные сценарии VPC (см. `tests/k6/`)
