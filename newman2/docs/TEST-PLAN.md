# Test Plan — newman2 coverage map (актуально на 2026-05-11)

Карта `(сервис, RPC) → классы → факт реализации`. Статусы:
`□` не начато, `◐` частично (есть happy ИЛИ negative), `■` базовое
покрытие (≥1 happy + ≥1 negative), `▣` расширенное (с BVA/STATE/CONF).

## NetworkService (10 RPC)

| RPC | Классы покрыто | Cases | Статус |
|---|---|---|---|
| Get | NEG (garbage→404), CONF, AUTHZ | NET-GET-NEG-NF, NET-GET-NEG-EMPTY-ID | ▣ |
| List | CRUD, VAL (folder req), AUTHZ, PAGE (4 BVA + token) | NET-LST-* (5) | ▣ |
| Create | CRUD, VAL (folder req), NEG (folder NF, dup name), CONC | NET-CR-* (4) | ▣ |
| Update | CRUD (desc patch), STATE (immutable folder_id), AUTHZ (sync-NF) | NET-UPD-* (3) | ▣ |
| Delete | NEG (sync-NF) | NET-DEL-AUTHZ-NF-SYNC | ◐ |
| Move | CRUD-OK, NEG (dest-NF) | NET-MV-* (2) | ■ |
| ListSubnets | CRUD (empty) | NET-LSUB-CRUD-EMPTY | ◐ |
| ListSecurityGroups | CRUD (default-SG check) | NET-LSG-CRUD-DEFAULT-SG | ◐ |
| ListRouteTables | CRUD (empty) | NET-LRT-CRUD-EMPTY | ◐ |
| ListOperations | CRUD (contains create-op) | NET-LOP-CRUD-OK | ◐ |

**Coverage: 10/10 RPC (100%) хотя бы 1 кейс.**

## SubnetService (11 RPC)

| RPC | Классы | Cases | Статус |
|---|---|---|---|
| Get | NEG | SUB-GET-NEG-NOT-FOUND | ◐ |
| List | CRUD, VAL (folder req) | SUB-LST-* (2) | ■ |
| Create | CRUD, VAL (zone req, zone unknown, cidr req, hostbits), NEG (net NF, overlap) | SUB-CR-* (7) | ▣ |
| Update | AUTHZ-sync-NF, STATE (immutable CIDR) | SUB-UPD-* (2) | ■ |
| Delete | AUTHZ-sync-NF | SUB-DEL-AUTHZ-NF-SYNC | ◐ |
| Move | (planned) | — | □ |
| AddCidrBlocks | CRUD-OK | SUB-ACB-CRUD-OK | ◐ |
| RemoveCidrBlocks | (planned) | — | □ |
| Relocate | NEG (in-use, "Invalid subnet state") | SUB-REL-NEG-IN-USE | ◐ |
| ListUsedAddresses | CRUD (empty) | SUB-LUA-CRUD-OK | ◐ |
| ListOperations | CRUD | SUB-LOP-CRUD-OK | ◐ |

**Coverage: 9/11 RPC (82%).**

## AddressService (9 RPC)

| RPC | Классы | Cases | Статус |
|---|---|---|---|
| Get | NEG | ADR-GET-NEG-NF | ◐ |
| List | CRUD, VAL (folder req) | ADR-LST-* (2) | ■ |
| Create | CRUD (int + ext), VAL (oneof, both-spec), NEG (subnet NF) | ADR-CR-* (5) | ▣ |
| Update | AUTHZ-sync-NF | ADR-UPD-AUTHZ-NF-SYNC | ◐ |
| Delete | AUTHZ-sync-NF | ADR-DEL-AUTHZ-NF-SYNC | ◐ |
| Move | (planned) | — | □ |
| GetByValue | NEG (security-NF) | ADR-GBV-NEG-NF | ◐ |
| ListBySubnet | CRUD | ADR-LBS-CRUD-OK | ◐ |
| ListOperations | CRUD | ADR-LOP-CRUD-OK | ◐ |

**Coverage: 8/9 RPC (89%).**

## RouteTableService (7 RPC)

