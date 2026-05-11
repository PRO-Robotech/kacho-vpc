# 02 — Data Flows

Sequence-диаграммы реальных VPC-сценариев (то что **в коде**).

## Содержание

1. [Network create + inline default-SG](#1-network-create--inline-default-sg)
2. [Subnet create + CIDR overlap protection](#2-subnet-create--cidr-overlap-protection)
3. [Address allocate cascade (external)](#3-address-allocate-cascade-external)
4. [Address allocate (internal IP в Subnet)](#4-address-allocate-internal-ip-в-subnet)
5. [Cross-service: folder → cloud_id lookup](#5-cross-service-folder--cloud_id-lookup)
6. [Operations LRO worker](#6-operations-lro-worker)
7. [InternalWatchService outbox stream](#7-internalwatchservice-outbox-stream)
8. [Admin: set CloudPoolSelector](#8-admin-set-cloudpoolselector)

---

## 1. Network create + inline default-SG

```mermaid
sequenceDiagram
  autonumber
  participant U as Client
  participant H as NetworkHandler (gRPC)
  participant S as NetworkService
  participant RM as resource-manager (FolderClient)
  participant DB as pg-vpc

  U->>H: Create(folder_id, name, …)
  H->>S: Create
  S->>S: sync validate (NameVPC, labels, mask)
  S->>S: ids.NewID(PrefixNetwork) → "enp..."
  S->>DB: INSERT operation (sync, done=false)
  S-->>H: Operation{id, metadata:{networkId}}
  H-->>U: Operation

  rect rgb(255,247,230)
  Note over S: async worker — operations.Run
  S->>RM: FolderService.Get(folder_id)
  alt folder not found
    S->>DB: UPDATE operation done=true, error=NotFound
  else folder OK
    S->>DB: BEGIN
    S->>DB: INSERT networks (id, folder_id, name, …)
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
- Раньше default-SG создавал отдельный `kacho-vpc-controllers` reconciler-loop, наблюдая outbox. **В Phase 2 упразднён** — теперь inline в worker'е, если `KACHO_VPC_DEFAULT_SG_INLINE=true` (default). При `=false` шаги 5-9 на диаграмме (default-SG TX) пропускаются.
- Mapping: `ALREADY_EXISTS` на `networks_folder_id_name_key` UNIQUE(folder_id, name). Для остальных 6 ресурсов аналогичный UNIQUE добавлен миграцией `0002_resource_name_unique.sql` (partial, `WHERE name <> ''`).

---

## 2. Subnet create + CIDR overlap protection

```mermaid
sequenceDiagram
  autonumber
  participant U as Client
  participant S as SubnetService
  participant RM as resource-manager
  participant N as NetworkService.repo
  participant DB as pg-vpc

  U->>S: Create(folder_id, network_id, zone_id, v4_cidr_blocks, …)
  S->>S: sync validate:<br/>  NameVPC, ZoneId (required + existence via ZoneRegistry → zones table),<br/>  CIDR host-bits=0 (netip.Masked),<br/>  CIDR disjoint в массиве
  S-->>U: Operation{subnetId}

  rect rgb(255,247,230)
  S->>RM: FolderService.Get(folder_id)
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
сервис-level (см. `subnet.go:382-388`).

---

## 3. Address allocate cascade (external)

Главный нетривиальный flow. Подробнее в [`03-ipam.md`](03-ipam.md).

```mermaid
sequenceDiagram
  autonumber
  participant U as Client
  participant AS as AddressService
  participant RM as resource-manager
  participant ALC as AddressAllocator
  participant POOL as AddressPoolService
  participant DB as pg-vpc

  U->>AS: Create(folder_id, externalIpv4Spec:{zone_id})
  AS-->>U: Operation{addressId}

  rect rgb(255,247,230)
  AS->>RM: FolderService.Get(folder_id) → exists?
  AS->>DB: INSERT addresses (external_ipv4 spec, address="")

  AS->>ALC: AllocateExternalIP(addressID)
  ALC->>POOL: ResolvePoolForAddress(addressID)
  Note over POOL,DB: cascade resolve — см. Step 1..5 ниже
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
  Start[addressID] --> Step1
  Step1[Step 1: address_pool_address_override<br/>WHERE address_id = $aid] -->|hit| R1[matched_via: address_override]
  Step1 -->|miss| FetchAddr[Get Address →<br/>folder_id, zone_id<br/>+ network_id для internal]

  FetchAddr --> Step2
  Step2[Step 2: network_default<br/>WHERE network_id=$nid<br/>только для internal IP] -->|hit| R2[matched_via: network_default]
  Step2 -->|miss| Step3

  Step3[Step 3: cloud-label-selector]
  Step3 --> S3a[FolderService.Get folder_id → cloud_id]
  S3a --> S3b{has<br/>cloud_pool_selector?}
  S3b -->|no| Step4
  S3b -->|yes| S3c[FindBySelectorMatch:<br/>pool.selector_labels @> $sel<br/>AND zone_id=$zid OR NULL<br/>AND kind=$kind<br/>ORDER BY size_diff ASC, priority DESC]
  S3c -->|hit| R3[matched_via: label_selector]
  S3c -->|miss| Step4

  Step4[Step 4: zone_default<br/>WHERE zone_id=$zid AND kind AND is_default] -->|hit| R4[matched_via: zone_default]
  Step4 -->|miss| Step5

  Step5[Step 5: global_default<br/>WHERE zone_id IS NULL AND kind AND is_default] -->|hit| R5[matched_via: global_default]
  Step5 -->|miss| Fail[FailedPrecondition<br/>'no address pool resolved']
```

Match-семантика inverse-containment: `cloud_selector ⊆ pool.selector_labels` (pool описывает whitelist; cloud — подмножество).

---

## 4. Address allocate internal IP в Subnet

То же что external, но:
- Spec: `internal_ipv4_address_spec.subnet_id`.
- Cascade: пропускаются Step 1, 2 — IP берётся из CIDR Subnet, никакого pool'а.
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

## 5. Cross-service: folder → cloud_id lookup

Единственная межсервисная зависимость VPC. Используется в IPAM cascade
Step 3 (cloud-pool-selector).

```mermaid
sequenceDiagram
  participant POOL as AddressPoolService
  participant FC as FolderClient (gRPC adapter)
  participant RM as resource-manager :9090
  participant Retry as corelib/retry

  POOL->>FC: GetCloudID(folder_id)
  FC->>Retry: OnUnavailable(...)
  Retry->>RM: FolderService.Get(folder_id)
  alt success
    RM-->>Retry: Folder{cloud_id}
    Retry-->>FC: Folder
    FC-->>POOL: cloud_id
  else NotFound
    Retry-->>FC: nil + grpcErr NotFound
    FC-->>POOL: "" + nil error<br/>(NotFound пропускается, caller сам решает)
  else Unavailable
    Note over Retry: retry до достижения backoff cap
  end
```

`FolderClient` не сообщает NotFound — возвращает empty cloud_id. Caller
(cascade) сам трактует empty как "skip step".

---

## 6. Operations LRO worker

Шаблон для всех мутаций (Create/Update/Delete/Move/AddCidrBlocks/...).

```mermaid
sequenceDiagram
  participant H as Handler (gRPC)
  participant S as Service
  participant Ops as corelib/operations
  participant DB as pg-vpc

  H->>S: Create
  S->>Ops: New(PrefixOperationVPC, description, metadata)
  Ops-->>S: Operation{id:enp..., done:false}   # PrefixOperationVPC == PrefixNetwork == "enp"
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
остаётся в `done=false` навсегда (TODO: heartbeat / cleanup).

---

## 7. InternalWatchService outbox stream

Server-to-server. UI/TUI/CLI **не используют** (полят).

```mermaid
sequenceDiagram
  participant Subscriber as gRPC Client (server)
  participant W as InternalWatchService
  participant Conn as dedicated pgx.Conn
  participant DB as pg-vpc

  Subscriber->>W: Watch(from_sequence_no)
  W->>Conn: pgx.Connect(MigrateDSN) — dedicated conn вне pool (для LISTEN), под inner timeout
  W->>Conn: LISTEN vpc_outbox

  Note over W,DB: Catch-up: события с прошлого from_seq
  W->>DB: SELECT * FROM vpc_outbox WHERE seq > $from
  loop catchup rows
    W-->>Subscriber: Event{seq, type, id, op, payload}
  end

  loop forever
    W->>Conn: WaitForNotification(ctx)
    Conn->>W: pg_notify('vpc_outbox', '<sequence_no>')
    W->>DB: SELECT * FROM vpc_outbox WHERE seq = $1
    W-->>Subscriber: Event
  end

  Note over W: defer UNLISTEN + conn.Close() + release semaphore slot
```

Триггер `vpc_outbox_notify_trg` на INSERT шлёт `pg_notify`. Без этого
триггера watch будет догонять только при следующем catch-up.

---

## 8. Admin: set CloudPoolSelector

Admin переключает cloud на премиум-pool.

```mermaid
sequenceDiagram
  participant Admin as Admin (UI / curl)
  participant H as InternalCloudHandler
  participant S as AddressPoolService
  participant Repo as cloudSel
  participant DB as pg-vpc

  Admin->>H: SetPoolSelector(cloud_id, selector, set_by)
  H->>S: SetCloudPoolSelector
  S->>S: validate cloud_id non-empty
  S->>Repo: Set(cloud_id, selector, set_by)
  Repo->>DB: BEGIN
  Repo->>DB: INSERT cloud_pool_selector ON CONFLICT (cloud_id) DO UPDATE
  Repo->>DB: INSERT vpc_outbox (CloudPoolSelector UPDATED)
  Repo->>DB: COMMIT
  Repo-->>S: nil
  S-->>H: nil
  H-->>Admin: SetCloudPoolSelectorResponse{}
```

Effect: следующий `AllocateExternalIP` для **любого** Address из folder этого Cloud попадёт в cascade Step 3 с этим selector.

---

## Где смотреть исходник

| Поток | Код |
|---|---|
| Network create + default-SG | `internal/service/network.go::doCreate` |
| Subnet create + CIDR | `internal/service/subnet.go::doCreate` |
| Address create | `internal/service/address.go::doCreate` |
| Cascade resolve | `internal/service/address_pool_service.go::resolveWithRunnerUp` |
| AllocateExternalIP retry-loop | `internal/service/address.go::AllocateExternalIP` (аллокатор inlined из бывшего `address_allocate.go`) |
| `isUniqueViolation` / двухфазный sweep | `internal/service/address.go` (бенчмарки — `address_allocate_bench_test.go`) |
| FolderClient.GetCloudID | `internal/clients/resourcemanager_client.go` |
| Operations worker | `kacho-corelib/operations/run.go` |
| Outbox + LISTEN/NOTIFY | `internal/handler/internal_watch_handler.go` |
