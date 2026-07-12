# Copyright (c) PRO-Robotech
# SPDX-License-Identifier: BUSL-1.1

"""External Address zone-coherence cases (ZC-VPC-ADDR-*) — track B GAP-3 (RED → GREEN).

Acceptance: docs/specs/sub-phase-nlb-vpc-zone-coherence-acceptance.md
  * ZC-VPC-ADDR-ZONE-01/02 — external Address (v4/v6) с несуществующей zone_id →
    sync InvalidArgument "unknown zone id '<X>'" (verbatim-зеркало subnet.validateZoneID).
  * ZC-VPC-ADDR-ZONE-03 — существующая zone_id → проходит.
  * ZC-VPC-ADDR-ZONE-04 — anycast (zone_id='') → освобождён от проверки.
Norm: .claude/rules/data-integrity.md §Placement-coherence («непроверенная зона
внешнего адреса — баг»).

Behaviour-level (skill testing-product-coach): negative ассертит ТОЧНУЮ строку
ошибки. RED до фикса rpc-implementer'а: CreateAddressUseCase не валидирует
ExternalSpec.ZoneID через geo → external Address с несуществующей зоной создаётся
(Create отдаёт 200 Operation вместо sync 400) → ZC-VPC-ADDR-ZONE-01/02 красные до GREEN.

REST base: /vpc/v1/addresses
"""

CASES = []

_ADDR = "/vpc/v1/addresses"
# Заведомо несуществующая зона (не в kacho_geo.zones).
_UNKNOWN_ZONE = "zzz-nonexistent-9"
_MSG_UNKNOWN_ZONE = f"unknown zone id '{_UNKNOWN_ZONE}'"


CASES.append(Case(
    # index: ZC-VPC-ADDR-ZONE-01 (placement-coherence GAP-3)
    id="ZC-VPC-ADDR-ZONE-01-NEG-UNKNOWN-ZONE",
    title="Create external IPv4 Address с несуществующей zone_id → sync 400 unknown-zone "
          "(Verifies ZC-VPC-ADDR-ZONE-01)",
    classes=["NEG", "CONF"], priority="P1",
    steps=[
        Step(name="create-unknown-zone-v4", method="POST", path=_ADDR,
             body={"projectId": "{{_suiteProjectId}}", "name": "zc-addr-uz-{{runId}}",
                   "externalIpv4AddressSpec": {"zoneId": _UNKNOWN_ZONE}},
             test_script=[
                 *assert_status(400),
                 *assert_grpc_code(3, "INVALID_ARGUMENT"),
                 f"pm.test('unknown zone id verbatim', () => pm.expect(pm.response.json().message).to.eql({_MSG_UNKNOWN_ZONE!r}));",
             ]),
    ],
))

CASES.append(Case(
    # index: ZC-VPC-ADDR-ZONE-02 (placement-coherence GAP-3, v6 symmetry)
    id="ZC-VPC-ADDR-ZONE-02-NEG-UNKNOWN-ZONE-V6",
    title="Create external IPv6 Address с несуществующей zone_id → sync 400 unknown-zone "
          "(Verifies ZC-VPC-ADDR-ZONE-02)",
    classes=["NEG", "CONF"], priority="P1",
    steps=[
        Step(name="create-unknown-zone-v6", method="POST", path=_ADDR,
             body={"projectId": "{{_suiteProjectId}}", "name": "zc-addr-uz6-{{runId}}",
                   "externalIpv6AddressSpec": {"zoneId": _UNKNOWN_ZONE}},
             test_script=[
                 *assert_status(400),
                 *assert_grpc_code(3, "INVALID_ARGUMENT"),
                 f"pm.test('unknown zone id verbatim', () => pm.expect(pm.response.json().message).to.eql({_MSG_UNKNOWN_ZONE!r}));",
             ]),
    ],
))

CASES.append(Case(
    # index: ZC-VPC-ADDR-ZONE-03 (placement-coherence GAP-3 happy)
    id="ZC-VPC-ADDR-ZONE-03-KNOWN-ZONE-OK",
    title="Create external IPv4 Address с существующей zone_id → проходит existence-check "
          "(Verifies ZC-VPC-ADDR-ZONE-03)",
    classes=["CRUD"], priority="P1",
    steps=[
        Step(name="create-known-zone", method="POST", path=_ADDR,
             body={"projectId": "{{_suiteProjectId}}", "name": "zc-addr-kz-{{runId}}",
                   "externalIpv4AddressSpec": {"zoneId": "{{existingZoneId}}"}},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.addressId", "zcAddrId")]),
        poll_operation_until_done(),
        Step(name="get-known-zone", method="GET", path=f"{_ADDR}/{{{{zcAddrId}}}}",
             test_script=[
                 "if (!pm.environment.get('zcAddrId')) return;",
                 *assert_status(200),
                 "pm.test('has external ipv4', () => pm.expect(pm.response.json().externalIpv4Address).to.be.an('object'));",
             ]),
        Step(name="cleanup-known-zone", method="DELETE", path=f"{_ADDR}/{{{{zcAddrId}}}}",
             test_script=[
                 "if (!pm.environment.get('zcAddrId')) { pm.environment.unset('opId'); return; }",
                 *save_from_response("j.id", "opId"),
             ]),
        poll_operation_until_done(),
    ],
))

CASES.append(Case(
    # index: ZC-VPC-ADDR-ZONE-04 (placement-coherence GAP-3 anycast exempt)
    id="ZC-VPC-ADDR-ZONE-04-ANYCAST-EMPTY-ZONE-OK",
    title="Create external IPv4 Address БЕЗ zone_id (anycast/global) → освобождён от проверки "
          "(Verifies ZC-VPC-ADDR-ZONE-04)",
    classes=["CRUD"], priority="P1",
    steps=[
        Step(name="create-anycast", method="POST", path=_ADDR,
             body={"projectId": "{{_suiteProjectId}}", "name": "zc-addr-any-{{runId}}",
                   "externalIpv4AddressSpec": {}},
             test_script=[
                 "pm.test('empty external zone (anycast) NOT rejected → 200 Operation', () => "
                 "  pm.expect(pm.response.code).to.eql(200));",
                 *save_from_response("j.id", "opId"),
                 *save_from_response("j.metadata && j.metadata.addressId", "zcAddrId"),
             ]),
        poll_operation_until_done(),
        Step(name="cleanup-anycast", method="DELETE", path=f"{_ADDR}/{{{{zcAddrId}}}}",
             test_script=[
                 "if (!pm.environment.get('zcAddrId')) { pm.environment.unset('opId'); return; }",
                 *save_from_response("j.id", "opId"),
             ]),
        poll_operation_until_done(),
    ],
))
