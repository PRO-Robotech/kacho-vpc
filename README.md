<!--
Copyright (c) PRO-Robotech
SPDX-License-Identifier: BUSL-1.1
-->

# kacho-vpc

**VPC control plane платформы Kachō.** Сервис управляет сетевой моделью облака —
он отвечает за *намерение* пользователя («какая сеть, какие подсети, какие правила
доступа»), а не за пакеты на проводе: только control plane, без data plane.

Управляемые ресурсы:

| Ресурс | Назначение |
|---|---|
| **Network** | изолированная сеть проекта; контейнер для подсетей и правил |
| **Subnet** | диапазон адресов (v4/v6 CIDR) в зоне; из него выделяются адреса |
| **Address** | выделенный внутренний или внешний IP |
| **NetworkInterface** | самостоятельный сетевой интерфейс (NIC), отвязанный от инстанса |
| **RouteTable** | таблица маршрутов, ассоциируемая с подсетями |
| **SecurityGroup** | набор правил доступа (stateful allow-rules) |
| **Gateway** | шлюз сети во внешний мир |
| **AddressPool** | admin-ресурс: пулы внешних адресов под выдачу (Internal API) |

## Контракт API

Каждый ресурс — плоский message; чтения синхронны, мутации асинхронны и возвращают
`Operation` (long-running operation), статус которой клиент опрашивает через
`OperationService.Get`. Доступ — по двум gRPC-листенерам:

| Порт | Поверхность | Потребители |
|---|---|---|
| `9090` | публичные `*Service` (Network/Subnet/Address/NetworkInterface/RouteTable/SecurityGroup/Gateway/Operation) | внешние клиенты через api-gateway |
| `9091` | `Internal*Service` (AddressPool, allocate IP, admin-операции Network) | UI, admin-tooling, соседние сервисы |

`Internal*`-методы и инфра-чувствительные данные никогда не публикуются на внешнем
endpoint — это разделение защищает топологию от разведки. Полное описание ресурсов,
методов и кодов ошибок — в документации (`docs-site/`).

## Быстрый старт

Требуется Go 1.25+ и Postgres 16+. Сервис собирается двумя бинарями — сам сервис и
мигратор схемы:

```bash
make build            # → bin/kacho-vpc
make build-migrator   # → bin/kacho-migrator

# Накатить схему БД
KACHO_VPC_DB_PASSWORD=secret bin/kacho-migrator up
KACHO_VPC_DB_PASSWORD=secret bin/kacho-migrator status

# Запустить сервис (конфиг — через YAML/ENV, см. docs-site «Установка»)
bin/kacho-vpc serve
```

Конфигурация грузится через viper (YAML + ENV-override с префиксом `KACHO_VPC_`).
По умолчанию AuthN работает в режиме `production` (анонимный доступ запрещен,
fail-closed); локальный режим без AuthN включается явно — `authn.mode=dev`.

## Архитектура

Чистая архитектура со строгим правилом зависимостей:

```
handler ─┐
         ├─→ use-case ─→ domain
repo ────┤                ↑ (только структуры)
clients ─┘
```

- `internal/domain` — сущности и self-validating newtypes (только stdlib + proto-типы);
- `internal/apps/kacho/api/<resource>` — use-case'ы (бизнес-логика, port-интерфейсы);
- `internal/repo` — адаптеры Postgres (sqlc + handwritten pgx; без ORM);
- `internal/clients` — gRPC-клиенты соседних сервисов;
- `internal/handler` — тонкий transport (parse → use-case → format);
- `cmd/vpc` — единственная точка сборки зависимостей; `cmd/migrator` — мигратор.

Within-service инварианты выражены на уровне БД (FK / UNIQUE / EXCLUDE / CHECK /
атомарный CAS), а не software-проверками. Мутации эмитят запись в outbox в той же
транзакции, что и DML, — это гарантирует атомарность изменения и его публикации.
Подробности — в `docs/architecture/` и `docs-site/`.

## Тестирование

```bash
make test-short   # быстрый прогон (моки, без внешних зависимостей)
make test         # полный прогон, включая integration на testcontainers (нужен Docker)
make vet          # go vet
make lint         # golangci-lint
```

- **unit** — use-case'ы через mock-порты, домен, конфиг;
- **integration** (`*_integration_test.go`) — реальный Postgres через testcontainers:
  CRUD, ограничения БД, конкурентные CAS/UNIQUE/EXCLUDE-сценарии;
- **e2e** (`tests/newman/`) — полный набор regression-коллекций (Newman/Postman),
  black-box через api-gateway: декларативные кейсы `cases/*.py` → Postman-коллекции
  (`gen.py`). 14 коллекций покрывают ресурсы и сквозные сценарии — `network`, `subnet`,
  `address`, `route-table`, `security-group`, `gateway`, `network-interface`,
  `operation`, `internal-network`, `internal-pool`, `list-filter-d`, `authz-deny`,
  `concurrency`, `observability`;
- **нагрузка** (`tests/k6/`) — k6 (HTTP) и ghz (gRPC), baseline в `tests/k6/results/`.

## Структура репозитория

```
cmd/            точки входа: vpc (сервис), migrator (схема БД)
internal/       domain, use-case'ы, repo, clients, handler, config
pkg/sdk/vpc/    Go-SDK для вызова сервиса
internal/migrations/   goose SQL-миграции (0001_initial — squashed baseline)
deploy/         Helm-чарт
docs/, docs-site/      архитектурные заметки и документация
tests/          newman (e2e) и k6 (нагрузка)
```

## Разработка и вклад

Как завести issue, оформить ветку и PR, какие требования к коду и тестам — см.
[`CONTRIBUTING.md`](CONTRIBUTING.md).

## Лицензия

Распространяется по **Business Source License 1.1** — свободное использование, кроме
случаев, когда продукт прямо или косвенно приносит коммерческую выгоду; такое
использование требует отдельной лицензии. Полный текст — [`LICENSE`](LICENSE).
