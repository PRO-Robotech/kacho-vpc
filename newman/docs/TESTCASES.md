# VPC Test-cases — раскрытая структура

Источник: `collections/kacho-vpc.postman_collection.json` после `scripts/rebuild-collection.py`.
Преамбула / тяг `Operation` / retry-on-429 / cleanup-poll / external→internal Address — обвязка
из rebuild-collection.py, не считается отдельными «тестовыми» шагами.

## Класс проверки (`Class`)

Новый идентификатор проверки. Один и тот же `Class` может встречаться у разных ресурсов —
он показывает, какой инвариант теста повторяется поперёк доменов.

| Class | Что проверяется |
|---|---|
| `CRUD-CR-OK` | happy-path Create + envelope `Operation` (id, createdAt, metadata.@type, response.id) |
| `CRUD-CRUD-SMOKE` | full smoke: Create → Get → List → Update → Delete |
| `CRUD-GET-OK` | happy-path Get returns resource |
| `CRUD-LIST-SHAPE` | List response shape (массив + опциональный nextPageToken) |
| `CRUD-PATCH-RENAME` | Update name через PATCH+updateMask |
| `CRUD-PATCH-LABELS` | Update labels через PATCH |
| `CRUD-PATCH-DESC` | Update description через PATCH |
| `BVA-NAME-MAX` | name на верхней границе (63) → 200 |
| `BVA-NAME-OVER` | name выше границы (64) → 400 |
| `BVA-LABELS-MAX` | 64 labels → 200 |
| `BVA-LABELS-OVER` | 65 labels → 400 |
| `BVA-DESC-MAX` | description 256 → 200 |
| `BVA-DESC-OVER-CR` | description 257 на Create → 400 |
| `BVA-DESC-OVER-UP` | description >256 на Update → 400 |
| `BVA-PS-NEG` | pageSize=-1 → 400 |
| `BVA-PS-OVER` | pageSize=1001 → 400 |
| `BVA-CIDR-MIN` | smallest valid CIDR (`/30`) → 200 |
| `BVA-CIDR-OVER` | за границей (`/32`) — 200 (Kachō decision) |
| `VAL-EMPTY-FOLDER` | POST без `folderId` → sync 400 |
| `VAL-EMPTY-NAME` | name="" → 200 (permissive) или специальная семантика |
| `VAL-EMPTY-NETWORK` | POST без `networkId` → sync 400 |
| `VAL-EMPTY-RULES` | POST без rules → empty rules в результате |
| `VAL-NAME-CASE-PERMISSIVE` | CAPS в name accepted (VPC-permissive regex) |
| `VAL-INVALID-ID-FORMAT` | GET с garbage id → 400 |
| `VAL-INVALID-FOLDER` | POST с garbage/non-existent folderId → Op.error (Kachō async) |
| `VAL-INVALID-NETWORK` | POST с non-existent networkId → Op.error |
| `VAL-INVALID-CIDR` | rule/CIDR с невалидным выражением |
| `VAL-INVALID-DIRECTION` | SG rule с невалидным direction (Kachō decision: no sync) |
| `VAL-INVALID-ZONE` | bad `zoneId` (Kachō decision: no sync) |
| `VAL-PORT-INVERTED` | `fromPort > toPort` (Kachō decision) |
| `VAL-DUP-NAME` | duplicate name (Kachō decision: allowed) |
| `VAL-DUP-DEST` | duplicate destinationPrefix в RT |
| `VAL-MASK-UNKNOWN` | PATCH с unknown updateMask field → 400 |
| `NF-GET-404` | Get non-existent → 404 |
| `LIST-FILTER-FOLDER` | List с `?folderId=` фильтрует |
| `LIST-FILTER-EXPR-INVALID` | List с garbage `?filter=` → 400 |
| `LIST-FILTER-EXPR-APPLIED` | filter expression применяется (по name) |
| `LIST-LABEL-SELECTOR-SILENT` | `?labelSelector=` молча игнорируется |
| `LIST-NO-FOLDER-ALL` | List без `?folderId=` → returns all (Kachō decision) |
| `LIST-OPS` | `/<kind>/{id}/operations` возвращает Create-op |
| `LIST-PS-ONE` | pageSize=1 → 1 элемент + nextPageToken |
| `LIST-PT-FORMAT` | nextPageToken — non-empty string |
| `LIST-PT-LEAK` | nextPageToken не утекает internals |
| `LIST-PT-INVALID` | garbage pageToken → 400 |
| `NESTED-LIST-SUBNETS-EMPTY` | `/networks/{id}/subnets` пуст для свежей сети |
| `NESTED-LIST-RT-EMPTY` | `/networks/{id}/route_tables` пуст |
| `PATCH-CLEAR-DESC` | PATCH description="" очищает поле |
| `PATCH-CLEAR-NAME-PERMISSIVE` | PATCH name="" → 200 (YC permissive) |
| `LIFECYCLE-MOVE` | `:move` возвращает Operation |
| `LIFECYCLE-RELOCATE-OK` | Subnet `:relocate` → Op success |
| `LIFECYCLE-RELOCATE-INVALID` | Subnet `:relocate` в bad zone → 400 / Op.error |
| `OPS-LOCAL-404` | `/operations` не доступны через per-domain gateway |
| `LOCALIZED-ERR-MISSING` | `Accept-Language` игнорируется, нет LocalizedMessage |
| `CIDR-ADD-VALID` | Subnet `:add-cidr-blocks` non-overlapping → 200 |
| `CIDR-ADD-OVERLAP` | overlapping CIDR → Op.error |
| `CIDR-ADD-INVALID` | garbage CIDR → 400 |
| `CIDR-ADD-EMPTY` | пустой массив → 400 |
| `CIDR-REMOVE-VALID` | `:remove-cidr-blocks` существующего CIDR → 200 |
| `CIDR-REMOVE-ABSENT` | удалить отсутствующий CIDR (Kachō idempotent) |
| `DHCP-ROUNDTRIP` | dhcpOptions stored & echoed |
| `DHCP-PATCH-CLEAR` | PATCH dhcpOptions={} clears |
| `DHCP-PATCH-REPLACE` | partial PATCH заменяет все поля |
| `IPV6-ALWAYS-EMPTY` | v6CidrBlocks always [] (Kachō decision) |
| `RT-EMPTY-ROUTES` | RT с staticRoutes=[] → 200 |
| `RT-MULTI-ROUTE` | RT с несколькими routes (включая default) — все preserved |
| `RT-PATCH-CLEAR-ROUTES` | PATCH staticRoutes=[] очищает |
| `RT-PATCH-ADD-ROUTE` | PATCH добавляет static route |
| `SG-RULES-ADD` | SG `:rules` add → rule появляется |
| `SG-RULES-DELETE` | SG `:rules` delete by id |
| `SG-RULE-UPDATE-OK` | SG UpdateRule возвращает родительский SG (regression) |
| `SG-RULE-UPDATE-NF` | UpdateRule с non-existent ruleId → Op.error |
| `SG-RULE-PROTO-NUMBER` | rule.protocolNumber=17 (UDP) accepted |
| `SG-RULE-WIDE-CIDR` | rule с `0.0.0.0/0` accepted |
| `SG-DEFAULT-NO-DELETE` | DELETE default SG → Op.error |
| `SG-ENDPOINTS-REGISTERED` | smoke: gRPC routes присутствуют |
| `SUBNET-DEL-WITH-ADDR-` | (pending) Subnet delete with attached Address |

