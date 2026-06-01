---
level: functionality
repo: kacho-vpc
anchors:
  - rpc: kacho.cloud.vpc.v1.privatelink.PrivateEndpointService/Create
  - rpc: kacho.cloud.vpc.v1.privatelink.PrivateEndpointService/Delete
  - rpc: kacho.cloud.vpc.v1.privatelink.PrivateEndpointService/Get
  - rpc: kacho.cloud.vpc.v1.privatelink.PrivateEndpointService/List
  - rpc: kacho.cloud.vpc.v1.privatelink.PrivateEndpointService/ListOperations
  - rpc: kacho.cloud.vpc.v1.privatelink.PrivateEndpointService/Update
status: implemented
source_sha: ""
---

# Private endpoints

CRUD `PrivateEndpoint` — privatelink-входа: приватного IP внутри подсети,
через который ресурсы tenant'а достигают сервис, не выходя во внешнюю сеть.

## Зачем

PrivateEndpoint реализует privatelink-паттерн: вместо публичного доступа к
сервису (managed-сервис, сторонний endpoint) tenant получает приватный IP в
своей подсети, и трафик к сервису идёт по внутренней сети. Это часть
proto-пакета `kacho.cloud.vpc.v1.privatelink` — выделенного sub-domain.

## Контракт

- `Get` / `List` / `ListOperations` — sync read.
- `Create` / `Update` / `Delete` — async, возвращают `Operation`.
- `Create` принимает `subnet_id` и kind целевого сервиса.
- `Update` мутирует description, labels, привязанный address.
- Нет `Move` — endpoint жёстко привязан к подсети.

## Lifecycle

- `Create` — `subnet_id` обязателен; под endpoint выделяется `Address`
  (privatelink-IP) из подсети.
- `Update` — UpdateMask discipline.
- `Delete` — async; FK на `Address` с `ON DELETE RESTRICT` — IP освобождается
  как часть удаления endpoint'а.

## Gotchas

- `PrivateEndpoint → Address` — within-service RESTRICT-FK: нельзя удалить
  адрес, пока на нём висит endpoint.
- Sub-package `privatelink` — отдельное proto-namespace внутри
  `kacho.cloud.vpc.v1`; FQN сервиса — `…vpc.v1.privatelink.PrivateEndpointService`.
- REST-пути — `/vpc/v1/endpoints/*`.

## Связанные заметки

<!-- archgraph:links -->
- ↑ Приклад: [[_l1-kacho-vpc]]
- ↓ Функции (call-дерево, RPC contract): [[l3-l2-private-endpoints]]
- Переменные: [[l4-kacho-vpc]]
<!-- /archgraph:links -->
