---
level: functionality
repo: kacho-vpc
anchors:
  - rpc: kacho.cloud.vpc.v1.InternalAddressPoolService/BindAsAddressOverride
  - rpc: kacho.cloud.vpc.v1.InternalAddressPoolService/BindAsNetworkDefault
  - rpc: kacho.cloud.vpc.v1.InternalAddressPoolService/Check
  - rpc: kacho.cloud.vpc.v1.InternalAddressPoolService/Create
  - rpc: kacho.cloud.vpc.v1.InternalAddressPoolService/Delete
  - rpc: kacho.cloud.vpc.v1.InternalAddressPoolService/ExplainResolution
  - rpc: kacho.cloud.vpc.v1.InternalAddressPoolService/Get
  - rpc: kacho.cloud.vpc.v1.InternalAddressPoolService/GetUtilization
  - rpc: kacho.cloud.vpc.v1.InternalAddressPoolService/List
  - rpc: kacho.cloud.vpc.v1.InternalAddressPoolService/ListAddresses
  - rpc: kacho.cloud.vpc.v1.InternalAddressPoolService/UnbindAddressOverride
  - rpc: kacho.cloud.vpc.v1.InternalAddressPoolService/UnbindNetworkDefault
  - rpc: kacho.cloud.vpc.v1.InternalAddressPoolService/Update
status: implemented
source_sha: ""
---

# Address pools

Admin-CRUD `AddressPool` — kacho-only ресурса (нет в YC): именованного набора
CIDR-блоков, из которых IPAM выделяет external-адреса, плюс bind-механика
resolution-chain и observability.

## Зачем

`AddressPool` — административный пул публичных/external CIDR'ов. Когда tenant
создаёт external `Address`, IPAM должен знать, **из какого пула** брать IP —
это решает resolution chain: per-Address override → per-Network default →
per-Cloud selector → глобальный default. AddressPool — internal-only ресурс
(admin), не виден tenant'у напрямую.

## Контракт — все sync (port 9091)

- `Create` / `Get` / `List` / `Update` / `Delete` — CRUD пула; `Create` /
  `Update` принимают CIDR-list (v4/v6 split, KAC-71; `Update` —
  `replace_cidrs`-семантика).
- `BindAsNetworkDefault` / `UnbindNetworkDefault` — назначить/снять пул как
  default для конкретной `Network`.
- `BindAsAddressOverride` / `UnbindAddressOverride` — pin пула для конкретного
  `Address`.
- `Check` — есть ли пул, обслуживающий заданную IP-family.
- `ExplainResolution` — трассировка resolution-chain: какой пул будет выбран
  и почему.
- `ListAddresses` — какие `Address` выделены из пула.
- `GetUtilization` — free/used per-CIDR.

## Lifecycle

- `Create` — пул с list CIDR'ов; `Update` заменяет CIDR-list.
- `Delete` — `FailedPrecondition` (RESTRICT), если пул связан bind'ом или из
  него выделены адреса.
- Bind/Unbind — идемпотентны.

## Gotchas

- Весь сервис — **internal-only**: маршруты `/vpc/v1/addressPools/*`
  зарегистрированы только на internal-listener api-gateway (`vpcInternalAddr`
  блок), не на external TLS (§Запрет #6).
- Resolution chain — частично пересекается с `InternalCloudService` (per-Cloud
  selector) — см. `l2-internal-projections`.
- Аллокация IP из пула под concurrency — `FOR UPDATE SKIP LOCKED LIMIT 1` +
  `DELETE … RETURNING` по `address_pool_free_ips` (миграция 0015).