---

## Соглашения по саб-шагам

```
setup-net          POST   /networks
setup-net-poll     GET    /operations/{setupNetOpId}
setup-<r>          POST   /<kind>
setup-poll         GET    /operations/{setupOpId}
<action>           метод  /<kind>...                    ← предмет теста
poll               GET    /operations/{opId}
verify             GET    /<kind>/{id}                  ← read-back
cleanup-<r>        DELETE /<kind>/{id}
cleanup-<r>.poll   GET    /operations/{_cleanupOpId}    ← освобождает quota
```

Любой positive case с мутацией следует этому шаблону. Negative-sync — обычно один шаг
(`<action>` без setup/cleanup). Negative-async (Kachō возвращает 200 + `Operation.error`) —
setup → action → poll, ассерт `op.done && op.error.code !== 0`.

`*.poll` после cleanup-DELETE инжектится `add_cleanup_poll_steps` и обязателен в `--env yc`.

---

## 00-preflight (5 шагов)

| Шаг | Метод/URL | Назначение |
|---|---|---|
| `pf.setup-org` | POST `/organization-manager/v1/organizations` | `_suiteOrgId`. Skip если `existingFolderId`. |
| `pf.setup-cloud` | POST `/resource-manager/v1/clouds` | `_suiteCloudId`. |
| `pf.setup-folder` | POST `/resource-manager/v1/folders` | `_suiteFolderId`, assert match `^[a-z0-9]{16,24}$`. |
| `pf.setup-net` | POST `/vpc/v1/networks` | shared `_suiteNetworkId`. |
| `pf.setup-subnet` | POST `/vpc/v1/subnets` (`10.42.0.0/24`, `ru-central1-a`) | shared `_suiteSubnetId`. |

## 99-teardown (5 шагов)

DELETE в обратном порядке: subnet → net → folder → cloud → org. Каждый ассертит `pm.response.code in [200,400,404]`.

---

# NETWORK (`NET-*`) — 27 active

