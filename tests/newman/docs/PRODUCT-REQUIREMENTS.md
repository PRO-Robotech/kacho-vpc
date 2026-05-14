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

### REQ-RES-06 — Move в текущий folder → InvalidArgument                          [P2]
`Move` ресурса в его же `folder_id` → sync `InvalidArgument "Illegal argument Destination folder
is the same as the source"` (verbatim YC, probe 2026-05-11; kacho-vpc#10). Ресурс не меняется.
- Validated-by: `*-MV-IDM-SAME-FOLDER`
- Agent-check: `internal/service/*.go` Move → `checkMoveDestination` в `internal/service/validate.go`; порядок sync-проверок: формат id → id required → destination required → `repo.Get` (NotFound) → same-folder/dest-exists.

### REQ-RES-07 — SecurityGroup: `network_id` опционален                          [P1]
`SecurityGroup` — folder-scoped; `network_id` в `Create` НЕ обязателен — допустима SG без сети
(привязка к Network — отдельная опциональная ассоциация). С `network_id` (валидным) → SG привязана;
`List` SecurityGroups поддерживает фильтр по `network_id` (возвращает только SG этой сети, не возвращает
SG без сети / другой сети).
- Validated-by: `SG-CR-NO-NETWORK-OK`, `SG-CR-WITH-NETWORK-OK`, `SG-LIST-FILTER-NETWORK-OK`
- Agent-check: `kacho-proto/.../security_group_service.proto` (`network_id` не required); `internal/service/security_group.go` doCreate (network-check только при заданном `network_id`); `internal/repo/security_group_repo.go` List (фильтр `network_id`).

### REQ-RES-08 — Network: `vpn_id` НЕ на публичной поверхности                   [P0]
Публичная проекция `Network` (`NetworkService.Get`/`List`) НЕ содержит `vpn_id` (24-bit data-plane-id —
инфра-чувствительное; см. workspace `CLAUDE.md` §«Инфра-чувствительные данные»). `vpn_id` виден только
через `InternalNetworkService.GetNetwork` (internal-port 9091) / internal-проекцию.
- Validated-by: `NET-GET-NO-VPNID-OK`
- Agent-check: `kacho-proto/.../network_service.proto` (public `Network` без `vpn_id`; `vpn_id` — в `InternalNetwork`); `internal/handler/network_handler.go` (публичный mapper не выставляет `vpn_id`); `internal/handler/internal_network_handler.go`.

---

## B. Валидация полей (sync, до создания Operation)

### REQ-VAL-01 — required-поля в Create                                          [P0]
Отсутствие required-поля → sync `InvalidArgument "<field> is required"`. Required: `folder_id`
(все ресурсы); `network_id` (Subnet/RouteTable/SecurityGroup/PrivateEndpoint); `zone_id` (Subnet);
`v4_cidr_blocks` (Subnet, ≥1); gateway-type oneof (Gateway); service-spec (PrivateEndpoint — `objectStorage`).
- Validated-by: `*-CR-VAL-REQ-FOLDERID`/`-NETWORKID`/`-ZONEID`/`-V4CIDRBLOCKS`, `*-CR-VAL-FOLDER-REQUIRED`, `*-CR-VAL-NETWORK-REQUIRED`, `*-CR-VAL-ZONE-REQUIRED`, `*-CR-VAL-CIDR-REQUIRED`, `*-CR-VAL-MISSING-TYPE`, `*-CR-VAL-SERVICE-MISSING`
- Agent-check: начало `Create` в `internal/service/*.go` — `corevalidate.Required`/явные проверки ДО `operations.New`.

### REQ-VAL-02 — malformed body / типы полей                                     [P1]
Malformed JSON → `400`. Неверный тип поля (`description`=число, `labels`=строка, `name`=null) → `400`.
Пустой body → `400`. Unknown поле в body — silent-ignore (200) ИЛИ `400` (документировать выбор).
Тело ответа на JSON-transcoding-ошибку: verbatim YC отдаёт plain-text, наш api-gateway — JSON
`{code,message}` (поведение runtime-библиотеки grpc-gateway; известное расхождение,
`07-known-divergences.md` §4) → кейсы `*-CR-VAL-DESC-INT-TYPE`/`-LABELS-STRING-TYPE`/`ADR-CR-VAL-BOTH-SPEC` defensive (`400` + непустое тело).
- Validated-by: `*-CR-VAL-MALFORMED-JSON`, `*-CR-VAL-DESC-INT-TYPE`, `*-CR-VAL-LABELS-STRING-TYPE`, `*-CR-VAL-NAME-NULL`, `*-CR-VAL-EMPTY-BODY`, `*-CR-VAL-EXTRA-FIELDS`, `ADR-CR-VAL-BOTH-SPEC`
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

### REQ-CIDR-06 — Relocate Subnet: всегда запрещён                               [P1]
`Relocate` Subnet → **всегда** sync `FailedPrecondition "Invalid subnet state"` (verbatim YC,
probe 2026-05-11; kacho-vpc#10) — даже для свежей подсети без адресов и валидной целевой зоны;
Operation не создаётся. Без `destinationZoneId` → sync `InvalidArgument`. Несуществующая подсеть → `NotFound`.
- Validated-by: `*-REL-NEG-IN-USE`, `*-REL-STATE-NO-ADDRESSES-OK`, `*-REL-VAL-NO-DEST`
- Agent-check: `internal/service/subnet.go` Relocate — после format-check id, валидации `destination_zone_id`, `repo.Get` → `return status.Error(codes.FailedPrecondition, "Invalid subnet state")`.

### REQ-CIDR-07 — Subnet IPv4-префикс ≤ /28                                      [P2]
Subnet с IPv4 CIDR-префиксом длиннее `/28` (`/29`, `/30`, `/31`, `/32`) → sync
`InvalidArgument "Illegal argument Invalid network prefix /N"` (verbatim YC, probe 2026-05-11;
kacho-vpc#10). Касается Create.v4_cidr_blocks и AddCidrBlocks. `/28` — допустимо.
- Validated-by: `SUB-CR-BVA-CIDR-28`, `SUB-CR-BVA-CIDR-29`, `SUB-CR-BVA-CIDR-30`, `SUB-CR-BVA-CIDR-31`
- Agent-check: `validateSubnetV4CIDR` в `internal/service/validate.go` (`prefix.Addr().Is4() && prefix.Bits() > 28`).

### REQ-CIDR-08 — Subnet: IPv4 CIDR опционален (CIDR-less subnet)                [P1]
`Subnet.Create` БЕЗ `v4_cidr_blocks` → 200, создаётся CIDR-less (или v6-only) подсеть.
`Address.Create` с internal-spec в подсеть без IPv4 CIDR → `FailedPrecondition`/`InvalidArgument`
(`"subnet <id> has no IPv4 CIDR"` — некуда аллоцировать v4-IP).
- Validated-by: `SUB-CR-NO-CIDR-OK`, `SUB-CR-NEG-ADDR-INTO-CIDRLESS`
- Agent-check: `internal/service/subnet.go` Create (`v4_cidr_blocks` не required); `internal/service/address.go` doCreate (guard «no IPv4 CIDR» перед allocate; kacho-proto#8).

### REQ-CIDR-09 — Subnet: IPv6 CIDR (dual-stack / v6-only)                       [P1]
`Subnet.Create` с `v6_cidr_blocks` → 200, `v6_cidr_blocks` виден в GET; допустимы dual-stack
(v4+v6) и v6-only подсети. v6-CIDR с host-bits → `InvalidArgument` (как v4).
- Validated-by: `SUB-CR-V6-OK`
- Agent-check: `internal/service/subnet.go` Create (валидация `v6_cidr_blocks`, host-bits); миграция `subnets.v6_cidr_*` + EXCLUDE `subnets_no_overlap_v6`.

### REQ-CIDR-10 — Subnet: v6-CIDR изменяется через AddCidrBlocks/RemoveCidrBlocks [P1]
`Subnet.AddCidrBlocks` с IPv6-блоком → блок добавлен в `v6_cidr_blocks` (Subnet становится dual-stack);
`RemoveCidrBlocks` с ним → блок убран. v6-блок с host-bits в AddCidrBlocks → `InvalidArgument`.
(Прямое изменение `v6_cidr_blocks` через `Update.mask` — soft-immutable no-op, см. REQ-UPD-05.)
- Validated-by: `SUB-CIDR-ADD-V6-OK`, `SUB-CIDR-ADD-V6-NEG-HOSTBITS`, `SUB-CIDR-REMOVE-V6-OK`
- Agent-check: `internal/service/subnet.go` AddCidrBlocks/RemoveCidrBlocks (family-aware: v4→`v4_cidr_blocks`, v6→`v6_cidr_blocks`); `validateCIDRPrefix` (host-bits для обеих семей).

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

### REQ-UPD-03 — hard-immutable поле в mask → InvalidArgument (verbatim text)     [P1]
`Update` с hard-immutable-полем в `update_mask` → `InvalidArgument "<field> is immutable after <Resource>.Create"`.
Hard-immutable по ресурсам: **все** — `folder_id`; **Subnet** — `network_id`,`zone_id`;
**Address** — `external_ipv4_address_spec`,`internal_ipv4_address_spec`; **PrivateEndpoint** — `network_id`,`subnet_id`,`service_type`,`address_id`;
**RouteTable/SecurityGroup** — `network_id`.
Subnet `v4_cidr_blocks`/`v6_cidr_blocks` — **soft-immutable**: в mask → НЕ ошибка (verbatim YC
`200`; kacho-vpc#10); у нас принимается, но `repo.Update` CIDR не перезаписывает → no-op.
- Validated-by: `*-UPD-STATE-IMMUTABLE-FOLDER`/`-FOLDER-ID`, `SUB-UPD-STATE-IMMUTABLE-CIDR` (→ `200`), `*-UPD-STATE-IMMUTABLE-NETWORK-ID`/`-ZONE-ID`, `*-UPD-STATE-IMMUTABLE-EXTERNAL-IPV4-ADDRESS-SPEC`/`-INTERNAL-IPV4-ADDRESS-SPEC`, `*-UPD-STATE-IMMUTABLE-SUBNET-ID`/`-SERVICE-TYPE`/`-ADDRESS-ID`
- Agent-check: начало `Update` в `internal/service/*.go` — `switch field { case <hard-immutable>: return invalidArg(...) }`; список в `CLAUDE.md` §4.4 / `06-conventions.md`; для Subnet НЕ должно быть `v4_cidr_blocks`/`v6_cidr_blocks` в reject-switch.

### REQ-UPD-04 — mask=<single mutable> → меняется только это поле                [P2]
`Update` с `update_mask=name` (или одно mutable-поле) → меняется только оно; description/labels не трогаются.
- Validated-by: `*-UPD-VAL-MASK-NAME-ONLY`, `*-UPD-CRUD-MULTI-MASK`
- Agent-check: `internal/service/*.go` Update — применение по mask.

### REQ-UPD-05 — Subnet.Update с `v6_cidr_blocks` в mask → 200, no-op (soft-immutable) [P2]
`Subnet.Update` с `update_mask` содержащим `v6_cidr_blocks` (+ значение в body) → 200, операция
завершается без error; `repo.Update` v6-CIDR-колонки не перезаписывает (verbatim YC принимает в mask
и меняет — у нас no-op; kacho-vpc#10). Реальное изменение v6-CIDR — через `:add-cidr-blocks`/`:remove-cidr-blocks` (REQ-CIDR-10).
- Validated-by: `SUB-UPD-V6-NOOP`
- Agent-check: `internal/service/subnet.go` Update — `v6_cidr_blocks` НЕ в hard-immutable reject-switch; `internal/repo/subnet_repo.go` Update не трогает v6-колонки; `kacho-proto/.../subnet_service.proto` `UpdateSubnetRequest.v6_cidr_blocks` (kacho-proto#8).

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
- Agent-check: `internal/service/*.go` Delete — `corevalidate.ResourceID(...)` (первым стейтментом) + `repo.Get` + `AssertFolderOwnership` ДО Operation. (id-syntax → `InvalidArgument`: см. REQ-YC-04.)

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

### REQ-DEL-06 — Subnet: нельзя удалить с internal **v6** Address                 [P0]
`Delete` Subnet, в которой есть internal Address (v4 ИЛИ v6) → `FailedPrecondition`. FK
`addresses_internal_subnet_fkey` через generated-колонку `addresses.internal_subnet_id`,
выводимую из `internal_ipv4->>'subnet_id'` ИЛИ `internal_ipv6->>'subnet_id'` (миграция 0013, KAC-34);
sync-precheck `AddressesBySubnet` тоже покрывает обе семьи.
- Validated-by: `SUB-DEL-NEG-HAS-V6-ADDRESS` (+ `*-DEL-NEG-HAS-ADDRESSES` для v4)
- Agent-check: `internal/migrations/0013_address_internal_subnet_id_v6.sql`; `internal/service/subnet.go` Delete (`AddressesBySubnet` precheck); `mapRepoErr` `23503` → `ErrFailedPrecondition`.

### REQ-DEL-07 — Subnet: нельзя удалить с NetworkInterface                        [P0]
`Delete` Subnet, к которой привязан хоть один `NetworkInterface` → sync `FailedPrecondition`
со списком NIC-id (сначала удалите NIC'и). FK `network_interfaces.subnet_id` ON DELETE RESTRICT
(миграция 0012, KAC-33 — откат CASCADE из KAC-31). Порядок удаления — снизу вверх: NIC → Address → Subnet → Network.
- Validated-by: `SUB-DEL-NEG-HAS-NIC`
- Agent-check: `internal/migrations/0012_nic_subnet_restrict.sql`; `internal/service/subnet.go` Delete (sync-precheck NIC); FK RESTRICT в worker'е как backstop.

### REQ-DEL-08 — Network: транзитивно нельзя удалить (Subnet с NIC)               [P0]
`Delete` Network, у которой Subnet содержит NIC → `FailedPrecondition "network is not empty"`
(NIC блокирует Subnet, Subnet блокирует Network). Удаление возможно только после зачистки снизу вверх.
- Validated-by: `NET-DEL-NEG-HAS-SUBNET-WITH-NIC` (+ `*-DEL-NEG-HAS-SUBNETS` базовый)
- Agent-check: FK-цепочка `network_interfaces→subnets→networks` (все RESTRICT); `internal/service/network.go` doDelete.

### REQ-DEL-09 — Address: нельзя удалить, если референсится NIC                   [P0]
`Delete` Address, который указан в `v4_address_ids`/`v6_address_ids` хоть одного `NetworkInterface`
→ `FailedPrecondition` (сначала detach Address у NIC). Один Address — максимум на одном NIC
(enforced сервис-слоем через `addresses.used` + referrer-tracking; KAC-31).
- Validated-by: `ADDR-DEL-NEG-USED-BY-NIC`
- Agent-check: `internal/service/address.go` Delete — проверка referrer'ов (NIC) ДО Operation; `internal/service/network_interface.go` (referrer-tracking при Create/Attach).

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

### REQ-OPS-04 — история операций переживает удаление ресурса                     [P1]
После `Delete` ресурса его операции НЕ удаляются: `<Resource>.ListOperations(<deleted-id>)` всё ещё
возвращает историю (create + delete), `OperationService.Get(<opId>)` по операции удалённого ресурса → 200.
Таблица `operations` не имеет FK-cascade от ресурсных таблиц.
- Validated-by: `NET-LISTOPS-AFTER-DELETE-OK`, `OP-LIST-AFTER-DELETE-OK`
- Agent-check: `internal/migrations/0001_initial.sql` (`operations` без FK на ресурсы); `internal/repo/*.go` Delete (`DELETE FROM <table>` — не трогает `operations`); `kacho-corelib/operations` Repo.

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

### REQ-YC-04 — id-syntax sync-валидация                                          [P1]
Каждый id-берущий RPC первым стейтментом вызывает `corevalidate.ResourceID(resourceType, ids.PrefixXxx, id)`:
malformed / нераспознанный resource-id (нет известного 3-char prefix `b1g/bpf/enp/e9b/epd/fd8`) → sync `InvalidArgument "invalid <res> id '<X>'"`
(verbatim YC, probe 2026-05-11); well-formed-но-несуществующий (известный prefix) → `NotFound` через `repo.Get`. Семантика family-agnostic.
- Validated-by: `*-GET-NEG-NF`, `*-GET-NEG-NOT-FOUND`, `*-UPD-NEG-NF-INVALID-PREFIX`, `*-DEL-NEG-NF-INVALID-PREFIX`
- Agent-check: `corevalidate.ResourceID` вызывается первым стейтментом в `internal/service/*.go` для каждого id-берущего RPC; `06-conventions.md` gotcha #1.
- Divergence: нет — выровнено с verbatim YC (`kacho-vpc#7` закрыт).

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

### REQ-MOVE-02 — Move: destination / resource NotFound / отсутствует             [P1]
`Move` в несуществующий folder → sync `NOT_FOUND "Folder with id <X> not found"` (kacho-vpc#8).
`Move` без `destination_folder_id` → sync `InvalidArgument`. `Move` несуществующего ресурса
(well-formed id) → sync `NOT_FOUND "<Resource> ... not found"` (kacho-vpc#10 — Move делает sync
`repo.Get`). `Move` в текущий folder → см. REQ-RES-06.
- Validated-by: `*-MV-NEG-DEST-FOLDER-NF`, `*-MV-VAL-NO-DEST`, `*-MV-AUTHZ-NF-SYNC`, `*-MV-CONF-NF-TEXT`
- Agent-check: `internal/service/*.go` Move — sync `repo.Get` → `checkMoveDestination` ДО `operations.New`.

---

## O. NetworkInterface (NIC) — first-class ресурс (эпик KAC-2)

### REQ-NIC-01 — NIC — folder-scoped ресурс, принадлежит Subnet                  [P1]
`NetworkInterface` — публичный folder-scoped ресурс (`folder_id` обязателен), принадлежит `Subnet`
(`subnet_id` обязателен). Полный CRUD (`Get`/`List`/`Create`/`Update`/`Delete`) + `ListOperations`.
`Create` с garbage `subnet_id` → async `NotFound "Subnet ... not found"`. REST: `/vpc/v1/networkInterfaces`.
- Validated-by: `NIC-CR-CRUD-OK`, `NIC-CR-NEG-BAD-SUBNET`, `NIC-LIST-OK`, `NIC-DEL-OK`
- Agent-check: `kacho-proto/.../network_interface_service.proto`; `internal/service/network_interface.go`; `cmd/vpc/main.go` (регистрация).

### REQ-NIC-02 — Delete NIC освобождает референсные Address                       [P1]
`Delete` не-приаттаченного NIC → Operation → NIC исчезает; привязанные через `v4_address_ids`/`v6_address_ids`
Address освобождаются (`Address.used` → false, referrer снят).
- Validated-by: `NIC-DEL-OK`
- Agent-check: `internal/service/network_interface.go` doDelete — снятие referrer'ов / `addresses.used`.

### REQ-NIC-03 — Attach/Detach: `used_by` зеркалит привязку; приаттаченный NIC нельзя удалить [P1]
`AttachToInstance` → `used_by` = `{compute_instance, <instance_id>}`; `DetachFromInstance` → `used_by` очищен.
`Delete` NIC с непустым `used_by` (приаттачен) → `FailedPrecondition` (сначала Detach).
- Validated-by: `NIC-ATTACH-DETACH-OK`, `NIC-DEL-NEG-ATTACHED`
- Agent-check: `internal/service/network_interface.go` Attach/Detach (flat-колонки `used_by_*` на `network_interfaces`); doDelete — guard на `used_by`.

### REQ-NIC-04 — NIC ссылается на Address по id; занятый Address нельзя удалить    [P0]
`NetworkInterface` ссылается на `Address`-ресурсы по id: `v4_address_ids[]` / `v6_address_ids[]`.
Один `Address` — максимум на одном NIC (enforced сервис-слоем: `addresses.used` + referrer-tracking).
`Create` NIC с предсозданными v4/v6 internal Address → 200, address(а) привязаны. `Address.Delete`
референсимого NIC'ом адреса → `FailedPrecondition` (REQ-DEL-09).
- Validated-by: `NIC-CR-WITH-ADDR-OK`, `NIC-CR-WITH-V6-ADDR-OK`, `ADDR-DEL-NEG-USED-BY-NIC`
- Agent-check: `internal/service/network_interface.go` Create/Attach (referrer-tracking, как `address_references`); `internal/service/address.go` Delete (referrer-check).

### REQ-NIC-05 — NIC несёт `security_group_ids[]` (SG folder-scoped)              [P2]
`NetworkInterface` несёт `security_group_ids[]`; SG не обязана быть привязана к network NIC'а
(SG folder-scoped, привязка к network опциональна — REQ-RES-07). `Create` NIC с такой SG → 200.
- Validated-by: `NIC-CR-WITH-UNBOUND-SG-OK`
- Agent-check: `internal/service/network_interface.go` Create/Update — валидация существования SG (не привязки к network).

### REQ-NIC-06 — публичная проекция NIC — lean (без инфра-полей)                  [P0]
Публичная `NetworkInterface` (`Get`/`List`/`Create`-result) содержит ТОЛЬКО `id`/`folder_id`/`name`/
`labels`/`subnet_id`/`v4_address_ids`/`v6_address_ids`/`security_group_ids`/`used_by`/`mac_address`/`status`.
Инфра-чувствительные поля (`vpn_id`, `hv_id`, `sid`/`sid_seq`, `host_iface`, `netns`, `gateway_ip`,
`container_id`, `network_id`, `instance_id`, `index`) — ТОЛЬКО на `InternalNetworkInterface`
(`InternalNetworkInterfaceService`, internal-port 9091), НИКОГДА не на публичной поверхности.
- Validated-by: `NIC-LIST-OK`, `NIC-CR-CRUD-OK` (assert «no infra-sensitive fields»)
- Agent-check: `kacho-proto/.../network_interface_service.proto` (public `NetworkInterface` vs `InternalNetworkInterface`); `internal/handler/network_interface_handler.go` (public mapper не выставляет инфра-поля); workspace `CLAUDE.md` §«Инфра-чувствительные данные».

### REQ-NIC-07 — Update NIC: меняются mutable-поля, subnet_id/инфра — нет          [P1]
`Update` NIC через mask (`name`/`labels`/`security_group_ids`) → Operation → новые значения видны;
`subnet_id` — immutable (в mask → `InvalidArgument`); инфра-поля недоступны для записи через публичный API.
- Validated-by: `NIC-UPD-OK`
- Agent-check: `internal/service/network_interface.go` Update — `subnet_id` в hard-immutable reject-switch; mask-применение только к mutable.

### REQ-NIC-08 — NIC.mac_address — output-only, стабилен, cloud-wide unique         [P1]
`mac_address` на публичной `NetworkInterface` (AWS-ENI semantics): аллоцируется системой
при `NetworkInterfaceService.Create`, **клиент задать не может**, **неизменен** на протяжении
жизни NIC (Attach/Detach/Update name/labels/SG не меняют MAC), **уникален в пределах всего
облака** (DB-level UNIQUE-constraint). Формат — lowercase, colon-separated, 6 октетов;
префикс `0e:` (locally-administered, unicast) зарезервирован под Kachō — все наши MAC начинаются
с него; остальные 5 байт — `crypto/rand` (40 бит энтропии); коллизии ловятся UNIQUE-constraint'ом
и retry'ятся в service-слое (до 3 попыток).
- Validated-by: `NIC-CR-MAC-OK` (формат + стабильность при Update)
- Agent-check: `kacho-proto/.../network_interface.proto` (field 19, mac_address); `internal/migrations/0014_nic_mac_address.sql` (UNIQUE + backfill); `internal/service/mac.go` (`GenerateMAC` + префикс `0e:`); `internal/service/network_interface.go` (retry-loop в `doCreate`); `internal/repo/network_interface_repo.go` (Insert + isNICMacCollision); KAC-48.

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
