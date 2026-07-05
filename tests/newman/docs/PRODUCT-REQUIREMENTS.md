# Регламент продуктовых требований — kacho-vpc (от QA)

> **Contract-removal**: RPC `Move` (Network/Subnet/Address/RouteTable/
> SecurityGroup/Gateway), `Subnet.Relocate`, NIC `AttachToInstance`/`DetachFromInstance`,
> AddressPool `BindAsAddressOverride`/`UnbindAddressOverride`/`Check`/`ExplainResolution`
> и весь `InternalCloudService` (CloudPoolSelector) **удалены**. Связанные требования
> ниже — `REQ-RES-06`, `REQ-CIDR-06`, `REQ-RESOLVE-04`, `REQ-RESOLVE-06`,
> `REQ-SG-MOVE-NETWORK-BOUND`, `REQ-MOVE-01/02`, и override/selector-часть
> `REQ-IPL-BIND-FAMILY-AGNOSTIC` — **сняты** (no longer enforced). IPAM cascade
> сведен к network_default → zone_default → global_default.

Нормативный список **продуктовых требований** к публичному API `kacho-vpc`, выведенный из
каталога тест-кейсов (`CASES-INDEX.md`) и контракта Kachō (proto + acceptance-spec). Это
**регламент**, на соответствие которому ревьюер проверяет любое изменение кода / proto /
миграций / тестов: для каждого затронутого `REQ-*` — соблюден ли он. Нарушение → блокирующее замечание.

