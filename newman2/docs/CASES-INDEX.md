# newman2 — индекс уникальных кейсов

Сгенерированный индекс 571 тест-сценариев, сгруппированных по
170 уникальным паттернам (по RPC-методу × классу × детали).

Один паттерн = конкретная проверка, применённая к одному или нескольким
ресурсам. Например, `*-LST-BVA-PAGESIZE-ZERO` применён к 7 List RPC.

## Сводка по методам

| RPC method | Паттернов | Описание |
|---|---|---|
| - | 5 | HTTP-level / cross-method |
| AddCidrBlocks | 3 | Subnet: добавить CIDR-блоки |
| Create | 64 | Создание ресурса (async, возвращает Operation) |
| Delete | 5 | Удаление (async, sync-NF от AuthZ-Get) |
| Get | 13 | Чтение по id (sync, может быть NotFound) |
| GetByValue | 4 | Address: lookup по конкретному IP |
| List | 26 | Листинг с фильтром по folder_id + пагинацией |
| ListBySubnet | 2 | Address: список в подсети |
| ListOperations | 2 | Operations связанные с ресурсом |
| ListRouteTables | 2 | Network: дочерние RouteTable |
| ListSecurityGroups | 2 | Network: дочерние SG |
| ListSubnets | 2 | Network: дочерние Subnet |
| ListUsedAddresses | 2 | Subnet: используемые IP |
| Move | 6 | Перемещение в другой folder (async) |
| Relocate | 3 | Subnet: сменить zone |
| RemoveCidrBlocks | 3 | Subnet: убрать CIDR-блоки |
| Update | 16 | Изменение (PATCH с UpdateMask, async) |
| UpdateRule | 3 | SG: единичное правило |
| UpdateRules | 7 | SG: batch обновление правил (xmin OCC) |

## Сводка по классам

| Класс | Описание |
|---|---|
| CRUD | happy path сценарий |
| NEG | negative scenario (NotFound, conflict) |
| VAL | sync-валидация: required, format, regex |
| AUTHZ | AuthZ check (cross-tenant, sync-NF guard) |
| BVA | Boundary Value Analysis (page_size 0/1/1000/1001/10000, len 63/64/256/257, labels 64/65) |
| PAGE | Pagination semantics (token, page boundary) |
| STATE | State transition (immutable fields, status, idempotency) |
| CONF | Verbatim YC text conformance |
| FILTER | Filter syntax (name=, garbage, unknown field) |
| IDM | Idempotency (retry-safe) |
| CONC | Concurrency invariant (dup-name race) |
| PERF | Performance baseline (response time budget) |

---

## Кейсы по методам

Формат: `case-id-pattern` | классы | P | ресурсы | что проверяем


### Cross-method (HTTP-level)

*HTTP-level / cross-method*

| Pattern | Classes | P | Apps | Что проверяет |
|---|---|---|---|---|
| `*-AUTHZ-EMPTY-FOLDER-HEADER` | AUTHZ | P1 | 7 (add,gat,net,pri,rou,sec,sub) | List с пустым x-kacho-folder-id header → текущее: 200 (dev mode) |
| `*-HEADERS-MISSING-CT` | NEG,VAL | P3 | 3 (add,gat,net) | POST без Content-Type → 415 или 400 или 200 (lenient) |
| `*-METHOD-DELETE-LIST` | NEG,VAL | P3 | 7 (add,gat,net,pri,rou,sec,sub) | DELETE на List endpoint (без id) → 405 или 404 |
| `*-METHOD-NOT-ALLOWED` | NEG,VAL | P3 | 1 (pri) | PUT/HEAD на /endpoints → не разрешено |
| `*-METHOD-PUT-NOT-ALLOWED` | NEG,VAL | P3 | 7 (add,gat,net,pri,rou,sec,sub) | PUT на List endpoint → 405 или 404 |

### AddCidrBlocks

*Subnet: добавить CIDR-блоки*

| Pattern | Classes | P | Apps | Что проверяет |
|---|---|---|---|---|
| `*-ACB-CRUD-OK` | CRUD | P1 | 1 (sub) | AddCidrBlocks → новый блок виден в GET |
| `*-ACB-NEG-OVERLAP` | NEG | P1 | 1 (sub) | AddCidrBlocks с CIDR пересекающимся с existing → InvalidArgument/FailedPrecondition |
| `*-ACB-STATE-DISJOINT-CIDRS` | CONF,STATE,VAL | P1 | 1 (sub) | AddCidrBlocks с пересекающимися CIDR в одном запросе → InvalidArgument |

### Create

