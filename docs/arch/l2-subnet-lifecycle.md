---
level: functionality
repo: kacho-vpc
anchors:
  - rpc: kacho.cloud.vpc.v1.SubnetService/AddCidrBlocks
  - rpc: kacho.cloud.vpc.v1.SubnetService/Create
  - rpc: kacho.cloud.vpc.v1.SubnetService/Delete
  - rpc: kacho.cloud.vpc.v1.SubnetService/Get
  - rpc: kacho.cloud.vpc.v1.SubnetService/List
  - rpc: kacho.cloud.vpc.v1.SubnetService/ListOperations
  - rpc: kacho.cloud.vpc.v1.SubnetService/ListUsedAddresses
  - rpc: kacho.cloud.vpc.v1.SubnetService/Move
  - rpc: kacho.cloud.vpc.v1.SubnetService/Relocate
  - rpc: kacho.cloud.vpc.v1.SubnetService/RemoveCidrBlocks
  - rpc: kacho.cloud.vpc.v1.SubnetService/Update
status: implemented
source_sha: ""
---

# Subnet lifecycle

CRUD подсети (`Subnet`) — CIDR-блока внутри `Network`, привязанного к зоне —
плюс динамическое управление CIDR-списком, cross-zone `Relocate` и
IPAM-utilization read.

## Зачем

`Subnet` — единица L3-адресации: несёт один или несколько CIDR-блоков (v4/v6
dual-stack, KAC-71 split), из которых IPAM выделяет адреса для `NetworkInterface`
и `Address`. Подсеть живёт в зоне (`zone_id`) — Geography-домен `kacho-compute`.

## Контракт

- `Get` / `List` / `ListUsedAddresses` / `ListOperations` — sync read.
  `ListUsedAddresses` отдаёт IPAM-utilization (какие IP заняты).
- `Create` / `Update` / `Delete` / `Move` — async.
- `AddCidrBlocks` / `RemoveCidrBlocks` — async суффикс-actions: расширение и
  сужение списка CIDR (`:add-cidr-blocks` / `:remove-cidr-blocks`) — отдельно
  от `Update`, который трогает только name/labels/description.
- `Relocate` — async (`:relocate`) — перенос подсети между зонами (KAC-15).

## Lifecycle

- `Create` — `network_id` обязателен; `zone_id` валидируется вызовом
  `compute.ZoneService.Get`; CIDR-блок не должен пересекаться с другими
  подсетями сети — гарантия на DB-уровне через `EXCLUDE USING gist`
  (`subnets_no_overlap_v4`).
- `RemoveCidrBlocks` — проверка, что в удаляемом диапазоне нет выделенных
  адресов; иначе `FailedPrecondition`.
- `Delete` — async; FK `addresses → subnets ON DELETE RESTRICT` —
  при наличии `Address` удаление блокируется (`FailedPrecondition`).

## Gotchas

- Пересечение CIDR ловится EXCLUDE-constraint'ом, **не** software-проверкой —
  это within-service инвариант на DB-уровне (§Запрет #10), race-proof.
- `zone_id` — cross-service ref: FK невозможен (database-per-service);
  валидируется на request-path, на чтении dangling-ref переживается
  деградированным статусом.
- `Relocate` меняет зону — публикуется как async-операция, поскольку требует
  переноса data-plane-wiring.