**Кто ведет.** Тестировщики добавляют сюда новые `REQ-*` по мере выявления требований (из ревью,
прогонов, probe'ов эталонного контракта). Формат записи — ниже. Не путать с:
- `REQUIREMENTS.md` — бэклог *улучшений* (testability/contract-clarification asks), не нормативный.
- `docs/architecture/07-known-divergences.md` — намеренные расхождения с контрактом (это **исключения** из регламента, помечаются в REQ как «divergence: …»).
- баги/задачи — issue-трекер репозитория.

**Формат REQ.**
```
### REQ-<AREA>-<NN> — <короткий заголовок>           [P0|P1|P2|P3]
<нормативная формулировка: продукт ДОЛЖЕН / НЕ ДОЛЖЕН ...>
- Validated-by: <case-id-паттерны из CASES-INDEX, через запятую> (или «gap — нет кейса»)
- Проверка: <где смотреть, чтобы проверить соответствие: файл/слой/proto/миграция>
- Divergence: <если это намеренное отклонение от контракта — ссылка на 07-known-divergences>
```

**Как пользоваться регламентом.** Получив diff/PR:
1. Определи, какие области (`RES`/`VAL`/`NAME`/`CIDR`/`IPAM`/`UPD`/`LIST`/`DEL`/`OPS`/`AUTHZ`/`SEC`/`SG`/`CONF`/`MOVE`) затронуты.
2. Для каждого `REQ-*` в этих областях — проверь раздел «Проверка»: соответствует ли изменение требованию.
3. Если новый/измененный RPC — пройдись по `TAXONOMY.md` «Применение по методам»: все обязательные классы покрыты кейсами? соответствующие `REQ-*` не нарушены?
4. Нарушение `REQ-*` (или регресс кейса из Validated-by) → **блокирующее** замечание со ссылкой на REQ.
5. Если изменение вводит новое поведение, не покрытое регламентом — предложи новый `REQ-*` (и кейс в `cases/*.py`).

---

## A. Модель ресурсов и жизненный цикл

### REQ-RES-01 — 6 публичных project-scoped ресурсов с CRUD [P1]
Продукт ДОЛЖЕН предоставлять ресурсы Network / Subnet / Address / RouteTable / SecurityGroup /
Gateway; все project-scoped (`project_id` обязателен в Create); все поддерживают
`Get`/`List`/`Create`/`Update`/`Delete`, а Network/Subnet/Address/RouteTable/SecurityGroup/Gateway — еще и `Move`.
- Validated-by: `*-LIFECYCLE-CONF`, `*-CR-CRUD-OK`, `*-GET-CRUD-OK`, `*-LST-CRUD-OK`, `*-UPD-CRUD-OK`, `*-DEL-CRUD-OK`, `*-MV-CRUD-OK`
- Проверка: `internal/service/*.go` (по сервису на ресурс), `cmd/vpc/main.go` (регистрация), `kacho-proto/.../<res>_service.proto`.

### REQ-RES-02 — все мутации возвращают Operation (async) [P0]
`Create`/`Update`/`Delete`/`Move`/`AddCidrBlocks`/`RemoveCidrBlocks`/`Relocate`/`UpdateRules`/`UpdateRule`
ДОЛЖНЫ возвращать `operation.Operation`; реальная работа — в worker-горутине; клиент поллит
`OperationService.Get(id)` до `done=true`. Возвращать сам ресурс синхронно из мутации — ЗАПРЕЩЕНО.
- Validated-by: `OP-GET-CRUD-OK`, `*-LOP-CRUD-OK` (ListOperations содержит create-op), все `*-CR-*`/`*-UPD-*`/`*-DEL-*` (poll-паттерн)
- Проверка: сигнатуры RPC в `.proto` (`returns (operation.Operation)`); `internal/service/*.go` — `operations.New` + `operations.Run` шаблон; правило «мутации возвращают Operation».

### REQ-RES-03 — Delete-операция: response = Empty, metadata = DeleteXxxMetadata [P1]
В завершенной Delete-`Operation`: `response` = `google.protobuf.Empty`, `metadata` = `DeleteXxxMetadata{<res>_id}`.
- Validated-by: `*-DEL-CRUD-OK` (poll → response shape)
- Проверка: worker всех `Delete` в `internal/service/*.go` (`return anypb.New(&emptypb.Empty{})`); proto-options `response`/`metadata` в `<res>_service.proto`.

### REQ-RES-04 — Create retry-safe (идемпотентность по input) [P1]
Повторный `Create` с тем же input (где это детектируемо) ДОЛЖЕН давать консистентный результат
(не дубль-ресурс при одинаковом `name` — см. REQ-NAME-04).
- Validated-by: `*-CR-IDM-RETRY`
- Проверка: `internal/service/*.go` doCreate — UNIQUE-violation → `AlreadyExists` (не сырой 500); idempotency на уровне Operation.

### REQ-RES-05 — hard-delete, без soft-delete/tombstone [P2]
`Delete` физически удаляет строку (`DELETE FROM`); в схеме нет `deletion_timestamp`/`finalizers`,
flat-таблицы без K8s-envelope.
- Validated-by: косвенно `*-DEL-CRUD-OK` + `*-GET-NEG-NF` после Delete
- Проверка: `internal/migrations/0001_initial.sql` (нет envelope-колонок); `internal/repo/*.go` (`DELETE FROM`).

### REQ-RES-06 — Move в текущий project → InvalidArgument [P2]
`Move` ресурса в его же `project_id` → sync `InvalidArgument "Illegal argument Destination project
is the same as the source"` (контракт Kachō). Ресурс не меняется.
- Validated-by: `*-MV-IDM-SAME-PROJECT`
- Проверка: `internal/service/*.go` Move → `checkMoveDestination` в `internal/service/validate.go`; порядок sync-проверок: формат id → id required → destination required → `repo.Get` (NotFound) → same-project/dest-exists.

### REQ-RES-07 — SecurityGroup: `network_id` ОБЯЗАТЕЛЕН + НЕИЗМЕНЯЕМ [P1]
> ** реверт ``.** Раньше `network_id` был опционален (SG без сети допустима) —
> **отброшено**. Теперь `SecurityGroup` обязан принадлежать ровно одной `Network` своего проекта.
`network_id` в `Create` **обязателен** (пустой/отсутствует → sync `INVALID_ARGUMENT` `network_id required`,
Operation НЕ создается); well-formed но несуществующий → sync `NOT_FOUND` `Network … not found`.
`network_id` **неизменяем**: не входит в `Update` known-mask (`{name,description,labels,rule_specs}`) →
явный `update_mask=network_id` → `INVALID_ARGUMENT`; full-PATCH (mask пустой) с `network_id` в теле —
silent-ignore (не меняется). Нет RPC attach/detach/reassign. `List` SecurityGroups поддерживает фильтр
по `network_id` (возвращает только SG этой сети, не возвращает SG другой сети). Same-network SG-rule —
см. REQ-SG-RULE-SAME-NETWORK; Move-guard — REQ-SG-MOVE-NETWORK-BOUND.
- Validated-by: `SG-NET-01-NEG-CREATE-NO-NETWORK`, `SG-NET-02-CREATE-OK`, `SG-NET-03-NEG-NETWORK-NOTFOUND`,
 `SG-NET-04-NEG-UPDATE-MASK-NETWORK`, `SG-CR-WITH-NETWORK-OK`, `SG-LIST-FILTER-NETWORK-OK`
- Проверка: `kacho-proto/.../security_group_service.proto` (`CreateSecurityGroupRequest.network_id`
 `(required)=true`; `network_id` НЕ в `UpdateSecurityGroupRequest`); `internal/apps/kacho/api/securitygroup/create.go`
 (`network_id required` sync + `networkReader.Get`); `validateSGUpdate`/`applySGMask` (network_id не в known-mask,
 silent-ignore); `internal/repo/security_group_repo.go` List (фильтр `network_id`).

### REQ-RES-08 — Network: нет data-plane-идентификатора на публичной поверхности [P0]
У `Network` нет числового data-plane-идентификатора. Соответствующее internal-поле прежней
реализации с data-plane-идентификатором было полностью удалено — ни кодом, ни клиентами оно
не используется. Регрессионное требование: публичная проекция `Network`
(`NetworkService.Get`/`List`) НИКОГДА не должна нести data-plane-инфо (защита от случайного
reintroduce инфра-чувствительного поля — см. правило про инфра-чувствительные данные).
- Validated-by: `NET-GET-NO-VPNID-OK` (guard: публичный GET не содержит data-plane-поля)
- Проверка: `kacho-proto/.../network_service.proto` (public `Network` без data-plane-id); `internal/handler/network_handler.go` (публичный mapper не выставляет инфра-полей).

---

## B. Валидация полей (sync, до создания Operation)

### REQ-VAL-01 — required-поля в Create [P0]
Отсутствие required-поля → sync `InvalidArgument "<field> is required"`. Required: `project_id`
(все ресурсы); `network_id` (Subnet/RouteTable/SecurityGroup); `zone_id` (Subnet);
`v4_cidr_blocks` (Subnet, ≥1); gateway-type oneof (Gateway).
- Validated-by: `*-CR-VAL-REQ-PROJECTID`/`-NETWORKID`/`-ZONEID`/`-V4CIDRBLOCKS`, `*-CR-VAL-PROJECT-REQUIRED`, `*-CR-VAL-NETWORK-REQUIRED`, `*-CR-VAL-ZONE-REQUIRED`, `*-CR-VAL-CIDR-REQUIRED`, `*-CR-VAL-MISSING-TYPE`, `*-CR-VAL-SERVICE-MISSING`
- Проверка: начало `Create` в `internal/service/*.go` — `corevalidate.Required`/явные проверки ДО `operations.New`.

### REQ-VAL-02 — malformed body / типы полей [P1]
Malformed JSON → `400`. Неверный тип поля (`description`=число, `labels`=строка, `name`=null) → `400`.
Пустой body → `400`. Unknown поле в body — silent-ignore (200) ИЛИ `400` (документировать выбор).
Тело ответа на JSON-transcoding-ошибку: контракт Kachō отдает plain-text, наш api-gateway — JSON
`{code,message}` (поведение runtime-библиотеки grpc-gateway; известное расхождение,
`07-known-divergences.md`, раздел 4) → кейсы `*-CR-VAL-DESC-INT-TYPE`/`-LABELS-STRING-TYPE`/`ADR-CR-VAL-BOTH-SPEC` defensive (`400` + непустое тело).
- Validated-by: `*-CR-VAL-MALFORMED-JSON`, `*-CR-VAL-DESC-INT-TYPE`, `*-CR-VAL-LABELS-STRING-TYPE`, `*-CR-VAL-NAME-NULL`, `*-CR-VAL-EMPTY-BODY`, `*-CR-VAL-EXTRA-FIELDS`, `ADR-CR-VAL-BOTH-SPEC`
- Проверка: grpc-gateway transcoding (api-gateway) + handler-слой; protobuf JSON-unmarshal поведение.

### REQ-VAL-03 — description ≤ 256, labels ≤ 64 пар, label-key regex [P1]
`description` len ≤ 256 (257 → `InvalidArgument`); ≤ 64 пар `labels` (65 → `400`); ключ `labels`
по regex (lowercase, без спец-символов, не UPPERCASE) — нарушение → `400`.
- Validated-by: `*-CR-BVA-DESC-MAX-256`/`-OVER-257`, `*-CR-BVA-LABELS-MAX-64`/`-OVER-65`, `*-CR-VAL-LABELS-INVALID-KEY-CHAR`, `*-CR-VAL-LABELS-UPPERCASE-KEY`
- Проверка: `corevalidate.Description`/`corevalidate.Labels` в `internal/service/validate.go` + вызовы.

### REQ-VAL-04 — DhcpOptions / static_routes валидация [P1]
Subnet `dhcp_options`: `domain_name` по RFC 1123 (invalid → `400`); `domain_name_servers[]`/`ntp_servers[]` — валидные IP (invalid → `400`).
RouteTable `static_routes[]`: непустой `destination_prefix` (валидный CIDR) и `next_hop_address` (валидный IP) — иначе `400`.
- Validated-by: `*-CR-VAL-DHCP-DOMAIN-INVALID`/`-OK`, `*-CR-VAL-DHCP-NS-INVALID-IP`/`-OK`, `*-CR-VAL-DHCP-NTP-INVALID-IP`/`-OK`, `*-CR-VAL-ROUTE-EMPTY-HOP`/`-EMPTY-PREFIX`/`-INVALID-HOP`/`-INVALID-PREFIX`/`-OK`
- Проверка: `internal/service/subnet.go` (DhcpOptions), `internal/service/route_table.go` (static_routes) — sync-валидация.

---

## C. Имена ресурсов (контракт Kachō name policy)

### REQ-NAME-01 — NameVPC permissive для Network/Subnet/Address/RouteTable/SecurityGroup [P1]
`name` этих ресурсов — **необязателен** и валидируется permissive-regex `^([a-zA-Z]([-_a-zA-Z0-9]{0,61}[a-zA-Z0-9])?)?$`
(пустое / UPPERCASE / underscore — разрешены). НЕ возвращать `"name is required"`.
- Validated-by: `*-CR-BVA-NAME-EMPTY`, `*-CR-VAL-NAME-UPPERCASE`, `*-CR-BVA-NAME-MAX-63`
- Проверка: `corevalidate.NameVPC` + вызовы в `internal/service/{network,subnet,address,route_table,security_group}.go`.

### REQ-NAME-02 — Gateway: strict NameGateway [P1]
`Gateway.name` — strict: `^[a-z]([-a-z0-9]{0,61}[a-z0-9])?$` (lowercase, без uppercase/underscore).
- Validated-by: `GW-CR-VAL-NAME-*` (см. паттерны `*-CR-VAL-NAME-DIGIT-START`/`-HYPHEN-START`/`-SPECIAL-CHARS`/`-UPPERCASE` на app `gat`)
- Проверка: `corevalidate.NameGateway` в `internal/service/gateway.go`.

### REQ-NAME-03 — name boundary & format [P1]
`name` len > 63 → `InvalidArgument`. Начинается с цифры/дефиса, содержит спец-символы → `400`
(для strict-ресурсов; для permissive — UPPERCASE/underscore допустимы, остальное по regex).
- Validated-by: `*-CR-BVA-NAME-OVER-64`, `*-CR-VAL-NAME-DIGIT-START`, `*-CR-VAL-NAME-HYPHEN-START`, `*-CR-VAL-NAME-SPECIAL-CHARS`
- Проверка: regex'ы в `kacho-corelib/validate/validate.go`.

### REQ-NAME-04 — UNIQUE (project_id, name) — все 7 ресурсов [P1]
В пределах project не может быть двух ресурсов одного типа с одинаковым непустым `name` →
async `ALREADY_EXISTS`. Пустое `name` от уникальности освобождено (partial UNIQUE `WHERE name <> ''`,
кроме Network — там non-partial).
- Validated-by: `*-CR-NEG-DUP-NAME`, `*-CR-NEG-DUP-NAME-CHECK`
- Проверка: `internal/migrations/0001_initial.sql` (`networks_project_id_name_key`) + `0002_resource_name_unique.sql`; `mapRepoErr` (`23505` → `ErrAlreadyExists`).

---

## D. CIDR / Subnet semantics

### REQ-CIDR-01 — host-bits = 0 [P0]
CIDR с host-bits ≠ 0 (`10.0.0.5/24`) → sync `InvalidArgument`. Касается Create.v4/v6_cidr_blocks и AddCidrBlocks.
- Validated-by: `*-CR-VAL-CIDR-HOSTBITS`, `*-ACB-VAL-HOST-BITS`
- Проверка: `validateCIDRPrefix` (`netip.Prefix.Masked == prefix`) в `internal/service/validate.go`.

### REQ-CIDR-02 — overlap внутри Network запрещен (race-free) [P0]
Два Subnet с пересекающимися CIDR в одной Network → второй `FAILED_PRECONDITION "Subnet CIDRs can not overlap"`.
Защита atomic — DB EXCLUDE constraint (`23P01` → `FailedPrecondition`).
- Validated-by: `*-CR-NEG-CIDR-OVERLAP`, `*-ACB-NEG-OVERLAP`, `*-ACB-NEG-OVERLAP-SELF`
- Проверка: `internal/migrations/0001_initial.sql` — `subnets_no_overlap_v4`/`v6` EXCLUDE GIST; `mapRepoErr` `23P01`.

### REQ-CIDR-03 — CIDR внутри одного запроса не пересекаются [P1]
В Create или AddCidrBlocks несколько CIDR в одном запросе не должны пересекаться между собой → `InvalidArgument`.
- Validated-by: `*-ACB-STATE-DISJOINT-CIDRS`
- Проверка: `checkCIDRDisjoint` в `internal/service/subnet.go`.

### REQ-CIDR-04 — AddCidrBlocks [P1]
`AddCidrBlocks` добавляет 1+ CIDR; новые блоки видны в `Get`; пересечение с existing → `InvalidArgument`/`FailedPrecondition`.
- Validated-by: `*-ACB-CRUD-OK`, `*-ACB-CRUD-ADD-ONE`, `*-ACB-CRUD-ADD-MULTIPLE`
- Проверка: `internal/service/subnet.go` AddCidrBlocks.

### REQ-CIDR-05 — RemoveCidrBlocks: нельзя удалить primary/last [P0]
`RemoveCidrBlocks` для primary (первого) v4-CIDR → отказ (`FailedPrecondition "cannot remove last CIDR block from subnet"`).
CIDR не из списка → `InvalidArgument`/`FailedPrecondition` (документировать). Add+Remove roundtrip — state invariant.
- Validated-by: `*-RCB-NEG-CANNOT-REMOVE-PRIMARY`, `*-RCB-NEG-NF`, `*-RCB-NEG-NOT-PRESENT`, `*-RCB-CRUD-OK`, `*-RCB-CRUD-REMOVE-ONE`, `*-RCB-CONF-STATE`, `*-ACB-RCB-ROUNDTRIP`
- Проверка: `internal/service/subnet.go` RemoveCidrBlocks.

### REQ-CIDR-06 — Relocate Subnet: всегда запрещен [P1]
`Relocate` Subnet → **всегда** sync `FailedPrecondition "Invalid subnet state"` (контракт Kachō,
контракт Kachō) — даже для свежей подсети без адресов и валидной целевой зоны;
Operation не создается. Без `destinationZoneId` → sync `InvalidArgument`. Несуществующая подсеть → `NotFound`.
- Validated-by: `*-REL-NEG-IN-USE`, `*-REL-STATE-NO-ADDRESSES-OK`, `*-REL-VAL-NO-DEST`
- Проверка: `internal/service/subnet.go` Relocate — после format-check id, валидации `destination_zone_id`, `repo.Get` → `return status.Error(codes.FailedPrecondition, "Invalid subnet state")`.

### REQ-CIDR-07 — Subnet IPv4-префикс ≤ /28 [P2]
Subnet с IPv4 CIDR-префиксом длиннее `/28` (`/29`, `/30`, `/31`, `/32`) → sync
`InvalidArgument "Illegal argument Invalid network prefix /N"` (контракт Kachō
). Касается Create.v4_cidr_blocks и AddCidrBlocks. `/28` — допустимо.
- Validated-by: `SUB-CR-BVA-CIDR-28`, `SUB-CR-BVA-CIDR-29`, `SUB-CR-BVA-CIDR-30`, `SUB-CR-BVA-CIDR-31`
- Проверка: `validateSubnetV4CIDR` в `internal/service/validate.go` (`prefix.Addr.Is4 && prefix.Bits > 28`).

### REQ-CIDR-08 — Subnet: IPv4 CIDR опционален (CIDR-less subnet) [P1]
`Subnet.Create` БЕЗ `v4_cidr_blocks` → 200, создается CIDR-less (или v6-only) подсеть.
`Address.Create` с internal-spec в подсеть без IPv4 CIDR → `FailedPrecondition`/`InvalidArgument`
(`"subnet <id> has no IPv4 CIDR"` — некуда аллоцировать v4-IP).
- Validated-by: `SUB-CR-NO-CIDR-OK`, `SUB-CR-NEG-ADDR-INTO-CIDRLESS`
- Проверка: `internal/service/subnet.go` Create (`v4_cidr_blocks` не required); `internal/service/address.go` doCreate (guard «no IPv4 CIDR» перед allocate).

### REQ-CIDR-09 — Subnet: IPv6 CIDR (dual-stack / v6-only) [P1]
`Subnet.Create` с `v6_cidr_blocks` → 200, `v6_cidr_blocks` виден в GET; допустимы dual-stack
(v4+v6) и v6-only подсети. v6-CIDR с host-bits → `InvalidArgument` (как v4).
- Validated-by: `SUB-CR-V6-OK`
- Проверка: `internal/service/subnet.go` Create (валидация `v6_cidr_blocks`, host-bits); миграция `subnets.v6_cidr_*` + EXCLUDE `subnets_no_overlap_v6`.

### REQ-CIDR-10 — Subnet: v6-CIDR изменяется через AddCidrBlocks/RemoveCidrBlocks [P1]
`Subnet.AddCidrBlocks` с IPv6-блоком → блок добавлен в `v6_cidr_blocks` (Subnet становится dual-stack);
`RemoveCidrBlocks` с ним → блок убран. v6-блок с host-bits в AddCidrBlocks → `InvalidArgument`.
(Прямое изменение `v6_cidr_blocks` через `Update.mask` — soft-immutable no-op, см. REQ-UPD-05.)
- Validated-by: `SUB-CIDR-ADD-V6-OK`, `SUB-CIDR-ADD-V6-NEG-HOSTBITS`, `SUB-CIDR-REMOVE-V6-OK`
- Проверка: `internal/service/subnet.go` AddCidrBlocks/RemoveCidrBlocks (family-aware: v4→`v4_cidr_blocks`, v6→`v6_cidr_blocks`); `validateCIDRPrefix` (host-bits для обеих семей).

---

## E. IPAM / Address allocation

### REQ-IPAM-01 — Address spec oneof: ровно один из external/internal [P0]
`Address.Create` ДОЛЖЕН требовать ровно один из `external_ipv4_address_spec` / `internal_ipv4_address_spec`.
Оба → `InvalidArgument`; ни одного → `InvalidArgument`. Internal-spec с `subnet_id` + external-spec одновременно → `400 oneof`.
- Validated-by: `*-CR-VAL-BOTH-SPEC`, `*-CR-VAL-SPEC-ONEOF`, `*-CR-VAL-EXT-WITH-SUBNET-FK`, `*-CR-CRUD-EXT`, `*-CR-CRUD-INT`
- Проверка: `internal/service/address.go` — oneof-валидация sync.

### REQ-IPAM-02 — external Address → IP из резолвленного pool; internal → IP в subnet [P1]
`Create` external Address (с `zone_id`) → IP выделяется из pool по cascade-резолву (см. `docs/architecture/03-ipam.md`).
`Create` internal Address → IP в пределах `v4_cidr_blocks` указанного Subnet; explicit IP вне CIDR → `InvalidArgument`.
- Validated-by: `*-CR-CRUD-EXT`, `*-CR-CRUD-INT`, `*-CR-VAL-RESERVED-USED-OK`
- Проверка: `internal/service/address.go` doCreate (inline allocate, cascade); `internal/service/address_pool_service.go`.

### REQ-IPAM-03 — аллокатор race-free [P0]
Параллельные `AllocateExternalIP` / параллельные internal-allocate ДОЛЖНЫ выдавать уникальные IP
(UNIQUE constraint `addresses_external_pool_ip_uniq` + retry на violation).
- Validated-by: **gap — нет concurrency-кейса** (см. `REQUIREMENTS.md` REQ-007 / backlog); инвариант проверяется integration-тестом `ipam_cascade_integration_test.go` (частично).
- Проверка: `internal/service/address.go` — двухфазный аллокатор + UNIQUE-retry; миграция `addresses_external_pool_ip_uniq`.

### REQ-IPAM-04 — Address.GetByValue: невалидный IP → 400, отсутствующий → 404 [P1]
`GetByValue` с не-IP значением → `InvalidArgument "Cannot parse address: <X>"` (контракт Kachō).
Отсутствующий IP → `NOT_FOUND` (см. REQ-AUTHZ-04).
- Validated-by: `*-GBV-VAL-INVALID-IP`, `*-GBV-NEG-NF`, `*-GBV-CRUD-OK`
- Проверка: `internal/service/address.go` GetByValue — `netip.ParseAddr` sync, затем `repo.GetByValue`.

### REQ-IPL-CR-01 — Create v4-only AddressPool [P0]
`InternalAddressPoolService.Create` с `v4_cidr_blocks` непустым и `v6_cidr_blocks=[]` →
200, pool создается как v4-only. Free-list `address_pool_free_ips` материализуется
для каждого `/N` блока (`2^(32-N)-2` строк). `ipv6_pool_cursors` для v4-only pool
не инициализируется.
- Validated-by: `IPL-CR-CRUD-V4-OK`
- Проверка: `kacho-proto/.../internal_address_pool_service.proto` (`AddressPool.v4_cidr_blocks=13`); `internal/service/address_pool_service.go::Create`.

### REQ-IPL-CR-02 — Create v6-only AddressPool [P0]
`Create` с `v4_cidr_blocks=[]` и `v6_cidr_blocks` непустым → 200, pool создается как
v6-only. `ipv6_pool_cursors` инициализируется (`InitIPv6PoolCursor`, sparse counter).
- Validated-by: `IPL-CR-CRUD-V6-OK`
- Проверка: `internal/service/address_pool_service.go::Create`; `internal/repo/address_pool_repo.go` (`InitIPv6PoolCursor` для v6-блоков).

### REQ-IPL-CR-03 — Create dual-stack AddressPool [P0]
`Create` с оба `v4_cidr_blocks` и `v6_cidr_blocks` непустыми → 200, pool dual-stack.
И free-list, и v6-cursor инициализируются.
- Validated-by: `IPL-CR-CRUD-DS-OK`, `IPL-RESOLVE-DUALSTACK-OK`
- Проверка: `internal/service/address_pool_service.go::Create` — оба пути материализации.

### REQ-IPL-CR-04 — Pool с обоими CIDR-списками пустыми → InvalidArgument [P0]
`Create` (и `Update` post-state) с `v4_cidr_blocks=[]` И `v6_cidr_blocks=[]` →
sync `InvalidArgument` с подстрокой "v4_cidr_blocks and v6_cidr_blocks must not be
both empty". Pool не создается / не обновляется. Defensive backstop на DB-уровне —
PG `RAISE EXCEPTION` в backfill миграции `0022` (REQ-MIG-06, см. ниже).
- Validated-by: `IPL-CR-VAL-BOTH-EMPTY`, `IPL-RMCIDR-OK` (remove-last → empty rejected)
- Проверка: `internal/apps/kacho/api/addresspool/create.go` / `remove_cidr_blocks.go` post-state guard.

### REQ-IPL-CR-05 — Cross-family prefix → InvalidArgument [P0]
IPv6 prefix в `v4_cidr_blocks` (или v4 в `v6_cidr_blocks`) → sync `InvalidArgument`
`"v4_cidr_blocks[N]: %q is not an IPv4 prefix"` / `"v6_cidr_blocks[N]: ... is not an IPv6 prefix"`.
Family detection: `netip.ParsePrefix` + `Addr.Is6 && !Addr.Is4In6`.
- Validated-by: `IPL-CR-VAL-CROSS-V4-IN-V6`
- Проверка: `internal/service/address_pool_service.go` — per-slot validate family.

### REQ-IPL-UPD-01 — Update НЕ меняет CIDR-блоки [P0]
`Update` (`PATCH /vpc/v1/addressPools/{id}`) мутирует только `name` / `description` /
`labels` / `is_default` / `selector_*`. CIDR-состав пула (`v4_cidr_blocks` /
`v6_cidr_blocks`) через `Update` **не меняется** — proto убрал `replace_v4/v6_cidr_blocks`
и `v4/v6_cidr_blocks` из `UpdateAddressPoolRequest`. Изменение CIDR — только через
`:addCidrBlocks` / `:removeCidrBlocks` (parity с Subnet). Это устраняет implicit
replace-семантику и связанный с ней freelist-rebuild на Update.
- Validated-by: `IPL-UPD-CRUD-OK` (description/labels/isDefault), unit `TestAddressPool_KAC269_Update_DoesNotTouchCIDR`
- Проверка: `internal/apps/kacho/api/addresspool/update.go` — CIDR-поля не читаются.

### REQ-IPL-ADDCIDR-01 — AddCidrBlocks: добавить CIDR + дедуп + freelist-дельта [P0]
`POST /vpc/v1/addressPools/{id}:addCidrBlocks` с `v4_cidr_blocks` и/или `v6_cidr_blocks` →
sync 200 с обновленным пулом: новые блоки добавлены (append), уже присутствующие —
дедуплицированы (повторный add того же блока не создает дубль). Family-strict +
host-bits=0 валидация (как на Create). `address_pool_free_ips` материализуется ТОЛЬКО
для новой v4-дельты (не реитерируя существующие free_ips). v6-блок впервые на пуле →
init `ipv6_pool_cursors`. Атомарно (Get → Update → AddCidrToFreelist → outbox в одной
writer-TX). Cross-family prefix → InvalidArgument.
- Validated-by: `IPL-ADDCIDR-OK`; integration `TestIntegration_AddressPoolCIDR_AddCidrBlocks_PopulatesFreelist`;
 unit `TestAddressPool_KAC269_AddCidrBlocks_*`
- Проверка: `internal/apps/kacho/api/addresspool/add_cidr_blocks.go`; repo `AddCidrToFreelist`.

### REQ-IPL-RMCIDR-01 — RemoveCidrBlocks: удалить чистый CIDR + freelist-cleanup [P0]
`POST /vpc/v1/addressPools/{id}:removeCidrBlocks` с `v4_cidr_blocks` и/или `v6_cidr_blocks` →
sync 200: блоки убраны из пула, соответствующие `address_pool_free_ips` удалены. Если блок
отсутствует в пуле → `FailedPrecondition` "one or more CIDR blocks not found in address pool".
Если удаление опустошит пул (`v4 ∪ v6 = ∅`) → `InvalidArgument` "must not be both empty after
removal" (пул не может стать пустым). Атомарно в одной writer-TX.
- Validated-by: `IPL-RMCIDR-OK`; integration `TestIntegration_AddressPoolCIDR_RemoveClean_DeletesFreeIPs`;
 unit `TestAddressPool_KAC269_RemoveCidrBlocks_{Clean_OK,NotPresent_FailedPrecondition,Empties_InvalidArgument}`
- Проверка: `internal/apps/kacho/api/addresspool/remove_cidr_blocks.go`; repo `DeleteFreelistForCidrs`.

### REQ-IPL-RMCIDR-02 — RemoveCidrBlocks use-guard: CIDR с выделенным IP → FailedPrecondition [P0]
`:removeCidrBlocks` для CIDR, в котором есть хотя бы один выделенный external-IPv4 (Address с
`external_ipv4.address_pool_id=pool` И `address ∈ cidr`) → `FailedPrecondition` "address pool
CIDR <cidr> has allocated addresses" (в едином стиле с Subnet "network is not empty"). Пул
не меняется (TX abort). Атомарность guard'а: в одной writer-TX сперва DELETE free_ips
удаляемого CIDR (row-lock сериализует против конкурентного allocate), затем use-check count
по addresses; конкурентный alloc, успевший закоммитить IP → count>0 → abort.
- Validated-by: `IPL-RMCIDR-NEG-INUSE`; integration
 `TestIntegration_AddressPoolCIDR_{RemoveInUse_FailedPrecondition,ConcurrentAllocVsRemove}`;
 unit `TestAddressPool_KAC269_RemoveCidrBlocks_InUse_FailedPrecondition`
- Проверка: `internal/apps/kacho/api/addresspool/remove_cidr_blocks.go`; repo `CountAllocatedInCidrs`.

### REQ-IPL-OVERLAP-01 — AddressPool CIDR не пересекаются per kind (DB EXCLUDE) [P0]
CIDR-блоки AddressPool'ов одного `kind` (EXTERNAL_PUBLIC — единственный активный) обязаны быть
попарно непересекающимися — **внутри** одного пула И **между** пулами (cross-zone public CIDR
глобально непересекающиеся → zone в exclusion-key не входит). Иначе IPAM аллоцирует один и тот же
external-IP дважды: per-pool UNIQUE `addresses_external_pool_ip_uniq (address_pool_id, address)`
не ловит коллизию между разными `pool_id`. Инвариант — DB-уровень (within-service инвариант на DB-уровне):
нормализованная child-таблица `address_pool_cidrs` + `EXCLUDE USING gist (kind WITH =, block &&)`
(миграция 0004; declarative, race-free by construction — зеркалит `subnets_no_overlap_v4`). При
пересечении на `Create` / `:addCidrBlocks` → `FailedPrecondition` "address pool CIDRs can not
overlap" (SQLSTATE 23P01 → ErrFailedPrecondition; grpc-gateway отдает 400). Sync within-request
precheck (блоки в самом запросе попарно disjoint) → `InvalidArgument` тем же текстом (fast-fail);
DB EXCLUDE — backstop для cross-pool/concurrent. `:removeCidrBlocks` освобождает диапазон
(удаляет block из `address_pool_cidrs` → новый пул с тем же CIDR проходит). Pool.Delete каскадит
(FK ON DELETE CASCADE).
- Validated-by: `IPL-CR-NEG-OVERLAP`, `IPL-ADDCIDR-NEG-OVERLAP`; integration
 `TestIntegration_AddressPoolOverlap_{AcrossPools,AddCidrOverlapExisting,ConcurrentOverlap,RemoveFreesBlock}`
- Проверка: `internal/migrations/0004_address_pool_cidrs.sql` (EXCLUDE gist); repo
 `InsertCidrBlocks`/`DeleteCidrBlocks` (`internal/repo/kacho/pg/address_pool.go`); use-cases
 `create.go`/`add_cidr_blocks.go` (InsertCidrBlocks + `checkPoolCIDRsDisjoint`),
 `remove_cidr_blocks.go` (DeleteCidrBlocks).

### REQ-IPL-BIND-FAMILY-AGNOSTIC — Bind*/Override*/SetPoolSelector family-agnostic [P0]
`BindAddressPoolAsNetworkDefault`, `OverridePoolForAddress`, `SetPoolSelector` (на Network/Cloud) —
НЕ валидируют family pool'а в момент связывания. Можно забиндить v4-only pool к Network с
v6-allocate-намерением и наоборот; binding сохраняется. Family-фильтр работает ТОЛЬКО на
resolve-этапе (`address_pool_service.go::doResolve`, 5 шагов cascade). Это позволяет
"пре-биндить" pool под будущее использование без runtime-знания, что нужно tenant'у.
- Validated-by: `IPL-BIND-FAMILY-AGNOSTIC`
- Проверка: `internal/service/network_service.go::BindAsNetworkDefault` + аналогичные RPC — НЕТ family-check; family-фильтр только в `doResolve`.

### REQ-RESOLVE-01 — Cascade family-skip для v6-allocate [P0]
В cascade 5 шагов (`address_pool_address_override` → `address_pool_network_default` →
label_selector → zone_default → global_default): pool, у которого `v6_cidr_blocks=[]`,
пропускается при `family=v6`. Если ни один pool с v6-блоками не найден → `ErrPoolNotResolved`
→ gRPC `FailedPrecondition` (код 9, не `Internal` 13). После фильтр работает через
`len(pool.V6CIDRBlocks) > 0` (без runtime-парсинга CIDR).
- Validated-by: `ADR-CR-EXT-FALLTHROUGH-V6`, `ADR-CR-EXT-V6-FAMILY-FALLTHROUGH` (наследник pre-split поведения)
- Проверка: `internal/service/address_pool_service.go::doResolve` — `poolHasFamily` заменен на `len(V6CIDRBlocks)>0` для FamilyV6.

### REQ-RESOLVE-02 — Cascade family-skip для v4-allocate [P0]
Зеркало REQ-RESOLVE-01 для v4: pool с `v4_cidr_blocks=[]` пропускается при `family=v4`.
- Validated-by: `ADR-CR-EXT-FALLTHROUGH-V4`
- Проверка: `internal/service/address_pool_service.go::doResolve` — `len(V4CIDRBlocks)>0` для FamilyV4.

### REQ-RESOLVE-04 — ExplainResolution на fall-through → matched_via="none" [P1]
`InternalAddressPoolService.ExplainResolution` при `ErrPoolNotResolved` из cascade
возвращает HTTP 200 / gRPC OK с `matched_via="none"` и пустым `selected_pool` (вместо
gRPC `FailedPrecondition`, как для Allocate-методов). Это требует **handler-change** —
`ExplainResolution` ловит `ErrPoolNotResolved` отдельно (до `mapPoolErr`). Семантика
остальных Allocate-методов (REQ-RESOLVE-01/02) — без изменений: `FailedPrecondition`.
- Validated-by: `IPL-EXPLAIN-NONE`
- Проверка: `internal/handler/internal_address_pool_handler.go::ExplainResolution`.

### REQ-RESOLVE-06 — Cascade Step 1 (per-address override) family-skip [P0]
Per-address override на pool не той family, что у address'а — cascade Step 1 находит pool,
family-фильтр пропускает, fall-through до конца. Override НЕ форсирует family-mismatch:
семантика family-filter unified на всех 5 шагах.
- Validated-by: `IPL-RESOLVE-OVERRIDE-FAMILY-SKIP`
- Проверка: `internal/service/address_pool_service.go::doResolve` — Step 1 (address_pool_address_override) применяет тот же family-filter.

### REQ-RESOLVE-07 — Cascade Step 2 (per-network default) family-skip [P0]
Per-network binding на pool не той family — cascade Step 2 пропускает; fall-through. Симметрично
REQ-RESOLVE-06; binding НЕ форсирует family-mismatch.
- Validated-by: `IPL-RESOLVE-NETWORK-DEFAULT-FAMILY-SKIP`
- Проверка: `internal/service/address_pool_service.go::doResolve` — Step 2 (address_pool_network_default) с тем же family-filter.

---

## F. UpdateMask / immutability

### REQ-UPD-01 — empty mask → full-PATCH [P1]
`Update` с пустым `update_mask` → применяются все mutable-поля из тела; immutable-поля из тела
**silently игнорируются** (контракт Kachō).
- Validated-by: `*-UPD-VAL-MASK-EMPTY`, `*-UPD-CRUD-DESC`/`-DESCRIPTION`/`-LABELS`/`-NAME`/`-MULTI-MASK`
- Проверка: `internal/service/*.go` Update — ветка `len(mask)==0`.

### REQ-UPD-02 — unknown поле в mask → InvalidArgument [P1]
`Update` с полем в `update_mask`, которого нет в known-set ресурса → `InvalidArgument`. Несколько unknown → `400`.
- Validated-by: `*-UPD-VAL-UNKNOWN-MASK`, `*-UPD-VAL-MASK-MULTIPLE-UNKNOWN`
- Проверка: `corevalidate.UpdateMask(known-set)` в `internal/service/*.go`.

### REQ-UPD-03 — hard-immutable поле в mask → InvalidArgument (точный текст) [P1]
`Update` с hard-immutable-полем в `update_mask` → `InvalidArgument "<field> is immutable after <Resource>.Create"`.
Hard-immutable по ресурсам: **все** — `project_id`; **Subnet** — `network_id`,`zone_id`;
**Address** — `external_ipv4_address_spec`,`internal_ipv4_address_spec`;
**RouteTable/SecurityGroup** — `network_id`.
Subnet `v4_cidr_blocks`/`v6_cidr_blocks` — **soft-immutable**: в mask → НЕ ошибка (контракт Kachō
`200`); у нас принимается, но `repo.Update` CIDR не перезаписывает → no-op.
- Validated-by: `*-UPD-STATE-IMMUTABLE-PROJECT`/`-PROJECT-ID`, `SUB-UPD-STATE-IMMUTABLE-CIDR` (→ `200`), `*-UPD-STATE-IMMUTABLE-NETWORK-ID`/`-ZONE-ID`, `*-UPD-STATE-IMMUTABLE-EXTERNAL-IPV4-ADDRESS-SPEC`/`-INTERNAL-IPV4-ADDRESS-SPEC`, `*-UPD-STATE-IMMUTABLE-SUBNET-ID`/`-SERVICE-TYPE`/`-ADDRESS-ID`
- Проверка: начало `Update` в `internal/service/*.go` — `switch field { case <hard-immutable>: return invalidArg(...) }`; список в `docs/architecture/06-conventions.md`; для Subnet НЕ должно быть `v4_cidr_blocks`/`v6_cidr_blocks` в reject-switch.

### REQ-UPD-04 — mask=<single mutable> → меняется только это поле [P2]
`Update` с `update_mask=name` (или одно mutable-поле) → меняется только оно; description/labels не трогаются.
- Validated-by: `*-UPD-VAL-MASK-NAME-ONLY`, `*-UPD-CRUD-MULTI-MASK`
- Проверка: `internal/service/*.go` Update — применение по mask.

### REQ-UPD-05 — Subnet.Update с `v6_cidr_blocks` в mask → 200, no-op (soft-immutable) [P2]
`Subnet.Update` с `update_mask` содержащим `v6_cidr_blocks` (+ значение в body) → 200, операция
завершается без error; `repo.Update` v6-CIDR-колонки не перезаписывает (контракт Kachō принимает в mask
и меняет — у нас no-op). Реальное изменение v6-CIDR — через `:add-cidr-blocks`/`:remove-cidr-blocks` (REQ-CIDR-10).
- Validated-by: `SUB-UPD-V6-NOOP`
- Проверка: `internal/service/subnet.go` Update — `v6_cidr_blocks` НЕ в hard-immutable reject-switch; `internal/repo/subnet_repo.go` Update не трогает v6-колонки; `kacho-proto/.../subnet_service.proto` `UpdateSubnetRequest.v6_cidr_blocks`.

---

## G. List / pagination / filter

### REQ-LIST-01 — project_id required в List [P0]
`List<Resource>s` без `project_id` → `InvalidArgument`.
- Validated-by: `*-LST-VAL-PROJECT-REQUIRED`
- Проверка: handler/service `List` — required-проверка.

### REQ-LIST-02 — page_size bounds [P2]
`page_size`: 0 → default (50); 1..1000 — ok; 1001 / >1000 / отрицательный → `InvalidArgument "page_size must be in [0..1000]"`.
Boundary 1000 → ok; 1001 → `400`.
- Validated-by: `*-LST-BVA-PAGESIZE-ZERO`/`-1`/`-OVER-MAX`, `*-LST-PAGESIZE-EXACTLY-1000`/`-1001`, `*-LST-PAGE-NEGATIVE-SIZE`, `*-LST-PAGE-OVER`
- Проверка: `corevalidate.PageSize` + вызовы.

### REQ-LIST-03 — page_size contract: ответ не превышает page_size [P2]
`List` с `page_size=N` → в ответе ≤ N элементов; есть еще → непустой `next_page_token`.
- Validated-by: `*-LST-CONTRACT-NEVER-EXCEEDS-PAGESIZE`, `*-LST-BVA-PAGESIZE-1`
- Проверка: `internal/repo/*.go` `List` — `LIMIT page_size+1` (cursor-pagination).

### REQ-LIST-04 — page_token roundtrip; garbage token → InvalidArgument [P1]
`next_page_token` из ответа подается в следующий `List` → продолжение без пропусков/дублей.
Невалидный (не-decodable base64 / garbage) `page_token` → `InvalidArgument`; НЕ silent-fallback на page 1.
- Validated-by: `*-LST-PAGE-ROUNDTRIP`, `*-LST-ROUNDTRIP`, `*-LST-PAGE-TOKEN-GARBAGE`
- Проверка: `internal/repo/*.go` decode page_token (base64 `{created_at,id}`); ошибка декода → `ErrInvalidArg`.

### REQ-LIST-05 — filter: только whitelisted поля [P1]
`filter` (опционален): поддерживается `name="<value>"` (текущая фаза). Filter на не-whitelisted поле →
`InvalidArgument`. Garbage filter syntax → `InvalidArgument`. Пустой filter → ok (опционален). SQLi в filter → НЕ 500.
- Validated-by: `*-LST-FILTER-NAME-OK`/`-MATCH`/`-EMPTY`, `*-LST-FILTER-GARBAGE`, `*-LST-FILTER-UNKNOWN-FIELD`, `*-LST-SEC-FILTER-SQLI`, `*-LST-FILTER-CASE-SENSITIVITY`, `*-LST-FILTER-SPECIAL-CHARS`
- Проверка: `kacho-corelib/filter.Parse(whitelist)` + вызовы; параметризация в `internal/repo/*.go` (никакой строковой конкатенации в SQL).

### REQ-LIST-06 — child-list RPC: parent NotFound → 404 [P1]
`Network.ListSubnets`/`ListSecurityGroups`/`ListRouteTables`, `Subnet.ListUsedAddresses`, `Address.ListBySubnet`,
`<Resource>.ListOperations` для несуществующего parent → `NOT_FOUND` (для ListBySubnet/ListUsedAddresses/ListOperations
допускается `404` ИЛИ пустой `200` — документировать).
- Validated-by: `*-LSUB-NEG-PARENT-NF`, `*-LSG-NEG-PARENT-NF`, `*-LRT-NEG-PARENT-NF`, `*-LUA-NEG-PARENT-NF`, `*-LBS-NEG-PARENT-NF`, `*-LOP-NEG-PARENT-NF`
- Проверка: `internal/service/*.go` child-list — parent-existence check.

### REQ-LIST-07 — ListSecurityGroups содержит default SG (при inline-режиме) [P1]
При `KACHO_VPC_DEFAULT_SG_INLINE=true` (default) после `Network.Create` → `ListSecurityGroups` возвращает auto-созданный default SG `default-sg-<8>`.
- Validated-by: `*-LSG-CRUD-DEFAULT-SG`
- Проверка: `internal/service/network.go` doCreate (inline default-SG) + `SetSGRepo` в `cmd/vpc/main.go`.

---

## H. Удаление / FK-constraints

### REQ-DEL-01 — Delete несуществующего → sync 404 (точный текст) [P1]
`Delete` несуществующего ресурса → sync `NOT_FOUND "<Resource> <id> not found"` (не Operation).
- Validated-by: `*-DEL-AUTHZ-NF-SYNC`, `*-DEL-CONF-NF-TEXT`, `*-DEL-CONF-FULLTEXT`, `*-DEL-NEG-NF-INVALID-PREFIX`
- Проверка: `internal/service/*.go` Delete — `corevalidate.ResourceID(...)` (первым стейтментом) + `repo.Get` + `AssertProjectOwnership` ДО Operation. (id-syntax → `InvalidArgument`: см. REQ-CONF-04.)

### REQ-DEL-02 — Network: нельзя удалить с детьми (FK RESTRICT) [P0]
`Delete` Network, у которой есть Subnet / RouteTable / не-default SecurityGroup → `FailedPrecondition "network is not empty"` (FK RESTRICT).
- Validated-by: `*-DEL-NEG-HAS-SUBNETS`, `*-DEL-NEG-HAS-ROUTE-TABLE`, `*-DEL-NEG-HAS-NONDEFAULT-SG`
- Проверка: миграция — FK `ON DELETE RESTRICT` от children к networks; `mapRepoErr` `23503` → `ErrFailedPrecondition`.

### REQ-DEL-03 — Subnet: нельзя удалить с internal Address (FK RESTRICT) [P0]
`Delete` Subnet с привязанным internal Address → `FailedPrecondition`.
- Validated-by: `*-DEL-NEG-HAS-ADDRESSES`
- Проверка: миграция — FK addresses→subnets `RESTRICT`.

### REQ-DEL-04 — Network с только default-SG удаляется (auto-cleanup) [P1]
`Delete` Network, у которой единственный child — auto-default-SG → worker сначала удаляет default-SG, потом Network → успех.
Прямой `Delete` default-SG в обход → отказ.
- Validated-by: `*-DEL-CRUD-ONLY-DEFAULT-SG`, `*-DEL-STATE-DEFAULT-SG`
- Проверка: `internal/service/network.go` doDelete; `internal/service/security_group.go` — запрет удаления default напрямую.

### REQ-DEL-05 — deletion_protection: sync-check перед Delete [P1]
`Delete` ресурса с `deletion_protection=true` → sync `FailedPrecondition "... deletion_protection enabled; clear it via Update before Delete"`.
- Validated-by: **gap — нет явного кейса в текущем индексе** (документировано в gotchas; добавить `*-DEL-NEG-DELETION-PROTECTION`)
- Проверка: `internal/service/*.go` Delete — sync-проверка `deletion_protection` ДО Operation.

### REQ-DEL-06 — Subnet: нельзя удалить с internal **v6** Address [P0]
`Delete` Subnet, в которой есть internal Address (v4 ИЛИ v6) → `FailedPrecondition`. FK
`addresses_internal_subnet_fkey` через generated-колонку `addresses.internal_subnet_id`,
выводимую из `internal_ipv4->>'subnet_id'` ИЛИ `internal_ipv6->>'subnet_id'` (миграция 0013);
sync-precheck `AddressesBySubnet` тоже покрывает обе семьи.
- Validated-by: `SUB-DEL-NEG-HAS-V6-ADDRESS` (+ `*-DEL-NEG-HAS-ADDRESSES` для v4)
- Проверка: `internal/migrations/0013_address_internal_subnet_id_v6.sql`; `internal/service/subnet.go` Delete (`AddressesBySubnet` precheck); `mapRepoErr` `23503` → `ErrFailedPrecondition`.

### REQ-DEL-07 — Subnet: нельзя удалить с NetworkInterface [P0]
`Delete` Subnet, к которой привязан хоть один `NetworkInterface` → sync `FailedPrecondition`
со списком NIC-id (сначала удалите NIC'и). FK `network_interfaces.subnet_id` ON DELETE RESTRICT
(миграция 0012 — откат CASCADE из). Порядок удаления — снизу вверх: NIC → Address → Subnet → Network.
- Validated-by: `SUB-DEL-NEG-HAS-NIC`
- Проверка: `internal/migrations/0012_nic_subnet_restrict.sql`; `internal/service/subnet.go` Delete (sync-precheck NIC); FK RESTRICT в worker'е как backstop.

### REQ-DEL-08 — Network: транзитивно нельзя удалить (Subnet с NIC) [P0]
`Delete` Network, у которой Subnet содержит NIC → `FailedPrecondition "network is not empty"`
(NIC блокирует Subnet, Subnet блокирует Network). Удаление возможно только после зачистки снизу вверх.
- Validated-by: `NET-DEL-NEG-HAS-SUBNET-WITH-NIC` (+ `*-DEL-NEG-HAS-SUBNETS` базовый)
- Проверка: FK-цепочка `network_interfaces→subnets→networks` (все RESTRICT); `internal/service/network.go` doDelete.

### REQ-DEL-09 — Address: нельзя удалить, если референсится NIC [P0]
`Delete` Address, который указан в `v4_address_ids`/`v6_address_ids` хоть одного `NetworkInterface`
→ `FailedPrecondition` (сначала detach Address у NIC). Один Address — максимум на одном NIC
(enforced сервис-слоем через `addresses.used` + referrer-tracking).
- Validated-by: `ADDR-DEL-NEG-USED-BY-NIC`
- Проверка: `internal/service/address.go` Delete — проверка referrer'ов (NIC) ДО Operation; `internal/service/network_interface.go` (referrer-tracking при Create/Attach).

---

## I. Operations service

### REQ-OPS-01 — OperationService.Get свежесозданной op → done=true с response [P1]
После завершения worker'а `OperationService.Get(id)` → `done=true`, `response` = ресурс (для Create/Update) или Empty (Delete), либо `error` (`google.rpc.Status`).
- Validated-by: `OP-GET-CRUD-OK`
- Проверка: `internal/handler/operation_handler.go`; `kacho-corelib/operations` worker.

### REQ-OPS-02 — OperationService.Get bad id [P1]
Несуществующий op-id с правильным prefix → `NOT_FOUND "Operation <id> not found"`. Malformed / unknown-prefix id →
`InvalidArgument "invalid operation id <X>"`; well-formed id с prefix без backend → `NOT_FOUND`.
- Validated-by: `OP-GET-NEG-NF-VALID-PREFIX`, `OP-GET-NEG-NF-INVALID-PREFIX`
- Проверка: `kacho-api-gateway/internal/opsproxy/proxy.go` (`resolveBackend`); см. `07-known-divergences.md`, раздел 2.
- Divergence: исторически возвращалось `400 "unknown prefix"` для любого нероутируемого id — приведено к контракту Kachō.

### REQ-OPS-03 — ListOperations<Resource>: содержит create-op [P1]
`<Resource>.ListOperations(<id>)` после Create → список содержит create-Operation. Несуществующий parent → `404` или пустой.
- Validated-by: `*-LOP-CRUD-OK`, `*-LOP-NEG-PARENT-NF`
- Проверка: `internal/service/*.go` ListOperations (filter по resource_id в `operations`).

### REQ-OPS-04 — история операций переживает удаление ресурса [P1]
После `Delete` ресурса его операции НЕ удаляются: `<Resource>.ListOperations(<deleted-id>)` все еще
возвращает историю (create + delete), `OperationService.Get(<opId>)` по операции удаленного ресурса → 200.
Таблица `operations` не имеет FK-cascade от ресурсных таблиц.
- Validated-by: `NET-LISTOPS-AFTER-DELETE-OK`, `OP-LIST-AFTER-DELETE-OK`
- Проверка: `internal/migrations/0001_initial.sql` (`operations` без FK на ресурсы); `internal/repo/*.go` Delete (`DELETE FROM <table>` — не трогает `operations`); `kacho-corelib/operations` Repo.

---

## J. AuthZ / tenant isolation

### REQ-AUTHZ-01 — Get/Update/Delete/Move/AddCidr/.../UpdateRules: cross-tenant → PERMISSION_DENIED [P0]
RPC, оперирующие конкретным ресурсом, ДОЛЖНЫ проверять, что `resource.project_id` принадлежит caller'у;
чужой ресурс → `PERMISSION_DENIED` (в `dev`-mode AuthN permissive — anonymous=admin; в `production`/`production-strict` fail-closed).
- Validated-by: `*-AUTHZ-NF-SYNC` (Get/Update/Delete/Move/UpdateRule/UpdateRules), `*-AUTHZ-EMPTY-PROJECT-HEADER`; **gap** — полноценная cross-tenant matrix с двумя header-set'ами (см. `REQUIREMENTS.md` REQ-006)
- Проверка: `internal/handler/*.go` — `AssertProjectOwnership` после `repo.Get`; `internal/handler/tenant_interceptor.go`; `internal/config/config.go` `AuthMode`.

### REQ-AUTHZ-02 — List: project isolation [P0]
Ресурс в project A не виден в `List` по project B.
- Validated-by: `*-LST-AUTHZ-CROSS-PROJECT-ISOLATION`, `*-LST-CRUD-OK`
- Проверка: `internal/repo/*.go` `List` — `WHERE project_id = $1`.

### REQ-AUTHZ-03 — мутация несуществующего ресурса → sync ошибка, не async [P1]
`Update`/`Delete`/`Move`/`AddCidrBlocks`/... несуществующего → sync `NOT_FOUND`/`PERMISSION_DENIED` (через `repo.Get`+`AssertProjectOwnership` до Operation), а не Operation, которая потом падает. Часть контракта Kachō.
- Validated-by: `*-UPD-AUTHZ-NF-SYNC`, `*-DEL-AUTHZ-NF-SYNC`, `*-MV-AUTHZ-NF-SYNC`, `*-UR-AUTHZ-NF-SYNC`, `*-URL-AUTHZ-NF-SYNC`, `*-UPD-CONF-NF-TEXT`, `*-DEL-CONF-NF-TEXT`, `*-MV-CONF-NF-TEXT`
- Проверка: `internal/service/*.go` — `repo.Get` ДО `operations.New` для не-Create мутаций.

### REQ-AUTHZ-04 — GetByValue: no info-leak (404 для чужого и несуществующего) [P0]
`Address.GetByValue` чужого (cross-tenant) Address И несуществующего IP дают **одинаковый** `NOT_FOUND` — нельзя по коду ответа пробить, какие IP выделены.
- Validated-by: `*-GBV-CONF-NOLEAK-FOR-EXISTING-OTHER`, `*-GBV-NEG-NF`
- Проверка: `internal/service/address.go` GetByValue — cross-tenant и not-found сливаются в `ErrNotFound`.

---

## K. Security probes (resilience)

### REQ-SEC-01 — injection-payloads в полях не вызывают 5xx [P0]
`name`/`description`/`labels`/`filter` с SQLi / XSS / cmd-injection / path-traversal / null-byte / union / long-payload →
обработано (`InvalidArgument`/`200`), **никогда** `500`/`Internal` с утечкой стектрейса/SQLSTATE.
- Validated-by: `*-CR-SEC-SQLI`/`-XSS`/`-CMD`/`-PATH`/`-NULLBYTE`/`-UNION`/`-LONGPAYLOAD`, `*-LST-SEC-FILTER-SQLI`
- Проверка: параметризованные запросы (pgx) во всех `internal/repo/*.go`; `mapRepoErr` — generic `"internal database error"`, без сырого pgx-текста; то же для Internal handlers (`internalMapErr`).

### REQ-SEC-02 — HTTP-метод/Content-Type robustness [P3]
`PUT`/`HEAD`/`DELETE` на List-endpoint → `405` или `404` (не 500). `POST` без `Content-Type` → `400`/`415`/`200` (lenient).
- Validated-by: `*-METHOD-PUT-NOT-ALLOWED`, `*-METHOD-DELETE-LIST`, `*-METHOD-NOT-ALLOWED`, `*-HEADERS-MISSING-CT`
- Проверка: api-gateway routing (grpc-gateway mux).

---

## L. SecurityGroup rules

### REQ-SG-01 — UpdateRules / UpdateRule: модификация правил [P1]
`UpdateRules` (batch) и `UpdateRule` (single) добавляют/меняют правила; результат виден в `Get`.
`UpdateRule` несуществующего `rule_id` → `NOT_FOUND`.
- Validated-by: `*-URL-CRUD-OK`, `*-UR-CRUD-OK`, `*-UR-NEG-RULE-NF`
- Проверка: `internal/service/security_group.go` UpdateRules/UpdateRule.

### REQ-SG-02 — rule-field валидация [P1]
Правило: `direction` ∈ {INGRESS,EGRESS} (иначе `400`); `protocol` — известный (иначе `400`); порт ∈ [-1.65535]
(`-1` = any; отрицательный кроме `-1` или > 65535 → `400`).
- Validated-by: `*-URL-VAL-DIRECTION-UNKNOWN`, `*-URL-VAL-PROTOCOL-UNKNOWN`, `*-URL-VAL-PORT-ANY-MINUS-1`, `*-URL-VAL-PORT-NEG`, `*-URL-VAL-PORT-OVER-65535`
- Проверка: `internal/service/security_group.go` — rule-валидация; `sgDirection`/`sgStatus` в `internal/protoconv/protoconv.go`.

### REQ-SG-03 — optimistic concurrency для UpdateRules (xmin) [P1]
Конкурентные `UpdateRules` на одну SG не теряют изменения — read-modify-write через Postgres `xmin::text` (lost-update protection).
- Validated-by: **gap — нет concurrency-кейса в newman**; покрыто integration-тестом `security_group_occ_integration_test.go`
- Проверка: `internal/repo/security_group_repo.go` — `SELECT ..., xmin::text` / `UPDATE ... AND xmin::text = $`.

### REQ-SG-RULE-SAME-NETWORK — SG-target rule только в пределах той же Network [P1]
SG-target rule (`oneof target = security_group_id`) разрешен **только** если target-SG в той же
`Network`, что и редактируемая SG (SG разных сетей физически изолированы). Cross-network target →
`INVALID_ARGUMENT` «`security group rule can only reference a security group in the same network`».
Несуществующая target-SG → `INVALID_ARGUMENT` «`security group rule references a non-existent security
group`» (НЕ `NOT_FOUND`). Оба несут `google.rpc.BadRequest.field_violations[].field` (напр.
`rule_specs[0].security_group_id` / `addition_rule_specs[0].security_group_id` / `security_group_id`).
Sync fast-fail (Operation НЕ создается). Применяется на `Create` (`rule_specs`), `UpdateRules`
(`addition_rule_specs`), `UpdateRule` (смена target). CIDR-rule / `predefined_target` — не затронуты.
Same-network target → OK. Валидация на service-слое (достаточна без нормализованной
rule-target-таблицы).
- Validated-by: `SG-NET-07-NEG-RULE-CROSS-NETWORK-CREATE`, `SG-NET-08-RULE-SAME-NETWORK-OK`,
 `SG-NET-09-NEG-RULE-CROSS-NETWORK-UPDATERULES`, `SG-NET-09-RULE-SAME-NETWORK-UPDATERULES-OK`
 (UpdateRule cross-network 10 + target-notfound 11 — integration-only).
- Проверка: `internal/apps/kacho/api/securitygroup/{create,update_rules,update_rule}.go` — same-network
 reader + `BadRequest.field_violations`.

### REQ-SG-MOVE-NETWORK-BOUND — Move network-bound SG между проектами запрещен [P1]
`SecurityGroup.Move` (cross-project) для SG, привязанной к `Network` (в новой модели — все SG), →
sync `FAILED_PRECONDITION` «`security group cannot be moved between projects while bound to a network`».
SG неизменна (`project_id`/`network_id`). Guard в `MoveSecurityGroupUseCase` срабатывает **до**
`checkMoveDestination` (same-project check) и до создания Operation. Обоснование: `network_id`
mandatory+immutable, Network привязана к проекту → cross-project Move сделал бы `network_id` dangling.
- Validated-by: `SG-NET-19-NEG-MOVE-FORBIDDEN` (cross-project), `SG-MV-CRUD-OK` (same-project —
 guard precedence над same-project check).
- Проверка: `internal/apps/kacho/api/securitygroup/move.go` — `if cur.NetworkID != "" { … FailedPrecondition }`
 до `checkMoveDestination`.

### REQ-SG-DEL-NIC-REFCHECK — SG, прилинкованный к NIC, нельзя удалить [P0]
SecurityGroup, к которой один или более `NetworkInterface` ссылается через `security_group_ids[]`,
**нельзя** удалить. Попытка `SecurityGroup.Delete` обязана отдать `FAILED_PRECONDITION` (HTTP 400
sync, либо op.error.code=9 async). Для удаления — сначала detach SG из всех NIC
(`Update NIC` mask=`securityGroupIds`).
Способ enforcement — **DB-уровень** (within-service refs выражаются на стороне БД,
software-side TOCTOU-precheck запрещен within-service инвариантом на DB-уровне). Текущий gap: для
JSONB-массива `security_group_ids` ссылочный constraint еще не оформлен (нет FK / trigger).
- Validated-by: `SG-DEL-NEG-NIC-ATTACHED` (**persistent-RED** (verifies [#27](https://github.com/PRO-Robotech/kacho-vpc/issues/27)) до DB-trigger)
- Blocked by: (within-service refs through DB)
- Проверка: `internal/migrations/*.sql` — миграция, выражающая инвариант (BEFORE DELETE
 trigger на `security_groups`, проверяющий `NOT EXISTS … FROM network_interfaces WHERE
 security_group_ids ? sg.id`, либо эквивалент); `internal/repo/security_group_repo.go` —
 маппинг SQLSTATE → `mapRepoErr` (`23P01` / custom RAISE → `FailedPrecondition`).

### REQ-NET-LSG-DEFAULT — default SG живет ровно жизненный цикл Network [P1]
При `Network.Create` (когда `KACHO_VPC_DEFAULT_SG_INLINE=true`, default) автоматически
создается default SecurityGroup (`default_for_network=true`), ее id виден в
`Network.default_security_group_id`. При `Network.Delete` default SG **удаляется** —
explicit `GET /securityGroups/{defSgId}` после Network.Delete обязан вернуть `NOT_FOUND`.
- Validated-by: `*-LSG-CRUD-DEFAULT-SG` (create-side), `NET-DEL-CRUD-DEFAULT-SG-REMOVED` (delete-side).
- Проверка: `internal/service/network.go::doCreate` (inline `CreateDefaultForNetwork`);
 `internal/service/network.go::doDelete` (predeletion default-SG cleanup).

### REQ-RT-SUBNET-AUTO-ASSOC — RouteTable.Create auto-associate Subnet'ы сети [P1]
При `RouteTable.Create` с `network_id`, все `Subnet`-ы этой сети, у которых еще **не задан**
свой `route_table_id` (NULL), ДОЛЖНЫ автоматически получить `route_table_id` = id новой RT.
Subnet, у которого `route_table_id` уже задан клиентом, изменяться НЕ должен (explicit
user choice имеет приоритет).
- Validated-by: `RT-CR-STATE-SUBNET-AUTO-ASSOC` (green), integration
 `route_table_auto_association_integration_test.go::TestIntegration_VPC_AutoAssociation_RT_AutoAssoc_Subnets`.
- Проверка: `internal/migrations/0019_vpc_auto_associations.sql` — `rt_auto_assoc_subnets_trg`
 AFTER INSERT ON route_tables (PL/pgSQL `UPDATE subnets SET route_table_id = NEW.id WHERE network_id = NEW.network_id AND route_table_id IS NULL`).

### REQ-SUB-AUTO-PICK-RT — Subnet.Create auto-pick RT, если она есть [P1]
Если в сети уже существует одна или несколько `RouteTable`, и клиент создает `Subnet` **без**
явного `route_table_id`, продукт ДОЛЖЕН подставить id самой ранней по `created_at` RouteTable.
Если RT нет — `route_table_id` остается NULL (auto-assoc сработает позже при RT.Create — см.
REQ-RT-SUBNET-AUTO-ASSOC). Explicit `route_table_id` от клиента имеет приоритет.
- Validated-by: `SUB-CR-STATE-AUTO-PICK-RT`, integration
 `route_table_auto_association_integration_test.go::TestIntegration_VPC_AutoAssociation_Subnet_AutoPick_RT`.
- Проверка: `internal/migrations/0019_vpc_auto_associations.sql` — `subnet_auto_pick_rt_trg`
 BEFORE INSERT ON subnets (PL/pgSQL `IF NEW.route_table_id IS NULL THEN SELECT id INTO ... FROM route_tables WHERE network_id = NEW.network_id ORDER BY created_at ASC LIMIT 1`).

### REQ-RT-DEL-CLEANUP-FK — RouteTable.Delete очищает subnet.route_table_id [P1]
При `RouteTable.Delete` все `Subnet`-ы, ссылающиеся на эту RT через `route_table_id`, ДОЛЖНЫ
получить `route_table_id = NULL` (не оставлять dangling ref). Реализуется DB-уровневым FK
`subnets.route_table_id → route_tables(id) ON DELETE SET NULL` — никакой service-логики
не требуется.
- Validated-by: integration `route_table_auto_association_integration_test.go::TestIntegration_VPC_AutoAssociation_RT_Delete_FK_SetNull`.
- Проверка: `internal/migrations/0019_vpc_auto_associations.sql` — `subnets_route_table_id_fkey`.

### REQ-VPC-OUTBOX-TRIGGER-EMIT — outbox-эмит для triggered UPDATE'ов subnets [P2]
Изменения `subnets.route_table_id`, вызванные DB-trigger'ом (RT auto-assoc / FK SET NULL),
ДОЛЖНЫ эмитить `Subnet.UPDATED` событие в `vpc_outbox` — watch-клиенты должны видеть state
change даже если service-слой не делал прямую UPDATE-операцию. Payload (упрощенный
`jsonb_build_object`) содержит маркер `auto_association: true`.
- Validated-by: integration `route_table_auto_association_integration_test.go::TestIntegration_VPC_AutoAssociation_OutboxEmit_OnTriggeredUpdate`.
- Проверка: `internal/migrations/0019_vpc_auto_associations.sql` — `subnets_outbox_emit_route_table_change_trg`
 AFTER UPDATE OF route_table_id ON subnets (WHEN OLD.route_table_id IS DISTINCT FROM NEW.route_table_id).

---

## M. Conformance: тексты, коды, форматы

### REQ-CONF-01 — канонические тексты ошибок [P1]
Тексты в `google.rpc.Status.message` ДОЛЖНЫ дословно совпадать с каноническим контрактом Kachō: `"Project <X> not found"`,
`"Network <X> not found"`, `"Subnet CIDRs can not overlap"`, `"Invalid subnet state"`, `"<field> is immutable after <Resource>.Create"`,
`"<field> is required"`, `"page_size must be in [0..1000]"`, и т.д. (полный список — `docs/architecture/06-conventions.md`, раздел 3.1).
- Validated-by: `*-CR-CONF-PROJECT-NF-TEXT`/`-NET-NF-TEXT`/`-SUB-NF-TEXT`, `*-GET-CONF-NF-TEXT`/`-FULLTEXT`, `*-UPD-CONF-NF-TEXT`, `*-DEL-CONF-NF-TEXT`, `*-MV-CONF-NF-TEXT`, `*-UPD-STATE-IMMUTABLE-*`
- Проверка: строки в `internal/service/*.go`; сверка с контрактом; идеально — snapshot-differential suite (`REQUIREMENTS.md` REQ-008).

### REQ-CONF-02 — created_at truncate до секунд [P1]
Все `created_at` в proto-ответах — `timestamppb.New(t.Truncate(time.Second))`; микросекунды не уходят клиенту.
- Validated-by: косвенно `*-CR-CRUD-OK`/`*-GET-CRUD-OK` (если кейс ассертит формат); явный кейс — желательно добавить
- Проверка: `internal/protoconv/protoconv.go` — `ts(t)` хелпер во всех конвертерах; unit-тест `protoconv_test.go::TestCreatedAt_TruncatedToSeconds`.

### REQ-CONF-03 — status-code mapping [P0]
Маппинг ошибок → gRPC-коды по таблице (`06-conventions.md` / `docs/architecture/06-conventions.md`, раздел 3.3):
NotFound→`NOT_FOUND`, AlreadyExists→`ALREADY_EXISTS`, CIDR overlap/FK/relocate-blocked/deletion_protection→`FAILED_PRECONDITION`,
поля/mask/page_size→`INVALID_ARGUMENT`, project-check-unavailable→`UNAVAILABLE`, repo-error→`INTERNAL` (generic, без leak).
- Validated-by: все `*-NEG-*`/`*-VAL-*` кейсы (ассертят grpc-код)
- Проверка: `internal/service/network.go::mapRepoErr` + `internal/handler/internal_maperr.go`.

### REQ-CONF-04 — id-syntax sync-валидация [P1]
Каждый id-берущий RPC первым стейтментом вызывает `corevalidate.ResourceID(resourceType, ids.PrefixXxx, id)`:
malformed / нераспознанный resource-id (нет известного 3-char prefix `b1g/bpf/enp/e9b/epd/fd8`) → sync `InvalidArgument "invalid <res> id '<X>'"`
(контракт Kachō); well-formed-но-несуществующий (известный prefix) → `NotFound` через `repo.Get`. Семантика family-agnostic.
- Validated-by: `*-GET-NEG-NF`, `*-GET-NEG-NOT-FOUND`, `*-UPD-NEG-NF-INVALID-PREFIX`, `*-DEL-NEG-NF-INVALID-PREFIX`
- Проверка: `corevalidate.ResourceID` вызывается первым стейтментом в `internal/service/*.go` для каждого id-берущего RPC; `06-conventions.md` gotcha #1.
- Divergence: нет — выровнено с контракт Kachō (`` закрыт).

### REQ-CONF-05 — REST-пути стабильны и не нормализуются [P2]
REST-пути (`google.api.http` в `kacho-proto`): kebab у custom-методов (`:add-cidr-blocks`,`:move`), snake у child-list
(`security_groups`,`route_tables`), camel у top-level, `/operations/{id}` без `/vpc/v1/`. НЕ «причесывать» — это осознанный выбор контракта Kachō.
- Validated-by: косвенно — все REST-кейсы используют эти пути; явный — `04-api-surface.md`
- Проверка: `google.api.http`-аннотации в `kacho-proto/.../<res>_service.proto`; `07-known-divergences.md`, раздел 1.
- Divergence: видимая «неоднородность» — by-design (контракт Kachō).

---

## N. Move semantics

### REQ-MOVE-01 — Move в другой project обновляет project_id [P1]
`Move(destination_project_id)` существующего ресурса → `project_id` обновлен; ресурс виден в `List` нового project.
- Validated-by: `*-MV-CRUD-OK`
- Проверка: `internal/service/*.go` doMove.

### REQ-MOVE-02 — Move: destination / resource NotFound / отсутствует [P1]
`Move` в несуществующий project → sync `NOT_FOUND "Project <X> not found"`.
`Move` без `destination_project_id` → sync `InvalidArgument`. `Move` несуществующего ресурса
(well-formed id) → sync `NOT_FOUND "<Resource> ... not found"` (— Move делает sync
`repo.Get`). `Move` в текущий project → см. REQ-RES-06.
- Validated-by: `*-MV-NEG-DEST-PROJECT-NF`, `*-MV-VAL-NO-DEST`, `*-MV-AUTHZ-NF-SYNC`, `*-MV-CONF-NF-TEXT`
- Проверка: `internal/service/*.go` Move — sync `repo.Get` → `checkMoveDestination` ДО `operations.New`.

---

## O. NetworkInterface (NIC) — first-class ресурс (эпик)

### REQ-NIC-01 — NIC — project-scoped ресурс, принадлежит Subnet [P1]
`NetworkInterface` — публичный project-scoped ресурс (`project_id` обязателен), принадлежит `Subnet`
(`subnet_id` обязателен). Полный CRUD (`Get`/`List`/`Create`/`Update`/`Delete`) + `ListOperations`.
`Create` с garbage `subnet_id` → async `NotFound "Subnet ... not found"`. REST: `/vpc/v1/networkInterfaces`.
- Validated-by: `NIC-CR-CRUD-OK`, `NIC-CR-NEG-BAD-SUBNET`, `NIC-LIST-OK`, `NIC-DEL-OK`
- Проверка: `kacho-proto/.../network_interface_service.proto`; `internal/service/network_interface.go`; `cmd/vpc/main.go` (регистрация).

### REQ-NIC-02 — Delete NIC освобождает референсные Address [P1]
`Delete` не-приаттаченного NIC → Operation → NIC исчезает; привязанные через `v4_address_ids`/`v6_address_ids`
Address освобождаются (`Address.used` → false, referrer снят).
- Validated-by: `NIC-DEL-OK`
- Проверка: `internal/service/network_interface.go` doDelete — снятие referrer'ов / `addresses.used`.

### REQ-NIC-03 — Attach/Detach: `used_by` зеркалит привязку; приаттаченный NIC нельзя удалить [P1]
`AttachToInstance` → `used_by` = `{compute_instance, <instance_id>}`; `DetachFromInstance` → `used_by` очищен.
`Delete` NIC с непустым `used_by` (приаттачен) → `FailedPrecondition` (сначала Detach).
- Validated-by: `NIC-ATTACH-DETACH-OK`, `NIC-DEL-NEG-ATTACHED`
- Проверка: `internal/service/network_interface.go` Attach/Detach (flat-колонки `used_by_*` на `network_interfaces`); doDelete — guard на `used_by`.

### REQ-NIC-04 — NIC ссылается на Address по id; занятый Address нельзя удалить [P0]
`NetworkInterface` ссылается на `Address`-ресурсы по id: `v4_address_ids[]` / `v6_address_ids[]`.
Один `Address` — максимум на одном NIC (enforced сервис-слоем: `addresses.used` + referrer-tracking).
`Create` NIC с предсозданными v4/v6 internal Address → 200, address(а) привязаны. `Address.Delete`
референсимого NIC'ом адреса → `FailedPrecondition` (REQ-DEL-09).
- Validated-by: `NIC-CR-WITH-ADDR-OK`, `NIC-CR-WITH-V6-ADDR-OK`, `ADDR-DEL-NEG-USED-BY-NIC`
- Проверка: `internal/service/network_interface.go` Create/Attach (referrer-tracking, как `address_references`); `internal/service/address.go` Delete (referrer-check).

### REQ-NIC-05 — NIC несет `security_group_ids[]` [P2]
`NetworkInterface` несет `security_group_ids[]` — ссылки на существующие SG. `Create` NIC с такой SG → 200.
(SG теперь mandatory-network — REQ-RES-07; кейс создает SG, привязанную к сети NIC'а. NIC сам
network-bound валидацию SG-привязки не навязывает — проверяет лишь существование SG.)
- Validated-by: `NIC-CR-WITH-UNBOUND-SG-OK`
- Проверка: `internal/service/network_interface.go` Create/Update — валидация существования SG.

### REQ-NIC-06 — проекция NIC — lean (control-plane only, без инфра-полей) [P0]
`NetworkInterface` (`Get`/`List`/`Create`-result) содержит ТОЛЬКО `id`/`project_id`/`name`/
`labels`/`subnet_id`/`v4_address_ids`/`v6_address_ids`/`security_group_ids`/`used_by`/`mac_address`/`status`.
Инфра/data-plane-полей у `kacho-vpc` нет — они появятся на стороне будущего data-plane сервиса
`kacho-vpc-implement`. Регрессионное требование: публичная NIC НИКОГДА не должна нести инфра-чувствительные
поля (placement, SRv6-SID, host-wiring) — защита от случайного reintroduce.
- Validated-by: `NIC-LIST-OK`, `NIC-CR-CRUD-OK` (assert «no infra-sensitive fields»)
- Проверка: `kacho-proto/.../network_interface_service.proto` (public `NetworkInterface` без инфра-полей); `internal/handler/network_interface_handler.go` (public mapper не выставляет инфра-поля); разделом про инфра-чувствительные данные.

### REQ-NIC-07 — Update NIC: меняются mutable-поля, subnet_id/инфра — нет [P1]
`Update` NIC через mask (`name`/`labels`/`security_group_ids`) → Operation → новые значения видны;
`subnet_id` — immutable (в mask → `InvalidArgument`); инфра-поля недоступны для записи через публичный API.
- Validated-by: `NIC-UPD-OK`
- Проверка: `internal/service/network_interface.go` Update — `subnet_id` в hard-immutable reject-switch; mask-применение только к mutable.

### REQ-NIC-08 — NIC.mac_address — output-only, стабилен, cloud-wide unique [P1]
`mac_address` на публичной `NetworkInterface` (самостоятельный сетевой интерфейс): аллоцируется системой
при `NetworkInterfaceService.Create`, **клиент задать не может**, **неизменен** на протяжении
жизни NIC (Attach/Detach/Update name/labels/SG не меняют MAC), **уникален в пределах всего
облака** (DB-level UNIQUE-constraint). Формат — lowercase, colon-separated, 6 октетов;
префикс `0e:` (locally-administered, unicast) зарезервирован под Kachō — все наши MAC начинаются
с него; остальные 5 байт — `crypto/rand` (40 бит энтропии); коллизии ловятся UNIQUE-constraint'ом
и retry'ятся в service-слое (до 3 попыток).
- Validated-by: `NIC-CR-MAC-OK` (формат + стабильность при Update)
- Проверка: `kacho-proto/.../network_interface.proto` (field 19, mac_address); `internal/migrations/0014_nic_mac_address.sql` (UNIQUE + backfill); `internal/service/mac.go` (`GenerateMAC` + префикс `0e:`); `internal/service/network_interface.go` (retry-loop в `doCreate`); `internal/repo/network_interface_repo.go` (Insert + isNICMacCollision).

---

## Покрытие регламента (gaps)

| REQ | Статус кейс-покрытия | Тикет |
|---|---|---|
| REQ-IPAM-03 (allocator race-free) | gap в newman (есть integration `ipam_cascade_integration_test.go`) | `REQUIREMENTS.md` REQ-007 (backlog) |
| REQ-SG-03 (xmin OCC) | gap в newman (есть integration `security_group_occ_integration_test.go`) | — |
| REQ-AUTHZ-01 (полная cross-tenant matrix) | частично (`*-AUTHZ-NF-SYNC`, нет two-header-set прогона) | `REQUIREMENTS.md` REQ-006 (backlog) |
| REQ-DEL-05 (deletion_protection кейс) | gap — нет явного кейса в индексе | добавить `*-DEL-NEG-DELETION-PROTECTION` |
| REQ-CONF-01 (канонические тексты byte-level) | частично (CONF-кейсы есть; нет snapshot-differential) | `REQUIREMENTS.md` REQ-008 (backlog) |
| REQ-CONF-02 (created_at явный кейс) | косвенно (нет отдельного assert-кейса формата) | добавить `*-CR-CONF-CREATED-AT-SECONDS` |

---

## Связанные документы

- `CASES-INDEX.md` — каталог тест-паттернов (источник, из которого выведены REQ).
- `TAXONOMY.md` — классы кейсов + «Применение по методам» (обязательные классы по RPC).
- `TEST-PLAN.md` — карта `(RPC × класс) → статус покрытия`.
- `REQUIREMENTS.md` — бэклог *улучшений* (не нормативный).
- `docs/architecture/07-known-divergences.md` — намеренные расхождения с контрактом (исключения из регламента).
- `docs/architecture/06-conventions.md` — справочник по конвенциям контракта Kachō.
