---
level: functionality
repo: kacho-vpc
anchors:
  - rpc: kacho.cloud.vpc.v1.NetworkService/Create
  - rpc: kacho.cloud.vpc.v1.NetworkService/Delete
  - rpc: kacho.cloud.vpc.v1.NetworkService/Get
  - rpc: kacho.cloud.vpc.v1.NetworkService/List
  - rpc: kacho.cloud.vpc.v1.NetworkService/ListOperations
  - rpc: kacho.cloud.vpc.v1.NetworkService/ListRouteTables
  - rpc: kacho.cloud.vpc.v1.NetworkService/ListSecurityGroups
  - rpc: kacho.cloud.vpc.v1.NetworkService/ListSubnets
  - rpc: kacho.cloud.vpc.v1.NetworkService/Move
  - rpc: kacho.cloud.vpc.v1.NetworkService/Update
status: implemented
source_sha: ""
---

# Network lifecycle

CRUD изолированной виртуальной сети (`Network`) — корневого контейнера
VPC-топологии — плюс nav-helpers для перечисления дочерних ресурсов и
cross-project `Move`.

## Зачем

`Network` — верхний уровень сетевой иерархии: внутри неё живут `Subnet`,
`SecurityGroup`, `RouteTable`. Сеть привязана к проекту (`project_id`,
заменивший старый `folder_id` после KAC-124) и служит границей L2/L3-изоляции —
ресурсы разных сетей не видят друг друга на уровне data-plane.

## Контракт

- `Get` / `List` / `ListOperations` — sync read.
- `ListSubnets` / `ListSecurityGroups` / `ListRouteTables` — sync
  nav-helpers: перечисление дочерних ресурсов по `network_id` (REST
  `GET /vpc/v1/networks/{id}/subnets` и т.п.).
- `Create` / `Update` / `Delete` / `Move` — async, возвращают `Operation`.
- `Move` — суффикс-action `:move` (`POST /vpc/v1/networks/{id}:move`) —
  перенос сети между проектами.

## Lifecycle

- `Create` — `project_id` обязателен и валидируется вызовом
  `iam.ProjectService.Get`; уникальность имени — в пределах проекта
  (`UNIQUE (project_id, name)`).
- `Update` — UpdateMask discipline: known-поля (`name` / `description` /
  `labels`) мутируют, immutable — `InvalidArgument`, неизвестные — `InvalidArgument`.
- `Delete` — async; если в сети остались `Subnet` —
  `FailedPrecondition "network is not empty"` (within-service FK
  `subnets.network_id → networks(id) ON DELETE RESTRICT`).

## Gotchas

- Default-SG: при `Create` сети может inline-создаваться default
  security-group (`KACHO_VPC_DEFAULT_SG_INLINE=true`) — `kacho-vpc-controllers`
  упразднён, эта логика теперь в `network.go::doCreate`.
- Инфра-поле `vpn_id` (24-bit data-plane id) **удалено** миграцией 0023
  (KAC-79/KAC-36) — underlay перешёл на kube-ovn, в публичной/internal-проекции
  Network его больше нет.
- `Move` не каскадит в compute — ссылки оттуда через границу сервиса
  переживаются как dangling-ref.