| ID | Class | Описание | Саб-шаги | Ключевые ассерты |
|---|---|---|---|---|
| `NET-CR-OK` | `CRUD-CR-OK` | Create returns Operation envelope | POST `/networks` → `netcrok.poll-op` GET op → cleanup+poll | id/createdAt/createdBy present; `metadata.@type` matches `CreateNetworkMetadata$`; after poll: `done:true`, `op.response.id == networkId` |
| `NET-CR-NAME-MAX` | `BVA-NAME-MAX` | name=63 (boundary) | `ncrmax.create` → poll → cleanup+poll | 200 + Op success |
| `NET-CR-NAME-OVER` | `BVA-NAME-OVER` | name=64 → 400 | `ncrover.create` (single step) | 400, `body.code===3` |
| `NET-CR-NAME-ACCEPTS` | `VAL-NAME-CASE-PERMISSIVE` | CAPS in name | POST `BadCAPS{runId}` → `netcrnameok.poll-op` → cleanup+poll | `op.response.name === 'BadCAPS' + runId` (case preserved) |
| `NET-CR-LABELS-MAX` | `BVA-LABELS-MAX` | 64 labels | `ncrlbl.create` (k0..k63) → poll → cleanup+poll | 200 |
| `NET-CR-LABELS-OVER` | `BVA-LABELS-OVER` | 65 labels → 400 | `ncrlblov.create` (single step) | 400 |
| `NET-CR-DESC-MAX` | `BVA-DESC-MAX` | description=256 | `ncrdesc.create` → poll → cleanup+poll | 200 |
| `NET-CR-DESC-OVER` | `BVA-DESC-OVER-CR` | description=257 → 400 | `ncrdescov.create` | 400 |
| `NET-CR-EMPTY-FOLDER` | `VAL-EMPTY-FOLDER` | sync 400 без folderId | `ncrnof.create` body `{"name":"x"}` | 400 |
| `NET-CR-EMPTY-NAME` | `VAL-EMPTY-NAME` | name="" permissive | POST `name:""` → cleanup+poll | 200 |
| `NET-GET-NOTFOUND` | `NF-GET-404` | Get non-existent | `ngetnf.get /networks/enp00000000nonexist0` | 404 |
| `NET-GET-INVALID-FORMAT` | `VAL-INVALID-ID-FORMAT` | bad id format | `ngetbad.get /networks/not-a-valid-id` | 400 |
| `NET-LIST` | `CRUD-LIST-SHAPE` | response shape | GET `/networks?folderId=&pageSize=3` | networks[] present |
| `NET-LIST-FILTER` | `LIST-FILTER-FOLDER` | filter applied | 2× setup-net → list `?folderId=_suite` → 2× cleanup+poll | оба id present, `folderId == _suiteFolderId` |
| `NET-LIST-PS-ONE` | `LIST-PS-ONE` | pageSize=1 | 2× setup-net → `nlsps1.list pageSize=1` → 2× cleanup+poll | length===1, nextPageToken present |
| `NET-LIST-PAGE-TOKEN-FORMAT` | `LIST-PT-FORMAT` | nextPageToken format | 2× setup → `ptfmt.list-pageSize1` → 2× cleanup | nextPageToken non-empty string |
| `NET-LIST-PAGE-TOKEN-LEAK` | `LIST-PT-LEAK` | no internals | GET `pageSize=1` | nextPageToken не содержит read-internal patterns |
| `NET-LIST-PT-INVALID` | `LIST-PT-INVALID` | garbage token | `nlsptbad.list ?pageToken=garbage-token` | 400 |
| `NET-LIST-OPS` | `LIST-OPS` | ListOperations | setup+poll → `nlistop.list-ops GET /networks/{id}/operations` → cleanup+poll | содержит Create-op |
| `NET-LIST-SUBNETS-EMPTY` | `NESTED-LIST-SUBNETS-EMPTY` | nested empty | setup+poll → `nlsempt.list GET /networks/{id}/subnets` → cleanup+poll | `[]` |
| `NET-LIST-RT-EMPTY` | `NESTED-LIST-RT-EMPTY` | nested empty | setup+poll → `nlrtempt.list GET /networks/{id}/route_tables` → cleanup+poll | `[]` |
| `NET-UP-NAME` | `CRUD-PATCH-RENAME` | rename | setup → `nupn.1 PATCH name+updateMask=name` → `nupn.2 GET op` → cleanup+poll | `op.response.name` updated |
| `NET-PATCH-CLEAR-DESC` | `PATCH-CLEAR-DESC` | description="" clears | POST(desc) → `npcd.2 PATCH desc=""` → `npcd.3 GET` → cleanup+poll | description==="" |
| `NET-PATCH-NAME-EMPTY-OK` | `PATCH-CLEAR-NAME-PERMISSIVE` | name="" | setup → `npcn.1 PATCH name:""` → cleanup+poll | 200 (YC permissive) |
| `NET-DESC-UP-OVER` | `BVA-DESC-OVER-UP` | desc=300 → 400 | setup → `ndup300.1 PATCH desc 300×'a'` → cleanup+poll | 400 + `code:3` |
| `NET-UPDATE-MASK-UNKNOWN` | `VAL-MASK-UNKNOWN` | bogus mask | setup → `umun.1 PATCH updateMask:"bogus"` → cleanup+poll | 400 + `code:3` |

---

# SUBNET (`SU-*`, `SUBNET-*`) — 19 active