*Создание ресурса (async, возвращает Operation)*

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
| `*-CR-CONF-DUP-NAME-FINDING-005` | CONF,NEG | P0 | 1 (sub) | FINDING-005: Subnet НЕ имеет UNIQUE (folder_id, name) — duplicate проходит |
| `*-CR-CONF-FOLDER-NF-TEXT` | CONF,NEG | P1 | 2 (add,net) | Create network в garbage folder → verbatim 'Folder with id ... not found' |
| `*-CR-CONF-NET-NF-TEXT` | CONF,NEG | P1 | 4 (pri,rou,sec,sub) | Create subnet в garbage network → verbatim text 'Network ... not found' |
| `*-CR-CONF-SUB-NF-TEXT` | CONF,NEG | P1 | 1 (add) | Create address с garbage subnet → verbatim 'Subnet ... not found' |
| `*-CR-CRUD-EXT` | CRUD | P1 | 1 (add) | Create external Address → IP из default pool |
| `*-CR-CRUD-INT` | CRUD | P1 | 1 (add) | Create internal Address → IP в subnet |
| `*-CR-CRUD-OK` | CRUD | P1 | 6 (gat,net,pri,rou,sec,sub) | Create subnet → Operation → Subnet visible in GET |
| `*-CR-IDM-RETRY` | CONC,IDM | P1 | 1 (net) | Retry-safe: повторный Create same input → consistent result |
| `*-CR-NEG-CIDR-OVERLAP` | NEG | P0 | 1 (sub) | Create двух subnet с пересекающимися CIDR → второй FailedPrecondition |
| `*-CR-NEG-DUP-NAME` | CONC,NEG | P1 | 1 (net) | Create с duplicate name в folder → async ALREADY_EXISTS |
| `*-CR-NEG-DUP-NAME-CHECK` | CONC,NEG | P1 | 6 (add,gat,net,rou,sec,sub) | Создать дубль с тем же name → проверить ALREADY_EXISTS или silent (FINDING) |
| `*-CR-NEG-FOLDER-NF` | CONF,NEG | P0 | 1 (gat) | Create Gateway в несуществующий folder → async NotFound |
| `*-CR-NEG-FOLDER-NOT-FOUND` | NEG | P0 | 1 (net) | Create с garbage folderId → async NOT_FOUND |
| `*-CR-NEG-NETWORK-NF` | NEG | P0 | 2 (pri,rou) | Create в несуществующую network → async NotFound |
| `*-CR-NEG-NETWORK-NOT-FOUND` | NEG | P0 | 1 (sub) | Create в несуществующей network → async NOT_FOUND |
| `*-CR-NEG-SUBNET-NF-FINDING-006` | NEG | P1 | 1 (pri) | FINDING-006: PE Create с garbage subnetId silent success — нет existence validation |
| `*-CR-NEG-SUBNET-NOT-FOUND` | NEG | P0 | 1 (add) | Create internal с garbage subnetId → async NotFound |
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

*Удаление (async, sync-NF от AuthZ-Get)*

| Pattern | Classes | P | Apps | Что проверяет |
|---|---|---|---|---|
| `*-DEL-AUTHZ-NF-SYNC` | AUTHZ,NEG | P1 | 7 (add,gat,net,pri,rou,sec,sub) | Delete несуществующего → sync 404 |
| `*-DEL-CONF-FULLTEXT` | CONF,NEG | P1 | 7 (add,gat,net,pri,rou,sec,sub) | Delete garbage → 'Subnet ... not found' |
| `*-DEL-CONF-NF-TEXT` | CONF,NEG | P1 | 7 (add,gat,net,pri,rou,sec,sub) | Delete несуществующего Subnet → verbatim 'Subnet ... not found' |
| `*-DEL-CRUD-OK` | CRUD | P1 | 7 (add,gat,net,pri,rou,sec,sub) | Subnet Delete happy path |
| `*-DEL-NEG-NF-INVALID-PREFIX` | NEG,STATE | P1 | 1 (net) | Delete с id без VPC-префикса → sync 404 |

### Get

*Чтение по id (sync, может быть NotFound)*

| Pattern | Classes | P | Apps | Что проверяет |
|---|---|---|---|---|
| `*-GET-CONF-FULLTEXT` | CONF,NEG | P1 | 7 (add,gat,net,pri,rou,sec,sub) | Get garbage → 'Subnet <id> not found' формат |
| `*-GET-CONF-NF-FULLTEXT` | CONF,NEG | P1 | 1 (pri) | Get garbage PE → 'PrivateEndpoint <id> not found' формат |
| `*-GET-CONF-NF-TEXT` | CONF,NEG | P1 | 8 (add,gat,net,ope,pri,rou,sec,sub) | Get garbage — verbatim text 'Subnet ... not found' |
| `*-GET-CRUD-OK` | CRUD | P1 | 1 (ope) | Get свежесозданной operation → done=true с response |
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

