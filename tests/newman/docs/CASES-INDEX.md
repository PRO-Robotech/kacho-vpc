# newman — индекс уникальных кейсов

~755 кейсов / 245+ паттернов.

> Contract-removal: удалены RPC и их кейсы — `*-MV-*` (Move у
> Network/Subnet/Address/RouteTable/SecurityGroup/Gateway), `*-REL-*`
> (Subnet.Relocate), NIC `AttachToInstance`/`DetachFromInstance` (паттерны
> `NIC-ATTACH-*`/`NIC-DETACH-*`/`NIC-DEL-NEG-ATTACHED`/`NIC-ATTACH-CONC-BURST`),
> AddressPool `BindAsAddressOverride`/`UnbindAddressOverride`/`Check`/
> `ExplainResolution` (`IPL-ADDROVR-*`/`IPL-CHK-*`/`IPL-EXPLAIN-*`/
> `IPL-DEL-NEG-OVERRIDE-EXISTS`/`IPL-RESOLVE-{SELECTOR,OVERRIDE}-FAMILY-SKIP`),
> и весь `InternalCloudService` PoolSelector suite (`CLD-SEL-*`, файл
> `cases/internal-cloud.py` удален). IPAM cascade сведен к
> network_default → zone_default → global_default.

> v20: AddressPool split CIDR family (`cidr_blocks` → `v4_cidr_blocks`
> + `v6_cidr_blocks`) — payload existing IPL-* кейсов обновлен под split-shape,
> добавлено 18 новых case-id (16 IPL-* в `cases/internal-pool.py` + 2 ADR-* в
> `cases/address.py`). Покрытие: Create v4-only/v6-only/dual-stack + cross-family/
> both-empty валидация; Update replace_v4/v6 семантика; ExplainResolution
> matched_via="none" fall-through; Bind* family-agnostic; cascade Step 1/2/3 family-
> skip + dual-stack zone_default.

> v18: + кейсы NetworkInterface (first-class ресурс) — секция
> «NetworkInterface (NIC)» ниже; + v6-Subnet / optional-CIDR-Subnet / SG-без-network /
> NIC↔Subnet-RESTRICT / multi-resource delete-chain / operation-history-survives-delete /
> Network-public-проекция-без-data-plane-id / v6-CIDR-через-verbs. Любой новый кейс ОБЯЗАН
> пройти `scripts/validate-cases.py` (дубль case-id и не-каталогизированный кейс →
> hard-fail в CI до newman; правила добавления кейсов — ниже).

> Из этого каталога выведен **нормативный регламент продуктовых требований** —
> `PRODUCT-REQUIREMENTS.md` (`REQ-*`: что продукт ДОЛЖЕН / НЕ ДОЛЖЕН; ведут тестировщики).
> Соответствие регламенту проверяется при ревью изменений.
> Каждый кейс должен мапиться на `REQ-*` (поле `Validated-by` в регламенте).