| ID | Class | Описание | Саб-шаги | Ключевые ассерты |
|---|---|---|---|---|
| `SU-CR-VALID` | `CRUD-CR-OK` | happy-path | setup-net → `suv.1 POST /subnets` (10.20.0.0/24) → `suv.2 GET op` → cleanup-su+poll → cleanup-net+poll | Op success, response shape |
| `SU-ZONE-OK` | `CRUD-CR-OK` | valid zone | setup-net → `suzok.1 POST` (`ru-central1-b`) → `suzok.2 GET subnet` | zoneId echoed |
| `SU-CR-INVALID-FOLDER` | `VAL-INVALID-FOLDER` | garbage folderId | `sucrnf.create` → `sucrnf.poll` | Op.error (Kachō async) |
| `SU-CR-MISSING-CIDR` | `VAL-INVALID-CIDR` | без `v4CidrBlocks` | setup-net+poll → `sucrnocid.create` → poll → cleanup×2+poll | Op.error (Kachō decision: no sync) |
| `SU-CR-PREFIX-30` | `BVA-CIDR-MIN` | `/30` | setup-net+poll → `sucrm30.create` → poll → cleanup×2+poll | 200 |
| `SU-CR-PREFIX-32` | `BVA-CIDR-OVER` | `/32` (Kachō accepts) | setup-net+poll → `sucrm32.create` → cleanup×2+poll | 200 (vs YC reject) |
| `SU-DHCP-VALID` | `DHCP-ROUNDTRIP` | dhcpOptions stored | setup-net → `sdv.1 POST` (dhcp) → `sdv.2 GET op` → `sdv.3 GET subnet` → cleanup×2+poll | dhcpOptions echoed |
| `SU-DHCP-PATCH-CLEAR` | `DHCP-PATCH-CLEAR` | clear via {} | setup-net + POST(dhcp) → `sdc.2 PATCH dhcpOptions:{}` → `sdc.3 GET subnet` → cleanup×2+poll | поля очищены |
| `SU-DHCP-PATCH-UPDATE` | `DHCP-PATCH-REPLACE` | partial replaces all | setup-net + POST(full dhcp) → `sdu.2 PATCH partial` → `sdu.3 GET` → cleanup×2+poll | ntpServers gone |
| `SU-V6CIDR-EMPTY` | `IPV6-ALWAYS-EMPTY` | v6=[] | setup-net + POST → `sve.2 GET subnet` → cleanup×2+poll | `v6CidrBlocks: []` |
| `SU-PATCH-CLEAR-DESC` | `PATCH-CLEAR-DESC` | desc="" | setup-net + POST(desc) → `spcd.2 PATCH desc=""` → `spcd.3 GET` → cleanup×2+poll | очищено |
| `SU-ADD-CIDR-VALID` | `CIDR-ADD-VALID` | non-overlapping | setup-net+setup-su → `suadcv.add-cidr POST :add-cidr-blocks` (10.0.1.0/24) → poll → `suadcv.verify GET` → cleanup×2+poll | оба CIDR present |
| `SU-ADD-CIDR-OVERLAP` | `CIDR-ADD-OVERLAP` | overlap | setup → `suadcov.add-cidr` (10.0.0.128/25) → poll → cleanup×2+poll | Op.error |
| `SU-ADD-CIDR-INVALID` | `CIDR-ADD-INVALID` | garbage | setup → `suadcbad.add-cidr` (`999.999.999.0/24`) → cleanup×2+poll | 400 |
| `SU-ADD-CIDR-EMPTY` | `CIDR-ADD-EMPTY` | пустой массив | setup → `suadcem.add-cidr` (`[]`) → cleanup×2+poll | 400 |
| `SU-REMOVE-CIDR-VALID` | `CIDR-REMOVE-VALID` | удалить existing | setup-net+setup-su(multi) → `suremv.remove POST :remove-cidr-blocks` → poll → verify → cleanup×2+poll | CIDR удалён |
| `SU-REMOVE-CIDR-NOT-PRESENT` | `CIDR-REMOVE-ABSENT` | absent CIDR | setup → `suremnp.remove` → poll → cleanup×2+poll | Kachō idempotent (Op success) |
| `SU-LIST-USED-EMPTY` | `LIST-NO-FOLDER-ALL` (variant) | ListUsedAddresses | setup-net+setup-su → `sulueusm.list-used GET /subnets/{id}/addresses` → cleanup×2+poll | `[]` |
| `SU-LIST-OPS` | `LIST-OPS` | ListOperations | setup-net+setup-su → `suops.list-ops GET /subnets/{id}/operations` → cleanup×2+poll | содержит Create-op |
| `SU-RELOCATE-VALID` | `LIFECYCLE-RELOCATE-OK` | :relocate | setup → `surel.relocate POST :relocate` → cleanup×2+poll | Op returned |
| `SU-RELOCATE-INVALID-ZONE` | `LIFECYCLE-RELOCATE-INVALID` | bad zone | setup → `surelnz.relocate` → cleanup×2+poll | Op.error |

---

# ADDRESS (`A-*`) — 8 active

> Все: `externalIpv4AddressSpec` patched на `internalIpv4AddressSpec:{subnetId:_suiteSubnetId}` (`switch_address_to_internal`).

| ID | Class | Описание | Саб-шаги | Ключевые ассерты |
|---|---|---|---|---|
| `A-CR-EXTERNAL` | `CRUD-CR-OK` | happy-path | `aex.1 POST /addresses` → `aex.2 GET op` → cleanup-addr+poll | Op success |
| `A-CR-EMPTY-FOLDER` | `VAL-EMPTY-FOLDER` | sync 400 | `acrnof.create` без folderId | 400 |
| `A-GET-NOTFOUND` | `NF-GET-404` | Get non-existent | `agetnf.get /addresses/enp00000000nonexist0` | 404 |
| `A-LIST-OPS` | `LIST-OPS` | ListOperations | `aops.create` → `aops.list-ops GET /addresses/{id}/operations` → cleanup-addr+poll | содержит Create-op |
| `A-CR-NAME-DUP` | `VAL-DUP-NAME` | duplicate names | `acrdup.create-1` POST → `acrdup.create-2` POST same name → 2× cleanup+poll | оба 200 (Kachō decision) |
| `A-CR-LABELS-MAX` | `BVA-LABELS-MAX` | 64 labels | `acrlbl.create` → cleanup-addr+poll | 200 |
| `A-CR-INVALID-ZONE` | `VAL-INVALID-ZONE` | bad zoneId | `acrinz.create` → cleanup-addr+poll | Kachō: 200 (no sync validation) |
| `A-LIST-NO-FOLDER` | `LIST-NO-FOLDER-ALL` | без folderId | `alsempf.list GET /addresses` | returns all (Kachō decision) |

---

# ROUTE TABLE (`RT-*`) — 14 active

