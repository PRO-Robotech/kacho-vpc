---
level: functionality
repo: kacho-vpc
anchors:
  - rpc: kacho.cloud.vpc.v1.NetworkInterfaceService/AttachToInstance
  - rpc: kacho.cloud.vpc.v1.NetworkInterfaceService/Create
  - rpc: kacho.cloud.vpc.v1.NetworkInterfaceService/Delete
  - rpc: kacho.cloud.vpc.v1.NetworkInterfaceService/DetachFromInstance
  - rpc: kacho.cloud.vpc.v1.NetworkInterfaceService/Get
  - rpc: kacho.cloud.vpc.v1.NetworkInterfaceService/List
  - rpc: kacho.cloud.vpc.v1.NetworkInterfaceService/ListOperations
  - rpc: kacho.cloud.vpc.v1.NetworkInterfaceService/Update
status: implemented
source_sha: ""
---

# Network interfaces

CRUD `NetworkInterface` (NIC) — first-class ресурса сетевого подключения —
плюс attach/detach к Compute.Instance под атомарным CAS.

## Зачем

`NetworkInterface` — точка подключения инстанса к подсети: несёт primary
IPv4-адрес, ссылку на `Subnet` и список `SecurityGroup`. **Структурное
расхождение с YC** (где NIC inline в Instance) — в Kachō NIC сделан
first-class ресурсом AWS-ENI-стиля (эпик KAC-2): NIC можно создать заранее,
attach к одному инстансу, detach и переиспользовать.

## Контракт

- `Get` / `List` / `ListOperations` — sync read.
- `Create` / `Update` / `Delete` — async.
- `AttachToInstance` — async (`:attach`) — привязка NIC к Compute.Instance.
- `DetachFromInstance` — async (`:detach`) — отвязка.
- `Update` мутирует name/labels/description и список SG.

## Lifecycle

- `Create` — `subnet_id` обязателен; SG-список валидируется; primary-IP
  выделяется IPAM через `InternalAddressService`.
- `AttachToInstance` — атомарный CAS на `used_by_id`:
  `UPDATE network_interfaces SET used_by_id=$new WHERE id=$id AND
  (used_by_id='' OR used_by_id=$new) RETURNING …`. 0 rows → `FailedPrecondition`.
- `DetachFromInstance` — CAS назад к `used_by_id=''`.
- `Delete` — `FailedPrecondition`, если NIC ещё attached.

## Gotchas

- Attach-race (инцидент 2026-05-14, KAC-52): два `Instance.Create` указали
  один NIC, software-`if used_by_id != ""` пропустил оба, second-writer-wins.
  Фикс — single-statement CAS (миграция 0017; ошибочный `UNIQUE`-backstop из
  0016 откачен — для multi-NIC он семантически неверен). Покрыто
  `network_interface_attach_race_integration_test.go`.
- `InternalNetworkInterfaceService.ReportNiDataplane` (write-back из
  `kacho-vpc-implement`) — internal-проекция с инфра-полями (`hv_id`, `sid`,
  `host_iface`, …); proto-surface in-progress (KAC-2).
- placement / SID / host-iface NIC — инфра-чувствительны, на публичной
  проекции не отдаются (§«Инфра-чувствительные данные»).
