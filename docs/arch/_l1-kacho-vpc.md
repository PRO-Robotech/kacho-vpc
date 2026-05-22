---
level: container
repo: kacho-vpc
---

# kacho-vpc — приклад

`kacho-vpc` — сетевой сервис платформы Kachō, канонический владелец доменных
ресурсов виртуальной сети: **Network / Subnet / SecurityGroup / RouteTable /
Address / Gateway / PrivateEndpoint / NetworkInterface**. Сверх control-plane
CRUD сервис несёт IPAM (выделение external/internal IPv4/IPv6 из подсетей и
admin-пулов), kacho-only ресурс `AddressPool` с resolution-chain, а также
internal-проекции ресурсов для control-plane-компонентов (compute, api-gateway,
data-plane impl-controller).

## Зона ответственности

- **L2/L3-топология** — `Network` (изолированный VPC внутри проекта), `Subnet`
  (CIDR-блок в зоне; v4/v6 dual-stack, EXCLUDE-constraint на пересечение),
  `RouteTable` (статическая маршрутизация, ассоциация с подсетями).
- **Connectivity / egress** — `Gateway` (shared-egress NAT-gateway),
  `PrivateEndpoint` (privatelink — приватный вход к сервису внутри подсети).
- **Безопасность** — `SecurityGroup` (stateful firewall-правила; OCC через
  `xmin`), default-SG на сеть.
- **Адресация / IPAM** — `Address` (статический/эфемерный external/internal IP),
  `AddressPool` (admin-управляемый пул CIDR'ов с per-Cloud / per-Network /
  per-Address resolution chain).
- **Attach-модель** — `NetworkInterface` — first-class ресурс (AWS-ENI-стиль;
  **структурное расхождение с YC**, где NIC inline в Instance — эпик KAC-2):
  создаётся в подсети, attach/detach к Compute.Instance под атомарным CAS.

Все мутации возвращают `operation.Operation` (async LRO — §Запрет #9); чтения
синхронны. Инфра-чувствительные и admin-операции вынесены в `Internal*`-сервисы
на internal-listener (port 9091) и не публикуются на external TLS (§Запрет #6).

## Контракт — 14 gRPC-сервисов

| Сервис | Назначение |
|---|---|
| `NetworkService` | CRUD сетей + nav-helpers (subnets/SG/RT) + `Move` |
| `SubnetService` | CRUD подсетей + CIDR-блоки + cross-zone `Relocate` + `Move` |
| `SecurityGroupService` | CRUD security-group + bulk/single rule-update + `Move` |
| `RouteTableService` | CRUD route-table со static-routes + `Move` |
| `AddressService` | CRUD адресов + `GetByValue` / `ListBySubnet` + `Move` |
| `GatewayService` | CRUD egress-gateway + `Move` |
| `PrivateEndpointService` | CRUD privatelink-эндпоинтов |
| `NetworkInterfaceService` | CRUD NIC + `AttachToInstance` / `DetachFromInstance` |
| `InternalAddressPoolService` | admin CRUD AddressPool + bind/resolution/utilization |
| `InternalAddressService` | IPAM allocate (external/internal v4/v6) + reference-mgmt |
| `InternalCloudService` | per-Cloud AddressPool selector (resolution chain) |
| `InternalNetworkService` | admin-projection Network — `SetDefaultSecurityGroupId` |
| `InternalWatchService` | server-streaming Watch — **deprecated с 1.0** |

Плюс общий `kacho.cloud.operation.OperationService` (`Get` / `Cancel`) для
поллинга async-операций.

**Видимость.** Public-listener (9090): Network / Subnet / SecurityGroup /
RouteTable / Address / Gateway / PrivateEndpoint / NetworkInterface. Внешний
клиент мутирует ресурсы и поллит `OperationService`. Internal-listener (9091, не
на external TLS — §Запрет #6): InternalAddressPool / InternalAddress /
InternalCloud / InternalNetwork / InternalWatch — доступны control-plane
сервисам, admin-UI и тулингу через api-gateway internal-mux (`vpcInternalAddr`
блок).

## Связи

`kacho-vpc` — **consumer** домена Geography и **consumer** домена IAM; сам он —
владелец сетевых ресурсов, в него звонят compute и api-gateway.

**kacho-vpc зовёт (исходящие runtime-рёбра):**

- `kacho-vpc → kacho-compute` — `compute.v1.ZoneService.Get` — валидация
  `zone_id` на request-path `Subnet.Create` / `Relocate` (Geography — домен
  compute, эпик KAC-15; раньше зоны были в vpc, ребро инвертировано).
- `kacho-vpc → kacho-iam` — `ProjectService.Get` (валидация `project_id`,
  заменившего старый `folder_id`) + `InternalIAMService.Check` (per-RPC
  authorization-gate перед мутацией ресурса).

**Кто зовёт kacho-vpc:**

- `kacho-compute → kacho-vpc` — валидация NIC-spec (`SubnetService.Get` /
  `SecurityGroupService.Get`), IPAM эфемерных адресов
  (`InternalAddressService.AllocateInternalIP` / `AllocateExternalIP`,
  `SetAddressReference`).
- `kacho-vpc-implement → kacho-vpc` — write-back data-plane-state
  `InternalNetworkInterfaceService.ReportNiDataplane` (эпик KAC-2; proto-surface
  in-progress).
- `kacho-api-gateway → kacho-vpc` — проксирование public RPC на TLS-edge и
  internal RPC (`Internal*`) на cluster-internal listener.

**Циклы запрещены.** Ребро `kacho-compute → kacho-vpc` (proxy зон) было удалено
и заменено на `kacho-vpc → kacho-compute` (валидация zone_id) — A↔B не
допускается (§«Кросс-доменные ссылки на ресурсы»).

## Связанные заметки

<!-- archgraph:links -->
- ↑ Проект: [[_l0-kacho]]
- ↓ Функциональность: [[l2-addresses]]
- ↓ Функциональность: [[l2-address-pools]]
- ↓ Функциональность: [[l2-gateways]]
- ↓ Функциональность: [[l2-internal-projections]]
- ↓ Функциональность: [[l2-network-interfaces]]
- ↓ Функциональность: [[l2-network-lifecycle]]
- ↓ Функциональность: [[l2-operations]]
- ↓ Функциональность: [[l2-private-endpoints]]
- ↓ Функциональность: [[l2-route-tables]]
- ↓ Функциональность: [[l2-security-groups]]
- ↓ Функциональность: [[l2-subnet-lifecycle]]
- Переменные: [[l4-kacho-vpc]]
<!-- /archgraph:links -->