| ID | Class | Описание | Саб-шаги | Ключевые ассерты |
|---|---|---|---|---|
| `RT-CR-VALID` | `CRUD-CR-OK` | happy-path | setup-net → `rtv.1 POST /routeTables` → `rtv.2 GET op` → cleanup-rt+poll → cleanup-net+poll | Op success |
| `RT-EMPTY-OK` | `RT-EMPTY-ROUTES` | staticRoutes=[] | setup-net → `rtempty.1 POST` (empty routes) | 200 |
| `RT-MULTI-ROUTE` | `RT-MULTI-ROUTE` | 3 routes incl `0.0.0.0/0` | setup-net → `rtm.1 POST` (3 routes) → `rtm.2 GET op` → `rtm.3 GET RT` → cleanup×2+poll | все 3 preserved |
| `RT-CR-INVALID-FOLDER` | `VAL-EMPTY-FOLDER` | без folderId | `rtcrnof.create` | 400 |
| `RT-CR-DUP-DEST` | `VAL-DUP-DEST` | duplicate destinationPrefix | setup-net → `rtcrdp.create` → cleanup×2+poll | Op.error |
| `RT-GET-NOTFOUND` | `NF-GET-404` | Get non-existent | `rtgetnf.get /routeTables/enp00000000nonexist0` | 404 |
| `RT-LIST-OPS` | `LIST-OPS` | ListOperations | setup-net → create RT → `rtops.list-ops GET /routeTables/{id}/operations` → cleanup×2+poll | Create-op present |
| `RT-LIST-FILTER` | `LIST-FILTER-FOLDER` | filter by folderId | setup-net → create → `rtlsf.list ?folderId=_suite` → cleanup×2+poll | filter applied |
| `RT-LIST-PS-NEG` | `BVA-PS-NEG` | pageSize=-1 | `rtlspn.list ?pageSize=-1` | 400 (Kachō rejects) |
| `RT-LIST-PS-OVER` | `BVA-PS-OVER` | pageSize=1001 | `rtlspo.list ?pageSize=1001` | 400 |
| `RT-UP-NAME` | `CRUD-PATCH-RENAME` | rename | setup → `rtupn.update PATCH` → `rtupn.verify GET` → cleanup×2+poll | name updated |
| `RT-UP-ADD-ROUTE` | `RT-PATCH-ADD-ROUTE` | add route | setup → create → `rtuar.update PATCH staticRoutes` → poll → verify → cleanup×2+poll | route added |
| `RT-PATCH-CLEAR-ROUTES` | `RT-PATCH-CLEAR-ROUTES` | clear routes | setup → create(routes) → `rtpccr.update PATCH staticRoutes:[]` → poll → verify | routes cleared |
| `RT-PATCH-CLEAR-DESC` | `PATCH-CLEAR-DESC` | desc="" | setup-net → `rtpc.1 POST RT(desc)` → `rtpc.2 PATCH desc=""` → `rtpc.3 GET RT` → cleanup×2+poll | очищено |

---

# SECURITY GROUP (`SG-*`) — 23 active

| ID | Class | Описание | Саб-шаги | Ключевые ассерты |
|---|---|---|---|---|
| `SG-IMPLEMENTED` | `SG-ENDPOINTS-REGISTERED` | smoke | `sgnm.1 POST /securityGroups` (zero-uuid) + `sgnm.2 GET /securityGroups?folderId=zero` | 200 (routes registered) |
| `SG-CR-VALID` | `CRUD-CR-OK` | single rule | setup-net+poll → setup-sg+poll → `sgcrv.get` → cleanup-sg+poll → cleanup-net+poll | rule echoed |
| `SG-CR-EMPTY-RULES` | `VAL-EMPTY-RULES` | без rules | setup-net+poll → create no rules → `sgcrer.verify` → cleanup×2+poll | `rules: []` |
| `SG-CR-INGRESS-EGRESS` | `CRUD-CR-OK` (multi-rule) | оба direction | setup-net+poll → create 2 rules → `sgcrie.verify` → cleanup×2+poll | оба rule present |
| `SG-CR-PROTO-NUMBER` | `SG-RULE-PROTO-NUMBER` | UDP=17 | setup-net+poll → create rule(`protocolNumber:17`) → `sgcrpn.verify` → cleanup×2+poll | proto echoed |
| `SG-RULE-WIDE-CIDR` | `SG-RULE-WIDE-CIDR` | 0.0.0.0/0 | setup-net+poll → create rule(0.0.0.0/0) → cleanup×2+poll | accepted |
| `SG-CR-EMPTY-FOLDER` | `VAL-EMPTY-FOLDER` | без folderId | `sgcrnof.create` | 400 |
| `SG-CR-EMPTY-NETWORK` | `VAL-EMPTY-NETWORK` | без networkId | `sgcrnonet.create` | 400 |
| `SG-CR-NAME-OVER` | `BVA-NAME-OVER` | name=64 | setup-net+poll → `sgcrnov.create` → cleanup-net+poll | 400 |
| `SG-CR-INVALID-DIRECTION` | `VAL-INVALID-DIRECTION` | bad direction | setup-net+poll → create bad direction → cleanup×2+poll | Op error (Kachō decision) |
| `SG-CR-INVALID-CIDR` | `VAL-INVALID-CIDR` | garbage CIDR | setup-net+poll → create bad CIDR → cleanup×2+poll | Op error |
| `SG-CR-PORT-INVERTED` | `VAL-PORT-INVERTED` | from>to | setup-net+poll → create inverted ports → cleanup×2+poll | accepted (Kachō decision) |
| `SG-CR-INVALID-NETWORK` | `VAL-INVALID-NETWORK` | non-existent networkId | `sgcrinet.create` → `sgcrinet.poll` | Op.error |
| `SG-GET-NOTFOUND` | `NF-GET-404` | Get non-existent | `sggetnf.get /securityGroups/enp00000000nonexist0` | 404 |
| `SG-LIST-OPS` | `LIST-OPS` | ListOperations | setup-net+poll + setup-sg+poll → `sgops.list-ops GET /securityGroups/{id}/operations` → cleanup×2+poll | Create-op present |
| `SG-LIST-PS-NEG` | `BVA-PS-NEG` | pageSize=-1 | `sglspn.list` | 400 |
| `SG-LIST-PS-OVER` | `BVA-PS-OVER` | pageSize=1001 | `sglspo.list` | 400 |
| `SG-UP-NAME` | `CRUD-PATCH-RENAME` | rename | setup-net+poll + setup-sg+poll → `sgupn.update PATCH` → `sgupn.verify GET` → cleanup×2+poll | name updated |
| `SG-UP-DESC` | `CRUD-PATCH-DESC` | description | setup×2+poll → `sgupd.update PATCH` → `sgupd.verify` → cleanup×2+poll | desc updated |
| `SG-UPDATE-RULES-ADD` | `SG-RULES-ADD` | :rules add | setup×2+poll → `sgrules.add-rule PATCH /rules` → poll → `sgrules.verify` → cleanup×2+poll | rule appears |
| `SG-UPDATE-RULES-DELETE` | `SG-RULES-DELETE` | :rules delete | setup×2+poll → `sgrdel.get-rule` → `sgrdel.delete-rule PATCH /rules` → poll → verify → cleanup×2+poll | rule removed |
| `SG-UPDATE-RULE-OK` | `SG-RULE-UPDATE-OK` | UpdateRule returns parent | setup×2+poll → `sgrupok.get-rule` → `sgrupok.update-rule PATCH /rules/{ruleId}` → poll → cleanup×2+poll | response — parent SG (regression) |
| `SG-UPDATE-RULE-NOTFOUND` | `SG-RULE-UPDATE-NF` | non-existent ruleId | setup×2+poll → `sgrupnf.update-rule` (bad id) → poll → cleanup×2+poll | Op.error |
| `SG-DEFAULT-NO-DELETE` | `SG-DEFAULT-NO-DELETE` | DELETE default SG | setup-net+poll → `sgddel.find-default GET /networks/{id}/security_groups` → `sgddel.try-delete DELETE` → poll → cleanup-net+poll | Op.error |
| `SG-MOVE-VALID` | `LIFECYCLE-MOVE` | :move | setup-net+poll → `sgmv.setup-folder2 POST /folders` → setup-sg → `sgmv.move POST :move` → cleanup-sg+poll → cleanup-net+poll → cleanup-folder2 | Op returned |

