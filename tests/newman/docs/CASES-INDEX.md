# newman — индекс уникальных кейсов

~745 кейсов / 240+ паттернов.

> v18 (KAC-38): + кейсы NetworkInterface (first-class ресурс, эпик KAC-2) — секция
> «NetworkInterface (NIC)» ниже; + v6-Subnet / optional-CIDR-Subnet / SG-без-network /
> NIC↔Subnet-RESTRICT / multi-resource delete-chain / operation-history-survives-delete /
> Network-public-проекция-без-vpn_id / v6-CIDR-через-verbs. Любой новый кейс ОБЯЗАН
> пройти `scripts/validate-cases.py` (дубль case-id и не-каталогизированный кейс →
> hard-fail в CI до newman; см. `CLAUDE.md` §14.3, агент `vpc-newman-author`).

> Из этого каталога выведен **нормативный регламент продуктовых требований** —
> `PRODUCT-REQUIREMENTS.md` (`REQ-*`: что продукт ДОЛЖЕН / НЕ ДОЛЖЕН; ведут тестировщики).
> Агент `vpc-yc-parity-auditor` проверяет соответствие регламенту при ревью изменений
> (см. его §3.13). Каждый кейс должен мапиться на `REQ-*` (поле `Validated-by` в регламенте).

> v16 (TODO #35): + 45 кейсов для internal/admin-only IPAM RPC —
> `internal-pool` (26: AddressPool CRUD + bindings + Check/ExplainResolution/Utilization/ListAddresses,
> prefix `IPL-*`), `internal-region-zone` (15: Region/Zone CRUD + FK-empty-checks, prefix `RGN-*`/`ZON-*`),
> `internal-cloud` (4: Cloud poolSelector set/get/unset, prefix `CLD-*`). Эти RPC возвращают
> ресурсы напрямую (не Operation); таблицы паттернов ниже их не индексируют (кейсы — в `cases/internal-*.py`).

## По методам

| RPC method | Паттернов | Описание |
|---|---|---|
| - | 6 | Cross-method |
| AddCidrBlocks | 10 | Subnet: добавить CIDR (вкл. v6) |
| AttachToInstance / DetachFromInstance | 1 | NIC: attach/detach (used_by) |
| Create | 92 | Создание (async, Operation) |
| Delete | 16 | Удаление (async) |
| Get | 14 | Чтение по id |
| GetByValue | 4 | Address: lookup по IP |
| Lifecycle | 1 | Полный CRUD-цикл |
| List | 28 | Листинг + pagination |
| ListBySubnet | 2 | Address: в подсети |
| ListOperations | 4 | Operations (вкл. survive-delete) |
| ListRouteTables | 2 | Network: RT |
| ListSecurityGroups | 2 | Network: SG |
| ListSubnets | 2 | Network: subnets |
| ListUsedAddresses | 2 | Subnet: использ. IP |
| Move | 6 | Move в другой folder |
| Relocate | 3 | Subnet: сменить zone |
| RemoveCidrBlocks | 7 | Subnet: убрать CIDR (вкл. v6) |
| Update | 26 | PATCH с UpdateMask |
| UpdateRule | 3 | SG: 1 rule |
| UpdateRules | 7 | SG: batch rules |

---


### Cross-method

*Cross-method*

| Pattern | Classes | P | Apps | Что проверяет |
|---|---|---|---|---|
| `*-AUTHZ-EMPTY-FOLDER-HEADER` | AUTHZ | P1 | 7 (add,gat,net,pri,rou,sec,sub) | List с пустым x-kacho-folder-id header → текущее: 200 (dev mode) |
| `NET-SUBNET-ADDR-NIC-DELETE-CHAIN` | CONF,STATE | P0 | 1 (sub) | Multi-resource delete-chain: Network→Subnet→Address(internal)→NIC создаются, затем удаляются строго снизу вверх (NIC→Address→Subnet→Network); попытка удалить parent раньше child → `FailedPrecondition`. Verifies REQ-DEL-06/07/08. |
| `*-HEADERS-MISSING-CT` | NEG,VAL | P3 | 3 (add,gat,net) | POST без Content-Type → 415 или 400 или 200 (lenient) |
| `*-METHOD-DELETE-LIST` | NEG,VAL | P3 | 7 (add,gat,net,pri,rou,sec,sub) | DELETE на List endpoint (без id) → 405 или 404 |
| `*-METHOD-NOT-ALLOWED` | NEG,VAL | P3 | 1 (pri) | PUT/HEAD на /endpoints → не разрешено |
| `*-METHOD-PUT-NOT-ALLOWED` | NEG,VAL | P3 | 7 (add,gat,net,pri,rou,sec,sub) | PUT на List endpoint → 405 или 404 |

### AddCidrBlocks

*Subnet: добавить CIDR*

| Pattern | Classes | P | Apps | Что проверяет |
|---|---|---|---|---|
| `*-ACB-CRUD-ADD-MULTIPLE` | BVA,CRUD | P1 | 1 (sub) | AddCidrBlocks: добавить 3 CIDR за один request → все 3 видны |
| `*-ACB-CRUD-ADD-ONE` | CRUD | P1 | 1 (sub) | AddCidrBlocks: добавить 1 CIDR → виден в response |
| `*-ACB-CRUD-OK` | CRUD | P1 | 1 (sub) | AddCidrBlocks → новый блок виден в GET |
| `*-ACB-NEG-OVERLAP` | NEG | P1 | 1 (sub) | AddCidrBlocks с CIDR пересекающимся с existing → InvalidArgument/FailedPrecondition |
| `*-ACB-NEG-OVERLAP-SELF` | CONF,NEG | P0 | 1 (sub) | AddCidrBlocks с CIDR пересекающимся с existing prefix → FailedPrecondition |
| `*-ACB-RCB-ROUNDTRIP` | IDM,STATE | P2 | 1 (sub) | AddCidrBlocks + RemoveCidrBlocks roundtrip: добавили → убрали → не изменилось |
| `*-ACB-STATE-DISJOINT-CIDRS` | CONF,STATE,VAL | P1 | 1 (sub) | AddCidrBlocks с пересекающимися CIDR в одном запросе → InvalidArgument |
| `*-ACB-VAL-HOST-BITS` | NEG,VAL | P1 | 1 (sub) | AddCidrBlocks с host-bits в CIDR (10.180.30.5/24) → 400 |
| `SUB-CIDR-ADD-V6-OK` | CRUD | P1 | 1 (sub) | AddCidrBlocks с IPv6-блоком → v6_cidr_blocks обновлён; Subnet становится dual-stack. Verifies REQ-CIDR-10. |
| `SUB-CIDR-ADD-V6-NEG-HOSTBITS` | NEG,VAL | P1 | 1 (sub) | AddCidrBlocks с IPv6-блоком, в котором set host-bits → `InvalidArgument`. Verifies REQ-CIDR-10. |

### Create

*Создание (async, Operation)*

| Pattern | Classes | P | Apps | Что проверяет |
|---|---|---|---|---|
| `*-CR-BVA-CIDR-28` | BVA | P2 | 1 (sub) | Create subnet с prefix /28 → ожидаемое поведение |
| `*-CR-BVA-CIDR-29` | BVA | P2 | 1 (sub) | Create subnet с prefix /29 → ожидаемое поведение |
| `*-CR-BVA-CIDR-30` | BVA | P2 | 1 (sub) | Create subnet с prefix /30 → ожидаемое поведение |
| `*-CR-BVA-CIDR-31` | BVA | P2 | 1 (sub) | Create subnet с prefix /31 → ожидаемое поведение |
| `*-CR-BVA-DESC-MAX-256` | BVA | P2 | 6 (add,gat,net,rou,sec,sub) | Create с description len=256 (max) → ok |
| `*-CR-BVA-DESC-OVER-257` | BVA,VAL | P1 | 6 (add,gat,net,rou,sec,sub) | Create с description len=257 (over-max) → InvalidArgument |
| `*-CR-BVA-LABELS-MAX-64` | BVA | P2 | 6 (add,gat,net,rou,sec,sub) | Create с 64 labels (max) → ok |
| `*-CR-BVA-LABELS-OVER-65` | BVA,VAL | P1 | 6 (add,gat,net,rou,sec,sub) | Create с 65 labels (over-max) → 400 |
| `*-CR-BVA-NAME-EMPTY` | BVA,VAL | P2 | 7 (add,gat,net,pri,rou,sec,sub) | Create с empty name → VPC permissive (200) или 400 |
| `*-CR-BVA-NAME-MAX-63` | BVA | P2 | 7 (add,gat,net,pri,rou,sec,sub) | Create с name len=63 (max) → ok |
| `*-CR-BVA-NAME-OVER-64` | BVA,VAL | P1 | 7 (add,gat,net,pri,rou,sec,sub) | Create с name len=64 (over-max) → InvalidArgument |
| `*-CR-CONF-FOLDER-NF-TEXT` | CONF,NEG | P1 | 2 (add,net) | Create network в garbage folder → verbatim 'Folder with id ... not found' |
| `*-CR-CONF-NET-NF-TEXT` | CONF,NEG | P1 | 4 (pri,rou,sec,sub) | Create subnet в garbage network → verbatim text 'Network ... not found' |
| `*-CR-CONF-SUB-NF-TEXT` | CONF,NEG | P1 | 1 (add) | Create address с garbage subnet → verbatim 'Subnet ... not found' |
| `*-CR-CRUD-EXT` | CRUD | P1 | 1 (add) | Create external Address → IP из default pool |
| `*-CR-CRUD-INT` | CRUD | P1 | 1 (add) | Create internal Address → IP в subnet |
| `*-CR-CRUD-OK` | CRUD | P1 | 6 (gat,net,pri,rou,sec,sub) | Create subnet → Operation → Subnet visible in GET |
| `*-CR-CRUD-WITH-SUBNET` | CRUD | P2 | 1 (pri) | PE Create с валидным addressSpec.internalIpv4AddressSpec.subnetId → address привязан |
| `SUB-CR-NO-CIDR-OK` | CRUD | P1 | 1 (sub) | Create Subnet без `v4_cidr_blocks` (только v6 или вообще без IPv4) → 200, CIDR-less Subnet. Verifies REQ-CIDR-08. |
| `SUB-CR-NEG-ADDR-INTO-CIDRLESS` | NEG,CONF | P1 | 1 (sub) | Create internal Address в Subnet без IPv4-CIDR → `FailedPrecondition`/`InvalidArgument` (некуда аллоцировать v4-IP). Verifies REQ-CIDR-08. |
| `SUB-CR-V6-OK` | CRUD | P1 | 1 (sub) | Create Subnet с `v6_cidr_blocks` (IPv6-only или dual-stack) → 200, v6_cidr_blocks виден в GET. Verifies REQ-CIDR-09. |
| `SG-CR-NO-NETWORK-OK` | CRUD | P1 | 1 (sec) | Create SecurityGroup без `network_id` → 200 (network опционален; SG folder-scoped). Verifies REQ-RES-07. |
| `SG-CR-WITH-NETWORK-OK` | CRUD | P1 | 1 (sec) | Create SecurityGroup с валидным `network_id` → 200, network_id виден; SG привязан к сети. Verifies REQ-RES-07. |
| `NIC-CR-NEG-BAD-SUBNET` | NEG,CONF | P1 | 1 (nic) | Create NIC с garbage `subnet_id` → async `NotFound` 'Subnet ... not found'. Verifies REQ-NIC-01. |
| `NIC-CR-WITH-ADDR-OK` | CRUD | P1 | 1 (nic) | Create NIC с `v4_address_ids` (предсозданный internal Address) → 200, address привязан, `Address.used`=true. Verifies REQ-NIC-04. |
| `NIC-CR-WITH-V6-ADDR-OK` | CRUD | P1 | 1 (nic) | Create NIC с `v6_address_ids` (предсозданный v6 internal Address) → 200, v6-address привязан. Verifies REQ-NIC-04. |
| `NIC-CR-WITH-BOTH-ADDR-OK` | CRUD | P1 | 1 (nic) | Create NIC с `v4_address_ids` И `v6_address_ids` одновременно (dual-stack линковка при создании) → 200, оба address привязаны (KAC-53 (3)). Verifies REQ-NIC-04. |
| `NIC-CR-WITH-UNBOUND-SG-OK` | CRUD | P2 | 1 (nic) | Create NIC с `security_group_ids` на SG, не привязанный к network NIC'а → 200 (SG folder-scoped, привязка к network у SG опциональна). Verifies REQ-NIC-05. |
| `RT-CR-STATE-SUBNET-AUTO-ASSOC` | CRUD,STATE | P1 | 1 (rou) | Create RouteTable с `network_id` → Subnet'ы этой сети (без своего `route_table_id`) автоматически получают `route_table_id` = id новой RT (auto-association по аналогии с default-SG). **TDD-pending**: assertion помечен `pm.test.skip`, превращается в normal green после реализации auto-assoc в `RouteTableService.doCreate`. Verifies REQ-RT-SUBNET-AUTO-ASSOC. |
| `*-CR-IDM-RETRY` | CONC,IDM | P1 | 1 (net) | Retry-safe: повторный Create same input → consistent result |
| `*-CR-NEG-CIDR-OVERLAP` | NEG | P0 | 1 (sub) | Create двух subnet с пересекающимися CIDR → второй FailedPrecondition |
| `*-CR-NEG-DUP-NAME` | CONC,NEG | P1 | 2 (net,sub) | Create с duplicate name в folder → async ALREADY_EXISTS (FINDING-005 fixed) |
| `*-CR-NEG-DUP-NAME-CHECK` | CONC,NEG | P1 | 6 (add,gat,net,rou,sec,sub) | Создать дубль с тем же name → ALREADY_EXISTS (UNIQUE есть для всех ресурсов) |
| `*-CR-NEG-FOLDER-NF` | CONF,NEG | P0 | 1 (gat) | Create Gateway в несуществующий folder → async NotFound |
| `*-CR-NEG-FOLDER-NOT-FOUND` | NEG | P0 | 1 (net) | Create с garbage folderId → async NOT_FOUND |
| `*-CR-NEG-NETWORK-NF` | NEG | P0 | 2 (pri,rou) | Create в несуществующую network → async NotFound |
| `*-CR-NEG-NETWORK-NOT-FOUND` | NEG | P0 | 1 (sub) | Create в несуществующей network → async NOT_FOUND |
| `*-CR-NEG-SUBNET-NF` | NEG,CONF | P1 | 1 (pri) | PE Create с garbage addressSpec.internalIpv4AddressSpec.subnetId → async NotFound 'Subnet ... not found' |
| `*-CR-NEG-SUBNET-NOT-FOUND` | NEG | P0 | 1 (add) | Create internal с garbage subnetId → async NotFound |
| `*-CR-PAIRWISE-00` | CRUD,VAL | P2 | 1 (sub) | Pairwise [0]: zone=ru-central1-a prefix=/24 dhcp=True |
| `*-CR-PAIRWISE-01` | CRUD,VAL | P2 | 1 (sub) | Pairwise [1]: zone=ru-central1-a prefix=/28 dhcp=False |
| `*-CR-PAIRWISE-02` | CRUD,VAL | P2 | 1 (sub) | Pairwise [2]: zone=ru-central1-a prefix=/16 dhcp=True |
| `*-CR-PAIRWISE-03` | CRUD,VAL | P2 | 1 (sub) | Pairwise [3]: zone=ru-central1-b prefix=/24 dhcp=False |
| `*-CR-PAIRWISE-04` | CRUD,VAL | P2 | 1 (sub) | Pairwise [4]: zone=ru-central1-b prefix=/28 dhcp=True |
| `*-CR-PAIRWISE-05` | CRUD,VAL | P2 | 1 (sub) | Pairwise [5]: zone=ru-central1-b prefix=/16 dhcp=False |
| `*-CR-PAIRWISE-06` | CRUD,VAL | P2 | 1 (sub) | Pairwise [6]: zone=ru-central1-c prefix=/24 dhcp=True |
| `*-CR-PAIRWISE-07` | CRUD,VAL | P2 | 1 (sub) | Pairwise [7]: zone=ru-central1-c prefix=/28 dhcp=False |
| `*-CR-PAIRWISE-08` | CRUD,VAL | P2 | 1 (sub) | Pairwise [8]: zone=ru-central1-c prefix=/16 dhcp=True |
| `*-CR-SEC-CMD` | NEG,VAL | P0 | 6 (add,gat,net,rou,sec,sub) | Security probe: cmd in name → handled, no 500 |
| `*-CR-SEC-LONGPAYLOAD` | NEG,VAL | P0 | 6 (add,gat,net,rou,sec,sub) | Security probe: longpayload in name → handled, no 500 |
| `*-CR-SEC-NULLBYTE` | NEG,VAL | P0 | 6 (add,gat,net,rou,sec,sub) | Security probe: nullbyte in name → handled, no 500 |
| `*-CR-SEC-PATH` | NEG,VAL | P0 | 6 (add,gat,net,rou,sec,sub) | Security probe: path in name → handled, no 500 |
| `*-CR-SEC-SQLI` | NEG,VAL | P0 | 6 (add,gat,net,rou,sec,sub) | Security probe: sqli in name → handled, no 500 |
| `*-CR-SEC-UNION` | NEG,VAL | P0 | 6 (add,gat,net,rou,sec,sub) | Security probe: union in name → handled, no 500 |
| `*-CR-SEC-XSS` | NEG,VAL | P0 | 6 (add,gat,net,rou,sec,sub) | Security probe: xss in name → handled, no 500 |
| `*-CR-VAL-BOTH-SPEC` | VAL | P0 | 1 (add) | Create с обоими spec (external+internal) → InvalidArgument |
| `*-CR-VAL-CIDR-HOSTBITS` | VAL | P0 | 1 (sub) | Create с host-bits в CIDR (10.0.0.5/24) → InvalidArgument |
| `*-CR-VAL-CIDR-REQUIRED` | VAL | P0 | 1 (sub) | Create без v4_cidr_blocks → InvalidArgument |
| `*-CR-VAL-DESC-INT-TYPE` | NEG,VAL | P3 | 6 (add,gat,net,rou,sec,sub) | Create с description=число → 400 |
| `*-CR-VAL-DHCP-DOMAIN-INVALID` | NEG,VAL | P1 | 1 (sub) | DHCP options: SUB-CR-VAL-DHCP-DOMAIN-INVALID |
| `*-CR-VAL-DHCP-DOMAIN-OK` | CRUD,VAL | P2 | 1 (sub) | DHCP options: SUB-CR-VAL-DHCP-DOMAIN-OK |
| `*-CR-VAL-DHCP-NS-INVALID-IP` | NEG,VAL | P1 | 1 (sub) | DHCP options: SUB-CR-VAL-DHCP-NS-INVALID-IP |
| `*-CR-VAL-DHCP-NS-OK` | CRUD,VAL | P2 | 1 (sub) | DHCP options: SUB-CR-VAL-DHCP-NS-OK |
| `*-CR-VAL-DHCP-NTP-INVALID-IP` | NEG,VAL | P1 | 1 (sub) | DHCP options: SUB-CR-VAL-DHCP-NTP-INVALID-IP |
| `*-CR-VAL-DHCP-NTP-OK` | CRUD,VAL | P2 | 1 (sub) | DHCP options: SUB-CR-VAL-DHCP-NTP-OK |
| `*-CR-VAL-EMPTY-BODY` | NEG,VAL | P2 | 7 (add,gat,net,pri,rou,sec,sub) | Create с пустым body → 400 |
| `*-CR-VAL-EXT-WITH-SUBNET-FK` | NEG,VAL | P1 | 1 (add) | Create external + internal со заданным subnet_id → 400 oneof |
| `*-CR-VAL-EXTRA-FIELDS` | VAL | P3 | 1 (net) | Create Network с unknown полем в body → silent ignore (200) или 400 |
| `*-CR-VAL-FOLDER-REQUIRED` | VAL | P0 | 3 (gat,net,pri) | Create без folder → InvalidArgument |
| `*-CR-VAL-LABELS-INVALID-KEY-CHAR` | VAL | P1 | 6 (add,gat,net,rou,sec,sub) | Create с invalid char в label key → 400 |
| `*-CR-VAL-LABELS-STRING-TYPE` | NEG,VAL | P2 | 6 (add,gat,net,rou,sec,sub) | Create с labels=строка (вместо object) → 400 InvalidArgument |
| `*-CR-VAL-LABELS-UPPERCASE-KEY` | VAL | P1 | 6 (add,gat,net,rou,sec,sub) | Create с UPPERCASE label key → 400 |
| `*-CR-VAL-MALFORMED-JSON` | NEG,VAL | P2 | 7 (add,gat,net,pri,rou,sec,sub) | Create с malformed JSON → 400 |
| `*-CR-VAL-MISSING-TYPE` | NEG,VAL | P1 | 1 (gat) | Create Gateway без gateway type oneof → 400 |
| `*-CR-VAL-NAME-DIGIT-START` | VAL | P1 | 6 (add,gat,net,rou,sec,sub) | Create с name начинающимся с цифры → 400 (verbatim YC regex) |
| `*-CR-VAL-NAME-HYPHEN-START` | VAL | P1 | 6 (add,gat,net,rou,sec,sub) | Create с name начинающимся с дефиса → 400 |
| `*-CR-VAL-NAME-NULL` | NEG,VAL | P2 | 6 (add,gat,net,rou,sec,sub) | Create с name=null → 400 |
| `*-CR-VAL-NAME-SPECIAL-CHARS` | VAL | P1 | 6 (add,gat,net,rou,sec,sub) | Create с спец-символами в name → 400 |
| `*-CR-VAL-NAME-UPPERCASE` | VAL | P2 | 6 (add,gat,net,rou,sec,sub) | Create с UPPERCASE name → VPC permissive (200) или 400 |
| `*-CR-VAL-NETWORK-REQUIRED` | NEG,VAL | P0 | 3 (pri,rou,sec) | Create без network_id → InvalidArgument |
| `*-CR-VAL-REQ-FOLDERID` | VAL | P0 | 7 (add,gat,net,pri,rou,sec,sub) | Create без required поля 'folderId' → 400 InvalidArgument |
| `*-CR-VAL-REQ-NAME` | VAL | P0 | 7 (add,gat,net,pri,rou,sec,sub) | Create без required поля 'name' → 400 InvalidArgument |
| `*-CR-VAL-REQ-NETWORKID` | VAL | P0 | 4 (pri,rou,sec,sub) | Create без required поля 'networkId' → 400 InvalidArgument |
| `*-CR-VAL-REQ-V4CIDRBLOCKS` | VAL | P0 | 1 (sub) | Create без required поля 'v4CidrBlocks' → 400 InvalidArgument |
| `*-CR-VAL-REQ-ZONEID` | VAL | P0 | 1 (sub) | Create без required поля 'zoneId' → 400 InvalidArgument |
| `*-CR-VAL-RESERVED-USED-OK` | VAL | P2 | 1 (add) | Create address с reserved/used флагами (если разрешено) → 200 или 400 |
| `*-CR-VAL-ROUTE-EMPTY-HOP` | NEG,VAL | P1 | 1 (rou) | static_routes validation: RT-CR-VAL-ROUTE-EMPTY-HOP |
| `*-CR-VAL-ROUTE-EMPTY-PREFIX` | NEG,VAL | P1 | 1 (rou) | static_routes validation: RT-CR-VAL-ROUTE-EMPTY-PREFIX |
| `*-CR-VAL-ROUTE-INVALID-HOP` | NEG,VAL | P1 | 1 (rou) | static_routes validation: RT-CR-VAL-ROUTE-INVALID-HOP |
| `*-CR-VAL-ROUTE-INVALID-PREFIX` | NEG,VAL | P1 | 1 (rou) | static_routes validation: RT-CR-VAL-ROUTE-INVALID-PREFIX |
| `*-CR-VAL-ROUTE-OK` | CRUD,VAL | P1 | 1 (rou) | static_routes validation: RT-CR-VAL-ROUTE-OK |
| `*-CR-VAL-SERVICE-MISSING` | NEG,VAL | P1 | 1 (pri) | Create PE без objectStorage → 400 |
| `*-CR-VAL-SPEC-ONEOF` | VAL | P0 | 1 (add) | Create без external/internal spec → InvalidArgument |
| `*-CR-VAL-SUBNET-REQUIRED` | VAL | P2 | 1 (pri) | Create PE без subnetId → ожидаемое поведение |
| `*-CR-VAL-ZONE-REQUIRED` | VAL | P0 | 1 (sub) | Create без zone_id → InvalidArgument (zone_id required) |
| `*-CR-VAL-ZONE-UNKNOWN` | VAL | P0 | 1 (sub) | Create с несуществующей зоной → InvalidArgument (dynamic whitelist) |

### Delete

*Удаление (async)*

| Pattern | Classes | P | Apps | Что проверяет |
|---|---|---|---|---|
| `*-DEL-AUTHZ-NF-SYNC` | AUTHZ,NEG | P1 | 7 (add,gat,net,pri,rou,sec,sub) | Delete несуществующего → sync 404 |
| `*-DEL-CONF-FULLTEXT` | CONF,NEG | P1 | 7 (add,gat,net,pri,rou,sec,sub) | Delete garbage → 'Subnet ... not found' |
| `*-DEL-CONF-NF-TEXT` | CONF,NEG | P1 | 7 (add,gat,net,pri,rou,sec,sub) | Delete несуществующего Subnet → verbatim 'Subnet ... not found' |
| `*-DEL-CRUD-EMPTY-OK` | CRUD | P1 | 1 (sub) | Delete Subnet без зависимостей → OK |
| `*-DEL-CRUD-OK` | CRUD | P1 | 7 (add,gat,net,pri,rou,sec,sub) | Subnet Delete happy path |
| `*-DEL-CRUD-ONLY-DEFAULT-SG` | CRUD,STATE | P1 | 1 (net) | Delete Network у которой есть только default-SG → OK (auto-cleanup default) |
| `NET-DEL-CRUD-DEFAULT-SG-REMOVED` | CRUD,STATE | P1 | 1 (net) | После Network.Delete её default SG обязан исчезнуть — explicit `GET /securityGroups/{defSgId}` → 404 (life-cycle default SG, KAC-53 (1)). Verifies REQ-NET-LSG-DEFAULT. |
| `*-DEL-NEG-HAS-ADDRESSES` | CONF,NEG,STATE | P0 | 1 (sub) | Delete Subnet с internal Address → FailedPrecondition (FK RESTRICT) |
| `SUB-DEL-NEG-HAS-V6-ADDRESS` | CONF,NEG,STATE | P0 | 1 (sub) | Delete Subnet с internal **v6** Address → `FailedPrecondition` (FK `addresses_internal_subnet_fkey` через generated-колонку, выводимую из v4 ИЛИ v6 — миграция 0013, KAC-34). Verifies REQ-DEL-06. |
| `SUB-DEL-NEG-HAS-NIC` | CONF,NEG,STATE | P0 | 1 (sub) | Delete Subnet с привязанным NetworkInterface → sync `FailedPrecondition` со списком NIC-id (FK `network_interfaces.subnet_id` ON DELETE RESTRICT — миграция 0012, KAC-33). Verifies REQ-DEL-07. |
| `NET-DEL-NEG-HAS-SUBNET-WITH-NIC` | CONF,NEG,STATE | P0 | 1 (net) | Delete Network, у которой Subnet с NIC → `FailedPrecondition` 'network is not empty' (транзитивно: NIC блокирует Subnet, Subnet блокирует Network). Verifies REQ-DEL-08. |
| `NIC-DEL-OK` | CRUD | P1 | 1 (nic) | Delete NetworkInterface (не приаттаченный) → Operation → NIC исчезает из GET; привязанные Address освобождаются (`Address.used`=false). Verifies REQ-NIC-02. |
| `NIC-DEL-NEG-ATTACHED` | CONF,NEG,STATE | P1 | 1 (nic) | Delete NIC, приаттаченного к instance (`used_by` set) → `FailedPrecondition` (сначала DetachFromInstance). Verifies REQ-NIC-03. |
| `ADDR-DEL-NEG-USED-BY-NIC` | CONF,NEG,STATE | P0 | 1 (add/nic) | Delete Address, который референсится NIC'ом (`v4_address_ids`/`v6_address_ids`) → `FailedPrecondition` (сначала detach с NIC). Verifies REQ-NIC-04 / REQ-DEL-09. |
| `*-DEL-NEG-HAS-NONDEFAULT-SG` | CONF,NEG,STATE | P0 | 1 (net) | Delete Network с НЕ-default SG → FailedPrecondition (RESTRICT FK) |
| `*-DEL-NEG-HAS-ROUTE-TABLE` | CONF,NEG,STATE | P0 | 1 (net) | Delete Network c RouteTable → FailedPrecondition |
| `*-DEL-NEG-HAS-SUBNETS` | CONF,NEG,STATE | P0 | 1 (net) | Delete Network c Subnet → FailedPrecondition (FK RESTRICT) |
| `*-DEL-NEG-NF-INVALID-PREFIX` | NEG,STATE | P1 | 1 (net) | Delete с id без VPC-префикса → sync 404 |
| `*-DEL-STATE-DEFAULT-SG` | NEG,STATE | P1 | 1 (sec) | Delete default-SG напрямую → должен fail (нельзя delete default SG в обход) |
| `SG-DEL-NEG-NIC-ATTACHED` | NEG,STATE,CONF | P0 | 1 (sec) | Delete SG, прилинкованного к NIC через `security_group_ids[]` → `FailedPrecondition` (KAC-53 (2)). **TDD-red**: пока DB-уровневый ref-trigger по KAC-52 не реализован, кейс падает (SG удаляется, оставляя dangling ref в NIC.security_group_ids). Verifies REQ-SG-DEL-NIC-REFCHECK. Blocked by KAC-52. |

### Get

*Чтение по id*

| Pattern | Classes | P | Apps | Что проверяет |
|---|---|---|---|---|
| `*-GET-CONF-FULLTEXT` | CONF,NEG | P1 | 7 (add,gat,net,pri,rou,sec,sub) | Get garbage → 'Subnet <id> not found' формат |
| `*-GET-CONF-NF-FULLTEXT` | CONF,NEG | P1 | 1 (pri) | Get garbage PE → 'PrivateEndpoint <id> not found' формат |
| `*-GET-CONF-NF-TEXT` | CONF,NEG | P1 | 8 (add,gat,net,ope,pri,rou,sec,sub) | Get garbage — verbatim text 'Subnet ... not found' |
| `*-GET-CRUD-OK` | CRUD | P1 | 1 (ope) | Get свежесозданной operation → done=true с response |
| `NET-GET-NO-VPNID-OK` | CONF,CRUD | P1 | 1 (net) | Get Network через **публичный** API → ответ НЕ содержит `vpn_id` (инфра-чувствительное поле — только internal-проекция / `InternalNetworkService.GetNetwork`). Verifies REQ-RES-08 / REQ-YC-06. |
| `*-GET-EXTRA-QS` | VAL | P3 | 1 (pri) | Get PE с unused query params → не влияет |
| `*-GET-NEG-EMPTY-ID` | NEG | P2 | 1 (net) | Get empty id → 404 (gRPC-gateway routing) |
| `*-GET-NEG-NF` | NEG | P0 | 5 (add,gat,pri,rou,sec) | Get garbage → 404 |
| `*-GET-NEG-NF-INVALID-PREFIX` | NEG | P0 | 1 (ope) | Get opId без 3-char domain-prefix → 400 InvalidArgument 'unknown prefix' |
| `*-GET-NEG-NF-VALID-PREFIX` | NEG | P1 | 1 (ope) | Get несуществующего opId с правильным префиксом → NotFound |
| `*-GET-NEG-NOT-FOUND` | CONF,NEG | P0 | 2 (net,sub) | Get garbage → 404 |
| `*-GET-PERF-BASELINE` | CRUD,PERF | P2 | 6 (add,gat,net,rou,sec,sub) | Get existing — response time < 300ms |
| `*-GET-TRAILING-SLASH` | VAL | P3 | 5 (add,gat,net,rou,sec) | Get с trailing slash → 404 |
| `*-GET-WITH-QUERY-PARAMS` | CRUD,VAL | P3 | 1 (gat) | Get Gateway с дополнительными query params → 200 (ignored) |

### GetByValue

*Address: lookup по IP*

| Pattern | Classes | P | Apps | Что проверяет |
|---|---|---|---|---|
| `*-GBV-CONF-NOLEAK-FOR-EXISTING-OTHER` | AUTHZ,CONF | P0 | 1 (add) | GetByValue адреса из другого folder → NotFound (security info-leak) |
| `*-GBV-CRUD-OK` | CRUD | P1 | 1 (add) | GetByValue существующего external IP → 200 + сам Address |
| `*-GBV-NEG-NF` | AUTHZ,NEG | P0 | 1 (add) | GetByValue несуществующего IP → NotFound (security: не должно leak'ать существование) |
| `*-GBV-VAL-INVALID-IP` | NEG,VAL | P2 | 1 (add) | GetByValue с garbage IP → 400 или 404 |

### Lifecycle

*Полный CRUD-цикл*

| Pattern | Classes | P | Apps | Что проверяет |
|---|---|---|---|---|
| `*-LIFECYCLE-CONF` | CONF,CRUD,STATE | P1 | 3 (add,gat,net) | Full lifecycle conformance: CRUD invariants |

### List

*Листинг + pagination*

| Pattern | Classes | P | Apps | Что проверяет |
|---|---|---|---|---|
| `*-LST-AUTHZ-CROSS-FOLDER-ISOLATION` | AUTHZ,CRUD | P0 | 3 (add,gat,net) | Folder isolation: ресурс в folderA не виден в List по folderB |
| `*-LST-BVA-PAGESIZE-1` | BVA,PAGE | P2 | 7 (add,gat,net,pri,rou,sec,sub) | List pageSize=1 → ≤1 item |
| `*-LST-BVA-PAGESIZE-OVER-MAX` | BVA,VAL | P2 | 7 (add,gat,net,pri,rou,sec,sub) | List pageSize=10000 → InvalidArgument |
| `*-LST-BVA-PAGESIZE-ZERO` | BVA | P2 | 7 (add,gat,net,pri,rou,sec,sub) | List pageSize=0 → default applied (200) |
| `*-LST-CONTRACT-NEVER-EXCEEDS-PAGESIZE` | CRUD,PAGE | P2 | 7 (add,gat,net,pri,rou,sec,sub) | List с pageSize=5 → не более 5 элементов в response |
| `*-LST-CRUD-OK` | CRUD | P1 | 7 (add,gat,net,pri,rou,sec,sub) | List subnets в folder → 200 |
| `NIC-LIST-OK` | CRUD | P1 | 1 (nic) | List NetworkInterfaces в folder → 200; созданный NIC присутствует; в ответе только lean-проекция (нет инфра-полей `vpn_id`/`hv_id`/`sid`/...). Verifies REQ-NIC-06. |
| `SG-LIST-FILTER-NETWORK-OK` | CRUD,FILTER | P2 | 1 (sec) | List SecurityGroups с фильтром по `network_id` → возвращает только SG этой сети (и не возвращает SG без network / другой сети). Verifies REQ-RES-07. |
| `*-LST-DOUBLE-FOLDER-PARAM` | VAL | P3 | 5 (add,gat,net,rou,sec) | List с дубликатом folderId param → 200 (last wins) или 400 |
| `*-LST-FILTER-CASE-SENSITIVITY` | FILTER | P3 | 1 (gat) | Filter case-sensitivity на name field |
| `*-LST-FILTER-EMPTY` | CRUD,FILTER | P2 | 1 (gat) | List Gateway с пустым filter expression → 200 (filter optional) |
| `*-LST-FILTER-GARBAGE` | FILTER,VAL | P1 | 7 (add,gat,net,pri,rou,sec,sub) | List с garbage filter syntax → 400 InvalidArgument |
| `*-LST-FILTER-MATCH` | CRUD,FILTER | P2 | 6 (add,gat,net,rou,sec,sub) | Создать ресурс → list filter=name='X' → ресурс в результатах |
| `*-LST-FILTER-MULTI-CONDITIONS` | FILTER | P3 | 1 (net) | List с filter из несколько условий — современный YC pattern |
| `*-LST-FILTER-NAME-OK` | CRUD,FILTER | P2 | 7 (add,gat,net,pri,rou,sec,sub) | List с filter name="foo" → 200 |
| `*-LST-FILTER-SPECIAL-CHARS` | FILTER,VAL | P3 | 5 (add,gat,net,rou,sec) | List с filter содержащим спец-символы → 400 или 200 |
| `*-LST-FILTER-STATUS` | FILTER | P3 | 1 (pri) | List PE с фильтром по status (если поддерживается) |
| `*-LST-FILTER-UNKNOWN-FIELD` | FILTER,VAL | P2 | 7 (add,gat,net,pri,rou,sec,sub) | List с filter на unsupported field → 400 InvalidArgument |
| `*-LST-PAGE-NEGATIVE-SIZE` | BVA,VAL | P2 | 5 (add,gat,net,rou,sec) | List с pageSize=-1 → 400 или 200 |
| `*-LST-PAGE-OVER` | BVA,VAL | P2 | 1 (pri) | List PE с pageSize=10000 → 400 |
| `*-LST-PAGE-ROUNDTRIP` | BVA,CRUD,PAGE | P2 | 7 (add,gat,net,pri,rou,sec,sub) | Pagination: получить пустой/не-пустой ответ + nextPageToken и пройти ещё раз с ним |
| `*-LST-PAGE-TOKEN-GARBAGE` | PAGE,VAL | P1 | 7 (add,gat,net,pri,rou,sec,sub) | List с garbage page_token → InvalidArgument |
| `*-LST-PAGE-ZERO` | BVA | P2 | 1 (pri) | List PE с pageSize=0 → default applied |
| `*-LST-PAGESIZE-1001` | BVA,VAL | P1 | 5 (add,gat,net,rou,sec) | List с pageSize=1001 (over max) → 400 |
| `*-LST-PAGESIZE-EXACTLY-1000` | BVA | P2 | 5 (add,gat,net,rou,sec) | List с pageSize=1000 (boundary max) → 200 |
| `*-LST-PERF-BASELINE` | CRUD,PERF | P2 | 7 (add,gat,net,pri,rou,sec,sub) | List response time < 500ms (perf baseline) |
| `*-LST-ROUNDTRIP` | CRUD,PAGE | P2 | 1 (pri) | Pagination roundtrip PE |
| `*-LST-SEC-FILTER-SQLI` | NEG,VAL | P0 | 6 (add,gat,net,rou,sec,sub) | Security: SQL injection в filter → не 500 |
| `*-LST-VAL-FOLDER-REQUIRED` | AUTHZ,VAL | P0 | 7 (add,gat,net,pri,rou,sec,sub) | List без folderId → InvalidArgument |

### ListBySubnet

*Address: в подсети*

| Pattern | Classes | P | Apps | Что проверяет |
|---|---|---|---|---|
| `*-LBS-CRUD-OK` | CRUD | P2 | 1 (add) | ListBySubnet → массив (возможно пустой) |
| `*-LBS-NEG-PARENT-NF` | NEG | P2 | 1 (add) | ListBySubnet несуществующего subnet → 200 или 404 |

### ListOperations

*Operations*

| Pattern | Classes | P | Apps | Что проверяет |
|---|---|---|---|---|
| `*-LOP-CRUD-OK` | CRUD,NEG | P1 | 7 (add,gat,net,pri,rou,sec,sub) | ListOperations возвращает create-op |
| `*-LOP-NEG-PARENT-NF` | NEG | P2 | 7 (add,gat,net,pri,rou,sec,sub) | ListOperations несуществующего ресурса → 404 или 200 пустой (PE добавлен — `PE-LOP-NEG-PARENT-NF`) |
| `NET-LISTOPS-AFTER-DELETE-OK` | CRUD,STATE | P1 | 1 (net) | После Delete ресурса его `<Resource>.ListOperations(<id>)` всё ещё содержит историю операций (create+delete) — операции переживают удаление ресурса. Verifies REQ-OPS-04. |
| `OP-LIST-AFTER-DELETE-OK` | CRUD,STATE | P1 | 1 (ope) | `OperationService.Get(opId)` по операции удалённого ресурса → 200 (запись операции не удаляется вместе с ресурсом). Verifies REQ-OPS-04. |

### ListRouteTables

*Network: RT*

| Pattern | Classes | P | Apps | Что проверяет |
|---|---|---|---|---|
| `*-LRT-CRUD-EMPTY` | CRUD | P2 | 1 (net) | ListRouteTables → 200 + empty |
| `*-LRT-NEG-PARENT-NF` | NEG | P1 | 1 (net) | List route_tables в несуществующей network → 404 NotFound |

### ListSecurityGroups

*Network: SG*

| Pattern | Classes | P | Apps | Что проверяет |
|---|---|---|---|---|
| `*-LSG-CRUD-DEFAULT-SG` | CRUD | P1 | 1 (net) | ListSecurityGroups → default SG присутствует (inline create в doCreate) |
| `*-LSG-NEG-PARENT-NF` | NEG | P1 | 1 (net) | List security_groups в несуществующей network → 404 NotFound |

### ListSubnets

*Network: subnets*

| Pattern | Classes | P | Apps | Что проверяет |
|---|---|---|---|---|
| `*-LSUB-CRUD-EMPTY` | CRUD | P2 | 1 (net) | ListSubnets для пустой network → 200 + empty array |
| `*-LSUB-NEG-PARENT-NF` | NEG | P1 | 1 (net) | List subnets в несуществующей network → 404 NotFound |

### ListUsedAddresses

*Subnet: использ. IP*

| Pattern | Classes | P | Apps | Что проверяет |
|---|---|---|---|---|
| `*-LUA-CRUD-OK` | CRUD | P2 | 1 (sub) | ListUsedAddresses на пустой subnet → empty |
| `*-LUA-NEG-PARENT-NF` | NEG | P2 | 1 (sub) | ListUsedAddresses несуществующего subnet → 404 или 200 |

### Move

*Move в другой folder*

| Pattern | Classes | P | Apps | Что проверяет |
|---|---|---|---|---|
| `*-MV-AUTHZ-NF-SYNC` | AUTHZ,NEG | P1 | 6 (add,gat,net,rou,sec,sub) | Move несуществующего → sync 404 от AuthZ-Get |
| `*-MV-CONF-NF-TEXT` | CONF,NEG | P1 | 6 (add,gat,net,rou,sec,sub) | Move несуществующего → verbatim '<Resource> ... not found' text |
| `*-MV-CRUD-OK` | CRUD | P1 | 6 (add,gat,net,rou,sec,sub) | Move subnet в другой folder → folder_id обновлён |
| `*-MV-IDM-SAME-FOLDER` | CRUD,IDM | P2 | 6 (add,gat,net,rou,sec,sub) | Move в текущий folder → ok (idempotent), ресурс остаётся |
| `*-MV-NEG-DEST-FOLDER-NF` | NEG | P1 | 1 (net) | Move в garbage folder → async NOT_FOUND |
| `*-MV-VAL-NO-DEST` | VAL | P1 | 6 (add,gat,net,rou,sec,sub) | Move без destinationFolderId → InvalidArgument |

### Relocate

*Subnet: сменить zone*

| Pattern | Classes | P | Apps | Что проверяет |
|---|---|---|---|---|
| `*-REL-NEG-IN-USE` | CONF,NEG | P1 | 1 (sub) | Relocate subnet с Address-ом → FailedPrecondition 'Invalid subnet state' |
| `*-REL-STATE-NO-ADDRESSES-OK` | CRUD,STATE | P1 | 1 (sub) | Relocate subnet без Address → succeeds (zone_id обновляется) |
| `*-REL-VAL-NO-DEST` | NEG,VAL | P1 | 1 (sub) | Relocate без destinationZoneId → InvalidArgument |

### RemoveCidrBlocks

*Subnet: убрать CIDR*

| Pattern | Classes | P | Apps | Что проверяет |
|---|---|---|---|---|
| `*-RCB-CONF-STATE` | STATE | P1 | 1 (sub) | STATE для RemoveCidrBlocks: проверка инварианта после операции |
| `*-RCB-CRUD-OK` | CRUD | P1 | 1 (sub) | RemoveCidrBlocks: убрать дополнительный CIDR |
| `*-RCB-CRUD-REMOVE-ONE` | CRUD | P1 | 1 (sub) | RemoveCidrBlocks: добавить 3 → убрать 1 → 2 остаются |
| `*-RCB-NEG-CANNOT-REMOVE-PRIMARY` | NEG,STATE | P0 | 1 (sub) | RemoveCidrBlocks для primary v4_cidr (первый, primary) → отказ |
| `*-RCB-NEG-NF` | NEG,STATE,VAL | P1 | 1 (sub) | RemoveCidrBlocks с несуществующим CIDR → InvalidArgument |
| `*-RCB-NEG-NOT-PRESENT` | NEG,VAL | P1 | 1 (sub) | RemoveCidrBlocks с CIDR не из списка → ожидаемое поведение (FailedPrecondition или silent) |
| `SUB-CIDR-REMOVE-V6-OK` | CRUD | P1 | 1 (sub) | RemoveCidrBlocks с ранее добавленным IPv6-блоком → `v6_cidr_blocks` сжимается; Subnet снова IPv4-only. Verifies REQ-CIDR-10. |

### Update

*PATCH с UpdateMask*

| Pattern | Classes | P | Apps | Что проверяет |
|---|---|---|---|---|
| `*-UPD-AUTHZ-NF-SYNC` | AUTHZ,NEG | P1 | 7 (add,gat,net,pri,rou,sec,sub) | Update несуществующего → sync 404 от AuthZ-Get |
| `*-UPD-CONF-FULLTEXT` | CONF,NEG | P1 | 7 (add,gat,net,pri,rou,sec,sub) | Update garbage → точный текст 'Subnet ... not found' |
| `*-UPD-CONF-NF-TEXT` | CONF,NEG | P1 | 7 (add,gat,net,pri,rou,sec,sub) | Update несуществующего Subnet → verbatim 'Subnet ... not found' |
| `*-UPD-CRUD-DESC` | CRUD | P2 | 7 (add,gat,net,pri,rou,sec,sub) | Update happy description |
| `*-UPD-CRUD-DESCRIPTION` | CRUD | P1 | 1 (net) | Update description через mask → success + новое значение видно |
| `*-UPD-CRUD-LABELS` | CRUD | P2 | 7 (add,gat,net,pri,rou,sec,sub) | Update happy labels |
| `*-UPD-CRUD-MULTI-MASK` | CRUD,STATE | P2 | 7 (add,gat,net,pri,rou,sec,sub) | Update с mask=name,description,labels → все три поля обновлены |
| `*-UPD-CRUD-NAME` | CRUD | P2 | 6 (add,gat,net,rou,sec,sub) | Update happy name |
| `*-UPD-CRUD-OK` | CRUD | P1 | 6 (add,gat,pri,rou,sec,sub) | Update Subnet description |
| `*-UPD-NEG-NF-INVALID-PREFIX` | NEG,STATE | P1 | 1 (net) | Update с id без VPC-префикса → sync 404 (gateway prefix-routing) |
| `*-UPD-STATE-IMMUTABLE-ADDRESS-ID` | CONF,STATE,VAL | P1 | 1 (pri) | Update mask='address_id' (immutable) → 400 InvalidArgument verbatim |
| `*-UPD-STATE-IMMUTABLE-CIDR` | STATE,VAL | P1 | 1 (sub) | Update с mask=v4_cidr_blocks → InvalidArgument (immutable) |
| `*-UPD-STATE-IMMUTABLE-EXTERNAL-IPV4-ADDRESS-SPEC` | CONF,STATE,VAL | P1 | 1 (add) | Update mask='external_ipv4_address_spec' (immutable) → 400 InvalidArgument verbatim |
| `*-UPD-STATE-IMMUTABLE-FOLDER` | STATE,VAL | P1 | 7 (add,gat,net,pri,rou,sec,sub) | Update с mask=folder_id → InvalidArgument (immutable) |
| `*-UPD-STATE-IMMUTABLE-FOLDER-ID` | CONF,STATE,VAL | P1 | 7 (add,gat,net,pri,rou,sec,sub) | Update mask='folder_id' (immutable) → 400 InvalidArgument verbatim |
| `*-UPD-STATE-IMMUTABLE-INTERNAL-IPV4-ADDRESS-SPEC` | CONF,STATE,VAL | P1 | 1 (add) | Update mask='internal_ipv4_address_spec' (immutable) → 400 InvalidArgument verbatim |
| `*-UPD-STATE-IMMUTABLE-NETWORK-ID` | CONF,STATE,VAL | P1 | 4 (pri,rou,sec,sub) | Update mask='network_id' (immutable) → 400 InvalidArgument verbatim |
| `*-UPD-STATE-IMMUTABLE-SERVICE-TYPE` | CONF,STATE,VAL | P1 | 1 (pri) | Update mask='service_type' (immutable) → 400 InvalidArgument verbatim |
| `*-UPD-STATE-IMMUTABLE-SUBNET-ID` | CONF,STATE,VAL | P1 | 1 (pri) | Update mask='subnet_id' (immutable) → 400 InvalidArgument verbatim |
| `*-UPD-STATE-IMMUTABLE-V4-CIDR-BLOCKS` | CONF,STATE,VAL | P1 | 1 (sub) | Update mask='v4_cidr_blocks' (immutable) → 400 InvalidArgument verbatim |
| `*-UPD-STATE-IMMUTABLE-V6-CIDR-BLOCKS` | CONF,STATE,VAL | P1 | 1 (sub) | Update mask='v6_cidr_blocks' (immutable) → 400 InvalidArgument verbatim |
| `*-UPD-STATE-IMMUTABLE-ZONE-ID` | CONF,STATE,VAL | P1 | 1 (sub) | Update mask='zone_id' (immutable) → 400 InvalidArgument verbatim |
| `*-UPD-VAL-MASK-EMPTY` | STATE,VAL | P2 | 7 (add,gat,net,pri,rou,sec,sub) | Update с пустой mask → full PATCH (200) |
| `*-UPD-VAL-MASK-MULTIPLE-UNKNOWN` | STATE,VAL | P2 | 7 (add,gat,net,pri,rou,sec,sub) | Update с несколькими unknown полями в mask → 400 |
| `*-UPD-VAL-MASK-NAME-ONLY` | STATE,VAL | P2 | 6 (add,gat,net,rou,sec,sub) | Update mask=name → только name меняется, description/labels не трогаются |
| `*-UPD-VAL-UNKNOWN-MASK` | STATE,VAL | P1 | 7 (add,gat,net,pri,rou,sec,sub) | Update с unknown field в UpdateMask → InvalidArgument |
| `SUB-UPD-V6-NOOP` | STATE,CRUD | P2 | 1 (sub) | Update с `v6_cidr_blocks` в body+mask → 200, операция done без error (soft-immutable: YC принимает в mask и меняет, у нас no-op — реальное изменение через `:add-cidr-blocks`/`:remove-cidr-blocks`; kacho-vpc#10). Verifies REQ-UPD-05. |

### UpdateRule

*SG: 1 rule*

| Pattern | Classes | P | Apps | Что проверяет |
|---|---|---|---|---|
| `*-UR-AUTHZ-NF-SYNC` | AUTHZ,NEG,VAL | P1 | 1 (sec) | UpdateRule несуществующего SG → sync 404 от AuthZ-Get |
| `*-UR-CRUD-OK` | CRUD | P1 | 1 (sec) | UpdateRule (single) — добавить rule, обновить description |
| `*-UR-NEG-RULE-NF` | NEG | P1 | 1 (sec) | UpdateRule (single) несуществующего rule_id → 404 NotFound |

### UpdateRules

*SG: batch rules*

| Pattern | Classes | P | Apps | Что проверяет |
|---|---|---|---|---|
| `*-URL-AUTHZ-NF-SYNC` | AUTHZ,NEG,VAL | P1 | 1 (sec) | UpdateRules несуществующего SG → sync 404 от AuthZ-Get |
| `*-URL-CRUD-OK` | CRUD,STATE | P1 | 1 (sec) | UpdateRules: добавить правило |
| `*-URL-VAL-DIRECTION-UNKNOWN` | NEG,STATE,VAL | P1 | 1 (sec) | UpdateRules rule field: SG-URL-VAL-DIRECTION-UNKNOWN |
| `*-URL-VAL-PORT-ANY-MINUS-1` | STATE,VAL | P1 | 1 (sec) | UpdateRules rule field: SG-URL-VAL-PORT-ANY-MINUS-1 |
| `*-URL-VAL-PORT-NEG` | NEG,STATE,VAL | P1 | 1 (sec) | UpdateRules rule field: SG-URL-VAL-PORT-NEG |
| `*-URL-VAL-PORT-OVER-65535` | NEG,STATE,VAL | P1 | 1 (sec) | UpdateRules rule field: SG-URL-VAL-PORT-OVER-65535 |
| `*-URL-VAL-PROTOCOL-UNKNOWN` | NEG,STATE,VAL | P1 | 1 (sec) | UpdateRules rule field: SG-URL-VAL-PROTOCOL-UNKNOWN |

---

### NetworkInterface (NIC) — first-class ресурс (эпик KAC-2)

*Публичная проекция NIC — lean: `id`/`folderId`/`subnetId`/`v4AddressIds`/`v6AddressIds`/`securityGroupIds`/`usedBy`/`macAddress`/`status`/`name`/`labels`. Инфра-чувствительные data-plane-поля (`vpnId`/`hvId`/`sid`/`hostIface`/`netns`/`gatewayIp`/`containerId`/`networkId`/`instanceId`/`index`) — только на internal-проекции (`InternalNetworkInterfaceService`), НИКОГДА не на публичной. REST: `/vpc/v1/networkInterfaces`. Кейсы — в `cases/network-interface.py` (app-код `nic`). NIC-кейсы, совпадающие с generic-паттернами по суффиксу, — инстансы (`NIC-CR-CRUD-OK` → `*-CR-CRUD-OK`, `NIC-CR-NEG-DUP-NAME` → `*-CR-NEG-DUP-NAME`, `NIC-GET-*`/`NIC-LST-*`/`NIC-MV-*` и т.п.); ниже — NIC-специфичные паттерны.*

| Pattern | Classes | P | Apps | Что проверяет |
|---|---|---|---|---|
| `NIC-CR-CRUD-OK` | CRUD | P1 | 1 (nic) | (инстанс `*-CR-CRUD-OK`) Create NIC в Subnet → Operation → NIC в GET; lean-проекция, нет инфра-полей. Verifies REQ-NIC-01/REQ-NIC-06. |
| `NIC-CR-MAC-OK` | CRUD | P1 | 1 (nic) | Create NIC → `macAddress` в lean-проекции, формат `0e:xx:xx:xx:xx:xx` (lowercase hex, префикс `0e:`); Update name → MAC не меняется (AWS-ENI semantics). Verifies REQ-NIC-08. |
| `NIC-CR-NEG-DUP-NAME` | CONC,NEG | P1 | 1 (nic) | (инстанс `*-CR-NEG-DUP-NAME`) Create NIC с duplicate name в folder → async `ALREADY_EXISTS`. Verifies REQ-NAME-04. |
| `NIC-UPD-OK` | CRUD | P1 | 1 (nic) | Update NIC (name/labels/securityGroupIds через mask) → Operation → новые значения видны; subnetId/инфра-поля не меняются. Verifies REQ-NIC-07. |
| `NIC-ATTACH-DETACH-OK` | CRUD,STATE | P1 | 1 (nic) | `AttachToInstance` → `used_by`={compute_instance,<id>}; `DetachFromInstance` → `used_by` очищен. Verifies REQ-NIC-03. |

### AttachToInstance / DetachFromInstance

*NIC: привязка/отвязка от instance (выставляет/очищает `used_by`)*

| Pattern | Classes | P | Apps | Что проверяет |
|---|---|---|---|---|
| `NIC-ATTACH-DETACH-OK` | CRUD,STATE | P1 | 1 (nic) | (см. секцию NetworkInterface) roundtrip Attach→Detach; `used_by` зеркалит привязку. Verifies REQ-NIC-03. |
