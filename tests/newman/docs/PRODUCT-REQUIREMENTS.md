# Регламент продуктовых требований — kacho-vpc (от QA)

Нормативный список **продуктовых требований** к публичному API `kacho-vpc`, выведенный из
каталога тест-кейсов (`CASES-INDEX.md`) и контракта (verbatim-YC + acceptance-spec). Это
**регламент**, на соответствие которому агент-аудитор проверяет любое изменение кода / proto /
миграций / тестов: для каждого затронутого `REQ-*` — соблюдён ли он. Нарушение → блокирующее замечание.

**Кто ведёт.** Тестировщики добавляют сюда новые `REQ-*` по мере выявления требований (из ревью,
прогонов, probe'ов реального YC). Формат записи — ниже. Не путать с:
- `REQUIREMENTS.md` — бэклог *улучшений* (testability/contract-clarification asks), не нормативный.
- `docs/architecture/07-known-divergences.md` — намеренные расхождения с YC (это **исключения** из регламента, помечаются в REQ как «divergence: …»).
- баги/задачи — GitHub Issues (`CLAUDE.md` §14.4).

**Формат REQ.**
```
### REQ-<AREA>-<NN> — <короткий заголовок>           [P0|P1|P2|P3]
<нормативная формулировка: продукт ДОЛЖЕН / НЕ ДОЛЖЕН ...>
- Validated-by: <case-id-паттерны из CASES-INDEX, через запятую> (или «gap — нет кейса»)
- Agent-check: <где смотреть, чтобы проверить соответствие: файл/слой/proto/миграция>
- Divergence: <если это намеренное отклонение от verbatim-YC — ссылка на 07-known-divergences>
```

**Как агент использует регламент.** Получив diff/PR:
1. Определи, какие области (`RES`/`VAL`/`NAME`/`CIDR`/`IPAM`/`UPD`/`LIST`/`DEL`/`OPS`/`AUTHZ`/`SEC`/`SG`/`YC`/`MOVE`) затронуты.
2. Для каждого `REQ-*` в этих областях — проверь Agent-check: соответствует ли изменение требованию.
3. Если новый/изменённый RPC — пройдись по `TAXONOMY.md` «Применение по методам»: все обязательные классы покрыты кейсами? соответствующие `REQ-*` не нарушены?
4. Нарушение `REQ-*` (или регресс кейса из Validated-by) → **блокирующее** замечание со ссылкой на REQ.
5. Если изменение вводит новое поведение, не покрытое регламентом — предложи новый `REQ-*` (и кейс в `cases/*.py`).

---

## A. Модель ресурсов и жизненный цикл

### REQ-RES-01 — 7 публичных folder-scoped ресурсов с CRUD                     [P1]
Продукт ДОЛЖЕН предоставлять ресурсы Network / Subnet / Address / RouteTable / SecurityGroup /
Gateway / PrivateEndpoint; все folder-scoped (`folder_id` обязателен в Create); все поддерживают
`Get`/`List`/`Create`/`Update`/`Delete`, а Network/Subnet/Address/RouteTable/SecurityGroup/Gateway — ещё и `Move`.
- Validated-by: `*-LIFECYCLE-CONF`, `*-CR-CRUD-OK`, `*-GET-CRUD-OK`, `*-LST-CRUD-OK`, `*-UPD-CRUD-OK`, `*-DEL-CRUD-OK`, `*-MV-CRUD-OK`
- Agent-check: `internal/service/*.go` (по сервису на ресурс), `cmd/vpc/main.go` (регистрация), `kacho-proto/.../<res>_service.proto`.

### REQ-RES-02 — все мутации возвращают Operation (async)                       [P0]
`Create`/`Update`/`Delete`/`Move`/`AddCidrBlocks`/`RemoveCidrBlocks`/`Relocate`/`UpdateRules`/`UpdateRule`
ДОЛЖНЫ возвращать `operation.Operation`; реальная работа — в worker-горутине; клиент поллит
`OperationService.Get(id)` до `done=true`. Возвращать сам ресурс синхронно из мутации — ЗАПРЕЩЕНО.
- Validated-by: `OP-GET-CRUD-OK`, `*-LOP-CRUD-OK` (ListOperations содержит create-op), все `*-CR-*`/`*-UPD-*`/`*-DEL-*` (poll-паттерн)
- Agent-check: сигнатуры RPC в `.proto` (`returns (operation.Operation)`); `internal/service/*.go` — `operations.New` + `operations.Run` шаблон; запрет #9 workspace `CLAUDE.md`.

### REQ-RES-03 — Delete-операция: response = Empty, metadata = DeleteXxxMetadata [P1]
В завершённой Delete-`Operation`: `response` = `google.protobuf.Empty`, `metadata` = `DeleteXxxMetadata{<res>_id}`.
- Validated-by: `*-DEL-CRUD-OK` (poll → response shape)
- Agent-check: worker всех `Delete` в `internal/service/*.go` (`return anypb.New(&emptypb.Empty{})`); proto-options `response`/`metadata` в `<res>_service.proto`.

### REQ-RES-04 — Create retry-safe (идемпотентность по input)                   [P1]
Повторный `Create` с тем же input (где это детектируемо) ДОЛЖЕН давать консистентный результат
(не дубль-ресурс при одинаковом `name` — см. REQ-NAME-04).
- Validated-by: `*-CR-IDM-RETRY`
- Agent-check: `internal/service/*.go` doCreate — UNIQUE-violation → `AlreadyExists` (не сырой 500); idempotency на уровне Operation.

### REQ-RES-05 — hard-delete, без soft-delete/tombstone                          [P2]
`Delete` физически удаляет строку (`DELETE FROM`); в схеме нет `deletion_timestamp`/`finalizers`,
flat-таблицы без K8s-envelope.
- Validated-by: косвенно `*-DEL-CRUD-OK` + `*-GET-NEG-NF` после Delete
- Agent-check: `internal/migrations/0001_initial.sql` (нет envelope-колонок); `internal/repo/*.go` (`DELETE FROM`).

### REQ-RES-06 — Move в текущий folder — idempotent no-op                        [P2]
`Move` ресурса в его же `folder_id` ДОЛЖЕН успешно завершиться (200/Operation done), ресурс остаётся.
- Validated-by: `*-MV-IDM-SAME-FOLDER`
- Agent-check: `internal/service/*.go` doMove.

---

## B. Валидация полей (sync, до создания Operation)

### REQ-VAL-01 — required-поля в Create                                          [P0]
Отсутствие required-поля → sync `InvalidArgument "<field> is required"`. Required: `folder_id`
(все ресурсы); `network_id` (Subnet/RouteTable/SecurityGroup/PrivateEndpoint); `zone_id` (Subnet);
`v4_cidr_blocks` (Subnet, ≥1); gateway-type oneof (Gateway); service-spec (PrivateEndpoint — `objectStorage`).
- Validated-by: `*-CR-VAL-REQ-FOLDERID`/`-NETWORKID`/`-ZONEID`/`-V4CIDRBLOCKS`, `*-CR-VAL-FOLDER-REQUIRED`, `*-CR-VAL-NETWORK-REQUIRED`, `*-CR-VAL-ZONE-REQUIRED`, `*-CR-VAL-CIDR-REQUIRED`, `*-CR-VAL-MISSING-TYPE`, `*-CR-VAL-SERVICE-MISSING`
- Agent-check: начало `Create` в `internal/service/*.go` — `corevalidate.Required`/явные проверки ДО `operations.New`.

### REQ-VAL-02 — malformed body / типы полей                                     [P1]
Malformed JSON → `400`. Неверный тип поля (`description`=число, `labels`=строка, `name`=null) → `400 InvalidArgument`.
Пустой body → `400`. Unknown поле в body — silent-ignore (200) ИЛИ `400` (документировать выбор).
- Validated-by: `*-CR-VAL-MALFORMED-JSON`, `*-CR-VAL-DESC-INT-TYPE`, `*-CR-VAL-LABELS-STRING-TYPE`, `*-CR-VAL-NAME-NULL`, `*-CR-VAL-EMPTY-BODY`, `*-CR-VAL-EXTRA-FIELDS`
- Agent-check: grpc-gateway transcoding (api-gateway) + handler-слой; protobuf JSON-unmarshal поведение.

### REQ-VAL-03 — description ≤ 256, labels ≤ 64 пар, label-key regex             [P1]
`description` len ≤ 256 (257 → `InvalidArgument`); ≤ 64 пар `labels` (65 → `400`); ключ `labels`
по regex (lowercase, без спец-символов, не UPPERCASE) — нарушение → `400`.
- Validated-by: `*-CR-BVA-DESC-MAX-256`/`-OVER-257`, `*-CR-BVA-LABELS-MAX-64`/`-OVER-65`, `*-CR-VAL-LABELS-INVALID-KEY-CHAR`, `*-CR-VAL-LABELS-UPPERCASE-KEY`
- Agent-check: `corevalidate.Description`/`corevalidate.Labels` в `internal/service/validate.go` + вызовы.

### REQ-VAL-04 — DhcpOptions / static_routes валидация                          [P1]
Subnet `dhcp_options`: `domain_name` по RFC 1123 (invalid → `400`); `domain_name_servers[]`/`ntp_servers[]` — валидные IP (invalid → `400`).
RouteTable `static_routes[]`: непустой `destination_prefix` (валидный CIDR) и `next_hop_address` (валидный IP) — иначе `400`.
- Validated-by: `*-CR-VAL-DHCP-DOMAIN-INVALID`/`-OK`, `*-CR-VAL-DHCP-NS-INVALID-IP`/`-OK`, `*-CR-VAL-DHCP-NTP-INVALID-IP`/`-OK`, `*-CR-VAL-ROUTE-EMPTY-HOP`/`-EMPTY-PREFIX`/`-INVALID-HOP`/`-INVALID-PREFIX`/`-OK`
- Agent-check: `internal/service/subnet.go` (DhcpOptions), `internal/service/route_table.go` (static_routes) — sync-валидация.

---

## C. Имена ресурсов (verbatim-YC name policy)

### REQ-NAME-01 — NameVPC permissive для Network/Subnet/Address/RouteTable/SecurityGroup [P1]
`name` этих ресурсов — **необязателен** и валидируется permissive-regex `^([a-zA-Z]([-_a-zA-Z0-9]{0,61}[a-zA-Z0-9])?)?$`
(пустое / UPPERCASE / underscore — разрешены). НЕ возвращать `"name is required"`.
- Validated-by: `*-CR-BVA-NAME-EMPTY`, `*-CR-VAL-NAME-UPPERCASE`, `*-CR-BVA-NAME-MAX-63`
- Agent-check: `corevalidate.NameVPC` + вызовы в `internal/service/{network,subnet,address,route_table,security_group}.go`.

### REQ-NAME-02 — Gateway: strict NameGateway                                    [P1]
`Gateway.name` — strict: `^[a-z]([-a-z0-9]{0,61}[a-z0-9])?$` (lowercase, без uppercase/underscore).
- Validated-by: `GW-CR-VAL-NAME-*` (см. паттерны `*-CR-VAL-NAME-DIGIT-START`/`-HYPHEN-START`/`-SPECIAL-CHARS`/`-UPPERCASE` на app `gat`)
- Agent-check: `corevalidate.NameGateway` в `internal/service/gateway.go`.

### REQ-NAME-03 — name boundary & format                                         [P1]
`name` len > 63 → `InvalidArgument`. Начинается с цифры/дефиса, содержит спец-символы → `400`
(для strict-ресурсов; для permissive — UPPERCASE/underscore допустимы, остальное по regex).
- Validated-by: `*-CR-BVA-NAME-OVER-64`, `*-CR-VAL-NAME-DIGIT-START`, `*-CR-VAL-NAME-HYPHEN-START`, `*-CR-VAL-NAME-SPECIAL-CHARS`
- Agent-check: regex'ы в `kacho-corelib/validate/validate.go`.

### REQ-NAME-04 — UNIQUE (folder_id, name) — все 7 ресурсов                      [P1]
В пределах folder не может быть двух ресурсов одного типа с одинаковым непустым `name` →
async `ALREADY_EXISTS`. Пустое `name` от уникальности освобождено (partial UNIQUE `WHERE name <> ''`,
кроме Network — там non-partial).
- Validated-by: `*-CR-NEG-DUP-NAME`, `*-CR-NEG-DUP-NAME-CHECK`
- Agent-check: `internal/migrations/0001_initial.sql` (`networks_folder_id_name_key`) + `0002_resource_name_unique.sql`; `mapRepoErr` (`23505` → `ErrAlreadyExists`).

---

## D. CIDR / Subnet semantics

### REQ-CIDR-01 — host-bits = 0                                                  [P0]
CIDR с host-bits ≠ 0 (`10.0.0.5/24`) → sync `InvalidArgument`. Касается Create.v4/v6_cidr_blocks и AddCidrBlocks.
- Validated-by: `*-CR-VAL-CIDR-HOSTBITS`, `*-ACB-VAL-HOST-BITS`
- Agent-check: `validateCIDRPrefix` (`netip.Prefix.Masked() == prefix`) в `internal/service/validate.go`.

### REQ-CIDR-02 — overlap внутри Network запрещён (race-free)                    [P0]
Два Subnet с пересекающимися CIDR в одной Network → второй `FAILED_PRECONDITION "Subnet CIDRs can not overlap"`.
Защита atomic — DB EXCLUDE constraint (`23P01` → `FailedPrecondition`).
- Validated-by: `*-CR-NEG-CIDR-OVERLAP`, `*-ACB-NEG-OVERLAP`, `*-ACB-NEG-OVERLAP-SELF`
- Agent-check: `internal/migrations/0001_initial.sql` — `subnets_no_overlap_v4`/`v6` EXCLUDE GIST; `mapRepoErr` `23P01`.

### REQ-CIDR-03 — CIDR внутри одного запроса не пересекаются                      [P1]
В Create или AddCidrBlocks несколько CIDR в одном запросе не должны пересекаться между собой → `InvalidArgument`.
- Validated-by: `*-ACB-STATE-DISJOINT-CIDRS`
- Agent-check: `checkCIDRDisjoint` в `internal/service/subnet.go`.

### REQ-CIDR-04 — AddCidrBlocks                                                  [P1]
`AddCidrBlocks` добавляет 1+ CIDR; новые блоки видны в `Get`; пересечение с existing → `InvalidArgument`/`FailedPrecondition`.
- Validated-by: `*-ACB-CRUD-OK`, `*-ACB-CRUD-ADD-ONE`, `*-ACB-CRUD-ADD-MULTIPLE`
- Agent-check: `internal/service/subnet.go` AddCidrBlocks.

### REQ-CIDR-05 — RemoveCidrBlocks: нельзя удалить primary/last                  [P0]
`RemoveCidrBlocks` для primary (первого) v4-CIDR → отказ (`FailedPrecondition "cannot remove last CIDR block from subnet"`).
CIDR не из списка → `InvalidArgument`/`FailedPrecondition` (документировать). Add+Remove roundtrip — state invariant.
- Validated-by: `*-RCB-NEG-CANNOT-REMOVE-PRIMARY`, `*-RCB-NEG-NF`, `*-RCB-NEG-NOT-PRESENT`, `*-RCB-CRUD-OK`, `*-RCB-CRUD-REMOVE-ONE`, `*-RCB-CONF-STATE`, `*-ACB-RCB-ROUNDTRIP`
- Agent-check: `internal/service/subnet.go` RemoveCidrBlocks.

### REQ-CIDR-06 — Relocate Subnet: запрещено при наличии Address                 [P1]
`Relocate` Subnet, у которого есть internal Address-ресурсы → `FailedPrecondition "Invalid subnet state"` (verbatim YC).
Без адресов → успех, `zone_id` обновляется. Без `destinationZoneId` → `InvalidArgument`.
- Validated-by: `*-REL-NEG-IN-USE`, `*-REL-STATE-NO-ADDRESSES-OK`, `*-REL-VAL-NO-DEST`
- Agent-check: `internal/service/subnet.go` Relocate (`repo.AddressesBySubnet` check).

---

## E. IPAM / Address allocation

### REQ-IPAM-01 — Address spec oneof: ровно один из external/internal           [P0]
`Address.Create` ДОЛЖЕН требовать ровно один из `external_ipv4_address_spec` / `internal_ipv4_address_spec`.
Оба → `InvalidArgument`; ни одного → `InvalidArgument`. Internal-spec с `subnet_id` + external-spec одновременно → `400 oneof`.
- Validated-by: `*-CR-VAL-BOTH-SPEC`, `*-CR-VAL-SPEC-ONEOF`, `*-CR-VAL-EXT-WITH-SUBNET-FK`, `*-CR-CRUD-EXT`, `*-CR-CRUD-INT`
- Agent-check: `internal/service/address.go` — oneof-валидация sync.

### REQ-IPAM-02 — external Address → IP из резолвленного pool; internal → IP в subnet [P1]
`Create` external Address (с `zone_id`) → IP выделяется из pool по cascade-резолву (см. `docs/architecture/03-ipam.md`).
`Create` internal Address → IP в пределах `v4_cidr_blocks` указанного Subnet; explicit IP вне CIDR → `InvalidArgument`.
- Validated-by: `*-CR-CRUD-EXT`, `*-CR-CRUD-INT`, `*-CR-VAL-RESERVED-USED-OK`
- Agent-check: `internal/service/address.go` doCreate (inline allocate, cascade); `internal/service/address_pool_service.go`.

### REQ-IPAM-03 — аллокатор race-free                                           [P0]
Параллельные `AllocateExternalIP` / параллельные internal-allocate ДОЛЖНЫ выдавать уникальные IP
(UNIQUE constraint `addresses_external_pool_ip_uniq` + retry на violation).
- Validated-by: **gap — нет concurrency-кейса** (см. `REQUIREMENTS.md` REQ-007 / backlog); инвариант проверяется integration-тестом `ipam_cascade_integration_test.go` (частично).
- Agent-check: `internal/service/address.go` — двухфазный аллокатор + UNIQUE-retry; миграция `addresses_external_pool_ip_uniq`.

### REQ-IPAM-04 — Address.GetByValue: невалидный IP → 400, отсутствующий → 404   [P1]
`GetByValue` с не-IP значением → `InvalidArgument "Cannot parse address: <X>"` (verbatim YC, probe 2026-05-11).
Отсутствующий IP → `NOT_FOUND` (см. REQ-AUTHZ-04).
- Validated-by: `*-GBV-VAL-INVALID-IP`, `*-GBV-NEG-NF`, `*-GBV-CRUD-OK`
- Agent-check: `internal/service/address.go` GetByValue — `netip.ParseAddr` sync, затем `repo.GetByValue`.

---

## F. UpdateMask / immutability

### REQ-UPD-01 — empty mask → full-PATCH                                         [P1]
`Update` с пустым `update_mask` → применяются все mutable-поля из тела; immutable-поля из тела
**silently игнорируются** (verbatim YC).
- Validated-by: `*-UPD-VAL-MASK-EMPTY`, `*-UPD-CRUD-DESC`/`-DESCRIPTION`/`-LABELS`/`-NAME`/`-MULTI-MASK`
- Agent-check: `internal/service/*.go` Update — ветка `len(mask)==0`.

### REQ-UPD-02 — unknown поле в mask → InvalidArgument                           [P1]
`Update` с полем в `update_mask`, которого нет в known-set ресурса → `InvalidArgument`. Несколько unknown → `400`.
- Validated-by: `*-UPD-VAL-UNKNOWN-MASK`, `*-UPD-VAL-MASK-MULTIPLE-UNKNOWN`
- Agent-check: `corevalidate.UpdateMask(known-set)` в `internal/service/*.go`.

### REQ-UPD-03 — immutable поле в mask → InvalidArgument (verbatim text)         [P1]
`Update` с immutable-полем в `update_mask` → `InvalidArgument "<field> is immutable after <Resource>.Create"`.
Immutable по ресурсам: **все** — `folder_id`; **Subnet** — `v4_cidr_blocks`,`v6_cidr_blocks`,`network_id`,`zone_id`;
**Address** — `external_ipv4_address_spec`,`internal_ipv4_address_spec`; **PrivateEndpoint** — `network_id`,`subnet_id`,`service_type`,`address_id`;
**RouteTable/SecurityGroup** — `network_id`.
- Validated-by: `*-UPD-STATE-IMMUTABLE-FOLDER`/`-FOLDER-ID`, `*-UPD-STATE-IMMUTABLE-CIDR`/`-V4-CIDR-BLOCKS`/`-V6-CIDR-BLOCKS`/`-NETWORK-ID`/`-ZONE-ID`, `*-UPD-STATE-IMMUTABLE-EXTERNAL-IPV4-ADDRESS-SPEC`/`-INTERNAL-IPV4-ADDRESS-SPEC`, `*-UPD-STATE-IMMUTABLE-SUBNET-ID`/`-SERVICE-TYPE`/`-ADDRESS-ID`
- Agent-check: начало `Update` в `internal/service/*.go` — `switch field { case <immutable>: return invalidArg(...) }`; список immutable в `CLAUDE.md` §4.4 / `06-conventions.md`.

### REQ-UPD-04 — mask=<single mutable> → меняется только это поле                [P2]
`Update` с `update_mask=name` (или одно mutable-поле) → меняется только оно; description/labels не трогаются.
- Validated-by: `*-UPD-VAL-MASK-NAME-ONLY`, `*-UPD-CRUD-MULTI-MASK`
- Agent-check: `internal/service/*.go` Update — применение по mask.

---

## G. List / pagination / filter

### REQ-LIST-01 — folder_id required в List                                      [P0]
`List<Resource>s` без `folder_id` → `InvalidArgument`.
- Validated-by: `*-LST-VAL-FOLDER-REQUIRED`
- Agent-check: handler/service `List` — required-проверка.

### REQ-LIST-02 — page_size bounds                                               [P2]
`page_size`: 0 → default (50); 1..1000 — ok; 1001 / >1000 / отрицательный → `InvalidArgument "page_size must be in [0..1000]"`.
Boundary 1000 → ok; 1001 → `400`.
- Validated-by: `*-LST-BVA-PAGESIZE-ZERO`/`-1`/`-OVER-MAX`, `*-LST-PAGESIZE-EXACTLY-1000`/`-1001`, `*-LST-PAGE-NEGATIVE-SIZE`, `*-LST-PAGE-OVER`
- Agent-check: `corevalidate.PageSize` + вызовы.

### REQ-LIST-03 — page_size contract: ответ не превышает page_size              [P2]
`List` с `page_size=N` → в ответе ≤ N элементов; есть ещё → непустой `next_page_token`.
- Validated-by: `*-LST-CONTRACT-NEVER-EXCEEDS-PAGESIZE`, `*-LST-BVA-PAGESIZE-1`
- Agent-check: `internal/repo/*.go` `List` — `LIMIT page_size+1` (cursor-pagination).

### REQ-LIST-04 — page_token roundtrip; garbage token → InvalidArgument         [P1]
`next_page_token` из ответа подаётся в следующий `List` → продолжение без пропусков/дублей.
Невалидный (не-decodable base64 / garbage) `page_token` → `InvalidArgument`; НЕ silent-fallback на page 1.
- Validated-by: `*-LST-PAGE-ROUNDTRIP`, `*-LST-ROUNDTRIP`, `*-LST-PAGE-TOKEN-GARBAGE`
- Agent-check: `internal/repo/*.go` decode page_token (base64 `{created_at,id}`); ошибка декода → `ErrInvalidArg`.

### REQ-LIST-05 — filter: только whitelisted поля, YC-syntax                     [P1]
`filter` (опционален): поддерживается `name="<value>"` (текущая фаза). Filter на не-whitelisted поле →
`InvalidArgument`. Garbage filter syntax → `InvalidArgument`. Пустой filter → ok (опционален). SQLi в filter → НЕ 500.
- Validated-by: `*-LST-FILTER-NAME-OK`/`-MATCH`/`-EMPTY`, `*-LST-FILTER-GARBAGE`, `*-LST-FILTER-UNKNOWN-FIELD`, `*-LST-SEC-FILTER-SQLI`, `*-LST-FILTER-CASE-SENSITIVITY`, `*-LST-FILTER-SPECIAL-CHARS`
- Agent-check: `kacho-corelib/filter.Parse(whitelist)` + вызовы; параметризация в `internal/repo/*.go` (никакой строковой конкатенации в SQL).

### REQ-LIST-06 — child-list RPC: parent NotFound → 404                          [P1]
`Network.ListSubnets`/`ListSecurityGroups`/`ListRouteTables`, `Subnet.ListUsedAddresses`, `Address.ListBySubnet`,
`<Resource>.ListOperations` для несуществующего parent → `NOT_FOUND` (для ListBySubnet/ListUsedAddresses/ListOperations
допускается `404` ИЛИ пустой `200` — документировать).
- Validated-by: `*-LSUB-NEG-PARENT-NF`, `*-LSG-NEG-PARENT-NF`, `*-LRT-NEG-PARENT-NF`, `*-LUA-NEG-PARENT-NF`, `*-LBS-NEG-PARENT-NF`, `*-LOP-NEG-PARENT-NF`
- Agent-check: `internal/service/*.go` child-list — parent-existence check.

### REQ-LIST-07 — ListSecurityGroups содержит default SG (при inline-режиме)     [P1]
При `KACHO_VPC_DEFAULT_SG_INLINE=true` (default) после `Network.Create` → `ListSecurityGroups` возвращает auto-созданный default SG `default-sg-<8>`.
- Validated-by: `*-LSG-CRUD-DEFAULT-SG`
- Agent-check: `internal/service/network.go` doCreate (inline default-SG) + `SetSGRepo` в `cmd/vpc/main.go`.

---

## H. Удаление / FK-constraints

### REQ-DEL-01 — Delete несуществующего → sync 404 (verbatim text)              [P1]
`Delete` несуществующего ресурса → sync `NOT_FOUND "<Resource> <id> not found"` (не Operation).
- Validated-by: `*-DEL-AUTHZ-NF-SYNC`, `*-DEL-CONF-NF-TEXT`, `*-DEL-CONF-FULLTEXT`, `*-DEL-NEG-NF-INVALID-PREFIX`
- Agent-check: `internal/service/*.go` Delete — `repo.Get` + `AssertFolderOwnership` ДО Operation. (id-syntax → InvalidArgument: см. REQ-YC-04 / `kacho-vpc#7`.)

### REQ-DEL-02 — Network: нельзя удалить с детьми (FK RESTRICT)                  [P0]
`Delete` Network, у которой есть Subnet / RouteTable / не-default SecurityGroup → `FailedPrecondition "network is not empty"` (FK RESTRICT).
- Validated-by: `*-DEL-NEG-HAS-SUBNETS`, `*-DEL-NEG-HAS-ROUTE-TABLE`, `*-DEL-NEG-HAS-NONDEFAULT-SG`
- Agent-check: миграция — FK `ON DELETE RESTRICT` от children к networks; `mapRepoErr` `23503` → `ErrFailedPrecondition`.

### REQ-DEL-03 — Subnet: нельзя удалить с internal Address (FK RESTRICT)          [P0]
`Delete` Subnet с привязанным internal Address → `FailedPrecondition`.
- Validated-by: `*-DEL-NEG-HAS-ADDRESSES`
- Agent-check: миграция — FK addresses→subnets `RESTRICT`.

### REQ-DEL-04 — Network с только default-SG удаляется (auto-cleanup)            [P1]
`Delete` Network, у которой единственный child — auto-default-SG → worker сначала удаляет default-SG, потом Network → успех.
Прямой `Delete` default-SG в обход → отказ.
- Validated-by: `*-DEL-CRUD-ONLY-DEFAULT-SG`, `*-DEL-STATE-DEFAULT-SG`
- Agent-check: `internal/service/network.go` doDelete; `internal/service/security_group.go` — запрет удаления default напрямую.

### REQ-DEL-05 — deletion_protection: sync-check перед Delete                    [P1]
`Delete` ресурса с `deletion_protection=true` → sync `FailedPrecondition "... deletion_protection enabled; clear it via Update before Delete"`.
- Validated-by: **gap — нет явного кейса в текущем индексе** (документировано в gotchas; добавить `*-DEL-NEG-DELETION-PROTECTION`)
- Agent-check: `internal/service/*.go` Delete — sync-проверка `deletion_protection` ДО Operation.

---

## I. Operations service

### REQ-OPS-01 — OperationService.Get свежесозданной op → done=true с response   [P1]
После завершения worker'а `OperationService.Get(id)` → `done=true`, `response` = ресурс (для Create/Update) или Empty (Delete), либо `error` (`google.rpc.Status`).
- Validated-by: `OP-GET-CRUD-OK`
- Agent-check: `internal/handler/operation_handler.go`; `kacho-corelib/operations` worker.

### REQ-OPS-02 — OperationService.Get bad id                                     [P1]
Несуществующий op-id с правильным prefix → `NOT_FOUND "Operation <id> not found"`. Malformed / unknown-prefix id →
`InvalidArgument "invalid operation id <X>"`; well-formed id с prefix без backend → `NOT_FOUND`.
- Validated-by: `OP-GET-NEG-NF-VALID-PREFIX`, `OP-GET-NEG-NF-INVALID-PREFIX`
- Agent-check: `kacho-api-gateway/internal/opsproxy/proxy.go` (`resolveBackend`); см. `07-known-divergences.md` §2 + `kacho-api-gateway#2`.
- Divergence: исторически возвращалось `400 "unknown prefix"` для любого нероутируемого id — приводится к verbatim-YC (см. issue выше).

### REQ-OPS-03 — ListOperations<Resource>: содержит create-op                    [P1]
`<Resource>.ListOperations(<id>)` после Create → список содержит create-Operation. Несуществующий parent → `404` или пустой.
- Validated-by: `*-LOP-CRUD-OK`, `*-LOP-NEG-PARENT-NF`
- Agent-check: `internal/service/*.go` ListOperations (filter по resource_id в `operations`).

---

## J. AuthZ / tenant isolation

### REQ-AUTHZ-01 — Get/Update/Delete/Move/AddCidr/.../UpdateRules: cross-tenant → PERMISSION_DENIED [P0]
RPC, оперирующие конкретным ресурсом, ДОЛЖНЫ проверять, что `resource.folder_id` принадлежит caller'у;
чужой ресурс → `PERMISSION_DENIED` (в `dev`-mode AuthN permissive — anonymous=admin; в `production`/`production-strict` fail-closed).
- Validated-by: `*-AUTHZ-NF-SYNC` (Get/Update/Delete/Move/UpdateRule/UpdateRules), `*-AUTHZ-EMPTY-FOLDER-HEADER`; **gap** — полноценная cross-tenant matrix с двумя header-set'ами (см. `REQUIREMENTS.md` REQ-006)
- Agent-check: `internal/handler/*.go` — `AssertFolderOwnership` после `repo.Get`; `internal/handler/tenant_interceptor.go`; `internal/config/config.go` `AuthMode`.

### REQ-AUTHZ-02 — List: folder isolation                                        [P0]
Ресурс в folder A не виден в `List` по folder B.
- Validated-by: `*-LST-AUTHZ-CROSS-FOLDER-ISOLATION`, `*-LST-CRUD-OK`
- Agent-check: `internal/repo/*.go` `List` — `WHERE folder_id = $1`.

### REQ-AUTHZ-03 — мутация несуществующего ресурса → sync ошибка, не async       [P1]
`Update`/`Delete`/`Move`/`AddCidrBlocks`/... несуществующего → sync `NOT_FOUND`/`PERMISSION_DENIED` (через `repo.Get`+`AssertFolderOwnership` до Operation), а не Operation, которая потом падает. Подтверждено probe реального YC (2026-05-11).
- Validated-by: `*-UPD-AUTHZ-NF-SYNC`, `*-DEL-AUTHZ-NF-SYNC`, `*-MV-AUTHZ-NF-SYNC`, `*-UR-AUTHZ-NF-SYNC`, `*-URL-AUTHZ-NF-SYNC`, `*-UPD-CONF-NF-TEXT`, `*-DEL-CONF-NF-TEXT`, `*-MV-CONF-NF-TEXT`
- Agent-check: `internal/service/*.go` — `repo.Get` ДО `operations.New` для не-Create мутаций.

### REQ-AUTHZ-04 — GetByValue: no info-leak (404 для чужого и несуществующего)   [P0]
`Address.GetByValue` чужого (cross-tenant) Address И несуществующего IP дают **одинаковый** `NOT_FOUND` — нельзя по коду ответа пробить, какие IP выделены.
- Validated-by: `*-GBV-CONF-NOLEAK-FOR-EXISTING-OTHER`, `*-GBV-NEG-NF`
- Agent-check: `internal/service/address.go` GetByValue — cross-tenant и not-found сливаются в `ErrNotFound`.

---

## K. Security probes (resilience)

### REQ-SEC-01 — injection-payloads в полях не вызывают 5xx                       [P0]
`name`/`description`/`labels`/`filter` с SQLi / XSS / cmd-injection / path-traversal / null-byte / union / long-payload →
обработано (`InvalidArgument`/`200`), **никогда** `500`/`Internal` с утечкой стектрейса/SQLSTATE.
- Validated-by: `*-CR-SEC-SQLI`/`-XSS`/`-CMD`/`-PATH`/`-NULLBYTE`/`-UNION`/`-LONGPAYLOAD`, `*-LST-SEC-FILTER-SQLI`
- Agent-check: параметризованные запросы (pgx) во всех `internal/repo/*.go`; `mapRepoErr` — generic `"internal database error"`, без сырого pgx-текста; то же для Internal handlers (`internalMapErr`).

### REQ-SEC-02 — HTTP-метод/Content-Type robustness                              [P3]
`PUT`/`HEAD`/`DELETE` на List-endpoint → `405` или `404` (не 500). `POST` без `Content-Type` → `400`/`415`/`200` (lenient).
- Validated-by: `*-METHOD-PUT-NOT-ALLOWED`, `*-METHOD-DELETE-LIST`, `*-METHOD-NOT-ALLOWED`, `*-HEADERS-MISSING-CT`
- Agent-check: api-gateway routing (grpc-gateway mux).

---

## L. SecurityGroup rules

### REQ-SG-01 — UpdateRules / UpdateRule: модификация правил                      [P1]
`UpdateRules` (batch) и `UpdateRule` (single) добавляют/меняют правила; результат виден в `Get`.
`UpdateRule` несуществующего `rule_id` → `NOT_FOUND`.
- Validated-by: `*-URL-CRUD-OK`, `*-UR-CRUD-OK`, `*-UR-NEG-RULE-NF`
- Agent-check: `internal/service/security_group.go` UpdateRules/UpdateRule.

### REQ-SG-02 — rule-field валидация                                             [P1]
Правило: `direction` ∈ {INGRESS,EGRESS} (иначе `400`); `protocol` — известный (иначе `400`); порт ∈ [-1..65535]
(`-1` = any; отрицательный кроме `-1` или > 65535 → `400`).
- Validated-by: `*-URL-VAL-DIRECTION-UNKNOWN`, `*-URL-VAL-PROTOCOL-UNKNOWN`, `*-URL-VAL-PORT-ANY-MINUS-1`, `*-URL-VAL-PORT-NEG`, `*-URL-VAL-PORT-OVER-65535`
- Agent-check: `internal/service/security_group.go` — rule-валидация; `sgDirection`/`sgStatus` в `internal/protoconv/protoconv.go`.

### REQ-SG-03 — optimistic concurrency для UpdateRules (xmin)                     [P1]
Конкурентные `UpdateRules` на одну SG не теряют изменения — read-modify-write через Postgres `xmin::text` (lost-update protection).
- Validated-by: **gap — нет concurrency-кейса в newman**; покрыто integration-тестом `security_group_occ_integration_test.go`
- Agent-check: `internal/repo/security_group_repo.go` — `SELECT ..., xmin::text` / `UPDATE ... AND xmin::text = $`.

---

## M. Verbatim-YC conformance (тексты, коды, форматы)

### REQ-YC-01 — verbatim тексты ошибок                                           [P1]
Тексты в `google.rpc.Status.message` ДОЛЖНЫ дословно совпадать с reference YC: `"Folder with id <X> not found"`,
`"Network <X> not found"`, `"Subnet CIDRs can not overlap"`, `"Invalid subnet state"`, `"<field> is immutable after <Resource>.Create"`,
`"<field> is required"`, `"page_size must be in [0..1000]"`, и т.д. (полный список — `vpc-yc-parity-auditor.md` §3.1).
- Validated-by: `*-CR-CONF-FOLDER-NF-TEXT`/`-NET-NF-TEXT`/`-SUB-NF-TEXT`, `*-GET-CONF-NF-TEXT`/`-FULLTEXT`, `*-UPD-CONF-NF-TEXT`, `*-DEL-CONF-NF-TEXT`, `*-MV-CONF-NF-TEXT`, `*-UPD-STATE-IMMUTABLE-*` (verbatim)
- Agent-check: строки в `internal/service/*.go`; `vpc-yc-parity-auditor` агент; идеально — `--env yc` differential suite (`REQUIREMENTS.md` REQ-008).

### REQ-YC-02 — created_at truncate до секунд                                     [P1]
Все `created_at` в proto-ответах — `timestamppb.New(t.Truncate(time.Second))`; микросекунды не уходят клиенту.
- Validated-by: косвенно `*-CR-CRUD-OK`/`*-GET-CRUD-OK` (если кейс ассертит формат); явный кейс — желательно добавить
- Agent-check: `internal/protoconv/protoconv.go` — `ts(t)` хелпер во всех конвертерах; unit-тест `protoconv_test.go::TestCreatedAt_TruncatedToSeconds`.

### REQ-YC-03 — status-code mapping                                              [P0]
Маппинг ошибок → gRPC-коды по таблице (`06-conventions.md` / `vpc-yc-parity-auditor.md` §3.3):
NotFound→`NOT_FOUND`, AlreadyExists→`ALREADY_EXISTS`, CIDR overlap/FK/relocate-blocked/deletion_protection→`FAILED_PRECONDITION`,
поля/mask/page_size→`INVALID_ARGUMENT`, folder-check-unavailable→`UNAVAILABLE`, repo-error→`INTERNAL` (generic, без leak).
- Validated-by: все `*-NEG-*`/`*-VAL-*` кейсы (ассертят grpc-код)
- Agent-check: `internal/service/network.go::mapRepoErr` + `internal/handler/internal_maperr.go`.

### REQ-YC-04 — id-syntax sync-валидация (target — расхождение)                  [P1]
Реальный YC: malformed / wrong-prefix resource-id → sync `InvalidArgument "invalid <res> id <X>"`; well-formed-но-несуществующий → `NotFound`.
Текущий код возвращает `NotFound` на любой bad id (не валидирует синтаксис sync).
- Validated-by: `*-GET-NEG-NF`, `*-GET-NEG-NOT-FOUND`, `*-UPD-NEG-NF-INVALID-PREFIX`, `*-DEL-NEG-NF-INVALID-PREFIX` (ассертят текущее поведение; при закрытии #7 — переделать на `InvalidArgument` для malformed)
- Agent-check: `kacho-vpc#7`; `06-conventions.md` gotcha #1; `07-known-divergences.md` (секция «вынесено в issues»).
- Divergence: да — `kacho-vpc#7` (не баг, осознанная стейл-конвенция, target = sync id-validation).

### REQ-YC-05 — REST-пути дословно как YC (не нормализовать)                      [P2]
REST-пути (`google.api.http` в `kacho-proto`): kebab у custom-методов (`:add-cidr-blocks`,`:move`), snake у child-list
(`security_groups`,`route_tables`), camel у top-level, `/operations/{id}` без `/vpc/v1/`, PE на `/endpoints`. НЕ «причёсывать» — это калька с YC.
- Validated-by: косвенно — все REST-кейсы используют эти пути; явный — `04-api-surface.md`
- Agent-check: `google.api.http`-аннотации в `kacho-proto/.../<res>_service.proto`; `07-known-divergences.md` §1.
- Divergence: видимая «неоднородность» — by-design (verbatim-YC).

---

## N. Move semantics

### REQ-MOVE-01 — Move в другой folder обновляет folder_id                        [P1]
`Move(destination_folder_id)` существующего ресурса → `folder_id` обновлён; ресурс виден в `List` нового folder.
- Validated-by: `*-MV-CRUD-OK`
- Agent-check: `internal/service/*.go` doMove.

### REQ-MOVE-02 — Move: destination NotFound / отсутствует                        [P1]
`Move` в несуществующий folder → async `NOT_FOUND`. `Move` без `destination_folder_id` → sync `InvalidArgument`.
- Validated-by: `*-MV-NEG-DEST-FOLDER-NF`, `*-MV-VAL-NO-DEST`
- Agent-check: `internal/service/*.go` Move — sync required-check + worker folder-exists.

---

## Покрытие регламента (gaps)

| REQ | Статус кейс-покрытия | Тикет |
|---|---|---|
| REQ-IPAM-03 (allocator race-free) | gap в newman (есть integration `ipam_cascade_integration_test.go`) | `REQUIREMENTS.md` REQ-007 (backlog) |
| REQ-SG-03 (xmin OCC) | gap в newman (есть integration `security_group_occ_integration_test.go`) | — |
| REQ-AUTHZ-01 (полная cross-tenant matrix) | частично (`*-AUTHZ-NF-SYNC`, нет two-header-set прогона) | `REQUIREMENTS.md` REQ-006 (backlog) |
| REQ-DEL-05 (deletion_protection кейс) | gap — нет явного кейса в индексе | добавить `*-DEL-NEG-DELETION-PROTECTION` |
| REQ-YC-01 (verbatim тексты byte-level) | частично (CONF-кейсы есть; нет differential `--env yc`) | `REQUIREMENTS.md` REQ-008 (backlog) |
| REQ-YC-02 (created_at явный кейс) | косвенно (нет отдельного assert-кейса формата) | добавить `*-CR-CONF-CREATED-AT-SECONDS` |

---

## Связанные документы

- `CASES-INDEX.md` — каталог тест-паттернов (источник, из которого выведены REQ).
- `TAXONOMY.md` — классы кейсов + «Применение по методам» (обязательные классы по RPC).
- `TEST-PLAN.md` — карта `(RPC × класс) → статус покрытия`.
- `REQUIREMENTS.md` — бэклог *улучшений* (не нормативный).
- `docs/architecture/07-known-divergences.md` — намеренные расхождения с YC (исключения из регламента).
- `.claude/agents/vpc-yc-parity-auditor.md` — агент, проверяющий соответствие (потребитель этого регламента).
- `.claude/agents/qa-test-engineer.md` — QA-агент (владелец регламента; добавляет REQ).
