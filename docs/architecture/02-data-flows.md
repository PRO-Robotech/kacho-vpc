# 02 — Data Flows

Sequence-диаграммы реальных VPC-сценариев (то что **в коде**).

## Содержание

1. [Network create + inline default-SG](#1-network-create--inline-default-sg)
2. [Subnet create + CIDR overlap protection](#2-subnet-create--cidr-overlap-protection)
3. [Address allocate cascade (external)](#3-address-allocate-cascade-external)
4. [Address allocate (internal IP в Subnet)](#4-address-allocate-internal-ip-в-subnet)
5. [Cross-service: project existence check](#5-cross-service-project-existence-check)
6. [Operations LRO worker](#6-operations-lro-worker)
7. [Outbox-журнал доменных событий (polling-модель)](#7-outbox-журнал-доменных-событий-polling-модель)
8. [NetworkInterface create](#8-networkinterface-create)
9. [Dependency / delete-blocking chain (NIC → Address → Subnet → Network)](#9-dependency--delete-blocking-chain)

---

## 1. Network create + inline default-SG

```mermaid
sequenceDiagram
  autonumber
  participant U as Client
  participant H as NetworkHandler (gRPC)
  participant S as NetworkService
  participant RM as kacho-iam (ProjectClient)
  participant DB as pg-vpc

  U->>H: Create(project_id, name, …)
  H->>S: Create
  S->>S: sync validate (NameVPC, labels, mask)
  S->>S: ids.NewID(PrefixNetwork) → "net..."
  S->>DB: INSERT operation (sync, done=false)
  S-->>H: Operation{id, metadata:{networkId}}
  H-->>U: Operation

  rect rgb(255,247,230)
  Note over S: async worker — operations.Run
  S->>RM: ProjectService.Get(project_id)
  alt project not found
    S->>DB: UPDATE operation done=true, error=NotFound
  else project OK
    S->>DB: BEGIN
    S->>DB: INSERT networks (id, project_id, name, …)
    S->>DB: INSERT vpc_outbox (Network, CREATED) → pg_notify
    S->>DB: COMMIT

    Note over S: inline default-SG — только при KACHO_VPC_DEFAULT_SG_INLINE=true (default)
    S->>S: short = first-8-chars(net_id)
    S->>DB: BEGIN
    S->>DB: INSERT security_groups (default-sg-{short}, network_id, default_for_network=true)
    S->>DB: UPDATE networks SET default_security_group_id=...
    S->>DB: INSERT vpc_outbox (SG CREATED, Network UPDATED)
    S->>DB: COMMIT

    S->>DB: UPDATE operation done=true, response=Network
  end
  end
```

Особенности:
- Default-SG создается inline в worker'е, если `KACHO_VPC_DEFAULT_SG_INLINE=true` (default). При `=false` шаги default-SG TX на диаграмме пропускаются.
- `Network` несет internal-only инфра-идентификатор `vrf_id` (отдается только через `InternalNetworkService`, на публичной поверхности его нет).
- Mapping: `ALREADY_EXISTS` на `networks_project_id_name_key` UNIQUE(project_id, name). Для остальных 6 ресурсов аналогичный partial UNIQUE `(project_id, name) WHERE name <> ''`.

---

## 2. Subnet create + CIDR overlap protection

```mermaid
sequenceDiagram
  autonumber
  participant U as Client
  participant S as SubnetService
  participant RM as kacho-iam
  participant N as NetworkService.repo
  participant DB as pg-vpc

  U->>S: Create(project_id, network_id, zone_id, v4_cidr_blocks, …)
  S->>S: sync validate:<br/>  NameVPC, ZoneId (required + existence via ZoneRegistry → zones table),<br/>  CIDR host-bits=0 (netip.Masked),<br/>  CIDR disjoint в массиве
  S-->>U: Operation{subnetId}

  rect rgb(255,247,230)
  S->>RM: ProjectService.Get(project_id)
  S->>N: networkRepo.Get(network_id)
  S->>S: ids.NewID(PrefixSubnet)

  S->>DB: INSERT subnets (включая v4_cidr_primary computed)

  alt CIDR overlap с другим Subnet в той же Network
    DB-->>S: 23P01 (EXCLUDE USING gist violation)
    S->>S: mapRepoErr → FailedPrecondition<br/>"Subnet CIDRs can not overlap"
    S->>DB: UPDATE operation error
  else success
    S->>DB: INSERT vpc_outbox (Subnet CREATED)
    S->>DB: UPDATE operation done=true, response
  end
  end
```

EXCLUDE constraint (`subnets_no_overlap_v4`) проверяет только
`v4_cidr_primary` (array[0]). Для `AddCidrBlocks` второй+ CIDR — защита
сервис-level (`networkRepo.List` cross-check в `subnet/add_cidr_blocks.go`).

---

## 3. Address allocate cascade (external)

Главный нетривиальный flow. Подробнее в [`03-ipam.md`](03-ipam.md).

```mermaid
sequenceDiagram
  autonumber
  participant U as Client
  participant AS as AddressService
  participant RM as kacho-iam
  participant ALC as AddressAllocator
  participant POOL as AddressPoolService
  participant DB as pg-vpc

  U->>AS: Create(project_id, externalIpv4Spec:{zone_id})
  AS-->>U: Operation{addressId}

  rect rgb(255,247,230)
  AS->>RM: ProjectService.Get(project_id) → exists?
  AS->>DB: INSERT addresses (external_ipv4 spec, address="")

  AS->>ALC: AllocateExternalIP(addressID)
  ALC->>POOL: ResolvePoolForAddress(addressID)
  Note over POOL,DB: cascade resolve — см. Step 1..3 ниже
  POOL-->>ALC: ResolvedPool{pool, matched_via}

  loop for attempt in 1..max
    ALC->>ALC: pickRandomIPv4(cidr) — exclude .0/.255
    ALC->>DB: UPDATE addresses SET external_ipv4.address=$ip,<br/>address_pool_id=$pool_id WHERE id=...
    alt UNIQUE violation (addresses_external_pool_ip_uniq)
      Note over ALC: continue → try другой IP
    else success
      ALC->>DB: INSERT vpc_outbox (Address UPDATED)
      AS->>DB: UPDATE operation done=true, response=Address
    end
  end

  alt все CIDR исчерпаны
    ALC-->>AS: ResourceExhausted "address pool X exhausted (no free IP in any cidr_block)"
    AS->>DB: UPDATE operation error
  end
  end
```

### Cascade resolve внутри `POOL.ResolvePoolForAddress`

```mermaid
flowchart TD
  Start[addressID] --> FetchAddr[Get Address →<br/>zone_id, kind, family<br/>+ network_id для external]

  FetchAddr --> Step1
  Step1[Step 1: network_default<br/>WHERE network_id=$nid] -->|hit| R1[matched_via: network_default]
  Step1 -->|miss| Step2

  Step2[Step 2: zone_default<br/>WHERE zone_id=$zid AND kind AND is_default] -->|hit| R2[matched_via: zone_default]
  Step2 -->|miss| Step3

  Step3[Step 3: global_default<br/>WHERE zone_id IS NULL AND kind AND is_default] -->|hit| R3[matched_via: global_default]
  Step3 -->|miss| Fail[FailedPrecondition<br/>'no address pool resolved']
```

На каждом шаге pool пропускается, если его CIDR-список для запрошенного family пуст
(family-aware фильтр).

---

## 4. Address allocate internal IP в Subnet

То же что external, но:
- Spec: `internal_ipv4_address_spec.subnet_id`.
- Cascade не нужен — IP берется прямо из CIDR Subnet, никакого pool'а.
- UNIQUE: `(internal_subnet_id, address)` — нельзя повторить IP в той же Subnet.

```mermaid
sequenceDiagram
  participant AS as AddressService
  participant ALC as AddressAllocator
  participant SUB as SubnetRepo
  participant DB as pg-vpc

  AS->>ALC: AllocateInternalIP(addressID)
  ALC->>SUB: Get(subnet_id) → cidr_blocks
  loop attempt in 1..max
    ALC->>ALC: pickRandomIPv4(cidr) — exclude .0/.255 + reserved (.1?)
    ALC->>DB: UPDATE addresses SET internal_ipv4.address=$ip
    alt UNIQUE violation
      continue
    else success
      ALC->>DB: INSERT vpc_outbox (Address UPDATED)
    end
  end
```

---

## 5. Cross-service: project existence check

Межсервисная зависимость VPC на `kacho-iam`. Используется на request-path
каждой Create-мутации — проверить, что владелец-проект существует
(`project_id` — legacy-имя колонки = id владельца-проекта).

```mermaid
sequenceDiagram
  participant S as <Resource>Service (worker)
  participant FC as ProjectClient (gRPC adapter)
  participant RM as kacho-iam :9090
  participant Retry as corelib/retry

  S->>FC: Exists(project_id)
  FC->>Retry: OnUnavailable(...)
  Retry->>RM: ProjectService.Get(project_id)
  alt success
    RM-->>Retry: Project
    Retry-->>FC: Project
    FC-->>S: true
  else NotFound
    Retry-->>FC: nil + grpcErr NotFound
    FC-->>S: false → NotFound "Project X not found"
  else Unavailable
    Note over Retry: retry до достижения backoff cap → Unavailable
  end
```

---

## 6. Operations LRO worker

Шаблон для всех мутаций (Create/Update/Delete/AddCidrBlocks/...).

```mermaid
sequenceDiagram
  participant H as Handler (gRPC)
  participant S as Service
  participant Ops as corelib/operations
  participant DB as pg-vpc

  H->>S: Create
  S->>Ops: New(PrefixOperationVPC, description, metadata)
  Ops-->>S: Operation{id:enp..., done:false}   # PrefixOperationVPC = "enp" (декаплен от ресурсных prefix'ов)
  S->>DB: opsRepo.Create(op)
  S->>Ops: Run(ctx, opsRepo, opID, fn doCreate)
  Note right of S: Run = sync trigger goroutine
  S-->>H: &Operation
  H-->>Client: Operation (HTTP 200)

  Note over Ops: goroutine крутит fn
  par async
    Ops->>S: doCreate(ctx)
    S->>DB: бизнес-работа
    alt success
      S-->>Ops: anypb.Any(Resource)
      Ops->>DB: UPDATE operations SET done=true, response=...
    else error
      S-->>Ops: error
      Ops->>DB: UPDATE operations SET done=true, error=...
    end
  and client polling
    Client->>H: OperationService.Get(opID)
    H->>DB: SELECT * FROM operations WHERE id=$1
    H-->>Client: Operation{done?, response?, error?}
  end
```

Worker — на той же поде, что сервис. Если pod крашится — операция
остается в `done=false` (восстановление прогресса — через повторный запрос клиента).

> **`ListOperations` переживает удаление ресурса.** Для Network/Subnet/Address/NetworkInterface
> `ListOperations` больше не требует существования ресурса (precondition `repo.Get` убран и из
> сервиса, и из хэндлера): жив → проверка project-ownership; `NotFound` → пропускаем и отдаем
> накопленные операции; прочие ошибки пробрасываются. У `operations`-строк нет FK-каскада —
> история сохраняется. (Для route_table/SG/gateway `ListOperations` по-прежнему
> гейтит на `repo.Get` — это существующее поведение, изменены только эти четыре.)

---

## 7. Outbox-журнал доменных событий (polling-модель)

Каждая мутация ресурса в той же транзакции пишет событие в `vpc_outbox`
(`resource_kind/resource_id/event_type/payload`). Триггер `vpc_outbox_notify_trg`
на INSERT шлет `pg_notify('vpc_outbox', sequence_no)` — это in-cluster
`LISTEN/NOTIFY`-канал.

**Публичного per-resource Watch RPC в Kachō нет.** Клиенты (UI/TUI/CLI и peer-сервисы)
узнают о состоянии через polling:

```mermaid
sequenceDiagram
  participant Client as gRPC Client
  participant VPC as kacho-vpc
  participant DB as pg-vpc

  Note over Client,VPC: мутация → Operation (async)
  Client->>VPC: Create/Update/Delete<Resource>
  VPC->>DB: INSERT ресурс + INSERT vpc_outbox (одна TX)
  VPC-->>Client: Operation{id, done=false}

  loop polling до done=true
    Client->>VPC: OperationService.Get(operation_id)
    VPC-->>Client: Operation{done?, result}
  end

  Note over Client,VPC: периодический re-List для актуального состояния
  Client->>VPC: List<Resource>
  VPC-->>Client: текущие ресурсы
```

`vpc_outbox` — транзакционный журнал доменных событий; `pg_notify` доступен
in-cluster-потребителям, но наружу как Watch RPC не публикуется.

---

## 8. NetworkInterface create

NIC — first-class ресурс. Может быть создан без адресов.

```mermaid
sequenceDiagram
  autonumber
  participant U as Client
  participant S as NetworkInterfaceService
  participant RM as kacho-iam
  participant DB as pg-vpc

  U->>S: Create(project_id, subnet_id, v4_address_ids?, v6_address_ids?, security_group_ids?)
  S->>S: sync validate; default security_group_ids = Subnet.Network.default_security_group_id если пусто
  S-->>U: Operation{networkInterfaceId}

  rect rgb(255,247,230)
  S->>RM: ProjectService.Get(project_id)
  S->>DB: subnetRepo.Get(subnet_id) → network_id → default_security_group_id
  S->>S: ids.NewID(PrefixNetworkInterface) → "nic..."
  S->>DB: BEGIN
  S->>DB: INSERT network_interfaces (id, project_id, subnet_id, sg_ids, status=PROVISIONING)
  loop по v4_address_ids[] / v6_address_ids[]
    S->>DB: проверить Address.used == false (referrer-free) → INSERT address_references (referrer_type="network_interface")
    S->>DB: UPDATE addresses SET used=true
  end
  S->>DB: INSERT vpc_outbox (NetworkInterface CREATED)
  S->>DB: COMMIT
  S->>DB: UPDATE operation done=true, response=NetworkInterface
  end
```

`used_by` — денормализованное зеркало «кто использует NIC» (`{compute_instance, <instance_id>}` —
flat-колонки `used_by_type`/`used_by_id`/`used_by_name`); ставится атомарным CAS на смену владельца.
Compute-Instance со своей стороны ссылается на NIC через `nic_id`.

---

## 9. Dependency / delete-blocking chain

NIC → Address → Subnet → Network — все RESTRICT. Удаление снизу вверх.

```mermaid
flowchart TD
  delNIC[Delete NetworkInterface] -->|"detach addresses (clear referrer rows) → DELETE"| okNIC[ok]
  delAddr[Delete Address] -->|"address.used == true (referrer = NIC)"| failAddr["FailedPrecondition<br/>'address ... is in use by network interface ...; detach it before deleting the address'"]
  delAddr -->|"not used"| okAddr[ok]
  delSub[Delete Subnet] -->|"has internal Address v4/v6 (AddressesBySubnet checks internal_ipv4 AND internal_ipv6)"| failSub1["FailedPrecondition<br/>'Subnet has allocated internal addresses'"]
  delSub -->|"has NetworkInterface"| failSub2["FailedPrecondition<br/>'subnet ... has N network interface(s) (...); delete them first'"]
  delSub -->|"empty"| okSub["ok — DB backstops: addresses_internal_subnet_fkey (generated col covers v4+v6), network_interfaces_subnet_id_fkey ON DELETE RESTRICT"]
  delNet[Delete Network] -->|"has subnets / route tables / non-default SGs"| failNet["FailedPrecondition<br/>'Network ... is not empty'"]
  delNet -->|"only default SG"| okNet["ok — Delete-worker auto-deletes the default SG"]
```

> `network_interfaces.subnet_id` — `ON DELETE RESTRICT`: NIC всегда блокирует свою подсеть.
> Generated-колонка `addresses.internal_subnet_id` выводится из `internal_ipv4` ИЛИ `internal_ipv6`,
> поэтому FK `addresses_internal_subnet_fkey` блокирует подсеть и для v4-, и для v6-internal-адресов.

---

## Где смотреть исходник

| Поток | Код |
|---|---|
| Network create + default-SG | `internal/apps/kacho/api/network/` (`create.go`, `default_sg.go`, `helpers.go`) |
| Subnet create + CIDR | `internal/apps/kacho/api/subnet/create.go` |
| Subnet :add/:remove-cidr-blocks (v4 + v6) | `internal/apps/kacho/api/subnet/add_cidr_blocks.go` / `remove_cidr_blocks.go` |
| Address create + internal v4/v6 | `internal/apps/kacho/api/address/create.go` |
| NetworkInterface CRUD | `internal/apps/kacho/api/networkinterface/` (`create.go`, `update.go`, ...) |
| Cascade resolve | `internal/apps/kacho/api/addresspool/resolve.go` |
| AllocateExternalIP / AllocateInternalIP / AllocateInternalIPv6 | `internal/apps/kacho/api/address/allocate.go` (аллокатор-константы — `create.go`; бенчмарки — `internal/repo/address_pool_freelist_bench_test.go`) |
| ProjectClient.Exists → ProjectClient (IAM) | `internal/clients/iam_client.go` (+ `project_cache.go`) |
| Operations worker | `kacho-corelib/operations/run.go` |
| Outbox emit (в writer-TX) + LISTEN/NOTIFY trigger | `internal/repo/helpers/outbox.go`, `internal/repo/kacho/pg/*` (триггер `vpc_outbox_notify_trg` — `internal/migrations/0001_initial.sql`) |