---

# GATEWAY (`GW-*`) — 10 active

| ID | Class | Описание | Саб-шаги | Ключевые ассерты |
|---|---|---|---|---|
| `GW-CR-VALID` | `CRUD-CRUD-SMOKE` | full smoke | `gwc.create POST /gateways` → `gwc.get GET` → `gwc.list GET ?folderId=` → `gwc.update PATCH` → cleanup-gw+poll | full lifecycle ok |
| `GW-CR-NO-SPEC` | `VAL-EMPTY-RULES` (variant) | без sharedEgressGatewaySpec | `gwcrnsp.create` → cleanup-gw+poll | accepted (Kachō decision) |
| `GW-CR-EMPTY-FOLDER` | `VAL-EMPTY-FOLDER` | без folderId | `gwcrnof.create` | 400 |
| `GW-CR-NAME-OVER` | `BVA-NAME-OVER` | name=64 | `gwlname.create` (single step) | 400 |
| `GW-GET-NOTFOUND` | `NF-GET-404` | Get non-existent | `gwgetnf.get /gateways/enp00000000nonexist0` | 404 |
| `GW-LIST-OPS` | `LIST-OPS` | ListOperations | `gwops.create` → `gwops.list-ops GET /gateways/{id}/operations` → cleanup-gw+poll | Create-op present |
| `GW-LIST-FILTER-FOLDER` | `LIST-FILTER-FOLDER` | filter | `gwlsf.create` → `gwlsf.list ?folderId=_suite` → cleanup-gw+poll | filter applied |
| `GW-LIST-PS-NEG` | `BVA-PS-NEG` | pageSize=-1 | `gwlspn.list` | 400 |
| `GW-UP-LABELS` | `CRUD-PATCH-LABELS` | update labels | `gwuplbl.create` → `gwuplbl.update PATCH` → `gwuplbl.verify GET` → cleanup-gw+poll | labels updated |
| `GW-MOVE-VALID` | `LIFECYCLE-MOVE` | :move | `gwmv.setup-folder2 POST /folders` → `gwmv.create` → `gwmv.move POST :move` → cleanup-gw+poll → cleanup-folder2 | Op returned |

---

# PRIVATE ENDPOINT (`PE-*`) — 4 active

| ID | Class | Описание | Саб-шаги | Ключевые ассерты |
|---|---|---|---|---|
| `PE-CR-VALID` | `CRUD-CRUD-SMOKE` | full smoke | `pec.setup-net POST /networks` → `pec.create POST /endpoints` → `pec.get GET` → `pec.list GET ?folderId=` → cleanup-pe+poll → cleanup-net+poll | full lifecycle ok |
| `PE-CR-EMPTY-FOLDER` | `VAL-EMPTY-FOLDER` | без folderId | `pecrnof.create` | 400 |
| `PE-CR-EMPTY-NETWORK` | `VAL-EMPTY-NETWORK` | без networkId | `pecrnonet.create` | 400 |
| `PE-GET-NOTFOUND` | `NF-GET-404` | Get non-existent | `pegetnf.get /endpoints/enp00000000nonexist0` | 404 |
| `PE-LIST-PS-NEG` | `BVA-PS-NEG` | pageSize=-1 | `pelspn.list` | 400 |

---

# VPC cross-cutting (`VPC-*`, `OP-VPC-*`) — 4 active