*Address: lookup по конкретному IP*

| Pattern | Classes | P | Apps | Что проверяет |
|---|---|---|---|---|
| `*-GBV-CONF-NOLEAK-FOR-EXISTING-OTHER` | AUTHZ,CONF | P0 | 1 (add) | GetByValue адреса из другого folder → NotFound (security info-leak) |
| `*-GBV-CRUD-OK` | CRUD | P1 | 1 (add) | GetByValue существующего external IP → 200 + сам Address |
| `*-GBV-NEG-NF` | AUTHZ,NEG | P0 | 1 (add) | GetByValue несуществующего IP → NotFound (security: не должно leak'ать существование) |
| `*-GBV-VAL-INVALID-IP` | NEG,VAL | P2 | 1 (add) | GetByValue с garbage IP → 400 или 404 |

### List

*Листинг с фильтром по folder_id + пагинацией*

| Pattern | Classes | P | Apps | Что проверяет |
|---|---|---|---|---|
| `*-LST-AUTHZ-CROSS-FOLDER-ISOLATION` | AUTHZ,CRUD | P0 | 3 (add,gat,net) | Folder isolation: ресурс в folderA не виден в List по folderB |
| `*-LST-BVA-PAGESIZE-1` | BVA,PAGE | P2 | 7 (add,gat,net,pri,rou,sec,sub) | List pageSize=1 → ≤1 item |
| `*-LST-BVA-PAGESIZE-OVER-MAX` | BVA,VAL | P2 | 7 (add,gat,net,pri,rou,sec,sub) | List pageSize=10000 → InvalidArgument |
| `*-LST-BVA-PAGESIZE-ZERO` | BVA | P2 | 7 (add,gat,net,pri,rou,sec,sub) | List pageSize=0 → default applied (200) |
| `*-LST-CONTRACT-NEVER-EXCEEDS-PAGESIZE` | CRUD,PAGE | P2 | 7 (add,gat,net,pri,rou,sec,sub) | List с pageSize=5 → не более 5 элементов в response |
| `*-LST-CRUD-OK` | CRUD | P1 | 7 (add,gat,net,pri,rou,sec,sub) | List subnets в folder → 200 |
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
| `*-LST-VAL-FOLDER-REQUIRED` | AUTHZ,VAL | P0 | 7 (add,gat,net,pri,rou,sec,sub) | List без folderId → InvalidArgument |

### ListBySubnet

*Address: список в подсети*

| Pattern | Classes | P | Apps | Что проверяет |
|---|---|---|---|---|
| `*-LBS-CRUD-OK` | CRUD | P2 | 1 (add) | ListBySubnet → массив (возможно пустой) |
| `*-LBS-NEG-PARENT-NF` | NEG | P2 | 1 (add) | ListBySubnet несуществующего subnet → 200 или 404 |

### ListOperations

*Operations связанные с ресурсом*

| Pattern | Classes | P | Apps | Что проверяет |
|---|---|---|---|---|
| `*-LOP-CRUD-OK` | CRUD,NEG | P1 | 7 (add,gat,net,pri,rou,sec,sub) | ListOperations возвращает create-op |
| `*-LOP-NEG-PARENT-NF` | NEG | P2 | 6 (add,gat,net,rou,sec,sub) | ListOperations несуществующего subnet → 404 или 200 пустой |

### ListRouteTables

*Network: дочерние RouteTable*

| Pattern | Classes | P | Apps | Что проверяет |
|---|---|---|---|---|
| `*-LRT-CRUD-EMPTY` | CRUD | P2 | 1 (net) | ListRouteTables → 200 + empty |
| `*-LRT-NEG-PARENT-NF` | NEG | P1 | 1 (net) | List route_tables в несуществующей network → 404 NotFound |

### ListSecurityGroups

*Network: дочерние SG*

| Pattern | Classes | P | Apps | Что проверяет |
|---|---|---|---|---|
| `*-LSG-CRUD-DEFAULT-SG` | CRUD | P1 | 1 (net) | ListSecurityGroups → default SG присутствует (inline create в doCreate) |
| `*-LSG-NEG-PARENT-NF` | NEG | P1 | 1 (net) | List security_groups в несуществующей network → 404 NotFound |

### ListSubnets

*Network: дочерние Subnet*

| Pattern | Classes | P | Apps | Что проверяет |
|---|---|---|---|---|
| `*-LSUB-CRUD-EMPTY` | CRUD | P2 | 1 (net) | ListSubnets для пустой network → 200 + empty array |
| `*-LSUB-NEG-PARENT-NF` | NEG | P1 | 1 (net) | List subnets в несуществующей network → 404 NotFound |

### ListUsedAddresses

*Subnet: используемые IP*

| Pattern | Classes | P | Apps | Что проверяет |
|---|---|---|---|---|
| `*-LUA-CRUD-OK` | CRUD | P2 | 1 (sub) | ListUsedAddresses на пустой subnet → empty |
| `*-LUA-NEG-PARENT-NF` | NEG | P2 | 1 (sub) | ListUsedAddresses несуществующего subnet → 404 или 200 |

### Move

*Перемещение в другой folder (async)*

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

*Subnet: убрать CIDR-блоки*

| Pattern | Classes | P | Apps | Что проверяет |
|---|---|---|---|---|
| `*-RCB-CONF-STATE` | STATE | P1 | 1 (sub) | STATE для RemoveCidrBlocks: проверка инварианта после операции |
| `*-RCB-CRUD-OK` | CRUD | P1 | 1 (sub) | RemoveCidrBlocks: убрать дополнительный CIDR |
| `*-RCB-NEG-NF` | NEG,STATE,VAL | P1 | 1 (sub) | RemoveCidrBlocks с несуществующим CIDR → InvalidArgument |

### Update

*Изменение (PATCH с UpdateMask, async)*

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
| `*-UPD-STATE-IMMUTABLE-CIDR` | STATE,VAL | P1 | 1 (sub) | Update с mask=v4_cidr_blocks → InvalidArgument (immutable) |
| `*-UPD-STATE-IMMUTABLE-FOLDER` | STATE,VAL | P1 | 7 (add,gat,net,pri,rou,sec,sub) | Update с mask=folder_id → InvalidArgument (immutable) |
| `*-UPD-VAL-MASK-EMPTY` | STATE,VAL | P2 | 7 (add,gat,net,pri,rou,sec,sub) | Update с пустой mask → full PATCH (200) |
| `*-UPD-VAL-MASK-MULTIPLE-UNKNOWN` | STATE,VAL | P2 | 7 (add,gat,net,pri,rou,sec,sub) | Update с несколькими unknown полями в mask → 400 |
| `*-UPD-VAL-MASK-NAME-ONLY` | STATE,VAL | P2 | 6 (add,gat,net,rou,sec,sub) | Update mask=name → только name меняется, description/labels не трогаются |
| `*-UPD-VAL-UNKNOWN-MASK` | STATE,VAL | P1 | 7 (add,gat,net,pri,rou,sec,sub) | Update с unknown field в UpdateMask → InvalidArgument |

### UpdateRule

*SG: единичное правило*

| Pattern | Classes | P | Apps | Что проверяет |
|---|---|---|---|---|
| `*-UR-AUTHZ-NF-SYNC` | AUTHZ,NEG,VAL | P1 | 1 (sec) | UpdateRule несуществующего SG → sync 404 от AuthZ-Get |
| `*-UR-CRUD-OK` | CRUD | P1 | 1 (sec) | UpdateRule (single) — добавить rule, обновить description |
| `*-UR-NEG-RULE-NF` | NEG | P1 | 1 (sec) | UpdateRule (single) несуществующего rule_id → 404 NotFound |

### UpdateRules

*SG: batch обновление правил (xmin OCC)*

| Pattern | Classes | P | Apps | Что проверяет |
|---|---|---|---|---|
| `*-URL-AUTHZ-NF-SYNC` | AUTHZ,NEG,VAL | P1 | 1 (sec) | UpdateRules несуществующего SG → sync 404 от AuthZ-Get |
| `*-URL-CRUD-OK` | CRUD,STATE | P1 | 1 (sec) | UpdateRules: добавить правило |
| `*-URL-VAL-DIRECTION-UNKNOWN` | NEG,STATE,VAL | P1 | 1 (sec) | UpdateRules rule field: SG-URL-VAL-DIRECTION-UNKNOWN |
| `*-URL-VAL-PORT-ANY-MINUS-1` | STATE,VAL | P1 | 1 (sec) | UpdateRules rule field: SG-URL-VAL-PORT-ANY-MINUS-1 |
| `*-URL-VAL-PORT-NEG` | NEG,STATE,VAL | P1 | 1 (sec) | UpdateRules rule field: SG-URL-VAL-PORT-NEG |
| `*-URL-VAL-PORT-OVER-65535` | NEG,STATE,VAL | P1 | 1 (sec) | UpdateRules rule field: SG-URL-VAL-PORT-OVER-65535 |
| `*-URL-VAL-PROTOCOL-UNKNOWN` | NEG,STATE,VAL | P1 | 1 (sec) | UpdateRules rule field: SG-URL-VAL-PROTOCOL-UNKNOWN |
