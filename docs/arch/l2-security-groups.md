---
level: functionality
repo: kacho-vpc
anchors:
  - rpc: kacho.cloud.vpc.v1.SecurityGroupService/Create
  - rpc: kacho.cloud.vpc.v1.SecurityGroupService/Delete
  - rpc: kacho.cloud.vpc.v1.SecurityGroupService/Get
  - rpc: kacho.cloud.vpc.v1.SecurityGroupService/List
  - rpc: kacho.cloud.vpc.v1.SecurityGroupService/ListOperations
  - rpc: kacho.cloud.vpc.v1.SecurityGroupService/Move
  - rpc: kacho.cloud.vpc.v1.SecurityGroupService/Update
  - rpc: kacho.cloud.vpc.v1.SecurityGroupService/UpdateRule
  - rpc: kacho.cloud.vpc.v1.SecurityGroupService/UpdateRules
status: implemented
source_sha: ""
---

# Security groups

CRUD `SecurityGroup` — stateful firewall-набора правил, ассоциируемого с
`NetworkInterface` — плюс bulk- и single-rule-мутации под OCC.

## Зачем

`SecurityGroup` — единица сетевой фильтрации: список ingress/egress-правил
(protocol / port-range / CIDR или ссылка на другую SG), применяемых к NIC.
Создаётся в пределах `Network`; одна SG может быть назначена многим NIC.

## Контракт

- `Get` / `List` / `ListOperations` — sync read.
- `Create` / `Update` / `Delete` / `Move` — async.
- `UpdateRules` — async bulk-мутация: добавить/удалить набор правил за раз
  (`PATCH /vpc/v1/securityGroups/{id}/rules`).
- `UpdateRule` — async мутация одного правила по `rule_id`
  (`PATCH .../rules/{rule_id}`).
- `Update` трогает только метаданные группы (name/labels/description);
  правила — через `UpdateRule(s)`.

## Lifecycle

- `Create` — `network_id` обязателен.
- `Update` / `UpdateRule(s)` — оптимистичная блокировка (OCC) через
  `xmin::text`-snapshot: клиент берёт `xmin` из `Get`, мутация —
  `UPDATE ... WHERE xmin::text = $expected`; конкурентный writer получает
  `Aborted` (integration-test `security_group_occ_integration_test.go`).
- `Delete` — `FailedPrecondition`, если SG ещё назначена какому-либо NIC.

## Gotchas

- OCC по `xmin` — без отдельной колонки версии; это within-service
  read-modify-write инвариант на DB-уровне (§Запрет #10).
- Правило может ссылаться на другую SG (`predefined_target` /
  `security_group_id`) — это within-network ссылка.
- `Move` между проектами — async; правила-ссылки на SG из другого проекта
  становятся невалидны — by-design.

## Связанные заметки

<!-- archgraph:links -->
- ↑ Приклад: [[_l1-kacho-vpc]]
- ↓ Функции (call-дерево, RPC contract): [[l3-l2-security-groups]]
- Переменные: [[l4-kacho-vpc]]
<!-- /archgraph:links -->
