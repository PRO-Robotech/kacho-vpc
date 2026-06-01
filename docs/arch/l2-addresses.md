---
level: functionality
repo: kacho-vpc
anchors:
  - rpc: kacho.cloud.vpc.v1.AddressService/Create
  - rpc: kacho.cloud.vpc.v1.AddressService/Delete
  - rpc: kacho.cloud.vpc.v1.AddressService/Get
  - rpc: kacho.cloud.vpc.v1.AddressService/GetByValue
  - rpc: kacho.cloud.vpc.v1.AddressService/List
  - rpc: kacho.cloud.vpc.v1.AddressService/ListBySubnet
  - rpc: kacho.cloud.vpc.v1.AddressService/ListOperations
  - rpc: kacho.cloud.vpc.v1.AddressService/Move
  - rpc: kacho.cloud.vpc.v1.AddressService/Update
status: implemented
source_sha: ""
---

# Addresses

CRUD `Address` — статического/резервируемого IP (external из `AddressPool`,
internal из `Subnet`, v4/v6) — плюс lookup по значению и по подсети.

## Зачем

`Address` — tenant-facing проекция выделенного IP: то, что пользователь видит
и которым владеет. Статический внешний адрес можно зарезервировать заранее и
переиспользовать; эфемерные адреса (выделяемые на лету под NIC) живут в той же
таблице, но создаются через `InternalAddressService` — см. `l2-internal-projections`.

## Контракт

- `Get` / `List` / `ListOperations` — sync read.
- `GetByValue` — sync lookup по самому IP (`GET /vpc/v1/addresses:byValue?address=…`).
- `ListBySubnet` — sync перечисление адресов конкретной подсети.
- `Create` / `Update` / `Delete` / `Move` — async, возвращают `Operation`.
- `Update` мутирует name/labels/description и reserved-флаг.
- `Move` — суффикс-action `:move`, cross-project перенос.

## Lifecycle

- `Create` — oneof: external (v4) из `AddressPool` либо internal (v4/v6) из
  `Subnet`; IPAM выбирает свободный IP. `CHECK (external_ipv4 IS NOT NULL OR
  internal_ipv4 IS NOT NULL OR internal_ipv6 IS NOT NULL)`.
- `Update` — UpdateMask discipline.
- `Delete` — `FailedPrecondition`, если адрес `used_by` (назначен NIC / NLB /
  PrivateEndpoint).

## Gotchas

- Уникальность external-IP в пуле — partial `UNIQUE INDEX
  addresses_external_pool_ip_uniq … WHERE (external_ipv4 ->> 'address') <> ''`:
  один IP — максимум один `Address` (§Запрет #10).
- `used_by` — best-effort usage-hint; назначение/освобождение reference
  делает `InternalAddressService.SetAddressReference` / `ClearAddressReference`
  под атомарным CAS.
- Числовой data-plane-идентификатор адреса — инфра-инфа, на публичной
  проекции не отдаётся (§«Инфра-чувствительные данные»).

## Связанные заметки

<!-- archgraph:links -->
- ↑ Приклад: [[_l1-kacho-vpc]]
- ↓ Функции (call-дерево, RPC contract): [[l3-l2-addresses]]
- Переменные: [[l4-kacho-vpc]]
<!-- /archgraph:links -->
