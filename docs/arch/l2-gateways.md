---
level: functionality
repo: kacho-vpc
anchors:
  - rpc: kacho.cloud.vpc.v1.GatewayService/Create
  - rpc: kacho.cloud.vpc.v1.GatewayService/Delete
  - rpc: kacho.cloud.vpc.v1.GatewayService/Get
  - rpc: kacho.cloud.vpc.v1.GatewayService/List
  - rpc: kacho.cloud.vpc.v1.GatewayService/ListOperations
  - rpc: kacho.cloud.vpc.v1.GatewayService/Move
  - rpc: kacho.cloud.vpc.v1.GatewayService/Update
status: implemented
source_sha: ""
---

# Gateways

CRUD `Gateway` — egress-шлюза, через который ресурсы подсети получают
исходящий доступ во внешнюю сеть.

## Зачем

`Gateway` (тип `shared_egress_gateway`) — точка выхода трафика наружу:
маршрут в `RouteTable` указывает gateway как next-hop, и трафик за пределы
сети идёт через него (NAT-egress). Это позволяет инстансам без собственного
external-адреса иметь исходящий доступ в интернет.

## Контракт

- `Get` / `List` / `ListOperations` — sync read.
- `Create` / `Update` / `Delete` / `Move` — async, возвращают `Operation`.
- `Create` принимает тип шлюза (`shared_egress_gateway`).
- `Update` мутирует name/labels/description.
- `Move` — суффикс-action `:move`, cross-project перенос.

## Lifecycle

- `Create` — async; `project_id` валидируется через `iam.ProjectService.Get`.
- `Update` — UpdateMask discipline.
- `Delete` — `FailedPrecondition`, если на gateway ссылается хотя бы один
  static-route в `RouteTable` (within-service ссылка).

## Gotchas

- `Gateway → RouteTable` — within-service зависимость: удаление шлюза, на
  который смотрит маршрут, блокируется на DB-уровне (§Запрет #10), а не
  software-проверкой.
- Egress-gateway — control-plane-ресурс; фактический NAT-data-plane
  программируется отдельным слоем (kube-ovn / impl), детали wiring — internal.
