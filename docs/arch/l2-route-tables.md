---
level: functionality
repo: kacho-vpc
anchors:
  - rpc: kacho.cloud.vpc.v1.RouteTableService/Create
  - rpc: kacho.cloud.vpc.v1.RouteTableService/Delete
  - rpc: kacho.cloud.vpc.v1.RouteTableService/Get
  - rpc: kacho.cloud.vpc.v1.RouteTableService/List
  - rpc: kacho.cloud.vpc.v1.RouteTableService/ListOperations
  - rpc: kacho.cloud.vpc.v1.RouteTableService/Move
  - rpc: kacho.cloud.vpc.v1.RouteTableService/Update
status: implemented
source_sha: ""
---

# Route tables

CRUD `RouteTable` — набора статических маршрутов, ассоциируемого с подсетями
для управления L3-маршрутизацией внутри сети.

## Зачем

`RouteTable` задаёт, куда направлять трафик за пределы локальной подсети:
каждый static-route — это `destination_prefix` → next-hop (IP-адрес или
`Gateway`). Ассоциация route-table с `Subnet` определяет, какие маршруты
применяются к ресурсам подсети.

## Контракт

- `Get` / `List` / `ListOperations` — sync read.
- `Create` / `Update` / `Delete` / `Move` — async, возвращают `Operation`.
- `Create` принимает static-routes inline в запросе.
- `Update` — UpdateMask: либо полная замена списка маршрутов, либо
  add/remove отдельных маршрутов через mask-пути.
- `Move` — суффикс-action `:move`, cross-project перенос.

## Lifecycle

- `Create` — `network_id` обязателен; next-hop-`Gateway` (если указан)
  валидируется как within-service ссылка.
- `Update` — UpdateMask discipline: known-поля мутируют, immutable
  (`network_id`) — `InvalidArgument`.
- `Delete` — `FailedPrecondition`, если route-table ассоциирована хотя бы
  с одной подсетью (within-service FK).

## Gotchas

- Маршрут с next-hop-`Gateway` создаёт within-service ссылку
  `RouteTable → Gateway` — удаление gateway, на который ссылается route-table,
  блокируется на DB-уровне.
- `Move` между проектами не переносит ассоциированные подсети — ассоциация
  рвётся, это by-design.
- Конфликт `destination_prefix` внутри одной route-table проверяется при
  валидации запроса (нет двух маршрутов на один и тот же префикс).

## Связанные заметки

<!-- archgraph:links -->
- ↑ Приклад: [[_l1-kacho-vpc]]
- ↓ Функции (call-дерево, RPC contract): [[l3-l2-route-tables]]
- Переменные: [[l4-kacho-vpc]]
<!-- /archgraph:links -->