| RPC | Классы | Cases | Статус |
|---|---|---|---|
| Get | NEG | RT-GET-NEG-NF | ◐ |
| List | CRUD, VAL (folder req) | RT-LST-* (2) | ■ |
| Create | CRUD, VAL (network req), NEG (network NF) | RT-CR-* (3) | ▣ |
| Update | AUTHZ-sync-NF | RT-UPD-AUTHZ-NF-SYNC | ◐ |
| Delete | AUTHZ-sync-NF | RT-DEL-AUTHZ-NF-SYNC | ◐ |
| Move | (planned) | — | □ |
| ListOperations | CRUD | RT-LOP-CRUD-OK | ◐ |

**Coverage: 6/7 RPC (86%).**

## SecurityGroupService (9 RPC)

| RPC | Классы | Cases | Статус |
|---|---|---|---|
| Get | NEG | SG-GET-NEG-NF | ◐ |
| List | CRUD, VAL (folder req) | SG-LST-* (2) | ■ |
| Create | CRUD, VAL (network req) | SG-CR-* (2) | ■ |
| Update | AUTHZ-sync-NF | SG-UPD-AUTHZ-NF-SYNC | ◐ |
| Delete | AUTHZ-sync-NF | SG-DEL-AUTHZ-NF-SYNC | ◐ |
| Move | (planned) | — | □ |
| UpdateRules | CRUD, STATE (rules append) | SG-URL-CRUD-OK | ◐ |
| UpdateRule | (planned) | — | □ |
| ListOperations | CRUD | SG-LOP-CRUD-OK | ◐ |

**Coverage: 7/9 RPC (78%).**

## GatewayService (7 RPC)

| RPC | Классы | Cases | Статус |
|---|---|---|---|
| Get | NEG | GW-GET-NEG-NF | ◐ |
| List | CRUD, VAL | GW-LST-* (2) | ■ |
| Create | CRUD, VAL (folder req) | GW-CR-* (2) | ■ |
| Update | AUTHZ-sync-NF | GW-UPD-AUTHZ-NF-SYNC | ◐ |
| Delete | AUTHZ-sync-NF | GW-DEL-AUTHZ-NF-SYNC | ◐ |
| Move | (planned) | — | □ |
| ListOperations | CRUD | GW-LOP-CRUD-OK | ◐ |

**Coverage: 6/7 RPC (86%).**

## PrivateEndpointService (6 RPC)

| RPC | Классы | Cases | Статус |
|---|---|---|---|
| Get | NEG | PE-GET-NEG-NF | ◐ |
| List | CRUD, VAL | PE-LST-* (2) | ■ |
| Create | VAL (folder req), NEG (net NF) | PE-CR-* (2) | ◐ (нет CRUD-OK — ObjectStorage seed нужен) |
| Update | AUTHZ-sync-NF | PE-UPD-AUTHZ-NF-SYNC | ◐ |
| Delete | AUTHZ-sync-NF | PE-DEL-AUTHZ-NF-SYNC | ◐ |
| ListOperations | (planned) | — | □ |

**Coverage: 5/6 RPC (83%).**

## OperationService (1 RPC)

| RPC | Классы | Cases | Статус |
|---|---|---|---|
| Get | CRUD, NEG (invalid prefix → 400, valid prefix → 404) | OP-GET-* (3) | ▣ |

**Coverage: 1/1 RPC (100%).**

---

## Сводное покрытие

| Метрика | Значение |
|---|---|
| Публичных RPC | 60 |
| Покрыто (≥1 кейс) | 52 |
| **API surface coverage** | **87%** |
| Cases реализовано | 89 |
| Assertions выполняется | 467 |
| Passing | 467 |
| Pass rate | 100% |

## Backlog (приоритетный, для v2)

| Зона | RPC / класс | Priority |
|---|---|---|
| Move для всех ресурсов | Subnet/Address/RT/SG/GW Move CRUD + NEG | P1 |
| UpdateMask exhaustive | Update * с unknown field, immutable, empty mask | P1 |
| Pagination roundtrip | List * с page_token next-cycle | P2 |
| Filter syntax | List * с filter expression | P2 |
| Cross-folder AUTHZ | PERMISSION_DENIED matrix | P0 |
| Concurrency P0 | Allocator race, parallel Create same name | P0 |
| Subnet RemoveCidrBlocks | CRUD + NEG-CANNOT-REMOVE-LAST | P1 |
| SG UpdateRule single | CRUD + NEG-RULE-NF | P1 |
| Network Delete с детьми | NEG-NETWORK-NOT-EMPTY | P1 |
| PE CRUD happy | Create + Get + Update + Delete full lifecycle | P1 |
| Conformance verbatim YC | byte-level error text (--env yc) | P0 для production |
