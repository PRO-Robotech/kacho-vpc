---
level: functionality
repo: kacho-vpc
anchors:
  - rpc: kacho.cloud.vpc.v1.InternalAddressService/AllocateExternalIP
  - rpc: kacho.cloud.vpc.v1.InternalAddressService/AllocateInternalIP
  - rpc: kacho.cloud.vpc.v1.InternalAddressService/AllocateInternalIPv6
  - rpc: kacho.cloud.vpc.v1.InternalAddressService/ClearAddressReference
  - rpc: kacho.cloud.vpc.v1.InternalAddressService/GetAddressReference
  - rpc: kacho.cloud.vpc.v1.InternalAddressService/MarkAddressEphemeralInUse
  - rpc: kacho.cloud.vpc.v1.InternalAddressService/SetAddressReference
  - rpc: kacho.cloud.vpc.v1.InternalCloudService/GetPoolSelector
  - rpc: kacho.cloud.vpc.v1.InternalCloudService/SetPoolSelector
  - rpc: kacho.cloud.vpc.v1.InternalCloudService/UnsetPoolSelector
  - rpc: kacho.cloud.vpc.v1.InternalNetworkService/SetDefaultSecurityGroupId
  - rpc: kacho.cloud.vpc.v1.InternalWatchService/Watch
status: implemented
source_sha: ""
---

# Internal projections

Internal-only сервисы (port 9091) для control-plane-компонентов: IPAM-allocate
API, per-Cloud pool-selector, admin-проекция Network и устаревший Watch.
Не публикуются на external TLS (§Запрет #6).

## Зачем

Часть операций нужна не tenant'у, а другим сервисам Kachō (compute, NLB,
api-gateway, data-plane impl). Они вынесены в `Internal*`-сервисы на
cluster-internal listener — там, где допустимы инфра-чувствительные данные и
admin-семантика.

## Контракт

### `InternalAddressService` — IPAM allocate + reference-mgmt

- `AllocateInternalIP` / `AllocateInternalIPv6` — выделить эфемерный internal
  IP (v4/v6) из `Subnet`.
- `AllocateExternalIP` — выделить эфемерный external IP из `AddressPool`
  (через resolution chain).
- `SetAddressReference` / `ClearAddressReference` / `GetAddressReference` —
  пометить/снять/прочитать `used_by={id,kind}` адреса; `Set` — атомарный CAS.
- `MarkAddressEphemeralInUse` — отметить эфемерный адрес занятым (compute
  NIC-flow).

### `InternalCloudService` — per-Cloud pool selector

- `SetPoolSelector` / `UnsetPoolSelector` / `GetPoolSelector` — назначить/
  снять/прочитать default-`AddressPool` для cloud (звено resolution-chain).

### `InternalNetworkService` — admin-проекция Network

- `SetDefaultSecurityGroupId` — связать default-SG с `Network`. Инфра-поле
  `vpn_id` из этой проекции **удалено** миграцией 0023 (KAC-79/KAC-36,
  переход на kube-ovn).

### `InternalWatchService` — **deprecated**

- `Watch` — server-streaming `Event`. Выкинут с 1.0 (§«API contract — flat
  resources + Operations»): клиенты перешли на List-polling 2–5 с и
  `OperationService.Get`. Proto-файл оставлен для backward-compat, регистрация
  в api-gateway убрана.

## Gotchas

- Эфемерные адреса аллоцируются здесь, но живут в той же таблице, что и
  статические `Address` (см. `l2-addresses`) — разница в lifecycle, не в схеме.
- `SetAddressReference` под CAS — защита от двойного назначения одного IP
  (§Запрет #10).
- Эти RPC доступны только service→service / admin-UI через internal-mux;
  на external TLS их нет.
