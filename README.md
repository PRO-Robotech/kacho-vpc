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
дают event stream для `kacho-vpc-controllers` (см. §4.3).

### Dual gRPC ports

| Порт   | Сервисы                                                                           | Кто использует                       |
|--------|-----------------------------------------------------------------------------------|--------------------------------------|
| `9090` | NetworkService, SubnetService, AddressService, RouteTableService, SecurityGroupService, GatewayService, PrivateEndpointService, OperationService | api-gateway → внешние клиенты        |
| `9091` | InternalWatchService, InternalAddressService                                     | kacho-vpc-controllers, kacho-compute |

`Internal*` сервисы не маршрутизируются через api-gateway (запрет #6 из
workspace `CLAUDE.md`).

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

Три уровня (детали — `CLAUDE.md` §14):

```bash
make test-short              # unit (моки, без Docker)
make test                    # unit + integration (testcontainers)
cd newman && ./scripts/run.sh --env local   # e2e через api-gateway
```

Newman quota-aware 3-suite pipeline (RO / LIGHT / SEQ) — против local Kachō
и реального YC API. Variable convention, class taxonomy, PARITY.md registry —
см. `newman/README.md` и `vpc-newman-author` агента.

## Migrations

Боевые: `internal/migrations/*.sql` (12 файлов, embed FS). `migrations/` в
корне — staging для goose CLI. **Не редактировать применённые миграции** —
только новый файл.

```bash
KACHO_VPC_DB_PASSWORD=secret bin/kacho-vpc migrate up
KACHO_VPC_DB_PASSWORD=secret bin/kacho-vpc migrate status
```

## Spec & decision records

- Acceptance: `../../docs/specs/sub-phase-0.3-vpc-acceptance.md`
- Roadmap: `../../docs/specs/04-roadmap-and-phasing.md`
- Workspace правила: `../../CLAUDE.md`
- Outstanding tech-debt: `./TODO.md`

## Subagents (project-level в `.claude/agents/`)

13 общих + 4 VPC-специализированных:
- `vpc-yc-parity-auditor` — verbatim YC checks (texts/regex/codes/timestamps)
- `vpc-cidr-specialist` — CIDR (host-bits, EXCLUDE, overlap, internal IP)
- `vpc-outbox-watch-engineer` — outbox + LISTEN/NOTIFY + Internal services
- `vpc-newman-author` — Postman/Newman regression suites
