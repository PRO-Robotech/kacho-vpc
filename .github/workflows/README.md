# GitHub Actions workflows — kacho-vpc

## docker-build.yml — DockerHub multi-arch image build

Собирает Docker-образ `kacho-vpc` под `linux/amd64` + `linux/arm64` и
публикует multi-arch manifest в DockerHub. Дополняет `ci.yaml` (build/vet/test/lint/integration),
не заменяет его.

### Триггеры

- push в `main`
- push в feature-ветки
- push тегов `v[0-9]+.[0-9]+.[0-9]+` и `...rc[0-9]+`

### Образы и теги

| Образ | Теги |
|---|---|
| `<DOCKERHUB_USERNAME>/kacho-vpc` | `<branch>-<sha8>` (multiarch), `amd64-<branch>-<sha8>`, `arm64-<branch>-<sha8>` |

`kacho-vpc` — образ включает binary `kacho-vpc` (serve) + `kacho-migrator`.

### Требуемые GitHub secrets

| Secret | Назначение |
|---|---|
| `DOCKERHUB_USERNAME` | Docker Hub username (он же namespace для образов) |
| `DOCKERHUB_TOKEN` | Docker Hub access token (scope: Read/Write/Delete) |

Креды одинаковые для всех `kacho-*` репозиториев (один Docker Hub-аккаунт).

### Установка secrets (user-action)

```bash
gh secret set DOCKERHUB_USERNAME --body "<value>" --repo PRO-Robotech/kacho-vpc
gh secret set DOCKERHUB_TOKEN    --body "<value>" --repo PRO-Robotech/kacho-vpc
```

### Polyrepo build

`kacho-vpc` — часть polyrepo: `go.mod` использует `replace ../kacho-corelib`,
`../kacho-proto`; `Dockerfile` делает `COPY kacho-corelib` / `COPY kacho-proto`.
Workflow чекаутит main-репо + siblings (`kacho-corelib`, `kacho-proto`) в один
каталог; build context = этот каталог. Siblings пиннятся к `ref: main`; при
незакрытой кросс-репо зависимости — временно к соответствующей feature-ветке,
с возвратом на `ref: main` после merge.

### self-hosted runner

Job `docker-build-arm64` требует `runs-on: self-hosted` arm64-раннер. Если
arm64-раннер недоступен — образ соберется только под amd64 (job arm64 + manifest
push зафейлятся; amd64-тег при этом валиден).

## newman-e2e.yml — self-contained newman E2E authz gate

Полный Newman authz E2E (288-кейсовая default-deny матрица + 30-кейсовая
ServiceAccount/API-token матрица) гоняется **прямо в CI этого репо**: workflow
`newman-e2e.yml` поднимает реальный kind + helm umbrella-стек (Postgres + Ory +
OpenFGA + api-gateway + iam + vpc + compute) на локальном kind-кластере, сидит
shared authz-фикстуры и гоняет сьюты `kacho-iam` через REST api-gateway.

`newman-e2e.yml` **не требует никаких секретов**: весь стек билдится и
поднимается в одном job на локальном kind — authz-матрица здесь реальный
блокирующий гейт.

### Триггеры

- `pull_request` в `main`
- `push` в `main`
- `workflow_dispatch` (ручной прогон)

### Что делает

1. Checkout этого репо (ref под тестом) + sibling-репо (`kacho-deploy`,
   `kacho-corelib`, `kacho-proto`, `kacho-iam`, `kacho-compute`,
   `kacho-api-gateway`) на `ref: main`.
2. Билд всех `kacho-*:dev` образов, `kind load`.
3. `helm install` umbrella (`values.dev.yaml`), ожидание openfga-bootstrap.
4. Сид shared authz-фикстур + прогон 2 newman-сьют (`authz-deny`,
   `authz-sa-apitoken`) через port-forward api-gateway.
5. `assert authz suites green` — fail job если хоть один assertion красный.

Тяжелый (~15-30 мин) — отдельный workflow, не в быстром `ci.yaml`.

### Секреты

Не требуются. `kacho-ui` — приватный репо, его checkout best-effort
(`continue-on-error`), helm-чарт стабится если checkout не прошел.

`kacho-deploy/.github/workflows/newman-e2e.yml` остается как есть (он
self-contained и гоняется на push/PR в сам `kacho-deploy`).
