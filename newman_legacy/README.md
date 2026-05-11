# kacho-vpc/newman

VPC-domain regression suite (Newman/Postman) для `kacho-vpc`. Изолирована от Go-кода сервиса.

**Network домен унифицирован:** один и тот же набор `NET-*` кейсов исполняется идентично
в `--env local` (Kachō) и `--env yc` (реальный Yandex Cloud VPC API). Единственная развилка —
`00-preflight` (создаёт Org/Cloud/Folder в local; реиспользует pre-allocated в yc).

Прочие домены (Subnet / Address / RouteTable / SecurityGroup / Gateway / PrivateEndpoint, NetBox)
пока работают по legacy-схеме `requiresCloudMutationOK` / `backendKind` skip-маркеров.
По мере унификации они переедут в тот же шаблон.

Кейсы остальных доменов (Cloud / Folder / Organization / Operations / общий HTTP-gateway)
живут в `../../kacho-test/`.

## Структура

```
kacho-vpc/newman/
├─ README.md
├─ PARITY.md                                 — registry pending-parity / kacho-only
├─ package.json
├─ .gitignore
├─ collections/
│  └─ kacho-vpc.postman_collection.json      — единая коллекция
├─ environments/
│  ├─ local.postman_environment.json         — локальный Kachō стенд (port-forward 18080)
│  └─ yc.postman_environment.json            — реальный YC API (IAM-токен из CLI)
├─ scripts/
│  ├─ run.sh                                 — entrypoint с --env local|yc, --folder, glob
│  └─ rebuild-collection.py                  — миграционный rebuild (Network → unified NET-*)
└─ out/                                      — newman-отчёты last-run*.json (gitignore)
```

## Архитектура (унифицированный flow)

```
Kachō VPC QA Suite (unified)
├─ 00-preflight                              ← всегда первая
│   ├─ pf.setup-org      [skip if existingFolderId]
│   ├─ pf.setup-cloud    [skip if existingFolderId]
│   └─ pf.setup-folder   [skip if existingFolderId]
│   pre-request:
│     - на первом шаге генерирует runId
│     - если existingFolderId set → копирует existing* в _suite* и skipRequest
│   test:
│     - сетит _suiteOrgId / _suiteCloudId / _suiteFolderId из response.metadata
│
├─ NET-CR-OK                                 ← unified NET-* кейсы
├─ NET-CR-NAME-MAX
├─ ...                                       (34 unified Network-кейсов)
│
├─ <legacy non-Network top-level folders>    (Subnet/Address/RT/SG/GW/PE — пока legacy)
│
└─ 99-teardown                               ← всегда последняя
    ├─ td.cleanup-folder  [skip if existingFolderId]
    ├─ td.cleanup-cloud   [skip if existingFolderId]
    └─ td.cleanup-org     [skip if existingFolderId]
```

Каждый unified `NET-*` кейс самодостаточен внутри `{{_suiteFolderId}}` — ничего не создаёт
в обёртке Org/Cloud, не имеет per-request skip-маркеров.

Полный design-документ: `../../../docs/superpowers/specs/2026-05-05-vpc-newman-unification-design.md`.

## Подготовка стенда

```bash
# 1) Поднять Kachō локально
cd ../../kacho-deploy
make dev-up

# 2) Прокинуть api-gateway:8080 наружу
kubectl -n kacho port-forward svc/api-gateway 18080:8080 &
```

## Запуск

```bash
# Все кейсы локально (включая unified NET-* + legacy)
./scripts/run.sh                                  # или: npm test

# Только Network domain
./scripts/run.sh --folder 'NET-*'

# Все кейсы против реального YC API (preflight skip — реиспользует existingFolderId)
./scripts/run.sh --env yc

# Network domain в YC
./scripts/run.sh --env yc --folder 'NET-*'

# Один case (preflight + teardown auto-injected)
./scripts/run.sh --folder NET-CR-OK
./scripts/run.sh --env yc --folder NET-DEL-WITH-SUBNETS
```

## Variable convention

| Переменная | Источник | Назначение |
|---|---|---|
| `existingOrgId` / `existingCloudId` / `existingFolderId` | env-файл | YC: pre-allocated. Local: пусто (preflight создаёт). |
| `_suiteOrgId` / `_suiteCloudId` / `_suiteFolderId` | preflight | Используется во всех unified VPC-кейсах. Очищается teardown'ом. |
| `runId` | preflight (Math.random) | Префикс уникальных имён `qa-{{runId}}-<casePrefix>`. |
| `<caseId>_<resource>Id` | per-case test-script | Временный per-case state. Пример: `ncrok_netId`, `ndel1_subnetId`. |

`backendKind`, `requiresCloudMutationOK`, `orgId/cloudId/folderId/networkId/subnetId/operationId`,
`ycFolderId/ycCloudId/ycNetworkId/...` — **legacy**. Сохраняются в env-файлах до полного
покрытия всех доменов; новые кейсы их не используют.

## Findings

Markdown-описания VPC-расхождений и Kachō-decisions ведутся в `../../kacho-test/findings/`
(общий QA-репозиторий для всех доменов). При добавлении нового case:

1. Создать/обновить `kacho-test/findings/<ID>-<topic>.md`.
2. Добавить папку в `collections/kacho-vpc.postman_collection.json`
   с именем формата `<DOMAIN>-<KIND>-<DESC> — <description>`.
3. Прогнать локально: `./scripts/run.sh --folder '<id>'`.
4. (Network only) Прогнать в YC: `./scripts/run.sh --env yc --folder '<id>'`.

## Conventions

- **Имя папки** = `<DOMAIN>-<KIND>-<DESC> — <description>` (`NET-CR-OK`, `NET-DEL-WITH-SUBNETS`,
  `SU-CIDR-OVERLAP`, `SG-CR-VALID` …). Префикс `YC-` для нового кейса не используется —
  все unified-кейсы исполняются в обоих env.
- **Async Operations** — POST/DELETE возвращает Operation (`done:false`).
  Поллим через `GET /operations/{id}` и проверяем `done:true` + `response`/`error`.
  По умолчанию между запросами 50 мс задержки локально и 1500 мс в YC.
- **`pm.expect(body.code)`** — gRPC code мапится в HTTP status, но `body.code`
  остаётся integer gRPC-code (3=InvalidArgument, 5=NotFound, 6=AlreadyExists, 9=FailedPrecondition).
- **Identical assertions** — никаких `if (backendKind === 'yc') ...` ветвлений в test-scripts.
  Если YC и Kachō расходятся — либо фиксим Kachō (atomic PR), либо переносим кейс в `PARITY.md`.

## Запреты

- **Не использовать `yc` CLI в тестах.** В YC лезем напрямую через `https://vpc.api.cloud.yandex.net/...`.
  `yc` — только для получения IAM-токена в `run.sh`.
- **Не writing новый shell-скрипт** в `scripts/` для нового case — добавляем
  в коллекцию ещё одну папку.
- **Никаких per-request skip-маркеров** в новых unified кейсах. Расхождения KC↔YC
  обрабатываются по правилу atomic-PR (см. spec § «Расхождения KC ↔ YC»).