| ID | Class | Описание | Саб-шаги | Ключевые ассерты |
|---|---|---|---|---|
| `VPC-LIST-FILTER` | `LIST-FILTER-EXPR-INVALID` + `LIST-FILTER-EXPR-APPLIED` | filter parsing | `vlfs.setup-net1 POST` → `vlfs.1 GET ?filter=this_is_not_valid_expression` → `vlfs.2 GET ?filter=name="nonexistent"` → cleanup-net1+poll | (1) 400 InvalidArgument; (2) пустой массив (filter applied) |
| `VPC-LIST-LABEL-SELECTOR-SILENT` | `LIST-LABEL-SELECTOR-SILENT` | silent ignore | `vlls.setup-net POST` (`labels:{env:dev}`) → `vlls.1 GET ?labelSelector=env=prod` → cleanup-net+poll | dev network возвращается (selector ignored) |
| `VPC-LOCALIZED-ERRORS-MISSING` | `LOCALIZED-ERR-MISSING` | Accept-Language | `vlem.1 POST` (Accept-Language: fr-FR, name="!!!") | 400 + BadRequest only, no LocalizedMessage |
| `YC-OP-VPC-LOCAL-404` | `OPS-LOCAL-404` | per-domain ops 404 | `YC-OP-VPC-LOCAL-404.1 GET /vpc/v1/operations/anyid` | 404 |

---

# Матрица «Class × Resource» (покрытие)

`✓` = есть как минимум один кейс с этим классом для ресурса; `—` = не покрыт; `(d)` = drop / pending.

| Class | NET | SU | A | RT | SG | GW | PE | VPC× |
|---|:-:|:-:|:-:|:-:|:-:|:-:|:-:|:-:|
| `CRUD-CR-OK` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | — |
| `CRUD-CRUD-SMOKE` | — | — | — | — | — | ✓ | ✓ | — |
| `CRUD-PATCH-RENAME` | ✓ | — | — | ✓ | ✓ | — | — | — |
| `CRUD-PATCH-DESC` | — | — | — | — | ✓ | — | — | — |
| `CRUD-PATCH-LABELS` | — | — | — | — | — | ✓ | — | — |
| `CRUD-LIST-SHAPE` | ✓ | — | — | — | — | — | — | — |
| `BVA-NAME-MAX` | ✓ | — | — | — | — | — | — | — |
| `BVA-NAME-OVER` | ✓ | — | — | — | ✓ | ✓ | — | — |
| `BVA-LABELS-MAX` | ✓ | — | ✓ | — | — | — | — | — |
| `BVA-LABELS-OVER` | ✓ | — | — | — | — | — | — | — |
| `BVA-DESC-MAX` | ✓ | — | — | — | — | — | — | — |
| `BVA-DESC-OVER-CR` | ✓ | — | — | — | — | — | — | — |
| `BVA-DESC-OVER-UP` | ✓ | — | — | — | — | — | — | — |
| `BVA-PS-NEG` | (d) | — | — | ✓ | ✓ | ✓ | ✓ | — |
| `BVA-PS-OVER` | — | — | — | ✓ | ✓ | — | — | — |
| `BVA-CIDR-MIN` | — | ✓ | — | — | — | — | — | — |
| `BVA-CIDR-OVER` | — | ✓ | — | — | — | — | — | — |
| `VAL-EMPTY-FOLDER` | ✓ | — | ✓ | ✓ | ✓ | ✓ | ✓ | — |
| `VAL-EMPTY-NAME` | ✓ | — | — | — | — | — | — | — |
| `VAL-EMPTY-NETWORK` | — | — | — | — | ✓ | — | ✓ | — |
| `VAL-EMPTY-RULES` | — | — | — | ✓ (RT-EMPTY-OK) | ✓ | ✓ | — | — |
| `VAL-NAME-CASE-PERMISSIVE` | ✓ | — | — | — | — | — | — | — |
| `VAL-INVALID-ID-FORMAT` | ✓ | — | — | — | — | — | — | — |
| `VAL-INVALID-FOLDER` | (d) | ✓ | (d) | — | — | — | — | — |
| `VAL-INVALID-NETWORK` | — | (d) | — | (d) | ✓ | — | — | — |
| `VAL-INVALID-CIDR` | — | ✓ | — | — | ✓ | — | — | — |
| `VAL-INVALID-DIRECTION` | — | — | — | — | ✓ | — | — | — |
| `VAL-INVALID-ZONE` | — | (d) | ✓ | — | — | — | — | — |
| `VAL-PORT-INVERTED` | — | — | — | — | ✓ | — | — | — |
| `VAL-DUP-NAME` | (d) | — | ✓ | — | — | — | — | — |
| `VAL-DUP-DEST` | — | — | — | ✓ | — | — | — | — |
| `VAL-MASK-UNKNOWN` | ✓ | — | — | — | — | — | — | — |
| `NF-GET-404` | ✓ | — | ✓ | ✓ | ✓ | ✓ | ✓ | — |
| `LIST-FILTER-FOLDER` | ✓ | — | — | ✓ | — | ✓ | — | — |
| `LIST-FILTER-EXPR-INVALID` | — | — | — | — | — | — | — | ✓ |
| `LIST-FILTER-EXPR-APPLIED` | — | — | — | — | — | — | — | ✓ |
| `LIST-LABEL-SELECTOR-SILENT` | — | — | — | — | — | — | — | ✓ |
| `LIST-NO-FOLDER-ALL` | — | — | ✓ | — | — | — | — | — |
| `LIST-OPS` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | — | — |
| `LIST-PS-ONE` | ✓ | — | — | — | — | — | — | — |
| `LIST-PT-FORMAT` | ✓ | — | — | — | — | — | — | — |
| `LIST-PT-LEAK` | ✓ | — | — | — | — | — | — | — |
| `LIST-PT-INVALID` | ✓ | — | — | — | — | — | — | — |
| `NESTED-LIST-SUBNETS-EMPTY` | ✓ | — | — | — | — | — | — | — |
| `NESTED-LIST-RT-EMPTY` | ✓ | — | — | — | — | — | — | — |
| `PATCH-CLEAR-DESC` | ✓ | ✓ | (d) | ✓ | — | — | — | — |
| `PATCH-CLEAR-NAME-PERMISSIVE` | ✓ | — | — | — | — | — | — | — |
| `LIFECYCLE-MOVE` | (d) | (d) | (d) | (d) | ✓ | ✓ | — | — |
| `LIFECYCLE-RELOCATE-OK` | — | ✓ | — | — | — | — | — | — |
| `LIFECYCLE-RELOCATE-INVALID` | — | ✓ | — | — | — | — | — | — |
| `OPS-LOCAL-404` | — | — | — | — | — | — | — | ✓ |
| `LOCALIZED-ERR-MISSING` | — | — | — | — | — | — | — | ✓ |
| `CIDR-ADD-VALID` | — | ✓ | — | — | — | — | — | — |
| `CIDR-ADD-OVERLAP` | — | ✓ | — | — | — | — | — | — |
| `CIDR-ADD-INVALID` | — | ✓ | — | — | — | — | — | — |
| `CIDR-ADD-EMPTY` | — | ✓ | — | — | — | — | — | — |
| `CIDR-REMOVE-VALID` | — | ✓ | — | — | — | — | — | — |
| `CIDR-REMOVE-ABSENT` | — | ✓ | — | — | — | — | — | — |
| `DHCP-ROUNDTRIP` | — | ✓ | — | — | — | — | — | — |
| `DHCP-PATCH-CLEAR` | — | ✓ | — | — | — | — | — | — |
| `DHCP-PATCH-REPLACE` | — | ✓ | — | — | — | — | — | — |
| `IPV6-ALWAYS-EMPTY` | — | ✓ | — | — | — | — | — | — |
| `RT-EMPTY-ROUTES` | — | — | — | ✓ | — | — | — | — |
| `RT-MULTI-ROUTE` | — | — | — | ✓ | — | — | — | — |
| `RT-PATCH-CLEAR-ROUTES` | — | — | — | ✓ | — | — | — | — |
| `RT-PATCH-ADD-ROUTE` | — | — | — | ✓ | — | — | — | — |
| `SG-RULES-ADD` | — | — | — | — | ✓ | — | — | — |
| `SG-RULES-DELETE` | — | — | — | — | ✓ | — | — | — |
| `SG-RULE-UPDATE-OK` | — | — | — | — | ✓ | — | — | — |
| `SG-RULE-UPDATE-NF` | — | — | — | — | ✓ | — | — | — |
| `SG-RULE-PROTO-NUMBER` | — | — | — | — | ✓ | — | — | — |
| `SG-RULE-WIDE-CIDR` | — | — | — | — | ✓ | — | — | — |
| `SG-DEFAULT-NO-DELETE` | — | — | — | — | ✓ | — | — | — |
| `SG-ENDPOINTS-REGISTERED` | — | — | — | — | ✓ | — | — | — |