> v16: + 45 кейсов для internal/admin-only IPAM RPC —
> `internal-pool` (26 → 40 после v20: AddressPool CRUD + bindings + Check/ExplainResolution/Utilization/ListAddresses
> + split-shape family-фильтр и replace-семантика, prefix `IPL-*`). replace-семантика
> Update убрана (proto drop `replace_v4/v6_cidr_blocks`); удалены `IPL-UPD-REPLACE-V4/-REPLACE-V6/
> -CLEAR-V6-DUALSTACK-TO-V4-ONLY/-NO-FLAGS-NOOP/-EMPTY-BOTH-REPLACE`; добавлены `IPL-ADDCIDR-OK`,
> `IPL-RMCIDR-OK`, `IPL-RMCIDR-NEG-INUSE` (CIDR-management как у Subnet; Verifies REQ-IPL-UPD-01 /
> REQ-IPL-ADDCIDR-01 / REQ-IPL-RMCIDR-01 / REQ-IPL-RMCIDR-02).
> + `IPL-CR-NEG-OVERLAP`, `IPL-ADDCIDR-NEG-OVERLAP` (DB-level overlap-prevention для
> AddressPool CIDR — EXCLUDE gist на `address_pool_cidrs`; пересечение per kind →
> FailedPrecondition "address pool CIDRs can not overlap"; Verifies REQ-IPL-OVERLAP-01).
> `internal-cloud` (4: Cloud poolSelector set/get/unset, prefix `CLD-*`). Эти RPC возвращают
> ресурсы напрямую (не Operation); таблицы паттернов ниже их не индексируют (кейсы — в `cases/internal-*.py`).
>
> Geography (Region/Zone) вынесена в сервис compute: suite `internal-region-zone`
> (15 cases, `RGN-*`/`ZON-*`) удален из этого репозитория; Region/Zone теперь покрываются в compute.

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
| Move | 6 | Move в другой project |
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
| `*-AUTHZ-EMPTY-PROJECT-HEADER` | AUTHZ | P1 | 6 (add,gat,net,rou,sec,sub) | List с пустым x-kacho-project-id header → текущее: 200 (dev mode) |
| `NET-SUBNET-ADDR-NIC-DELETE-CHAIN` | CONF,STATE | P0 | 1 (sub) | Multi-resource delete-chain: Network→Subnet→Address(internal)→NIC создаются, затем удаляются строго снизу вверх (NIC→Address→Subnet→Network); попытка удалить parent раньше child → `FailedPrecondition`. Verifies REQ-DEL-06/07/08. |
| `*-HEADERS-MISSING-CT` | NEG,VAL | P3 | 3 (add,gat,net) | POST без Content-Type → 415 или 400 или 200 (lenient) |
| `*-METHOD-DELETE-LIST` | NEG,VAL | P3 | 6 (add,gat,net,rou,sec,sub) | DELETE на List endpoint (без id) → 405 или 404 |
| `*-METHOD-PUT-NOT-ALLOWED` | NEG,VAL | P3 | 6 (add,gat,net,rou,sec,sub) | PUT на List endpoint → 405 или 404 |

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
| `SUB-CIDR-ADD-V6-OK` | CRUD | P1 | 1 (sub) | AddCidrBlocks с IPv6-блоком → v6_cidr_blocks обновлен; Subnet становится dual-stack. Verifies REQ-CIDR-10. |
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
| `*-CR-BVA-NAME-EMPTY` | BVA,VAL | P2 | 6 (add,gat,net,rou,sec,sub) | Create с empty name → VPC permissive (200) или 400 |
| `*-CR-BVA-NAME-MAX-63` | BVA | P2 | 6 (add,gat,net,rou,sec,sub) | Create с name len=63 (max) → ok |
| `*-CR-BVA-NAME-OVER-64` | BVA,VAL | P1 | 6 (add,gat,net,rou,sec,sub) | Create с name len=64 (over-max) → InvalidArgument |
| `*-CR-CONF-PROJECT-NF-TEXT` | CONF,NEG | P1 | 2 (add,net) | Create network в garbage project → 'Project .. not found' |
| `*-CR-CONF-NET-NF-TEXT` | CONF,NEG | P1 | 3 (rou,sec,sub) | Create subnet в garbage network → точный текст 'Network .. not found' |
| `*-CR-CONF-SUB-NF-TEXT` | CONF,NEG | P1 | 1 (add) | Create address с garbage subnet → точный текст 'Subnet .. not found' |
| `*-CR-CRUD-EXT` | CRUD | P1 | 1 (add) | Create external Address → IP из default pool |
| `ADR-CR-CRUD-EXT-V6` | CRUD | P1 | 1 (add) | Create external_ipv6 Address → IP из v6 pool (sparse counter allocator/60). |
| `ADR-CR-NEG-EXT-V6-NO-POOL` | NEG | P0 | 1 (add) | Create external_ipv6 в зоне без v6 pool → Operation `FailedPrecondition` (cascade resolve fails / pool not initialised). |
| `ADR-DEL-EXT-V6-RELEASE-REUSE` | STATE,CONF | P1 | 1 (add) | Delete v6 Address пушит offset в `ipv6_released_offsets`; следующий Allocate берет его first → reused IP равен первому. Verifies sparse-allocator release-reuse contract. |
| `ADR-CR-EXT-V6-FAMILY-FALLTHROUGH` | CONF,NEG | P0 | 1 (add) | Family-filter в cascade resolve: external_v6 в зоне без v6 pool → cascade отвергает global v4 default по family и проваливается с `FailedPrecondition` (код 9). До был bug — `Internal` (код 13). |
| `ADR-CR-EXT-FALLTHROUGH-V4` | CONF,NEG | P0 | 1 (add) | Split-shape: Address.Create v4 в zone с v6-only default pool → cascade family-skip на каждом шаге → `FailedPrecondition` (код 9, не 13). Зеркало `ADR-CR-EXT-V6-FAMILY-FALLTHROUGH` для v4. Verifies REQ-RESOLVE-02. |
| `ADR-CR-EXT-FALLTHROUGH-V6` | CONF,NEG | P0 | 1 (add) | Split-shape: Address.Create v6 в zone с v4-only default pool → cascade family-skip → `FailedPrecondition` (код 9). После cascade использует `len(V4CIDRBlocks)>0`/`len(V6CIDRBlocks)>0` вместо runtime-парсинга CIDR. Verifies REQ-RESOLVE-01. |
| `*-CR-CRUD-INT` | CRUD | P1 | 1 (add) | Create internal Address → IP в subnet |
| `*-CR-CRUD-OK` | CRUD | P1 | 5 (gat,net,rou,sec,sub) | Create subnet → Operation → Subnet visible in GET |
| `SUB-CR-NO-CIDR-OK` | CRUD | P1 | 1 (sub) | Create Subnet без `v4_cidr_blocks` (только v6 или вообще без IPv4) → 200, CIDR-less Subnet. Verifies REQ-CIDR-08. |
| `SUB-CR-NEG-ADDR-INTO-CIDRLESS` | NEG,CONF | P1 | 1 (sub) | Create internal Address в Subnet без IPv4-CIDR → `FailedPrecondition`/`InvalidArgument` (некуда аллоцировать v4-IP). Verifies REQ-CIDR-08. |
| `SUB-CR-V6-OK` | CRUD | P1 | 1 (sub) | Create Subnet с `v6_cidr_blocks` (IPv6-only или dual-stack) → 200, v6_cidr_blocks виден в GET. Verifies REQ-CIDR-09. |
| `SG-CR-WITH-NETWORK-OK` | CRUD | P1 | 1 (sec) | Create SecurityGroup с валидным `network_id` → 200, network_id виден; SG привязан к сети. Verifies REQ-RES-07. |
| `SG-NET-01-NEG-CREATE-NO-NETWORK` | VAL,NEG | P0 | 1 (sec) | Create SecurityGroup без `network_id` → sync 400 INVALID_ARGUMENT `network_id required` (Operation НЕ создается; network_id mandatory). Verifies REQ-RES-07. |
| `SG-NET-02-CREATE-OK` | CRUD | P0 | 1 (sec) | Happy: Create SecurityGroup с валидным `network_id` → Operation done без error → GET echoes `networkId`. Verifies REQ-RES-07. |
| `SG-NET-03-NEG-NETWORK-NOTFOUND` | CONF,NEG | P1 | 1 (sec) | Create SG с well-formed но несуществующим `network_id` (`enp00…`) → sync 404 NOT_FOUND `Network … not found` (fast-fail, Operation НЕ создается). Verifies REQ-RES-07. |
| `SG-NET-04-NEG-UPDATE-MASK-NETWORK` | STATE,VAL,NEG | P1 | 1 (sec) | Update реальной SG с `update_mask=network_id` → sync 400 INVALID_ARGUMENT (network_id не в known-mask = immutable); GET → networkId неизменен. Verifies REQ-RES-07. |
| `SG-NET-07-NEG-RULE-CROSS-NETWORK-CREATE` | VAL,NEG,CONF | P1 | 1 (sec) | Create SG в net-A с SG-target rule на SG из net-B → 400 INVALID_ARGUMENT `security group rule can only reference a security group in the same network` + `BadRequest.field_violations[].field = rule_specs[0].security_group_id`. Verifies REQ-SG-RULE-SAME-NETWORK. |
| `SG-NET-08-RULE-SAME-NETWORK-OK` | CRUD,STATE | P1 | 1 (sec) | Happy: Create SG в net-A с SG-target rule на SG той же net-A → Operation done; rule сохранен (`securityGroupId` виден в GET). Verifies REQ-SG-RULE-SAME-NETWORK. |
| `SG-NET-09-NEG-RULE-CROSS-NETWORK-UPDATERULES` | VAL,NEG,STATE | P1 | 1 (sec) | UpdateRules на SG(net-A), добавляющий SG-target rule на SG из net-B → 400 INVALID_ARGUMENT same-network + `field_violations[].field = addition_rule_specs[0].security_group_id`; набор правил не изменен. Verifies REQ-SG-RULE-SAME-NETWORK. |
| `SG-NET-09-RULE-SAME-NETWORK-UPDATERULES-OK` | CRUD,STATE | P1 | 1 (sec) | Happy: UpdateRules на SG(net-A), добавляющий SG-target rule на SG той же net-A → Operation done; rule виден в GET. Verifies REQ-SG-RULE-SAME-NETWORK. |
| `NIC-CR-NEG-BAD-SUBNET` | NEG,CONF | P1 | 1 (nic) | Create NIC с garbage `subnet_id` → async `NotFound` 'Subnet .. not found'. Verifies REQ-NIC-01. |
| `NIC-CR-WITH-ADDR-OK` | CRUD | P1 | 1 (nic) | Create NIC с `v4_address_ids` (предсозданный internal Address) → 200, address привязан, `Address.used`=true. Verifies REQ-NIC-04. |
| `NIC-CR-WITH-V6-ADDR-OK` | CRUD | P1 | 1 (nic) | Create NIC с `v6_address_ids` (предсозданный v6 internal Address) → 200, v6-address привязан. Verifies REQ-NIC-04. |
| `NIC-CR-WITH-BOTH-ADDR-OK` | CRUD | P1 | 1 (nic) | Create NIC с `v4_address_ids` И `v6_address_ids` одновременно (dual-stack линковка при создании) → 200, оба address привязаны. Verifies REQ-NIC-04. |
| `NIC-CR-WITH-UNBOUND-SG-OK` | CRUD | P2 | 1 (nic) | Create NIC с `security_group_ids[]` на SG (bound к сети NIC'а) → 200; NIC ссылается на SG. (SG теперь mandatory-network — network-less SG больше нет; SG привязан к `{{netId}}`.) Verifies REQ-NIC-05. |
| `RT-CR-STATE-SUBNET-AUTO-ASSOC` | CRUD,STATE | P1 | 1 (rou) | Create RouteTable с `network_id` → Subnet'ы этой сети (с `route_table_id IS NULL`) автоматически получают `route_table_id` = id новой RT. Реализация: DB-trigger `rt_auto_assoc_subnets_trg` (миграция 0019). Verifies REQ-RT-SUBNET-AUTO-ASSOC. |
| `SUB-CR-STATE-AUTO-PICK-RT` | CRUD,STATE | P1 | 1 (sub) | Create Subnet в сети с уже существующей RouteTable → `subnet.route_table_id` auto-picked самой ранней RT по `created_at`. Реализация: DB-trigger `subnet_auto_pick_rt_trg` BEFORE INSERT (миграция 0019). Verifies REQ-SUB-AUTO-PICK-RT. |
| `*-CR-IDM-RETRY` | CONC,IDM | P1 | 1 (net) | Retry-safe: повторный Create same input → consistent result |
| `*-CR-NEG-CIDR-OVERLAP` | NEG | P0 | 1 (sub) | Create двух subnet с пересекающимися CIDR → второй FailedPrecondition |
| `*-CR-NEG-DUP-NAME` | CONC,NEG | P1 | 2 (net,sub) | Create с duplicate name в project → async ALREADY_EXISTS |
| `*-CR-NEG-DUP-NAME-CHECK` | CONC,NEG | P1 | 6 (add,gat,net,rou,sec,sub) | Создать дубль с тем же name → ALREADY_EXISTS (UNIQUE есть для всех ресурсов) |
| `*-CR-NEG-PROJECT-NF` | CONF,NEG | P0 | 1 (gat) | Create Gateway в несуществующий project → async NotFound |
| `*-CR-NEG-PROJECT-NOT-FOUND` | NEG | P0 | 1 (net) | Create с garbage projectId → async NOT_FOUND |
| `*-CR-NEG-NETWORK-NF` | NEG | P0 | 1 (rou) | Create в несуществующую network → async NotFound |
| `*-CR-NEG-NETWORK-NOT-FOUND` | NEG | P0 | 1 (sub) | Create в несуществующей network → async NOT_FOUND |
| `*-CR-NEG-SUBNET-NOT-FOUND` | NEG | P0 | 1 (add) | Create internal с garbage subnetId → async NotFound |
| `*-CR-PAIRWISE-00` | CRUD,VAL | P2 | 1 (sub) | Pairwise [0]: zone=zone-a prefix=/24 dhcp=True |
| `*-CR-PAIRWISE-01` | CRUD,VAL | P2 | 1 (sub) | Pairwise [1]: zone=zone-a prefix=/28 dhcp=False |
| `*-CR-PAIRWISE-02` | CRUD,VAL | P2 | 1 (sub) | Pairwise [2]: zone=zone-a prefix=/16 dhcp=True |
| `*-CR-PAIRWISE-03` | CRUD,VAL | P2 | 1 (sub) | Pairwise [3]: zone=zone-b prefix=/24 dhcp=False |
| `*-CR-PAIRWISE-04` | CRUD,VAL | P2 | 1 (sub) | Pairwise [4]: zone=zone-b prefix=/28 dhcp=True |
| `*-CR-PAIRWISE-05` | CRUD,VAL | P2 | 1 (sub) | Pairwise [5]: zone=zone-b prefix=/16 dhcp=False |
| `*-CR-PAIRWISE-06` | CRUD,VAL | P2 | 1 (sub) | Pairwise [6]: zone=zone-c prefix=/24 dhcp=True |
| `*-CR-PAIRWISE-07` | CRUD,VAL | P2 | 1 (sub) | Pairwise [7]: zone=zone-c prefix=/28 dhcp=False |
| `*-CR-PAIRWISE-08` | CRUD,VAL | P2 | 1 (sub) | Pairwise [8]: zone=zone-c prefix=/16 dhcp=True |
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
| `*-CR-VAL-EMPTY-BODY` | NEG,VAL | P2 | 6 (add,gat,net,rou,sec,sub) | Create с пустым body → 400 |
| `*-CR-VAL-EXT-WITH-SUBNET-FK` | NEG,VAL | P1 | 1 (add) | Create external + internal со заданным subnet_id → 400 oneof |
| `*-CR-VAL-EXTRA-FIELDS` | VAL | P3 | 1 (net) | Create Network с unknown полем в body → silent ignore (200) или 400 |
| `*-CR-VAL-PROJECT-REQUIRED` | VAL | P0 | 2 (gat,net) | Create без project → InvalidArgument |
| `*-CR-VAL-LABELS-INVALID-KEY-CHAR` | VAL | P1 | 6 (add,gat,net,rou,sec,sub) | Create с invalid char в label key → 400 |
| `*-CR-VAL-LABELS-STRING-TYPE` | NEG,VAL | P2 | 6 (add,gat,net,rou,sec,sub) | Create с labels=строка (вместо object) → 400 InvalidArgument |
| `*-CR-VAL-LABELS-UPPERCASE-KEY` | VAL | P1 | 6 (add,gat,net,rou,sec,sub) | Create с UPPERCASE label key → 400 |
| `*-CR-VAL-MALFORMED-JSON` | NEG,VAL | P2 | 6 (add,gat,net,rou,sec,sub) | Create с malformed JSON → 400 |
| `*-CR-VAL-MISSING-TYPE` | NEG,VAL | P1 | 1 (gat) | Create Gateway без gateway type oneof → 400 |
| `*-CR-VAL-NAME-DIGIT-START` | VAL | P1 | 6 (add,gat,net,rou,sec,sub) | Create с name начинающимся с цифры → 400 (контракт Kachō regex) |
| `*-CR-VAL-NAME-HYPHEN-START` | VAL | P1 | 6 (add,gat,net,rou,sec,sub) | Create с name начинающимся с дефиса → 400 |
| `*-CR-VAL-NAME-NULL` | NEG,VAL | P2 | 6 (add,gat,net,rou,sec,sub) | Create с name=null → 400 |
| `*-CR-VAL-NAME-SPECIAL-CHARS` | VAL | P1 | 6 (add,gat,net,rou,sec,sub) | Create с спец-символами в name → 400 |
| `*-CR-VAL-NAME-UPPERCASE` | VAL | P2 | 6 (add,gat,net,rou,sec,sub) | Create с UPPERCASE name → VPC permissive (200) или 400 |
| `*-CR-VAL-NETWORK-REQUIRED` | NEG,VAL | P0 | 2 (rou,sec) | Create без network_id → InvalidArgument |
| `*-CR-VAL-REQ-PROJECTID` | VAL | P0 | 6 (add,gat,net,rou,sec,sub) | Create без required поля 'projectId' → 400 InvalidArgument |
| `*-CR-VAL-REQ-NAME` | VAL | P0 | 6 (add,gat,net,rou,sec,sub) | Create без required поля 'name' → 400 InvalidArgument |
| `*-CR-VAL-REQ-NETWORKID` | VAL | P0 | 3 (rou,sec,sub) | Create без required поля 'networkId' → 400 InvalidArgument |
| `*-CR-VAL-REQ-V4CIDRBLOCKS` | VAL | P0 | 1 (sub) | Create без required поля 'v4CidrBlocks' → 400 InvalidArgument |
| `*-CR-VAL-REQ-ZONEID` | VAL | P0 | 1 (sub) | Create без required поля 'zoneId' → 400 InvalidArgument |
| `*-CR-VAL-RESERVED-USED-OK` | VAL | P2 | 1 (add) | Create address с reserved/used флагами (если разрешено) → 200 или 400 |
| `*-CR-VAL-ROUTE-EMPTY-HOP` | NEG,VAL | P1 | 1 (rou) | static_routes validation: RT-CR-VAL-ROUTE-EMPTY-HOP |
| `*-CR-VAL-ROUTE-EMPTY-PREFIX` | NEG,VAL | P1 | 1 (rou) | static_routes validation: RT-CR-VAL-ROUTE-EMPTY-PREFIX |
| `*-CR-VAL-ROUTE-INVALID-HOP` | NEG,VAL | P1 | 1 (rou) | static_routes validation: RT-CR-VAL-ROUTE-INVALID-HOP |
| `*-CR-VAL-ROUTE-INVALID-PREFIX` | NEG,VAL | P1 | 1 (rou) | static_routes validation: RT-CR-VAL-ROUTE-INVALID-PREFIX |
| `*-CR-VAL-ROUTE-OK` | CRUD,VAL | P1 | 1 (rou) | static_routes validation: RT-CR-VAL-ROUTE-OK |
| `*-CR-VAL-SPEC-ONEOF` | VAL | P0 | 1 (add) | Create без external/internal spec → InvalidArgument |
| `*-CR-VAL-ZONE-REQUIRED` | VAL | P0 | 1 (sub) | Create без zone_id → InvalidArgument (zone_id required) |
| `*-CR-VAL-ZONE-UNKNOWN` | VAL | P0 | 1 (sub) | Create с несуществующей зоной → InvalidArgument (dynamic whitelist) |

### Delete

*Удаление (async)*

| Pattern | Classes | P | Apps | Что проверяет |
|---|---|---|---|---|
| `*-DEL-AUTHZ-NF-SYNC` | AUTHZ,NEG | P1 | 6 (add,gat,net,rou,sec,sub) | Delete несуществующего → sync 404 |
| `*-DEL-CONF-FULLTEXT` | CONF,NEG | P1 | 6 (add,gat,net,rou,sec,sub) | Delete garbage → 'Subnet .. not found' |
| `*-DEL-CONF-NF-TEXT` | CONF,NEG | P1 | 6 (add,gat,net,rou,sec,sub) | Delete несуществующего Subnet → точный текст 'Subnet .. not found' |
| `*-DEL-CRUD-EMPTY-OK` | CRUD | P1 | 1 (sub) | Delete Subnet без зависимостей → OK |
| `*-DEL-CRUD-OK` | CRUD | P1 | 6 (add,gat,net,rou,sec,sub) | Subnet Delete happy path |
| `*-DEL-CRUD-ONLY-DEFAULT-SG` | CRUD,STATE | P1 | 1 (net) | Delete Network у которой есть только default-SG → OK (auto-cleanup default) |
| `NET-DEL-CRUD-DEFAULT-SG-REMOVED` | CRUD,STATE | P1 | 1 (net) | После Network.Delete ее default SG обязан исчезнуть — explicit `GET /securityGroups/{defSgId}` → 404 (life-cycle default SG (1)). Verifies REQ-NET-LSG-DEFAULT. |
| `*-DEL-NEG-HAS-ADDRESSES` | CONF,NEG,STATE | P0 | 1 (sub) | Delete Subnet с internal Address → FailedPrecondition (FK RESTRICT) |
| `SUB-DEL-NEG-HAS-V6-ADDRESS` | CONF,NEG,STATE | P0 | 1 (sub) | Delete Subnet с internal **v6** Address → `FailedPrecondition` (FK `addresses_internal_subnet_fkey` через generated-колонку, выводимую из v4 ИЛИ v6 — миграция 0013). Verifies REQ-DEL-06. |
| `SUB-DEL-NEG-HAS-NIC` | CONF,NEG,STATE | P0 | 1 (sub) | Delete Subnet с привязанным NetworkInterface → sync `FailedPrecondition` со списком NIC-id (FK `network_interfaces.subnet_id` ON DELETE RESTRICT — миграция 0012). Verifies REQ-DEL-07. |
| `NET-DEL-NEG-HAS-SUBNET-WITH-NIC` | CONF,NEG,STATE | P0 | 1 (net) | Delete Network, у которой Subnet с NIC → `FailedPrecondition` 'network is not empty' (транзитивно: NIC блокирует Subnet, Subnet блокирует Network). Verifies REQ-DEL-08. |
| `NIC-DEL-OK` | CRUD | P1 | 1 (nic) | Delete NetworkInterface (не приаттаченный) → Operation → NIC исчезает из GET; привязанные Address освобождаются (`Address.used`=false). Verifies REQ-NIC-02. |
| `ADDR-DEL-NEG-USED-BY-NIC` | CONF,NEG,STATE | P0 | 1 (add/nic) | Delete Address, который референсится NIC'ом (`v4_address_ids`/`v6_address_ids`) → `FailedPrecondition` (сначала detach с NIC). Verifies REQ-NIC-04 / REQ-DEL-09. |
| `*-DEL-NEG-HAS-NONDEFAULT-SG` | CONF,NEG,STATE | P0 | 1 (net) | Delete Network с НЕ-default SG → FailedPrecondition (RESTRICT FK) |
| `*-DEL-NEG-HAS-ROUTE-TABLE` | CONF,NEG,STATE | P0 | 1 (net) | Delete Network c RouteTable → FailedPrecondition |
| `*-DEL-NEG-HAS-SUBNETS` | CONF,NEG,STATE | P0 | 1 (net) | Delete Network c Subnet → FailedPrecondition (FK RESTRICT) |
| `*-DEL-NEG-NF-INVALID-PREFIX` | NEG,STATE | P1 | 1 (net) | Delete с id без VPC-префикса → sync 404 |
| `*-DEL-STATE-DEFAULT-SG` | NEG,STATE | P1 | 1 (sec) | Delete default-SG напрямую → должен fail (нельзя delete default SG в обход) |
| `SG-DEL-NEG-NIC-ATTACHED` | NEG,STATE,CONF | P0 | 1 (sec) | Delete SG, прилинкованного к NIC через `security_group_ids[]` → `FailedPrecondition`. **persistent-RED** (rule #13, verifies [#27](https://github.com/PRO-Robotech/kacho-vpc/issues/27)): пока DB-уровневый ref-trigger не реализован, кейс падает (SG удаляется, оставляя dangling ref в NIC.security_group_ids). Verifies REQ-SG-DEL-NIC-REFCHECK. |

### Get

*Чтение по id*

| Pattern | Classes | P | Apps | Что проверяет |
|---|---|---|---|---|
| `*-GET-CONF-FULLTEXT` | CONF,NEG | P1 | 6 (add,gat,net,rou,sec,sub) | Get garbage → 'Subnet <id> not found' формат |
| `*-GET-CONF-NF-TEXT` | CONF,NEG | P1 | 7 (add,gat,net,ope,rou,sec,sub) | Get garbage — точный текст 'Subnet .. not found' |
| `*-GET-CRUD-OK` | CRUD | P1 | 1 (ope) | Get свежесозданной operation → done=true с response |
| `NET-GET-NO-VPNID-OK` | CONF,CRUD | P1 | 1 (net) | Get Network через **публичный** API → ответ НЕ содержит data-plane-идентификатора (data-plane-поле прежней реализации удалено; кейс — регрессионный guard против reintroduce). Verifies REQ-RES-08 / REQ-CONF-06. |
| `*-GET-NEG-EMPTY-ID` | NEG | P2 | 1 (net) | Get empty id → 404 (gRPC-gateway routing) |
| `*-GET-NEG-NF` | NEG | P0 | 4 (add,gat,rou,sec) | Get garbage → 404 |
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
| `*-GBV-CONF-NOLEAK-FOR-EXISTING-OTHER` | AUTHZ,CONF | P0 | 1 (add) | GetByValue адреса из другого project → NotFound (security info-leak) |
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
| `SUBNET-LF-D-VISIBLE` | AUTHZ,CONF | P0 | 1 (list-filter-d) | per-object List (: subject с per-object viewer-грантом на подмножество видит granted subnet. Verifies per-object List-фильтр. |
| `SUBNET-LF-D-NOLEAK` | AUTHZ,NEG | P0 | 1 (list-filter-d) | per-object List no-leak : non-granted subnet того же проекта отсутствует в List. |
| `SUBNET-LF-D-GET-404` | AUTHZ,NEG | P0 | 1 (list-filter-d) | per-object no-leak (: Get(hidden) → 404 NOT_FOUND, НЕ 403 (не подтверждаем existence). |
| `SUBNET-LF-D-GET-OK` | AUTHZ,CONF | P1 | 1 (list-filter-d) | read==enforce (: Get(visible) → 200 (паритет с List-видимостью). |
| `SUBNET-LF-D-NONE` | AUTHZ,NEG | P1 | 1 (list-filter-d) | per-object List (: subject без гранта → пустой List, НЕ весь проект. |
| `*-LST-AUTHZ-CROSS-PROJECT-ISOLATION` | AUTHZ,CRUD | P0 | 3 (add,gat,net) | Project isolation: ресурс в projectA не виден в List по projectB |
| `*-LST-BVA-PAGESIZE-1` | BVA,PAGE | P2 | 6 (add,gat,net,rou,sec,sub) | List pageSize=1 → ≤1 item |
| `*-LST-BVA-PAGESIZE-OVER-MAX` | BVA,VAL | P2 | 6 (add,gat,net,rou,sec,sub) | List pageSize=10000 → InvalidArgument |
| `*-LST-BVA-PAGESIZE-ZERO` | BVA | P2 | 6 (add,gat,net,rou,sec,sub) | List pageSize=0 → default applied (200) |
| `*-LST-CONTRACT-NEVER-EXCEEDS-PAGESIZE` | CRUD,PAGE | P2 | 6 (add,gat,net,rou,sec,sub) | List с pageSize=5 → не более 5 элементов в response |
| `*-LST-CRUD-OK` | CRUD | P1 | 6 (add,gat,net,rou,sec,sub) | List subnets в project → 200 |
| `NIC-LIST-OK` | CRUD | P1 | 1 (nic) | List NetworkInterfaces в project → 200; созданный NIC присутствует; в ответе только lean control-plane-проекция (инфра/data-plane-полей нет). Verifies REQ-NIC-06. |
| `SG-LIST-FILTER-NETWORK-OK` | CRUD,FILTER | P2 | 1 (sec) | List SecurityGroups с фильтром по `network_id` → возвращает только SG net-A; SG из другой сети net-B отсутствует. (negative-сторона = SG из другой сети, т.к. network-less SG больше нет.) Verifies REQ-RES-07. |
| `*-LST-DOUBLE-PROJECT-PARAM` | VAL | P3 | 5 (add,gat,net,rou,sec) | List с дубликатом projectId param → 200 (last wins) или 400 |
| `*-LST-FILTER-CASE-SENSITIVITY` | FILTER | P3 | 1 (gat) | Filter case-sensitivity на name field |
| `*-LST-FILTER-EMPTY` | CRUD,FILTER | P2 | 1 (gat) | List Gateway с пустым filter expression → 200 (filter optional) |
| `*-LST-FILTER-GARBAGE` | FILTER,VAL | P1 | 6 (add,gat,net,rou,sec,sub) | List с garbage filter syntax → 400 InvalidArgument |
| `*-LST-FILTER-MATCH` | CRUD,FILTER | P2 | 6 (add,gat,net,rou,sec,sub) | Создать ресурс → list filter=name='X' → ресурс в результатах |
| `*-LST-FILTER-MULTI-CONDITIONS` | FILTER | P3 | 1 (net) | List с filter из несколько условий — multi-condition filter |
| `*-LST-FILTER-NAME-OK` | CRUD,FILTER | P2 | 6 (add,gat,net,rou,sec,sub) | List с filter name="foo" → 200 |
| `*-LST-FILTER-SPECIAL-CHARS` | FILTER,VAL | P3 | 5 (add,gat,net,rou,sec) | List с filter содержащим спец-символы → 400 или 200 |
| `*-LST-FILTER-UNKNOWN-FIELD` | FILTER,VAL | P2 | 6 (add,gat,net,rou,sec,sub) | List с filter на unsupported field → 400 InvalidArgument |
| `*-LST-PAGE-NEGATIVE-SIZE` | BVA,VAL | P2 | 5 (add,gat,net,rou,sec) | List с pageSize=-1 → 400 или 200 |
| `*-LST-PAGE-ROUNDTRIP` | BVA,CRUD,PAGE | P2 | 6 (add,gat,net,rou,sec,sub) | Pagination: получить пустой/не-пустой ответ + nextPageToken и пройти еще раз с ним |
| `*-LST-PAGE-TOKEN-GARBAGE` | PAGE,VAL | P1 | 6 (add,gat,net,rou,sec,sub) | List с garbage page_token → InvalidArgument |
| `*-LST-PAGESIZE-1001` | BVA,VAL | P1 | 5 (add,gat,net,rou,sec) | List с pageSize=1001 (over max) → 400 |
| `*-LST-PAGESIZE-EXACTLY-1000` | BVA | P2 | 5 (add,gat,net,rou,sec) | List с pageSize=1000 (boundary max) → 200 |
| `*-LST-PERF-BASELINE` | CRUD,PERF | P2 | 6 (add,gat,net,rou,sec,sub) | List response time < 500ms (perf baseline) |
| `*-LST-SEC-FILTER-SQLI` | NEG,VAL | P0 | 6 (add,gat,net,rou,sec,sub) | Security: SQL injection в filter → не 500 |
| `*-LST-VAL-PROJECT-REQUIRED` | AUTHZ,VAL | P0 | 6 (add,gat,net,rou,sec,sub) | List без projectId → InvalidArgument |

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
| `*-LOP-CRUD-OK` | CRUD,NEG | P1 | 6 (add,gat,net,rou,sec,sub) | ListOperations возвращает create-op |
| `*-LOP-NEG-PARENT-NF` | NEG | P2 | 6 (add,gat,net,rou,sec,sub) | ListOperations несуществующего ресурса → 404 или 200 пустой |
| `NET-LISTOPS-AFTER-DELETE-OK` | CRUD,STATE | P1 | 1 (net) | После Delete ресурса его `<Resource>.ListOperations(<id>)` все еще содержит историю операций (create+delete) — операции переживают удаление ресурса. Verifies REQ-OPS-04. |
| `OP-LIST-AFTER-DELETE-OK` | CRUD,STATE | P1 | 1 (ope) | `OperationService.Get(opId)` по операции удаленного ресурса → 200 (запись операции не удаляется вместе с ресурсом). Verifies REQ-OPS-04. |

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

### Move (удалено)

Move RPC у Network/Subnet/Address/RouteTable/SecurityGroup/Gateway удалены
(contract-removal). Паттерны `*-MV-*` и `SG-NET-19-NEG-MOVE-FORBIDDEN`
сняты вместе с RPC.

### Relocate (удалено)

`Subnet.Relocate` RPC удален (contract-removal). Паттерны `*-REL-*`
(`SUB-REL-NEG-IN-USE` / `SUB-REL-STATE-NO-ADDRESSES-OK` / `SUB-REL-VAL-NO-DEST`)
сняты вместе с RPC.

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
| `*-UPD-AUTHZ-NF-SYNC` | AUTHZ,NEG | P1 | 6 (add,gat,net,rou,sec,sub) | Update несуществующего → sync 404 от AuthZ-Get |
| `*-UPD-CONF-FULLTEXT` | CONF,NEG | P1 | 6 (add,gat,net,rou,sec,sub) | Update garbage → точный текст 'Subnet .. not found' |
| `*-UPD-CONF-NF-TEXT` | CONF,NEG | P1 | 6 (add,gat,net,rou,sec,sub) | Update несуществующего Subnet → точный текст 'Subnet .. not found' |
| `*-UPD-CRUD-DESC` | CRUD | P2 | 6 (add,gat,net,rou,sec,sub) | Update happy description |
| `*-UPD-CRUD-DESCRIPTION` | CRUD | P1 | 1 (net) | Update description через mask → success + новое значение видно |
| `*-UPD-CRUD-LABELS` | CRUD | P2 | 6 (add,gat,net,rou,sec,sub) | Update happy labels |
| `*-UPD-CRUD-MULTI-MASK` | CRUD,STATE | P2 | 6 (add,gat,net,rou,sec,sub) | Update с mask=name,description,labels → все три поля обновлены |
| `*-UPD-CRUD-NAME` | CRUD | P2 | 6 (add,gat,net,rou,sec,sub) | Update happy name |
| `*-UPD-CRUD-OK` | CRUD | P1 | 5 (add,gat,rou,sec,sub) | Update Subnet description |
| `*-UPD-NEG-NF-INVALID-PREFIX` | NEG,STATE | P1 | 1 (net) | Update с id без VPC-префикса → sync 404 (gateway prefix-routing) |
| `*-UPD-STATE-IMMUTABLE-CIDR` | STATE,VAL | P1 | 1 (sub) | Update с mask=v4_cidr_blocks → InvalidArgument (immutable) |
| `*-UPD-STATE-IMMUTABLE-EXTERNAL-IPV4-ADDRESS-SPEC` | CONF,STATE,VAL | P1 | 1 (add) | Update mask='external_ipv4_address_spec' (immutable) → 400 InvalidArgument (точный текст) |
| `*-UPD-STATE-IMMUTABLE-PROJECT` | STATE,VAL | P1 | 6 (add,gat,net,rou,sec,sub) | Update с mask=project_id → InvalidArgument (immutable) |
| `*-UPD-STATE-IMMUTABLE-PROJECT-ID` | CONF,STATE,VAL | P1 | 6 (add,gat,net,rou,sec,sub) | Update mask='project_id' (immutable) → 400 InvalidArgument (точный текст) |
| `*-UPD-STATE-IMMUTABLE-INTERNAL-IPV4-ADDRESS-SPEC` | CONF,STATE,VAL | P1 | 1 (add) | Update mask='internal_ipv4_address_spec' (immutable) → 400 InvalidArgument (точный текст) |
| `*-UPD-STATE-IMMUTABLE-NETWORK-ID` | CONF,STATE,VAL | P1 | 3 (rou,sec,sub) | Update mask='network_id' (immutable) → 400 InvalidArgument (точный текст) |
| `*-UPD-STATE-IMMUTABLE-V4-CIDR-BLOCKS` | CONF,STATE,VAL | P1 | 1 (sub) | Update mask='v4_cidr_blocks' (immutable) → 400 InvalidArgument (точный текст) |
| `*-UPD-STATE-IMMUTABLE-V6-CIDR-BLOCKS` | CONF,STATE,VAL | P1 | 1 (sub) | Update mask='v6_cidr_blocks' (immutable) → 400 InvalidArgument (точный текст) |
| `*-UPD-STATE-IMMUTABLE-ZONE-ID` | CONF,STATE,VAL | P1 | 1 (sub) | Update mask='zone_id' (immutable) → 400 InvalidArgument (точный текст) |
| `*-UPD-VAL-MASK-EMPTY` | STATE,VAL | P2 | 6 (add,gat,net,rou,sec,sub) | Update с пустой mask → full PATCH (200) |
| `*-UPD-VAL-MASK-MULTIPLE-UNKNOWN` | STATE,VAL | P2 | 6 (add,gat,net,rou,sec,sub) | Update с несколькими unknown полями в mask → 400 |
| `*-UPD-VAL-MASK-NAME-ONLY` | STATE,VAL | P2 | 6 (add,gat,net,rou,sec,sub) | Update mask=name → только name меняется, description/labels не трогаются |
| `*-UPD-VAL-UNKNOWN-MASK` | STATE,VAL | P1 | 6 (add,gat,net,rou,sec,sub) | Update с unknown field в UpdateMask → InvalidArgument |
| `SUB-UPD-V6-NOOP` | STATE,CRUD | P2 | 1 (sub) | Update с `v6_cidr_blocks` в body+mask → 200, операция done без error (soft-immutable: меняется не через Update, у нас no-op — реальное изменение через `:add-cidr-blocks`/`:remove-cidr-blocks`). Verifies REQ-UPD-05. |

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

### NetworkInterface (NIC) — first-class ресурс

*Проекция NIC — lean, control-plane only: `id`/`projectId`/`subnetId`/`v4AddressIds`/`v6AddressIds`/`securityGroupIds`/`usedBy`/`macAddress`/`status`/`name`/`labels`. Инфра/data-plane-полей у kacho-vpc нет — они появятся на стороне будущего data-plane сервиса `kacho-vpc-implement`; регрессионные кейсы guard'ят, что такие поля НИКОГДА не появятся на публичной поверхности. REST: `/vpc/v1/networkInterfaces`. Кейсы — в `cases/network-interface.py` (app-код `nic`). NIC-кейсы, совпадающие с generic-паттернами по суффиксу, — инстансы (`NIC-CR-CRUD-OK` → `*-CR-CRUD-OK`, `NIC-CR-NEG-DUP-NAME` → `*-CR-NEG-DUP-NAME`, `NIC-GET-*`/`NIC-LST-*`/`NIC-MV-*` и т.п.); ниже — NIC-специфичные паттерны.*

| Pattern | Classes | P | Apps | Что проверяет |
|---|---|---|---|---|
| `NIC-CR-CRUD-OK` | CRUD | P1 | 1 (nic) | (инстанс `*-CR-CRUD-OK`) Create NIC в Subnet → Operation → NIC в GET; lean-проекция, нет инфра-полей. Verifies REQ-NIC-01/REQ-NIC-06. |
| `NIC-CR-MAC-OK` | CRUD | P1 | 1 (nic) | Create NIC → `macAddress` в lean-проекции, формат `0e:xx:xx:xx:xx:xx` (lowercase hex, префикс `0e:`); Update name → MAC не меняется (самостоятельный сетевой интерфейс). Verifies REQ-NIC-08. |
| `NIC-CR-NEG-DUP-NAME` | CONC,NEG | P1 | 1 (nic) | (инстанс `*-CR-NEG-DUP-NAME`) Create NIC с duplicate name в project → async `ALREADY_EXISTS`. Verifies REQ-NAME-04. |
| `NIC-UPD-OK` | CRUD | P1 | 1 (nic) | Update NIC (name/labels/securityGroupIds через mask) → Operation → новые значения видны; subnetId/инфра-поля не меняются. Verifies REQ-NIC-07. |

### AttachToInstance / DetachFromInstance (удалено)

NIC `AttachToInstance` / `DetachFromInstance` RPC удалены (contract-removal).
NIC-ресурс и `used_by`-колонки сохранены, но через эти RPC не
выставляются. Паттерны `NIC-ATTACH-DETACH-OK` / `NIC-ATTACH-CONC-BURST` /
`NIC-ATTACH-NEG-ALREADY-USED` / `NIC-DETACH-*` / `NIC-DEL-NEG-ATTACHED` сняты.

---

### Concurrency / Burst

*Burst-best-effort cases для race-defense инвариантов через api-gateway. Newman пускает N `pm.sendRequest` почти-одновременно с одного runner'а; server обрабатывает в реальной race-window. True deterministic race — integration-территория (`internal/repo/*_integration_test.go`). Кейсы в `cases/concurrency.py` (новый файл).*

| Pattern | Classes | P | Apps | Что проверяет |
|---|---|---|---|---|
| `SUB-CR-CONC-OVERLAP-BURST` | CONC,NEG | P0 | 1 (sub) | 3 parallel Create Subnet same Network + same CIDR → 1 succeed + 2 FailedPrecondition (EXCLUDE `subnets_no_overlap_v4` race-defense). Verifies REQ-CIDR-02. |
| `ADR-CR-CONC-BURST-ALLOC` | CONC | P0 | 1 (add) | 5 parallel external Address.Create → 5 distinct IPs (UNIQUE `addresses_external_pool_ip_uniq` + retry on collision). Verifies REQ-IPAM-03. |
| `NIC-CR-CONC-MAC-UNIQUE` | CONC | P1 | 1 (nic) | 10 parallel NIC Create в одном subnet → 10 distinct MAC (UNIQUE `network_interfaces_mac_address_key` + `crypto/rand` MAC + retry on collision). Verifies REQ-NIC-08. |
| `SG-URL-CONC-OCC-CONFLICT` | CONC,STATE | P0 | 1 (sec) | 2 parallel UpdateRules same SG → 1 OK + 1 Aborted/FailedPrecondition (`xmin`-based OCC). |

### NIC negative + ephemeral lifecycle

| Pattern | Classes | P | Apps | Что проверяет |
|---|---|---|---|---|
| `NIC-CR-NEG-MULTI-V4-ADDR` | NEG,VAL | P0 | 1 (nic) | Create NIC с 2× `v4_address_ids` → InvalidArgument/FailedPrecondition (sync `validateNICAddressCardinality` либо DB CHECK constraint). Verifies REQ-NIC-04. |
| `NIC-CR-NEG-MULTI-V6-ADDR` | NEG,VAL | P0 | 1 (nic) | Симметрично для v6. |

### Subnet v6 / utilization / rollback

| Pattern | Classes | P | Apps | Что проверяет |
|---|---|---|---|---|
| `SUB-CR-NEG-DUP-CIDR-EXACT` | NEG,CONF | P0 | 1 (sub) | Create Subnet с CIDR exact match существующей → FailedPrecondition (EXCLUDE backstop). |
| `SUB-CR-NEG-V6-OVERLAP` | NEG,CONF | P0 | 1 (sub) | 2 v6-subnet overlapping в одной Network → 2nd FailedPrecondition (EXCLUDE `subnets_no_overlap_v6`). Verifies REQ-CIDR-02 для v6. |
| `SUB-LUA-CRUD-COUNT` | CRUD,STATE | P1 | 1 (sub) | Allocate 3 internal Address → `ListUsedAddresses` returns ≥3 entries. |
| `SUB-LUA-STATE-FRAGMENT` | STATE | P2 | 1 (sub) | Allocate 5 → delete middle 3 → ListUsedAddresses count decreased by exactly 3 (fragmentation handling). |
| `SUB-CR-NEG-ROLLBACK-NO-RESOURCE-IN-GET` | NEG,STATE | P1 | 1 (sub) | Failed Subnet.Create (parent network NF) → Get(<reserved-id>) → 404, List не включает. Async rollback verified. |

### Address release / idempotency

| Pattern | Classes | P | Apps | Что проверяет |
|---|---|---|---|---|
| `ADR-DEL-EXT-V4-RELEASE-REUSE` | STATE,CONF | P1 | 1 (add) | Allocate v4 → Delete → Allocate next: получает валидный IP (free-list возвращает row, sweep-allocator). |
| `ADR-DEL-IDM-DOUBLE` | IDM,NEG | P2 | 1 (add) | Delete address twice → first 200, second 404 (idempotent-safe, no 500 leak). |

### RouteTable delete-with-association

| Pattern | Classes | P | Apps | Что проверяет |
|---|---|---|---|---|
| `RT-DEL-WITH-ASSOC-OK` | CRUD,STATE | P1 | 1 (rou,sub) | Delete RouteTable с auto-assoc'нутой Subnet → 200, `subnet.route_table_id` обнулен через FK ON DELETE SET NULL. |

### Operation failure shape

| Pattern | Classes | P | Apps | Что проверяет |
|---|---|---|---|---|
| `OP-GET-ASYNC-FAILURE-RESPONSE` | STATE,CONF | P1 | 1 (ope) | Failed Operation: `done=true`, `error.code/message` populated, `response=null`, `metadata` preserved для tracing. Verifies REQ-OPS-01 / REQ-RES-02. |

### Pool exhaustion

| Pattern | Classes | P | Apps | Что проверяет |
|---|---|---|---|---|
| `IPL-ALLOC-POOL-EXHAUSTED` | NEG,STATE,CONF | P0 | 1 (add,internal-pool) | Pool /30 (2 usable) bound to fresh Network → 2 internal Address.Create OK → 3rd FailedPrecondition (pool exhausted via cascade resolve). Verifies REQ-IPAM-* и REQ-IPL-CR-01. |

> **Note**: исторические gaps каталога (project-rename, `AUTHZ-NETWORK-*` matrix) —
> отдельный tech-debt: правки только в `tests/`/`docs/`, без прод-кода. Re-sync индекса —
> follow-up в issue-трекере репозитория.

### Observability

*В scope — только `OBS-REQID-HEADER-ECHO`; `OBS-METRICS-*` (8 cases) ожидают экспонирования метрик vpc-сервиса на :9090. См. `cases/observability.py` docstring.*

| Pattern | Classes | P | Apps | Что проверяет |
|---|---|---|---|---|
| `OBS-REQID-HEADER-ECHO` | OBS | P2 | 1 (obs) | Client отправляет `X-Request-Id: <uuid>` → response header echoes тот же id (observability trace propagation через api-gateway). |

---

### pattern-registry: project-rename + AUTHZ matrix

*Doc-only sync: эти patterns добавлены для прохождения validate-cases.py после переименования project-полей и matrix `AUTHZ-NETWORK-*` (× 6 principals × 8 resources × 6 actions = 288 cases).*

**project-rename (project_id):**

| Pattern | Classes | P | Apps | Что проверяет |
|---|---|---|---|---|
| `*-CR-VAL-REQ-PROJECTID` | VAL | P0 | 7 (add,gat,net,nic,rou,sec,sub) | Create без required `projectId` → 400 InvalidArgument (переименование `projectId` → `projectId`; верифицирует REQ-VAL-01). |
| `*-UPD-STATE-IMMUTABLE-PROJECT-ID` | CONF,STATE,VAL | P1 | 7 (add,gat,net,nic,rou,sec,sub) | Update mask=`project_id` (immutable) → 400 InvalidArgument (точный текст). Зеркало `*-UPD-STATE-IMMUTABLE-PROJECT-ID` после rename. |

**AUTHZ matrix (288 literal ids):**

*Patterns в `cases/authz-deny.py` — matrix `AUTHZ-<RESOURCE>-<RPC>-<SCOPE>-<PRINCIPAL>`:
RESOURCE ∈ {NETWORK, SUBNET, ADDRESS, NIC, GATEWAY, ROUTE, SECURITY, APL, DATA, CD}, 
RPC ∈ {CR, LS, GT, UP, DL, MV}, SCOPE ∈ {OWN, CROSS}, PRINCIPAL ∈ {ANON, NOB, PA1, AAA, AAB, INV}.*

<details>
<summary>Полный реестр (336 literal case-ids — validate-cases substring-match)</summary>

```
ADR-CR-VAL-REQ-PROJECTID
ADR-UPD-STATE-IMMUTABLE-PROJECT-ID
AUTHZ-ADDRESS-CR-CROSS-AAA
AUTHZ-ADDRESS-CR-CROSS-AAB
AUTHZ-ADDRESS-CR-CROSS-ANON
AUTHZ-ADDRESS-CR-CROSS-INV
AUTHZ-ADDRESS-CR-CROSS-NOB
AUTHZ-ADDRESS-CR-CROSS-PA1
AUTHZ-ADDRESS-CR-OWN-AAA
AUTHZ-ADDRESS-CR-OWN-AAB
AUTHZ-ADDRESS-CR-OWN-ANON
AUTHZ-ADDRESS-CR-OWN-INV
AUTHZ-ADDRESS-CR-OWN-NOB
AUTHZ-ADDRESS-CR-OWN-PA1
AUTHZ-ADDRESS-DL-OWN-AAA
AUTHZ-ADDRESS-DL-OWN-AAB
AUTHZ-ADDRESS-DL-OWN-ANON
AUTHZ-ADDRESS-DL-OWN-INV
AUTHZ-ADDRESS-DL-OWN-NOB
AUTHZ-ADDRESS-DL-OWN-PA1
AUTHZ-ADDRESS-GT-OWN-AAA
AUTHZ-ADDRESS-GT-OWN-AAB
AUTHZ-ADDRESS-GT-OWN-ANON
AUTHZ-ADDRESS-GT-OWN-INV
AUTHZ-ADDRESS-GT-OWN-NOB
AUTHZ-ADDRESS-GT-OWN-PA1
AUTHZ-ADDRESS-LS-CROSS-AAA
AUTHZ-ADDRESS-LS-CROSS-AAB
AUTHZ-ADDRESS-LS-CROSS-ANON
AUTHZ-ADDRESS-LS-CROSS-INV
AUTHZ-ADDRESS-LS-CROSS-NOB
AUTHZ-ADDRESS-LS-CROSS-PA1
AUTHZ-ADDRESS-LS-OWN-AAA
AUTHZ-ADDRESS-LS-OWN-AAB
AUTHZ-ADDRESS-LS-OWN-ANON
AUTHZ-ADDRESS-LS-OWN-INV
AUTHZ-ADDRESS-LS-OWN-NOB
AUTHZ-ADDRESS-LS-OWN-PA1
AUTHZ-ADDRESS-UP-OWN-AAA
AUTHZ-ADDRESS-UP-OWN-AAB
AUTHZ-ADDRESS-UP-OWN-ANON
AUTHZ-ADDRESS-UP-OWN-INV
AUTHZ-ADDRESS-UP-OWN-NOB
AUTHZ-ADDRESS-UP-OWN-PA1
AUTHZ-APL-CR-AAA
AUTHZ-APL-CR-AAB
AUTHZ-APL-CR-ANON
AUTHZ-APL-CR-INV
AUTHZ-APL-CR-NOB
AUTHZ-APL-CR-PA1
AUTHZ-APL-DL-AAA
AUTHZ-APL-DL-AAB
AUTHZ-APL-DL-ANON
AUTHZ-APL-DL-INV
AUTHZ-APL-DL-NOB
AUTHZ-APL-DL-PA1
AUTHZ-APL-UP-AAA
AUTHZ-APL-UP-AAB
AUTHZ-APL-UP-ANON
AUTHZ-APL-UP-INV
AUTHZ-APL-UP-NOB
AUTHZ-APL-UP-PA1
AUTHZ-CD-SUBNET-XACCT-AAA
AUTHZ-CD-SUBNET-XACCT-AAB
AUTHZ-CD-SUBNET-XACCT-ANON
AUTHZ-CD-SUBNET-XACCT-INV
AUTHZ-CD-SUBNET-XACCT-NOB
AUTHZ-CD-SUBNET-XACCT-PA1
AUTHZ-DATA-LEAK-APL-LS-AAA
AUTHZ-DATA-LEAK-APL-LS-AAB
AUTHZ-DATA-LEAK-APL-LS-ANON
AUTHZ-DATA-LEAK-APL-LS-INV
AUTHZ-DATA-LEAK-APL-LS-NOB
AUTHZ-DATA-LEAK-APL-LS-PA1
AUTHZ-GATEWAY-CR-CROSS-AAA
AUTHZ-GATEWAY-CR-CROSS-AAB
AUTHZ-GATEWAY-CR-CROSS-ANON
AUTHZ-GATEWAY-CR-CROSS-INV
AUTHZ-GATEWAY-CR-CROSS-NOB
AUTHZ-GATEWAY-CR-CROSS-PA1
AUTHZ-GATEWAY-CR-OWN-AAA
AUTHZ-GATEWAY-CR-OWN-AAB
AUTHZ-GATEWAY-CR-OWN-ANON
AUTHZ-GATEWAY-CR-OWN-INV
AUTHZ-GATEWAY-CR-OWN-NOB
AUTHZ-GATEWAY-CR-OWN-PA1
AUTHZ-GATEWAY-DL-OWN-AAA
AUTHZ-GATEWAY-DL-OWN-AAB
AUTHZ-GATEWAY-DL-OWN-ANON
AUTHZ-GATEWAY-DL-OWN-INV
AUTHZ-GATEWAY-DL-OWN-NOB
AUTHZ-GATEWAY-DL-OWN-PA1
AUTHZ-GATEWAY-GT-OWN-AAA
AUTHZ-GATEWAY-GT-OWN-AAB
AUTHZ-GATEWAY-GT-OWN-ANON
AUTHZ-GATEWAY-GT-OWN-INV
AUTHZ-GATEWAY-GT-OWN-NOB
AUTHZ-GATEWAY-GT-OWN-PA1
AUTHZ-GATEWAY-LS-CROSS-AAA
AUTHZ-GATEWAY-LS-CROSS-AAB
AUTHZ-GATEWAY-LS-CROSS-ANON
AUTHZ-GATEWAY-LS-CROSS-INV
AUTHZ-GATEWAY-LS-CROSS-NOB
AUTHZ-GATEWAY-LS-CROSS-PA1
AUTHZ-GATEWAY-LS-OWN-AAA
AUTHZ-GATEWAY-LS-OWN-AAB
AUTHZ-GATEWAY-LS-OWN-ANON
AUTHZ-GATEWAY-LS-OWN-INV
AUTHZ-GATEWAY-LS-OWN-NOB
AUTHZ-GATEWAY-LS-OWN-PA1
AUTHZ-GATEWAY-UP-OWN-AAA
AUTHZ-GATEWAY-UP-OWN-AAB
AUTHZ-GATEWAY-UP-OWN-ANON
AUTHZ-GATEWAY-UP-OWN-INV
AUTHZ-GATEWAY-UP-OWN-NOB
AUTHZ-GATEWAY-UP-OWN-PA1
AUTHZ-NETWORK-CR-CROSS-AAA
AUTHZ-NETWORK-CR-CROSS-AAB
AUTHZ-NETWORK-CR-CROSS-ANON
AUTHZ-NETWORK-CR-CROSS-INV
AUTHZ-NETWORK-CR-CROSS-NOB
AUTHZ-NETWORK-CR-CROSS-PA1
AUTHZ-NETWORK-CR-OWN-AAA
AUTHZ-NETWORK-CR-OWN-AAB
AUTHZ-NETWORK-CR-OWN-ANON
AUTHZ-NETWORK-CR-OWN-INV
AUTHZ-NETWORK-CR-OWN-NOB
AUTHZ-NETWORK-CR-OWN-PA1
AUTHZ-NETWORK-DL-OWN-AAA
AUTHZ-NETWORK-DL-OWN-AAB
AUTHZ-NETWORK-DL-OWN-ANON
AUTHZ-NETWORK-DL-OWN-INV
AUTHZ-NETWORK-DL-OWN-NOB
AUTHZ-NETWORK-DL-OWN-PA1
AUTHZ-NETWORK-GT-OWN-AAA
AUTHZ-NETWORK-GT-OWN-AAB
AUTHZ-NETWORK-GT-OWN-ANON
AUTHZ-NETWORK-GT-OWN-INV
AUTHZ-NETWORK-GT-OWN-NOB
AUTHZ-NETWORK-GT-OWN-PA1
AUTHZ-NETWORK-LS-CROSS-AAA
AUTHZ-NETWORK-LS-CROSS-AAB
AUTHZ-NETWORK-LS-CROSS-ANON
AUTHZ-NETWORK-LS-CROSS-INV
AUTHZ-NETWORK-LS-CROSS-NOB
AUTHZ-NETWORK-LS-CROSS-PA1
AUTHZ-NETWORK-LS-OWN-AAA
AUTHZ-NETWORK-LS-OWN-AAB
AUTHZ-NETWORK-LS-OWN-ANON
AUTHZ-NETWORK-LS-OWN-INV
AUTHZ-NETWORK-LS-OWN-NOB
AUTHZ-NETWORK-LS-OWN-PA1
AUTHZ-NETWORK-UP-OWN-AAA
AUTHZ-NETWORK-UP-OWN-AAB
AUTHZ-NETWORK-UP-OWN-ANON
AUTHZ-NETWORK-UP-OWN-INV
AUTHZ-NETWORK-UP-OWN-NOB
AUTHZ-NETWORK-UP-OWN-PA1
AUTHZ-NIC-CR-CROSS-AAA
AUTHZ-NIC-CR-CROSS-AAB
AUTHZ-NIC-CR-CROSS-ANON
AUTHZ-NIC-CR-CROSS-INV
AUTHZ-NIC-CR-CROSS-NOB
AUTHZ-NIC-CR-CROSS-PA1
AUTHZ-NIC-CR-OWN-AAA
AUTHZ-NIC-CR-OWN-AAB
AUTHZ-NIC-CR-OWN-ANON
AUTHZ-NIC-CR-OWN-INV
AUTHZ-NIC-CR-OWN-NOB
AUTHZ-NIC-CR-OWN-PA1
AUTHZ-NIC-DL-OWN-AAA
AUTHZ-NIC-DL-OWN-AAB
AUTHZ-NIC-DL-OWN-ANON
AUTHZ-NIC-DL-OWN-INV
AUTHZ-NIC-DL-OWN-NOB
AUTHZ-NIC-DL-OWN-PA1
AUTHZ-NIC-GT-OWN-AAA
AUTHZ-NIC-GT-OWN-AAB
AUTHZ-NIC-GT-OWN-ANON
AUTHZ-NIC-GT-OWN-INV
AUTHZ-NIC-GT-OWN-NOB
AUTHZ-NIC-GT-OWN-PA1
AUTHZ-NIC-LS-CROSS-AAA
AUTHZ-NIC-LS-CROSS-AAB
AUTHZ-NIC-LS-CROSS-ANON
AUTHZ-NIC-LS-CROSS-INV
AUTHZ-NIC-LS-CROSS-NOB
AUTHZ-NIC-LS-CROSS-PA1
AUTHZ-NIC-LS-OWN-AAA
AUTHZ-NIC-LS-OWN-AAB
AUTHZ-NIC-LS-OWN-ANON
AUTHZ-NIC-LS-OWN-INV
AUTHZ-NIC-LS-OWN-NOB
AUTHZ-NIC-LS-OWN-PA1
AUTHZ-NIC-UP-OWN-AAA
AUTHZ-NIC-UP-OWN-AAB
AUTHZ-NIC-UP-OWN-ANON
AUTHZ-NIC-UP-OWN-INV
AUTHZ-NIC-UP-OWN-NOB
AUTHZ-NIC-UP-OWN-PA1
AUTHZ-ROUTE-TABLE-CR-CROSS-AAA
AUTHZ-ROUTE-TABLE-CR-CROSS-AAB
AUTHZ-ROUTE-TABLE-CR-CROSS-ANON
AUTHZ-ROUTE-TABLE-CR-CROSS-INV
AUTHZ-ROUTE-TABLE-CR-CROSS-NOB
AUTHZ-ROUTE-TABLE-CR-CROSS-PA1
AUTHZ-ROUTE-TABLE-CR-OWN-AAA
AUTHZ-ROUTE-TABLE-CR-OWN-AAB
AUTHZ-ROUTE-TABLE-CR-OWN-ANON
AUTHZ-ROUTE-TABLE-CR-OWN-INV
AUTHZ-ROUTE-TABLE-CR-OWN-NOB
AUTHZ-ROUTE-TABLE-CR-OWN-PA1
AUTHZ-ROUTE-TABLE-DL-OWN-AAA
AUTHZ-ROUTE-TABLE-DL-OWN-AAB
AUTHZ-ROUTE-TABLE-DL-OWN-ANON
AUTHZ-ROUTE-TABLE-DL-OWN-INV
AUTHZ-ROUTE-TABLE-DL-OWN-NOB
AUTHZ-ROUTE-TABLE-DL-OWN-PA1
AUTHZ-ROUTE-TABLE-GT-OWN-AAA
AUTHZ-ROUTE-TABLE-GT-OWN-AAB
AUTHZ-ROUTE-TABLE-GT-OWN-ANON
AUTHZ-ROUTE-TABLE-GT-OWN-INV
AUTHZ-ROUTE-TABLE-GT-OWN-NOB
AUTHZ-ROUTE-TABLE-GT-OWN-PA1
AUTHZ-ROUTE-TABLE-LS-CROSS-AAA
AUTHZ-ROUTE-TABLE-LS-CROSS-AAB
AUTHZ-ROUTE-TABLE-LS-CROSS-ANON
AUTHZ-ROUTE-TABLE-LS-CROSS-INV
AUTHZ-ROUTE-TABLE-LS-CROSS-NOB
AUTHZ-ROUTE-TABLE-LS-CROSS-PA1
AUTHZ-ROUTE-TABLE-LS-OWN-AAA
AUTHZ-ROUTE-TABLE-LS-OWN-AAB
AUTHZ-ROUTE-TABLE-LS-OWN-ANON
AUTHZ-ROUTE-TABLE-LS-OWN-INV
AUTHZ-ROUTE-TABLE-LS-OWN-NOB
AUTHZ-ROUTE-TABLE-LS-OWN-PA1
AUTHZ-ROUTE-TABLE-UP-OWN-AAA
AUTHZ-ROUTE-TABLE-UP-OWN-AAB
AUTHZ-ROUTE-TABLE-UP-OWN-ANON
AUTHZ-ROUTE-TABLE-UP-OWN-INV
AUTHZ-ROUTE-TABLE-UP-OWN-NOB
AUTHZ-ROUTE-TABLE-UP-OWN-PA1
AUTHZ-SECURITY-GROUP-CR-CROSS-AAA
AUTHZ-SECURITY-GROUP-CR-CROSS-AAB
AUTHZ-SECURITY-GROUP-CR-CROSS-ANON
AUTHZ-SECURITY-GROUP-CR-CROSS-INV
AUTHZ-SECURITY-GROUP-CR-CROSS-NOB
AUTHZ-SECURITY-GROUP-CR-CROSS-PA1
AUTHZ-SECURITY-GROUP-CR-OWN-AAA
AUTHZ-SECURITY-GROUP-CR-OWN-AAB
AUTHZ-SECURITY-GROUP-CR-OWN-ANON
AUTHZ-SECURITY-GROUP-CR-OWN-INV
AUTHZ-SECURITY-GROUP-CR-OWN-NOB
AUTHZ-SECURITY-GROUP-CR-OWN-PA1
AUTHZ-SECURITY-GROUP-DL-OWN-AAA
AUTHZ-SECURITY-GROUP-DL-OWN-AAB
AUTHZ-SECURITY-GROUP-DL-OWN-ANON
AUTHZ-SECURITY-GROUP-DL-OWN-INV
AUTHZ-SECURITY-GROUP-DL-OWN-NOB
AUTHZ-SECURITY-GROUP-DL-OWN-PA1
AUTHZ-SECURITY-GROUP-GT-OWN-AAA
AUTHZ-SECURITY-GROUP-GT-OWN-AAB
AUTHZ-SECURITY-GROUP-GT-OWN-ANON
AUTHZ-SECURITY-GROUP-GT-OWN-INV
AUTHZ-SECURITY-GROUP-GT-OWN-NOB
AUTHZ-SECURITY-GROUP-GT-OWN-PA1
AUTHZ-SECURITY-GROUP-LS-CROSS-AAA
AUTHZ-SECURITY-GROUP-LS-CROSS-AAB
AUTHZ-SECURITY-GROUP-LS-CROSS-ANON
AUTHZ-SECURITY-GROUP-LS-CROSS-INV
AUTHZ-SECURITY-GROUP-LS-CROSS-NOB
AUTHZ-SECURITY-GROUP-LS-CROSS-PA1
AUTHZ-SECURITY-GROUP-LS-OWN-AAA
AUTHZ-SECURITY-GROUP-LS-OWN-AAB
AUTHZ-SECURITY-GROUP-LS-OWN-ANON
AUTHZ-SECURITY-GROUP-LS-OWN-INV
AUTHZ-SECURITY-GROUP-LS-OWN-NOB
AUTHZ-SECURITY-GROUP-LS-OWN-PA1
AUTHZ-SECURITY-GROUP-UP-OWN-AAA
AUTHZ-SECURITY-GROUP-UP-OWN-AAB
AUTHZ-SECURITY-GROUP-UP-OWN-ANON
AUTHZ-SECURITY-GROUP-UP-OWN-INV
AUTHZ-SECURITY-GROUP-UP-OWN-NOB
AUTHZ-SECURITY-GROUP-UP-OWN-PA1
AUTHZ-SUBNET-CR-CROSS-AAA
AUTHZ-SUBNET-CR-CROSS-AAB
AUTHZ-SUBNET-CR-CROSS-ANON
AUTHZ-SUBNET-CR-CROSS-INV
AUTHZ-SUBNET-CR-CROSS-NOB
AUTHZ-SUBNET-CR-CROSS-PA1
AUTHZ-SUBNET-CR-OWN-AAA
AUTHZ-SUBNET-CR-OWN-AAB
AUTHZ-SUBNET-CR-OWN-ANON
AUTHZ-SUBNET-CR-OWN-INV
AUTHZ-SUBNET-CR-OWN-NOB
AUTHZ-SUBNET-CR-OWN-PA1
AUTHZ-SUBNET-DL-OWN-AAA
AUTHZ-SUBNET-DL-OWN-AAB
AUTHZ-SUBNET-DL-OWN-ANON
AUTHZ-SUBNET-DL-OWN-INV
AUTHZ-SUBNET-DL-OWN-NOB
AUTHZ-SUBNET-DL-OWN-PA1
AUTHZ-SUBNET-GT-OWN-AAA
AUTHZ-SUBNET-GT-OWN-AAB
AUTHZ-SUBNET-GT-OWN-ANON
AUTHZ-SUBNET-GT-OWN-INV
AUTHZ-SUBNET-GT-OWN-NOB
AUTHZ-SUBNET-GT-OWN-PA1
AUTHZ-SUBNET-LS-CROSS-AAA
AUTHZ-SUBNET-LS-CROSS-AAB
AUTHZ-SUBNET-LS-CROSS-ANON
AUTHZ-SUBNET-LS-CROSS-INV
AUTHZ-SUBNET-LS-CROSS-NOB
AUTHZ-SUBNET-LS-CROSS-PA1
AUTHZ-SUBNET-LS-OWN-AAA
AUTHZ-SUBNET-LS-OWN-AAB
AUTHZ-SUBNET-LS-OWN-ANON
AUTHZ-SUBNET-LS-OWN-INV
AUTHZ-SUBNET-LS-OWN-NOB
AUTHZ-SUBNET-LS-OWN-PA1
AUTHZ-SUBNET-UP-OWN-AAA
AUTHZ-SUBNET-UP-OWN-AAB
AUTHZ-SUBNET-UP-OWN-ANON
AUTHZ-SUBNET-UP-OWN-INV
AUTHZ-SUBNET-UP-OWN-NOB
AUTHZ-SUBNET-UP-OWN-PA1
GW-CR-VAL-REQ-PROJECTID
GW-UPD-STATE-IMMUTABLE-PROJECT-ID
NET-CR-VAL-REQ-PROJECTID
NET-UPD-STATE-IMMUTABLE-PROJECT-ID
RT-CR-VAL-REQ-PROJECTID
RT-UPD-STATE-IMMUTABLE-PROJECT-ID
SG-CR-VAL-REQ-PROJECTID
SG-UPD-STATE-IMMUTABLE-PROJECT-ID
SUB-CR-VAL-REQ-PROJECTID
SUB-UPD-STATE-IMMUTABLE-PROJECT-ID
```

</details>

> Эти ids — **инстансы matrix pattern**, не уникальные patterns в смысле coverage. Source of truth для генерации — `cases/authz-deny.py`. validate-cases.py использует substring-match на `CASES-INDEX.md` content.