## Заметки по матрице

- **`BVA-PS-NEG`** на NET-* в drop (`NET-LIST-PS-NEG` в `PARITY_PENDING`: YC clamps, KC rejects). RT/SG/GW/PE — реализовано как 400 для обоих env.
- **`VAL-EMPTY-FOLDER`** — стандартный sync-validation case, есть для большинства POST-эндпойнтов.
- **`VAL-INVALID-FOLDER` / `VAL-INVALID-NETWORK`** — async-vs-sync архитектурная разница: NET/RT/A drop'ы возвращаются после Kachō Go-refactor.
- **`LIFECYCLE-MOVE`** на NET/SU/A/RT — pending parity verification; покрыт только SG/GW.
- **`LIST-OPS`** покрыт у всех ресурсов кроме PE (нет smoke на этом subresource).
- **`PATCH-CLEAR-DESC`** покрыт у NET/SU/RT, у A — pending (eventual consistency после PATCH).
- **`NF-GET-404`** покрыт у всех ресурсов кроме SU (отсутствует — gap).

## Покрытие по ресурсам (счётчик)

| Ресурс | Active | Pending parity | Drop |
|---|---:|---:|---:|
| Network | 27 | 8 | — |
| Subnet | 19 | — | 11 |
| Address | 8 | — | 14 |
| RouteTable | 14 | — | 5 |
| SecurityGroup | 25 | — | — |
| Gateway | 10 | — | 1 |
| PrivateEndpoint | 4 | — | 1 |
| VPC cross-cutting | 4 | — | 1 |
| **Итого** | **111** | **8** | **33** |

## Источники / связки

- `scripts/rebuild-collection.py` → UNIFIED_MAP (Network), DOMAIN_AUTO_TRANSFORM_PREFIXES (others), PARITY_PENDING, DROP_NETWORK_FOLDERS
- `collections/kacho-vpc.postman_collection.json` → актуальная коллекция
- `PARITY.md` (workspace root) → причины drop/pending по архитектурной несовместимости YC↔Kachō
- `scripts/run.sh` → запуск (`./scripts/run.sh --env yc|local --folder NET-*`)
