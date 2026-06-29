# Copyright (c) PRO-Robotech
# SPDX-License-Identifier: BUSL-1.1

"""Case-set для SubnetService (kacho-vpc)."""

CASES = []

def _make_net(name_suffix="net"):
    """Helper: набор шагов для создания parent Network + сохранения netId."""
    return [
        Step(
            name="pre-create-net",
            method="POST",
            path="/vpc/v1/networks",
            body={"projectId": "{{_suiteProjectId}}", "name": f"sub-{name_suffix}-{{{{runId}}}}"},
            test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                         *save_from_response("j.metadata && j.metadata.networkId", "netId")],
        ),
        poll_operation_until_done(),
    ]


def _cleanup_net():
    return Step(name="cleanup-net", method="DELETE", path="/vpc/v1/networks/{{netId}}",
                test_script=[*assert_status(200), *save_from_response("j.id", "opId")])


def _cleanup_net_lenient():
    # См. route-table.py::_cleanup_net_lenient — wrap'нутый Create мог пройти permissive'но
    # (subnet создан) → DELETE сети блокируется FK RESTRICT (400). Оба исхода ОК.
    return Step(name="cleanup-net", method="DELETE", path="/vpc/v1/networks/{{netId}}",
                test_script=["pm.test('cleanup net (200 or 400 if child leaked)', () => pm.expect(pm.response.code).to.be.oneOf([200, 400]));",
                             *save_from_response("j.id", "opId")])


# ---------------------------------------------------------------------------
# SUB-CR
# ---------------------------------------------------------------------------

CASES.append(Case(
    id="SUB-CR-CRUD-OK",
    title="Create subnet → Operation → Subnet visible in GET",
    classes=["CRUD"],
    priority="P1",
    steps=[
        *_make_net("cr"),
        Step(
            name="create",
            method="POST",
            path="/vpc/v1/subnets",
            body={
                "projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}",
                "name": "sub-cr-{{runId}}", "zoneId": "{{existingZoneId}}",
                "v4CidrBlocks": ["10.42.0.0/24"],
            },
            test_script=[*assert_status(200), *assert_operation_envelope(),
                         *save_from_response("j.id", "opId"),
                         *save_from_response("j.metadata && j.metadata.subnetId", "subId")],
        ),
        poll_operation_until_done(),
        Step(
            name="get-confirms",
            method="GET",
            path="/vpc/v1/subnets/{{subId}}",
            test_script=[*assert_status(200),
                         "pm.test('cidr matches', () => pm.expect(pm.response.json().v4CidrBlocks).to.include('10.42.0.0/24'));"],
        ),
        Step(name="cleanup-sub", method="DELETE", path="/vpc/v1/subnets/{{subId}}",
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        _cleanup_net(),
    ],
))

# Auto-pick RT при Subnet.Create. Trigger BEFORE INSERT ON subnets
# (`subnet_auto_pick_rt_trg`) выбирает самую раннюю по `created_at` RouteTable в этой
# сети и подставляет в `NEW.route_table_id`, если клиент не задал его явно. Если RT в
# сети нет — поле остается NULL (auto-assoc сработает позже при RT.Create).
CASES.append(Case(
    id="SUB-CR-STATE-AUTO-PICK-RT",
    title="Create Subnet в сети с RT → subnet.route_table_id auto-picked самой ранней RT (DB-trigger)",
    classes=["CRUD", "STATE"], priority="P1",
    steps=[
        *_make_net("autopick"),
        # 1. Создаем RouteTable в этой сети.
        Step(name="cr-rt", method="POST", path="/vpc/v1/routeTables",
             body={"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}",
                   "name": "sub-autopick-rt-{{runId}}", "staticRoutes": []},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.routeTableId", "rtId")]),
        poll_operation_until_done(),
        # 2. Subnet без явного route_table_id — auto-pick подставит rtId.
        Step(name="cr-sub-autopick", method="POST", path="/vpc/v1/subnets",
             body={"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}",
                   "name": "sub-autopick-{{runId}}", "zoneId": "{{existingZoneId}}",
                   "v4CidrBlocks": ["10.246.0.0/24"]},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.subnetId", "subId")]),
        poll_operation_until_done(),
        Step(name="assert-sub-created", method="GET", path="/operations/{{opId}}",
             test_script=["const j = pm.response.json();",
                          "pm.test('Subnet.Create op done no error', () => pm.expect(j.done && !j.error).to.eql(true));"]),
        # 3. Главная проверка: subnet.route_table_id auto-picked.
        Step(name="get-sub-autopicked", method="GET", path="/vpc/v1/subnets/{{subId}}",
             test_script=[
                 *assert_status(200),
                 "const j = pm.response.json();",
                 "pm.test('subnet.route_table_id auto-picked == rtId (DB-trigger)', () => pm.expect(j.routeTableId).to.eql(pm.environment.get('rtId')));",
             ]),
        # Cleanup снизу вверх.
        Step(name="cleanup-sub", method="DELETE", path="/vpc/v1/subnets/{{subId}}",
             test_script=["pm.test('cleanup sub (200/400/404)', () => pm.expect(pm.response.code).to.be.oneOf([200, 400, 404]));",
                          *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="cleanup-rt", method="DELETE", path="/vpc/v1/routeTables/{{rtId}}",
             test_script=["pm.test('cleanup rt (200/400/404)', () => pm.expect(pm.response.code).to.be.oneOf([200, 400, 404]));",
                          *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        _cleanup_net(),
    ],
))

CASES.append(Case(
    id="SUB-CR-VAL-ZONE-REQUIRED",
    title="Create без zone_id → InvalidArgument (zone_id required)",
    classes=["VAL"],
    priority="P0",
    steps=[
        *_make_net("noz"),
        Step(
            name="create-no-zone",
            method="POST",
            path="/vpc/v1/subnets",
            body={"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}",
                  "name": "sub-noz-{{runId}}", "v4CidrBlocks": ["10.0.0.0/24"]},
            test_script=[*assert_status(400), *assert_grpc_code(3, "INVALID_ARGUMENT")],
        ),
        _cleanup_net(),
    ],
))

CASES.append(Case(
    id="SUB-CR-VAL-ZONE-UNKNOWN",
    title="Create с несуществующей зоной → sync 400 INVALID_ARGUMENT \"unknown zone id '...'\"",
    classes=["VAL"],
    priority="P0",
    steps=[
        *_make_net("zu"),
        Step(
            name="create-unknown-zone",
            method="POST",
            path="/vpc/v1/subnets",
            body={"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}",
                  "name": "sub-zu-{{runId}}", "zoneId": "zone-z-fake",
                  "v4CidrBlocks": ["10.0.0.0/24"]},
            # Отказ — flat {code,message} body, не Operation.
            test_script=[*assert_status(400), *assert_grpc_code(3, "INVALID_ARGUMENT"),
                         "pm.test('unknown zone text', () => pm.expect(pm.response.json().message).to.match(/^unknown zone id '.*'$/));"],
        ),
        _cleanup_net(),
    ],
))

CASES.append(Case(
    # v4_cidr_blocks не required — CIDR-less subnet легален; реальный диапазон
    # добавляется позже через :addCidrBlocks.
    id="SUB-CR-NO-CIDR-OK",
    title="Create subnet без v4CidrBlocks → success; get показывает пустой v4CidrBlocks; addCidrBlocks добавляет один",
    classes=["CRUD"],
    priority="P1",
    steps=[
        *_make_net("nocidr"),
        Step(
            name="create-no-cidr",
            method="POST",
            path="/vpc/v1/subnets",
            body={"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}",
                  "name": "sub-nocidr-{{runId}}", "zoneId": "{{existingZoneId}}"},
            test_script=[*assert_status(200), *assert_operation_envelope(),
                         *save_from_response("j.id", "opId"),
                         *save_from_response("j.metadata && j.metadata.subnetId", "subId")],
        ),
        poll_operation_until_done(),
        Step(
            name="get-empty-cidr",
            method="GET",
            path="/vpc/v1/subnets/{{subId}}",
            test_script=[*assert_status(200),
                         "pm.test('v4CidrBlocks empty', () => pm.expect(pm.response.json().v4CidrBlocks || []).to.have.lengthOf(0));"],
        ),
        Step(
            name="add-cidr",
            method="POST",
            path="/vpc/v1/subnets/{{subId}}:add-cidr-blocks",
            body={"v4CidrBlocks": ["10.77.0.0/24"]},
            test_script=[*assert_status(200), *save_from_response("j.id", "opId")],
        ),
        poll_operation_until_done(),
        Step(
            name="get-has-cidr",
            method="GET",
            path="/vpc/v1/subnets/{{subId}}",
            test_script=[*assert_status(200),
                         "pm.test('cidr now present', () => pm.expect(pm.response.json().v4CidrBlocks).to.include('10.77.0.0/24'));"],
        ),
        Step(name="cleanup-sub", method="DELETE", path="/vpc/v1/subnets/{{subId}}",
             test_script=[*save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        _cleanup_net(),
    ],
))

CASES.append(Case(
    id="SUB-CR-V6-OK",
    title="Create subnet с v6CidrBlocks → echoed back в GET",
    classes=["CRUD"],
    priority="P2",
    steps=[
        *_make_net("v6"),
        Step(
            name="create-v6",
            method="POST",
            path="/vpc/v1/subnets",
            body={"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}",
                  "name": "sub-v6-{{runId}}", "zoneId": "{{existingZoneId}}",
                  "v4CidrBlocks": ["10.78.0.0/24"], "v6CidrBlocks": ["fd00:dead:beef::/64"]},
            test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                         *save_from_response("j.metadata && j.metadata.subnetId", "subId")],
        ),
        poll_operation_until_done(),
        Step(
            name="get-v6",
            method="GET",
            path="/vpc/v1/subnets/{{subId}}",
            test_script=[*assert_status(200),
                         "pm.test('v6 cidr echoed', () => pm.expect(pm.response.json().v6CidrBlocks || []).to.include('fd00:dead:beef::/64'));"],
        ),
        Step(name="cleanup-sub", method="DELETE", path="/vpc/v1/subnets/{{subId}}",
             test_script=[*save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        _cleanup_net(),
    ],
))

CASES.append(Case(
    # v6_cidr_blocks soft-immutable: поле принимается в mask (200), но применение —
    # no-op (CIDR меняются через :add/removeCidrBlocks). UpdateSubnetRequest несет
    # поле v6_cidr_blocks, поэтому запрос проходит без ошибки.
    id="SUB-UPD-V6-NOOP",
    title="Update с v6CidrBlocks в body+mask → 200, без ошибки (soft-immutable no-op)",
    classes=["STATE", "CRUD"],
    priority="P2",
    steps=[
        *_make_net("v6upd"),
        Step(
            name="create",
            method="POST",
            path="/vpc/v1/subnets",
            body={"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}",
                  "name": "sub-v6upd-{{runId}}", "zoneId": "{{existingZoneId}}",
                  "v4CidrBlocks": ["10.79.0.0/24"]},
            test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                         *save_from_response("j.metadata && j.metadata.subnetId", "subId")],
        ),
        poll_operation_until_done(),
        Step(
            name="patch-v6",
            method="PATCH",
            path="/vpc/v1/subnets/{{subId}}",
            body={"updateMask": "v6CidrBlocks", "v6CidrBlocks": ["fd00:cafe::/64"]},
            test_script=[*assert_status(200), *save_from_response("j.id", "opId")],
        ),
        poll_operation_until_done(),
        Step(name="assert-no-error", method="GET", path="/operations/{{opId}}",
             test_script=["const j = pm.response.json();",
                          "pm.test('update op done without error', () => pm.expect(j.done && !j.error).to.eql(true));"]),
        Step(name="cleanup-sub", method="DELETE", path="/vpc/v1/subnets/{{subId}}",
             test_script=[*save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        _cleanup_net(),
    ],
))

CASES.append(Case(
    # Address с explicit internal_ipv4 в CIDR-less подсеть → FailedPrecondition
    # "subnet <id> has no IPv4 CIDR" (guard в address.go).
    id="SUB-CR-NEG-ADDR-INTO-CIDRLESS",
    title="Address.Create internal_ipv4 в CIDR-less subnet → 400 FailedPrecondition",
    classes=["NEG", "CONF"],
    priority="P1",
    steps=[
        *_make_net("addrcl"),
        Step(name="create-cidrless-sub", method="POST", path="/vpc/v1/subnets",
             body={"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}",
                   "name": "sub-addrcl-{{runId}}", "zoneId": "{{existingZoneId}}"},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.subnetId", "subId")]),
        poll_operation_until_done(),
        Step(name="addr-into-cidrless", method="POST", path="/vpc/v1/addresses",
             body={"projectId": "{{_suiteProjectId}}", "name": "addr-cl-{{runId}}",
                   "internalIpv4AddressSpec": {"subnetId": "{{subId}}", "address": "10.5.5.5"}},
             test_script=[*assert_status(400), *assert_grpc_code(9, "FAILED_PRECONDITION"),
                          "pm.test('mentions no IPv4 CIDR', () => pm.expect(pm.response.json().message).to.include('no IPv4 CIDR'));"]),
        Step(name="cleanup-sub", method="DELETE", path="/vpc/v1/subnets/{{subId}}",
             test_script=[*save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        _cleanup_net(),
    ],
))

CASES.append(Case(
    id="SUB-CR-VAL-CIDR-HOSTBITS",
    title="Create с host-bits в CIDR (10.0.0.5/24) → InvalidArgument",
    classes=["VAL"],
    priority="P0",
    steps=[
        *_make_net("hb"),
        Step(
            name="create-hostbits",
            method="POST",
            path="/vpc/v1/subnets",
            body={"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}",
                  "name": "sub-hb-{{runId}}", "zoneId": "{{existingZoneId}}",
                  "v4CidrBlocks": ["10.0.0.5/24"]},
            test_script=[*assert_status(400), *assert_grpc_code(3, "INVALID_ARGUMENT")],
        ),
        _cleanup_net(),
    ],
))

CASES.append(Case(
    id="SUB-CR-NEG-NETWORK-NOT-FOUND",
    title="Create в несуществующей network → sync 404 NOT_FOUND",
    classes=["NEG"],
    priority="P0",
    steps=[
        Step(
            name="create",
            method="POST",
            path="/vpc/v1/subnets",
            body={"projectId": "{{_suiteProjectId}}", "networkId": "{{garbageVpcId}}",
                  "name": "sub-nf-{{runId}}", "zoneId": "{{existingZoneId}}",
                  "v4CidrBlocks": ["10.10.0.0/24"]},
            test_script=[*assert_status(404), *assert_grpc_code(5, "NOT_FOUND"),
                         "pm.test('mentions network', () => pm.expect(pm.response.json().message.toLowerCase()).to.include('network'));"],
        ),
    ],
))

CASES.append(Case(
    id="SUB-CR-NEG-CIDR-OVERLAP",
    title="Create двух subnet с пересекающимися CIDR → второй sync 400 FAILED_PRECONDITION",
    classes=["NEG"],
    priority="P0",
    steps=[
        *_make_net("ov"),
        Step(
            name="create-first",
            method="POST",
            path="/vpc/v1/subnets",
            body={"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}",
                  "name": "sub-ov1-{{runId}}", "zoneId": "{{existingZoneId}}",
                  "v4CidrBlocks": ["10.50.0.0/16"]},
            test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                         *save_from_response("j.metadata && j.metadata.subnetId", "subId1")],
        ),
        poll_operation_until_done(),
        Step(
            name="create-second-overlap",
            method="POST",
            path="/vpc/v1/subnets",
            body={"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}",
                  "name": "sub-ov2-{{runId}}", "zoneId": "{{existingZoneId}}",
                  "v4CidrBlocks": ["10.50.5.0/24"]},  # overlaps with /16
            test_script=[*assert_status(400), *assert_grpc_code(9, "FAILED_PRECONDITION"),
                         "pm.test('overlap text', () => pm.expect(pm.response.json().message).to.eql('Subnet CIDRs can not overlap'));"],
        ),
        Step(name="cleanup-sub1", method="DELETE", path="/vpc/v1/subnets/{{subId1}}",
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        _cleanup_net(),
    ],
))

# ---------------------------------------------------------------------------
# SUB-GET / SUB-LST
# ---------------------------------------------------------------------------

CASES.append(Case(
    id="SUB-GET-NEG-NOT-FOUND",
    title="Get malformed id → 400 InvalidArgument 'invalid subnet id'",
    classes=["NEG"],
    priority="P0",
    steps=[
        Step(
            name="get-garbage",
            method="GET",
            path="/vpc/v1/subnets/{{garbageId}}",
            # malformed id (нет известного 3-char префикса) → 400 InvalidArgument
            # "invalid subnet id '<X>'". Проверка family-agnostic.
            test_script=[
                *assert_status(400),
                *assert_grpc_code(3, "INVALID_ARGUMENT"),
                "pm.test('mentions invalid id', () => { const m = pm.response.json().message; pm.expect(m).to.include('invalid'); pm.expect(m).to.include('id'); });",
            ],
        ),
    ],
))

CASES.append(Case(
    id="SUB-LST-CRUD-OK",
    title="List subnets в project → 200",
    classes=["CRUD"],
    priority="P1",
    steps=[
        Step(
            name="list",
            method="GET",
            path="/vpc/v1/subnets?projectId={{_suiteProjectId}}&pageSize=10",
            test_script=[*assert_status(200),
                         "pm.test('subnets array', () => pm.expect(pm.response.json().subnets || []).to.be.an('array'));"],
        ),
    ],
))

CASES.append(Case(
    id="SUB-LST-VAL-PROJECT-REQUIRED",
    title="List без projectId → InvalidArgument",
    classes=["VAL", "AUTHZ"],
    priority="P0",
    steps=[
        Step(name="list-no-project", method="GET", path="/vpc/v1/subnets",
             test_script=[*assert_status(400), *assert_grpc_code(3, "INVALID_ARGUMENT")]),
    ],
))

# ---------------------------------------------------------------------------
# SUB-UPD / SUB-DEL / SUB-MV
# ---------------------------------------------------------------------------

CASES.append(Case(
    id="SUB-UPD-AUTHZ-NF-SYNC",
    title="Update несуществующего → sync 404 от AuthZ-Get",
    classes=["NEG", "AUTHZ"],
    priority="P1",
    steps=[
        Step(name="patch-nx", method="PATCH", path="/vpc/v1/subnets/{{garbageVpcId}}",
             body={"updateMask": "description", "description": "x"},
             test_script=[*assert_status(404), *assert_grpc_code(5, "NOT_FOUND")]),
    ],
))

CASES.append(Case(
    id="SUB-UPD-STATE-IMMUTABLE-CIDR",
    title="Update с mask=v4_cidr_blocks → принимается (200); CIDR здесь не меняется, у нас no-op",
    classes=["STATE", "CRUD"],
    priority="P1",
    steps=[
        *_make_net("im"),
        Step(name="create-sub", method="POST", path="/vpc/v1/subnets",
             body={"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}",
                   "name": "sub-im-{{runId}}", "zoneId": "{{existingZoneId}}",
                   "v4CidrBlocks": ["10.30.0.0/24"]},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.subnetId", "subId")]),
        poll_operation_until_done(),
        # v4_cidr_blocks в update_mask не отвергается — 200. repo.Update CIDR-колонки
        # не перезаписывает, поэтому изменение CIDR через Update — no-op (диапазоны
        # меняются через :add/removeCidrBlocks).
        Step(name="patch-cidr-via-mask", method="PATCH", path="/vpc/v1/subnets/{{subId}}",
             body={"updateMask": "v4CidrBlocks", "v4CidrBlocks": ["10.31.0.0/24"]},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="cleanup-sub", method="DELETE", path="/vpc/v1/subnets/{{subId}}",
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        _cleanup_net(),
    ],
))

CASES.append(Case(
    id="SUB-DEL-AUTHZ-NF-SYNC",
    title="Delete несуществующего → sync 404",
    classes=["NEG", "AUTHZ"],
    priority="P1",
    steps=[
        Step(name="del-nx", method="DELETE", path="/vpc/v1/subnets/{{garbageVpcId}}",
             test_script=[*assert_status(404), *assert_grpc_code(5, "NOT_FOUND")]),
    ],
))

# ---------------------------------------------------------------------------
# SUB-ACB / SUB-RCB / SUB-REL / SUB-LUA
# ---------------------------------------------------------------------------

CASES.append(Case(
    id="SUB-ACB-CRUD-OK",
    title="AddCidrBlocks → новый блок виден в GET",
    classes=["CRUD"],
    priority="P1",
    steps=[
        *_make_net("acb"),
        Step(name="create-sub", method="POST", path="/vpc/v1/subnets",
             body={"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}",
                   "name": "sub-acb-{{runId}}", "zoneId": "{{existingZoneId}}",
                   "v4CidrBlocks": ["10.60.0.0/24"]},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.subnetId", "subId")]),
        poll_operation_until_done(),
        Step(name="add-cidr", method="POST", path="/vpc/v1/subnets/{{subId}}:add-cidr-blocks",
             body={"v4CidrBlocks": ["10.60.1.0/24"]},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="verify", method="GET", path="/vpc/v1/subnets/{{subId}}",
             test_script=[*assert_status(200),
                          "pm.test('has both cidrs', () => { const c = pm.response.json().v4CidrBlocks; pm.expect(c).to.include('10.60.0.0/24'); pm.expect(c).to.include('10.60.1.0/24'); });"]),
        Step(name="cleanup-sub", method="DELETE", path="/vpc/v1/subnets/{{subId}}",
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        _cleanup_net(),
    ],
))

CASES.append(Case(
    id="SUB-LUA-CRUD-OK",
    title="ListUsedAddresses на пустой subnet → empty",
    classes=["CRUD"],
    priority="P2",
    steps=[
        *_make_net("lua"),
        Step(name="create-sub", method="POST", path="/vpc/v1/subnets",
             body={"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}",
                   "name": "sub-lua-{{runId}}", "zoneId": "{{existingZoneId}}",
                   "v4CidrBlocks": ["10.80.0.0/24"]},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.subnetId", "subId")]),
        poll_operation_until_done(),
        Step(name="list-used", method="GET", path="/vpc/v1/subnets/{{subId}}/addresses",
             test_script=[*assert_status(200),
                          "pm.test('addresses array', () => pm.expect(pm.response.json().usedAddresses || pm.response.json().addresses || []).to.be.an('array'));"]),
        Step(name="cleanup-sub", method="DELETE", path="/vpc/v1/subnets/{{subId}}",
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        _cleanup_net(),
    ],
))

CASES.append(Case(
    id="SUB-LOP-CRUD-OK",
    title="ListOperations возвращает create-op",
    classes=["CRUD"],
    priority="P1",
    steps=[
        *_make_net("lop"),
        Step(name="create-sub", method="POST", path="/vpc/v1/subnets",
             body={"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}",
                   "name": "sub-lop-{{runId}}", "zoneId": "{{existingZoneId}}",
                   "v4CidrBlocks": ["10.90.0.0/24"]},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.id", "createOpId"),
                          *save_from_response("j.metadata && j.metadata.subnetId", "subId")]),
        poll_operation_until_done(),
        Step(name="list-ops", method="GET", path="/vpc/v1/subnets/{{subId}}/operations",
             test_script=[*assert_status(200),
                          "pm.test('at least 1 op', () => pm.expect((pm.response.json().operations || []).length).to.be.at.least(1));"]),
        Step(name="cleanup-sub", method="DELETE", path="/vpc/v1/subnets/{{subId}}",
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        _cleanup_net(),
    ],
))

# Расширение: BVA + CONF + STATE + AUTHZ-Move + Move-CRUD
CASES.extend(crud_list_bva_block("SUB", "/vpc/v1/subnets"))
CASES.append(conf_not_found_text("SUB", "/vpc/v1/subnets", "Subnet"))
CASES.append(state_update_unknown_mask("SUB", "/vpc/v1/subnets"))

CASES.append(Case(
    id="SUB-UPD-CRUD-OK",
    title="Update Subnet description",
    classes=["CRUD"], priority="P1",
    steps=[
        *_make_net("upd"),
        Step(name="create-sub", method="POST", path="/vpc/v1/subnets",
             body={"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}",
                   "name": "sub-upd-{{runId}}", "zoneId": "{{existingZoneId}}",
                   "v4CidrBlocks": ["10.120.0.0/24"]},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.subnetId", "subId")]),
        poll_operation_until_done(),
        Step(name="patch", method="PATCH", path="/vpc/v1/subnets/{{subId}}",
             body={"updateMask": "description", "description": "upd-newman"},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="cleanup-sub", method="DELETE", path="/vpc/v1/subnets/{{subId}}",
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        _cleanup_net(),
    ],
))

CASES.append(Case(
    id="SUB-RCB-CRUD-OK",
    title="RemoveCidrBlocks: убрать дополнительный CIDR",
    classes=["CRUD"], priority="P1",
    steps=[
        *_make_net("rcb"),
        Step(name="create-sub", method="POST", path="/vpc/v1/subnets",
             body={"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}",
                   "name": "sub-rcb-{{runId}}", "zoneId": "{{existingZoneId}}",
                   "v4CidrBlocks": ["10.140.0.0/24"]},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.subnetId", "subId")]),
        poll_operation_until_done(),
        Step(name="add-cidr", method="POST", path="/vpc/v1/subnets/{{subId}}:add-cidr-blocks",
             body={"v4CidrBlocks": ["10.140.1.0/24"]},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="remove-cidr", method="POST", path="/vpc/v1/subnets/{{subId}}:remove-cidr-blocks",
             body={"v4CidrBlocks": ["10.140.1.0/24"]},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="verify", method="GET", path="/vpc/v1/subnets/{{subId}}",
             test_script=[*assert_status(200),
                          "pm.test('cidr removed', () => pm.expect(pm.response.json().v4CidrBlocks).to.not.include('10.140.1.0/24'));"]),
        Step(name="cleanup-sub", method="DELETE", path="/vpc/v1/subnets/{{subId}}",
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        _cleanup_net(),
    ],
))

# Дополнение: STATE immutable project + VAL move-no-dest + BVA pagesize=1
CASES.append(state_immutable_project("SUB", "/vpc/v1/subnets"))
CASES.append(list_pagesize_1_bva("SUB", "/vpc/v1/subnets"))

# STATE для Subnet ACB/RCB/REL — пометить existing CRUD кейсы класса STATE
# через дополнительные state-сценарии
CASES.append(Case(
    id="SUB-ACB-STATE-DISJOINT-CIDRS",
    title="AddCidrBlocks с пересекающимися CIDR в одном запросе → InvalidArgument",
    classes=["STATE", "VAL", "CONF"], priority="P1",
    steps=[
        *_make_net("acbdj"),
        Step(name="create-sub", method="POST", path="/vpc/v1/subnets",
             body={"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}",
                   "name": "sub-acbdj-{{runId}}", "zoneId": "{{existingZoneId}}",
                   "v4CidrBlocks": ["10.150.0.0/24"]},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.subnetId", "subId")]),
        poll_operation_until_done(),
        Step(name="add-overlapping", method="POST",
             path="/vpc/v1/subnets/{{subId}}:add-cidr-blocks",
             body={"v4CidrBlocks": ["10.151.0.0/24", "10.151.0.5/30"]},
             test_script=[
                 "pm.test('rejected (400 sync)', () => pm.expect(pm.response.code).to.eql(400));",
                 *assert_grpc_code(3, "INVALID_ARGUMENT"),
             ]),
        Step(name="cleanup-sub", method="DELETE", path="/vpc/v1/subnets/{{subId}}",
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        _cleanup_net(),
    ],
))

CASES.append(Case(
    id="SUB-CR-CONF-NET-NF-TEXT",
    title="Create subnet в garbage network → точный текст 'Network ... not found'",
    classes=["CONF", "NEG"], priority="P1",
    steps=[
        Step(name="create-bad-net", method="POST", path="/vpc/v1/subnets",
             body={"projectId": "{{_suiteProjectId}}", "networkId": "{{garbageVpcId}}",
                   "name": "sub-confnf-{{runId}}", "zoneId": "{{existingZoneId}}",
                   "v4CidrBlocks": ["10.170.0.0/24"]},
             test_script=[
                 *assert_status(404), *assert_grpc_code(5, "NOT_FOUND"),
                 "pm.test('verbatim Network ... not found', () => pm.expect(pm.response.json().message).to.match(/^Network .* not found$/));",
             ]),
    ],
))

CASES.append(Case(
    id="SUB-CR-NEG-DUP-NAME",
    title="Subnet duplicate name в project → ALREADY_EXISTS (migration 0002 UNIQUE)",
    classes=["NEG", "CONF", "CONC"], priority="P0",
    steps=[
        *_make_net("dup"),
        Step(name="create-first", method="POST", path="/vpc/v1/subnets",
             body={"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}",
                   "name": "sub-dup-{{runId}}", "zoneId": "{{existingZoneId}}",
                   "v4CidrBlocks": ["10.180.0.0/24"]},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.subnetId", "subId1")]),
        poll_operation_until_done(),
        Step(name="create-dup", method="POST", path="/vpc/v1/subnets",
             body={"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}",
                   "name": "sub-dup-{{runId}}", "zoneId": "{{existingZoneId}}",
                   "v4CidrBlocks": ["10.181.0.0/24"]},  # другой CIDR — дубль только по name
             test_script=[*assert_status(409), *assert_grpc_code(6, "ALREADY_EXISTS"),
                          "pm.test('mentions already exists', () => pm.expect(pm.response.json().message.toLowerCase()).to.include('already exists'));"]),
        Step(name="cleanup-1", method="DELETE", path="/vpc/v1/subnets/{{subId1}}",
             test_script=[*save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        _cleanup_net(),
    ],
))

# === Финальное добивание ===
CASES.append(Case(
    id="SUB-DEL-CRUD-OK",
    title="Subnet Delete happy path",
    classes=["CRUD"], priority="P1",
    steps=[
        *_make_net("delok"),
        Step(name="create-sub", method="POST", path="/vpc/v1/subnets",
             body={"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}",
                   "name": "sub-delok-{{runId}}", "zoneId": "{{existingZoneId}}",
                   "v4CidrBlocks": ["10.200.0.0/24"]},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.subnetId", "subId")]),
        poll_operation_until_done(),
        Step(name="delete-happy", method="DELETE", path="/vpc/v1/subnets/{{subId}}",
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="get-after-del", method="GET", path="/vpc/v1/subnets/{{subId}}",
             test_script=[*assert_status(404)]),
        _cleanup_net(),
    ],
))

CASES.append(Case(
    id="SUB-ACB-NEG-OVERLAP",
    title="AddCidrBlocks с CIDR пересекающимся с existing → InvalidArgument/FailedPrecondition",
    classes=["NEG"], priority="P1",
    steps=[
        *_make_net("acbov"),
        Step(name="create-sub", method="POST", path="/vpc/v1/subnets",
             body={"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}",
                   "name": "sub-acbov-{{runId}}", "zoneId": "{{existingZoneId}}",
                   "v4CidrBlocks": ["10.210.0.0/24"]},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.subnetId", "subId")]),
        poll_operation_until_done(),
        Step(name="add-overlap-self", method="POST",
             path="/vpc/v1/subnets/{{subId}}:add-cidr-blocks",
             body={"v4CidrBlocks": ["10.210.0.0/24"]},  # overlaps with existing
             test_script=[
                 "pm.test('rejected (400 sync or async FailedPrecondition)', () => pm.expect(pm.response.code).to.be.oneOf([400, 200]));",
             ]),
        Step(name="cleanup-sub", method="DELETE", path="/vpc/v1/subnets/{{subId}}",
             test_script=[*save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        _cleanup_net(),
    ],
))

CASES.append(Case(
    id="SUB-RCB-NEG-NF",
    title="RemoveCidrBlocks с несуществующим CIDR → InvalidArgument",
    classes=["NEG", "VAL", "STATE"], priority="P1",
    steps=[
        *_make_net("rcbnf"),
        Step(name="create-sub", method="POST", path="/vpc/v1/subnets",
             body={"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}",
                   "name": "sub-rcbnf-{{runId}}", "zoneId": "{{existingZoneId}}",
                   "v4CidrBlocks": ["10.220.0.0/24"]},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.subnetId", "subId")]),
        poll_operation_until_done(),
        Step(name="rcb-nonexistent", method="POST",
             path="/vpc/v1/subnets/{{subId}}:remove-cidr-blocks",
             body={"v4CidrBlocks": ["192.168.99.0/24"]},  # never was in subnet
             test_script=[
                 "pm.test('rejected', () => pm.expect(pm.response.code).to.be.oneOf([200, 400]));",
             ]),
        Step(name="cleanup", method="DELETE", path="/vpc/v1/subnets/{{subId}}",
             test_script=[*save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        _cleanup_net(),
    ],
))

CASES.append(Case(
    id="SUB-LOP-NEG-PARENT-NF",
    title="ListOperations несуществующего subnet → 404 или 200 пустой",
    classes=["NEG"], priority="P2",
    steps=[
        Step(name="lop-nx", method="GET", path="/vpc/v1/subnets/{{garbageVpcId}}/operations",
             test_script=["pm.test('200 or 404', () => pm.expect(pm.response.code).to.be.oneOf([200, 404]));"]),
    ],
))

CASES.append(Case(
    id="SUB-LUA-NEG-PARENT-NF",
    title="ListUsedAddresses несуществующего subnet → 404 или 200",
    classes=["NEG"], priority="P2",
    steps=[
        Step(name="lua-nx", method="GET", path="/vpc/v1/subnets/{{garbageVpcId}}/addresses",
             test_script=["pm.test('200 or 404', () => pm.expect(pm.response.code).to.be.oneOf([200, 404]));"]),
    ],
))

CASES.append(Case(
    id="SUB-DEL-CONF-NF-TEXT",
    title="Delete несуществующего Subnet → точный текст 'Subnet ... not found'",
    classes=["CONF", "NEG"], priority="P1",
    steps=[
        Step(name="del-nx", method="DELETE", path="/vpc/v1/subnets/{{garbageVpcId}}",
             test_script=[*assert_status(404), *assert_grpc_code(5, "NOT_FOUND"),
                          "pm.test('Subnet ... not found', () => pm.expect(pm.response.json().message).to.match(/^Subnet .* not found$/));"]),
    ],
))

CASES.append(Case(
    id="SUB-UPD-CONF-NF-TEXT",
    title="Update несуществующего Subnet → точный текст 'Subnet ... not found'",
    classes=["CONF", "NEG"], priority="P1",
    steps=[
        Step(name="upd-nx", method="PATCH", path="/vpc/v1/subnets/{{garbageVpcId}}",
             body={"updateMask": "description", "description": "x"},
             test_script=[*assert_status(404), *assert_grpc_code(5, "NOT_FOUND"),
                          "pm.test('Subnet ... not found', () => pm.expect(pm.response.json().message).to.match(/^Subnet .* not found$/));"]),
    ],
))

CASES.append(Case(
    id="SUB-RCB-CONF-STATE",
    title="STATE для RemoveCidrBlocks: проверка инварианта после операции",
    classes=["STATE"], priority="P1",
    steps=[
        *_make_net("rcbstate"),
        Step(name="create-sub", method="POST", path="/vpc/v1/subnets",
             body={"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}",
                   "name": "sub-rcbst-{{runId}}", "zoneId": "{{existingZoneId}}",
                   "v4CidrBlocks": ["10.230.0.0/24"]},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.subnetId", "subId")]),
        poll_operation_until_done(),
        Step(name="add-then-remove", method="POST",
             path="/vpc/v1/subnets/{{subId}}:add-cidr-blocks",
             body={"v4CidrBlocks": ["10.230.1.0/24"]},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="remove-it", method="POST",
             path="/vpc/v1/subnets/{{subId}}:remove-cidr-blocks",
             body={"v4CidrBlocks": ["10.230.1.0/24"]},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="verify-state", method="GET", path="/vpc/v1/subnets/{{subId}}",
             test_script=[*assert_status(200),
                          "pm.test('removed cidr gone', () => pm.expect(pm.response.json().v4CidrBlocks).to.not.include('10.230.1.0/24'));",
                          "pm.test('primary cidr kept', () => pm.expect(pm.response.json().v4CidrBlocks).to.include('10.230.0.0/24'));"]),
        Step(name="cleanup", method="DELETE", path="/vpc/v1/subnets/{{subId}}",
             test_script=[*save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        _cleanup_net(),
    ],
))

# Exhaustive ECP/BVA: используем shared network на каждый кейс
# (более дорого, но изолировано). Альтернатива — общий network через preflight item.
# Делаем подмножество кейсов с общим preflight сетью.

def _sub_body_extra():
    return {
        "networkId": "{{netId}}", "zoneId": "{{existingZoneId}}",
        "v4CidrBlocks": ["10.41.0.0/24"],
    }


# Каждый ECP-кейс упакован в Case с _make_net+_cleanup_net
def _wrap_with_net(prefix, suffix, inner_case):
    """Обернуть inner_case (от ecp_*_block) в network preflight/teardown.
    Используем inner_case.id как суффикс — гарантированно уникален per case."""
    # Превратим case-id в short ASCII suffix (без дефисов и uppercase)
    uniq = inner_case.id.lower().replace("-", "")[-12:]
    return Case(
        id=inner_case.id,
        title=inner_case.title,
        classes=inner_case.classes,
        priority=inner_case.priority,
        steps=[
            *_make_net(uniq),
            *inner_case.steps,
            _cleanup_net_lenient(),
        ],
    )


for c in ecp_name_block("SUB", "/vpc/v1/subnets", _sub_body_extra()):
    CASES.append(_wrap_with_net("SUB", "ecp-n", c))
for c in ecp_description_block("SUB", "/vpc/v1/subnets", _sub_body_extra()):
    CASES.append(_wrap_with_net("SUB", "ecp-d", c))
for c in ecp_labels_block("SUB", "/vpc/v1/subnets", _sub_body_extra()):
    CASES.append(_wrap_with_net("SUB", "ecp-l", c))
CASES.extend(updatemask_decision_table("SUB", "/vpc/v1/subnets"))
CASES.extend(filter_syntax_block("SUB", "/vpc/v1/subnets"))
CASES.append(pagination_roundtrip("SUB", "/vpc/v1/subnets"))

# v7: update-per-field wrap'ed в network
for c in update_happy_per_field("SUB", "/vpc/v1/subnets", "/vpc/v1/subnets",
    {"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}",
     "zoneId": "{{existingZoneId}}", "v4CidrBlocks": ["10.241.0.0/24"]}):
    CASES.append(_wrap_with_net("SUB", "v7", c))

CASES.extend(perf_baseline_block("SUB", "/vpc/v1/subnets"))
CASES.extend(verbatim_text_pack("SUB", "Subnet", "/vpc/v1/subnets"))
CASES.extend(authz_caller_headers_block("SUB", "/vpc/v1/subnets"))

# move-self для subnet
# v8 subnet
CASES.append(_wrap_with_net("SUB", "v8m",
    update_happy_multi_field("SUB", "/vpc/v1/subnets", "/vpc/v1/subnets",
        {"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}",
         "zoneId": "{{existingZoneId}}", "v4CidrBlocks": ["10.243.0.0/24"]})))
CASES.append(_wrap_with_net("SUB", "v8f",
    list_filter_match_block("SUB", "/vpc/v1/subnets",
        {"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}",
         "zoneId": "{{existingZoneId}}", "v4CidrBlocks": ["10.244.0.0/24"]})))
for c in neg_invalid_types_block("SUB", "/vpc/v1/subnets",
    {"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}",
     "zoneId": "{{existingZoneId}}", "v4CidrBlocks": ["10.245.0.0/24"]}):
    CASES.append(_wrap_with_net("SUB", "v8nt", c))
CASES.extend(http_method_not_allowed_block("SUB", "/vpc/v1/subnets"))
CASES.extend(malformed_body_block("SUB", "/vpc/v1/subnets"))

# dup-name для Subnet покрыт hand-written SUB-CR-NEG-DUP-NAME (использует РАЗНЫЕ CIDR
# у обеих подсетей). Generated alreadyexists_dup_name_for тут не применим: он создает
# две подсети с одинаковым телом (тот же CIDR) → overlap проверяется раньше
# name-uniqueness и возвращается FAILED_PRECONDITION "Subnet CIDRs can not overlap",
# а не ALREADY_EXISTS.
for c in update_mask_partial_block("SUB", "/vpc/v1/subnets", "/vpc/v1/subnets",
    {"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}",
     "zoneId": "{{existingZoneId}}", "v4CidrBlocks": ["10.247.0.0/24"]}):
    CASES.append(_wrap_with_net("SUB", "v9p", c))
CASES.append(_wrap_with_net("SUB", "v9pf",
    perf_baseline_get_block("SUB", "/vpc/v1/subnets",
        {"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}",
         "zoneId": "{{existingZoneId}}", "v4CidrBlocks": ["10.248.0.0/24"]})))
CASES.extend(list_total_size_check_block("SUB", "/vpc/v1/subnets"))

# v10: subnet-specific dhcp_options + cidr boundary
def _sub_dhcp(opts):
    return {"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}",
            "zoneId": "{{existingZoneId}}", "v4CidrBlocks": ["10.250.0.0/24"],
            "dhcpOptions": opts}

# DHCP options ECP
for case_id, opts, expect_ok in [
    ("SUB-CR-VAL-DHCP-DOMAIN-OK", {"domainName": "example.com"}, True),
    ("SUB-CR-VAL-DHCP-DOMAIN-INVALID", {"domainName": "!!!"}, False),
    ("SUB-CR-VAL-DHCP-NS-OK", {"domainNameServers": ["8.8.8.8", "1.1.1.1"]}, True),
    ("SUB-CR-VAL-DHCP-NS-INVALID-IP", {"domainNameServers": ["999.999.999.999"]}, False),
    ("SUB-CR-VAL-DHCP-NTP-OK", {"ntpServers": ["169.254.169.123"]}, True),
    ("SUB-CR-VAL-DHCP-NTP-INVALID-IP", {"ntpServers": ["not-an-ip"]}, False),
]:
    inner = Case(
        id=case_id, title=f"DHCP options: {case_id}",
        classes=["VAL"] + (["CRUD"] if expect_ok else ["NEG"]),
        priority="P1" if not expect_ok else "P2",
        steps=[
            Step(name="cr-dhcp", method="POST", path="/vpc/v1/subnets",
                 body=dict(_sub_dhcp(opts), name=f"sub-dhcp-{case_id.lower()[-8:]}-{{{{runId}}}}"),
                 test_script=[
                     f"pm.test('{'200 ok' if expect_ok else '400 rejected'}', () => pm.expect(pm.response.code).to.eql({200 if expect_ok else 400}));",
                     *(save_from_response("j.id", "opId") if expect_ok else []),
                     *(save_from_response("j.metadata && j.metadata.subnetId", "subId") if expect_ok else []),
                 ]),
        ] + ([poll_operation_until_done(),
              Step(name="cleanup", method="DELETE", path="/vpc/v1/subnets/{{subId}}",
                   test_script=[*save_from_response("j.id", "opId")]),
              poll_operation_until_done()] if expect_ok else []),
    )
    CASES.append(_wrap_with_net("SUB", "v10dhcp" + case_id[-5:].lower(), inner))

# CIDR prefix boundary: /28 принимается; /29, /30, /31 → 400
# "Illegal argument Invalid network prefix /N".
CASES.append(_wrap_with_net("SUB", "v10cidr28",
    Case(
        id="SUB-CR-BVA-CIDR-28",
        title="Create subnet с prefix /28 → 200 (минимальный размер)",
        classes=["BVA", "CRUD"], priority="P2",
        steps=[
            Step(name="cr-prefix-28", method="POST", path="/vpc/v1/subnets",
                 body={"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}",
                       "zoneId": "{{existingZoneId}}", "v4CidrBlocks": ["10.255.0.0/28"],
                       "name": "sub-cidr-28-{{runId}}"},
                 test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                              *save_from_response("j.metadata && j.metadata.subnetId", "subId")]),
            poll_operation_until_done(),
            Step(name="cleanup-28", method="DELETE", path="/vpc/v1/subnets/{{subId}}",
                 test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
            poll_operation_until_done(),
        ],
    )))
for _n in ("29", "30", "31"):
    CASES.append(_wrap_with_net("SUB", "v10cidr" + _n,
        Case(
            id=f"SUB-CR-BVA-CIDR-{_n}",
            title=f"Create subnet с prefix /{_n} → 400 'Illegal argument Invalid network prefix /{_n}'",
            classes=["BVA", "VAL", "NEG"], priority="P2",
            steps=[
                Step(name=f"cr-prefix-{_n}", method="POST", path="/vpc/v1/subnets",
                     body={"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}",
                           "zoneId": "{{existingZoneId}}", "v4CidrBlocks": [f"10.255.0.0/{_n}"],
                           "name": f"sub-cidr-{_n}-{{{{runId}}}}"},
                     test_script=[*assert_status(400), *assert_grpc_code(3, "INVALID_ARGUMENT"),
                                  f"pm.test('verbatim text', () => pm.expect(pm.response.json().message).to.eql('Illegal argument Invalid network prefix /{_n}'));"]),
            ],
        )))

# === Delete Subnet с зависимыми Address ===

CASES.append(Case(
    id="SUB-DEL-NEG-HAS-ADDRESSES",
    title="Delete Subnet с internal Address → FailedPrecondition (FK RESTRICT)",
    classes=["NEG", "CONF", "STATE"], priority="P0",
    steps=[
        *_make_net("hasad"),
        Step(name="cr-sub", method="POST", path="/vpc/v1/subnets",
             body={"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}",
                   "name": "sub-hasad-{{runId}}", "zoneId": "{{existingZoneId}}",
                   "v4CidrBlocks": ["10.251.0.0/24"]},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.subnetId", "subId")]),
        poll_operation_until_done(),
        Step(name="cr-internal-addr", method="POST", path="/vpc/v1/addresses",
             body={"projectId": "{{_suiteProjectId}}", "name": "adr-hasad-{{runId}}",
                   "internalIpv4AddressSpec": {"subnetId": "{{subId}}"}},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.addressId", "addrId")]),
        poll_operation_until_done(),
        Step(name="del-sub-blocked", method="DELETE", path="/vpc/v1/subnets/{{subId}}",
             test_script=[
                 # Delete subnet с internal Address → sync FAILED_PRECONDITION
                 # "Subnet has allocated internal addresses".
                 *assert_status(400), *assert_grpc_code(9, "FAILED_PRECONDITION"),
                 "pm.test('verbatim text', () => pm.expect(pm.response.json().message).to.eql('Subnet has allocated internal addresses'));",
             ]),
        # cleanup
        Step(name="cleanup-addr", method="DELETE", path="/vpc/v1/addresses/{{addrId}}",
             test_script=[*save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="cleanup-sub", method="DELETE", path="/vpc/v1/subnets/{{subId}}",
             test_script=[*save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        _cleanup_net(),
    ],
))

CASES.append(Case(
    # v6-counterpart of SUB-DEL-NEG-HAS-ADDRESSES: внутренний IPv6-адрес,
    # выделенный в подсети, блокирует ее удаление точно так же, как v4. AddressesBySubnet
    # покрывает internal_ipv6, а generated-колонка addresses.internal_subnet_id выводится
    # из v4 ИЛИ v6.
    id="SUB-DEL-NEG-HAS-V6-ADDRESS",
    title="Delete Subnet с internal IPv6 Address → FailedPrecondition",
    classes=["NEG", "CONF", "STATE"], priority="P0",
    steps=[
        *_make_net("hasv6ad"),
        Step(name="cr-sub", method="POST", path="/vpc/v1/subnets",
             body={"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}",
                   "name": "sub-hasv6ad-{{runId}}", "zoneId": "{{existingZoneId}}",
                   "v4CidrBlocks": ["10.249.0.0/24"], "v6CidrBlocks": ["fd34:5678:9abc::/64"]},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.subnetId", "subId")]),
        poll_operation_until_done(),
        Step(name="cr-internal-v6-addr", method="POST", path="/vpc/v1/addresses",
             body={"projectId": "{{_suiteProjectId}}", "name": "adr-hasv6ad-{{runId}}",
                   "internalIpv6AddressSpec": {"subnetId": "{{subId}}"}},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.addressId", "addrId")]),
        poll_operation_until_done(),
        Step(name="del-sub-blocked", method="DELETE", path="/vpc/v1/subnets/{{subId}}",
             test_script=[
                 # Внутренний v6-адрес блокирует подсеть так же, как v4 →
                 # sync FAILED_PRECONDITION "Subnet has allocated internal addresses".
                 *assert_status(400), *assert_grpc_code(9, "FAILED_PRECONDITION"),
                 "pm.test('mentions internal address', () => pm.expect(pm.response.json().message).to.include('internal address'));",
             ]),
        # cleanup: delete the address → subnet delete now succeeds
        Step(name="cleanup-addr", method="DELETE", path="/vpc/v1/addresses/{{addrId}}",
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="del-sub", method="DELETE", path="/vpc/v1/subnets/{{subId}}",
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="assert-sub-deleted", method="GET", path="/operations/{{opId}}",
             test_script=["const j = pm.response.json();",
                          "pm.test('subnet delete op done no error', () => pm.expect(j.done && !j.error).to.eql(true));"]),
        _cleanup_net(),
    ],
))

CASES.append(Case(
    # Полная RESTRICT-цепочка Network → Subnet → Address → NIC: каждый родитель
    # жестко блокируется, пока существует ребенок; удаление снизу вверх.
    id="NET-SUBNET-ADDR-NIC-DELETE-CHAIN",
    title="RESTRICT chain: network/subnet/address все блокируются детьми; удаление снизу вверх",
    classes=["NEG", "CONF", "STATE"], priority="P0",
    steps=[
        *_make_net("chain"),
        Step(name="cr-sub", method="POST", path="/vpc/v1/subnets",
             body={"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}",
                   "name": "sub-chain-{{runId}}", "zoneId": "{{existingZoneId}}",
                   "v4CidrBlocks": ["10.248.0.0/24"]},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.subnetId", "subId")]),
        poll_operation_until_done(),
        Step(name="cr-addr", method="POST", path="/vpc/v1/addresses",
             body={"projectId": "{{_suiteProjectId}}", "name": "adr-chain-{{runId}}",
                   "internalIpv4AddressSpec": {"subnetId": "{{subId}}"}},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.addressId", "addrId")]),
        poll_operation_until_done(),
        Step(name="cr-nic", method="POST", path="/vpc/v1/networkInterfaces",
             body={"projectId": "{{_suiteProjectId}}", "subnetId": "{{subId}}",
                   "name": "nic-chain-{{runId}}", "v4AddressIds": ["{{addrId}}"]},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.networkInterfaceId", "nicId")]),
        poll_operation_until_done(),
        # 5. delete network → blocked (not empty)
        Step(name="del-net-blocked", method="DELETE", path="/vpc/v1/networks/{{netId}}",
             test_script=[*assert_status(400), *assert_grpc_code(9, "FAILED_PRECONDITION"),
                          "pm.test('network not empty', () => pm.expect(pm.response.json().message.toLowerCase()).to.include('not empty'));"]),
        # 6. delete subnet → blocked: address check runs first (not the NIC)
        Step(name="del-sub-blocked", method="DELETE", path="/vpc/v1/subnets/{{subId}}",
             test_script=[*assert_status(400), *assert_grpc_code(9, "FAILED_PRECONDITION"),
                          "pm.test('mentions internal address', () => pm.expect(pm.response.json().message).to.include('internal address'));"]),
        # 7. delete address → blocked by the NIC
        Step(name="del-addr-blocked", method="DELETE", path="/vpc/v1/addresses/{{addrId}}",
             test_script=[*assert_status(400), *assert_grpc_code(9, "FAILED_PRECONDITION"),
                          "pm.test('mentions network interface', () => pm.expect(pm.response.json().message).to.include('network interface'));"]),
        # 8. cleanup bottom-up: NIC → address → subnet → network, all succeed
        Step(name="del-nic", method="DELETE", path="/vpc/v1/networkInterfaces/{{nicId}}",
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="del-addr", method="DELETE", path="/vpc/v1/addresses/{{addrId}}",
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="del-sub", method="DELETE", path="/vpc/v1/subnets/{{subId}}",
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="del-net", method="DELETE", path="/vpc/v1/networks/{{netId}}",
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="assert-net-deleted", method="GET", path="/operations/{{opId}}",
             test_script=["const j = pm.response.json();",
                          "pm.test('network delete op done no error', () => pm.expect(j.done && !j.error).to.eql(true));"]),
    ],
))

CASES.append(Case(
    # NIC→Subnet FK — ON DELETE RESTRICT: NIC жестко блокирует свою подсеть, даже без
    # адресов. Удалять снизу вверх: NIC → Address → Subnet → Network.
    id="SUB-DEL-NEG-HAS-NIC",
    title="Delete Subnet с NIC (без address) → sync FailedPrecondition; после delete NIC — OK",
    classes=["NEG", "STATE"], priority="P0",
    steps=[
        *_make_net("hasnic"),
        Step(name="cr-sub", method="POST", path="/vpc/v1/subnets",
             body={"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}",
                   "name": "sub-hasnic-{{runId}}", "zoneId": "{{existingZoneId}}",
                   "v4CidrBlocks": ["10.253.0.0/24"]},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.subnetId", "subId")]),
        poll_operation_until_done(),
        Step(name="cr-nic-no-addr", method="POST", path="/vpc/v1/networkInterfaces",
             body={"projectId": "{{_suiteProjectId}}", "subnetId": "{{subId}}", "name": "nic-hasnic-{{runId}}"},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.networkInterfaceId", "nicId")]),
        poll_operation_until_done(),
        Step(name="del-sub-blocked", method="DELETE", path="/vpc/v1/subnets/{{subId}}",
             test_script=[
                 *assert_status(400), *assert_grpc_code(9, "FAILED_PRECONDITION"),
                 "pm.test('mentions network interface', () => pm.expect(pm.response.json().message).to.include('network interface'));",
             ]),
        # delete NIC → subnet delete now succeeds
        Step(name="del-nic", method="DELETE", path="/vpc/v1/networkInterfaces/{{nicId}}",
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="del-sub", method="DELETE", path="/vpc/v1/subnets/{{subId}}",
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="assert-sub-deleted", method="GET", path="/operations/{{opId}}",
             test_script=["const j = pm.response.json();",
                          "pm.test('subnet delete op done no error', () => pm.expect(j.done && !j.error).to.eql(true));"]),
        _cleanup_net(),
    ],
))

CASES.append(Case(
    id="SUB-DEL-CRUD-EMPTY-OK",
    title="Delete Subnet без зависимостей → OK",
    classes=["CRUD"], priority="P1",
    steps=[
        *_make_net("delempty"),
        Step(name="cr-sub", method="POST", path="/vpc/v1/subnets",
             body={"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}",
                   "name": "sub-delempty-{{runId}}", "zoneId": "{{existingZoneId}}",
                   "v4CidrBlocks": ["10.252.0.0/24"]},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.subnetId", "subId")]),
        poll_operation_until_done(),
        Step(name="del-empty-sub", method="DELETE", path="/vpc/v1/subnets/{{subId}}",
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="assert-success", method="GET", path="/operations/{{opId}}",
             test_script=[
                 "const j = pm.response.json();",
                 "pm.test('done with no error', () => pm.expect(j.done && !j.error).to.eql(true));",
             ]),
        _cleanup_net(),
    ],
))

# === Required-field matrix + Immutable matrix для Subnet ===
# Subnet нужен parent network — wrap в _wrap_with_net
for c in required_fields_matrix("SUB", "/vpc/v1/subnets",
    {"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}",
     "name": "sub-req-{{runId}}", "zoneId": "{{existingZoneId}}",
     "v4CidrBlocks": ["10.190.0.0/24"]},
    ["projectId", "networkId", "name", "zoneId", "v4CidrBlocks"]):
    CASES.append(_wrap_with_net("SUB", "req", c))
CASES.extend(immutable_fields_matrix("SUB", "/vpc/v1/subnets",
    ["project_id", "network_id", "zone_id", "v4_cidr_blocks", "v6_cidr_blocks"]))

# === Subnet CIDR expand/shrink pack — обернут в setup-сцену ===
# Создаем один Subnet с primary CIDR 10.180.0.0/24, accumulate 4 cidr через add,
# потом гоняем 8 кейсов remove/add/overlap/roundtrip.
def _subnet_cidr_setup_teardown(case):
    return Case(
        id=case.id, title=case.title, classes=case.classes, priority=case.priority,
        steps=[
            *_make_net("cidrexp"),
            Step(name="setup-sub", method="POST", path="/vpc/v1/subnets",
                 body={"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}",
                       "name": "sub-cidrexp-{{runId}}", "zoneId": "{{existingZoneId}}",
                       "v4CidrBlocks": ["10.180.0.0/24"]},
                 test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                              *save_from_response("j.metadata && j.metadata.subnetId", "addedSubId")]),
            poll_operation_until_done(),
            *case.steps,
            Step(name="cleanup-sub", method="DELETE", path="/vpc/v1/subnets/{{addedSubId}}",
                 test_script=[*save_from_response("j.id", "opId")]),
            poll_operation_until_done(),
            _cleanup_net(),
        ],
    )

for case in subnet_cidr_expand_shrink_pack():
    CASES.append(_subnet_cidr_setup_teardown(case))

# v14 — pairwise + security (parent net wrap)
for c in pairwise_subnet_pack():
    CASES.append(_wrap_with_net("SUB", "pw", c))
for c in security_injection_block("SUB", "/vpc/v1/subnets", "/vpc/v1/subnets",
    {"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}",
     "zoneId": "{{existingZoneId}}", "v4CidrBlocks": ["10.169.0.0/24"]}):
    CASES.append(_wrap_with_net("SUB", "sec", c))

# ---------------------------------------------------------------------------
# IPv6 CIDR add/remove on subnet verbs
# ---------------------------------------------------------------------------

CASES.append(Case(
    id="SUB-CIDR-ADD-V6-OK",
    title="AddCidrBlocks с v6CidrBlocks → IPv6-блок виден в GET",
    classes=["CRUD"], priority="P1",
    steps=[
        *_make_net("acb6"),
        Step(name="create-sub", method="POST", path="/vpc/v1/subnets",
             body={"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}",
                   "name": "sub-acb6-{{runId}}", "zoneId": "{{existingZoneId}}",
                   "v4CidrBlocks": ["10.220.0.0/24"]},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.subnetId", "subId")]),
        poll_operation_until_done(),
        Step(name="add-cidr-v6", method="POST", path="/vpc/v1/subnets/{{subId}}:add-cidr-blocks",
             body={"v6CidrBlocks": ["fd12:3456:789a::/64"]},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="verify", method="GET", path="/vpc/v1/subnets/{{subId}}",
             test_script=[*assert_status(200),
                          "pm.test('v6 cidr present', () => pm.expect(pm.response.json().v6CidrBlocks).to.include('fd12:3456:789a::/64'));"]),
        Step(name="cleanup-sub", method="DELETE", path="/vpc/v1/subnets/{{subId}}",
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        _cleanup_net(),
    ],
))

CASES.append(Case(
    id="SUB-CIDR-REMOVE-V6-OK",
    title="RemoveCidrBlocks с v6CidrBlocks → IPv6-блок убран",
    classes=["CRUD"], priority="P1",
    steps=[
        *_make_net("rcb6"),
        Step(name="create-sub", method="POST", path="/vpc/v1/subnets",
             body={"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}",
                   "name": "sub-rcb6-{{runId}}", "zoneId": "{{existingZoneId}}",
                   "v4CidrBlocks": ["10.221.0.0/24"], "v6CidrBlocks": ["fd12:3456:789b::/64"]},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.subnetId", "subId")]),
        poll_operation_until_done(),
        Step(name="remove-cidr-v6", method="POST", path="/vpc/v1/subnets/{{subId}}:remove-cidr-blocks",
             body={"v6CidrBlocks": ["fd12:3456:789b::/64"]},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="verify", method="GET", path="/vpc/v1/subnets/{{subId}}",
             test_script=[*assert_status(200),
                          "pm.test('v6 cidr removed', () => pm.expect(pm.response.json().v6CidrBlocks || []).to.not.include('fd12:3456:789b::/64'));"]),
        Step(name="cleanup-sub", method="DELETE", path="/vpc/v1/subnets/{{subId}}",
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        _cleanup_net(),
    ],
))

CASES.append(Case(
    id="SUB-CIDR-ADD-V6-NEG-HOSTBITS",
    title="AddCidrBlocks v6 с ненулевыми host-bits → InvalidArgument (sync 400)",
    classes=["NEG", "VAL"], priority="P1",
    steps=[
        *_make_net("acb6hb"),
        Step(name="create-sub", method="POST", path="/vpc/v1/subnets",
             body={"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}",
                   "name": "sub-acb6hb-{{runId}}", "zoneId": "{{existingZoneId}}",
                   "v4CidrBlocks": ["10.222.0.0/24"]},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.subnetId", "subId")]),
        poll_operation_until_done(),
        Step(name="add-cidr-v6-hostbits", method="POST",
             path="/vpc/v1/subnets/{{subId}}:add-cidr-blocks",
             body={"v6CidrBlocks": ["fd12:3456:789a::1/64"]},
             test_script=[
                 "pm.test('rejected (400 sync)', () => pm.expect(pm.response.code).to.eql(400));",
                 *assert_grpc_code(3, "INVALID_ARGUMENT"),
             ]),
        Step(name="cleanup-sub", method="DELETE", path="/vpc/v1/subnets/{{subId}}",
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        _cleanup_net(),
    ],
))


# ---------------------------------------------------------------------------
# Subnet v6 overlap / util / rollback
# ---------------------------------------------------------------------------

CASES.append(Case(
    id="SUB-CR-NEG-DUP-CIDR-EXACT",
    title="Create Subnet с CIDR, совпадающим с existing Subnet → sync FailedPrecondition (create-time overlap precheck)",
    classes=["NEG", "CONF"], priority="P0",
    steps=[
        *_make_net("dupCidr"),
        Step(name="sub1", method="POST", path="/vpc/v1/subnets",
             body={"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}",
                   "name": "sub-dup1-{{runId}}", "zoneId": "{{existingZoneId}}",
                   "v4CidrBlocks": ["10.230.0.0/24"]},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.subnetId", "subId1")]),
        poll_operation_until_done(),
        # v4-overlap (exact dup within the same network) is rejected SYNCHRONOUSLY by
        # the create-time overlap precheck (FailedPrecondition), before any Operation
        # is created — not as an async op error. The DB EXCLUDE constraint is only the
        # backstop; the sync precheck fires first.
        Step(name="sub2-same-cidr", method="POST", path="/vpc/v1/subnets",
             body={"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}",
                   "name": "sub-dup2-{{runId}}", "zoneId": "{{existingZoneId}}",
                   "v4CidrBlocks": ["10.230.0.0/24"]},
             test_script=[*assert_status(400), *assert_grpc_code(9, "FAILED_PRECONDITION")]),
        Step(name="cleanup-sub1", method="DELETE", path="/vpc/v1/subnets/{{subId1}}",
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        _cleanup_net(),
        poll_operation_until_done(),
    ],
))


CASES.append(Case(
    id="SUB-CR-NEG-V6-OVERLAP",
    title="2 v6-subnet с overlapping CIDR в одной Network → 2nd FailedPrecondition (EXCLUDE subnets_no_overlap_v6)",
    classes=["NEG", "CONF"], priority="P0",
    steps=[
        *_make_net("v6ov"),
        Step(name="sub1-v6", method="POST", path="/vpc/v1/subnets",
             body={"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}",
                   "name": "sub-v6ov1-{{runId}}", "zoneId": "{{existingZoneId}}",
                   "v4CidrBlocks": ["10.231.0.0/24"], "v6CidrBlocks": ["fd12:3456:7800::/64"]},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.subnetId", "subId1")]),
        poll_operation_until_done(),
        # Полный overlap по v6, разные v4 — должен fail через EXCLUDE v6.
        Step(name="sub2-v6-overlap", method="POST", path="/vpc/v1/subnets",
             body={"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}",
                   "name": "sub-v6ov2-{{runId}}", "zoneId": "{{existingZoneId}}",
                   "v4CidrBlocks": ["10.231.1.0/24"], "v6CidrBlocks": ["fd12:3456:7800::/64"]},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        Step(name="poll-fail-v6", method="GET", path="/operations/{{opId}}",
             test_script=[
                 "let _t = 0;",
                 "const _s = () => pm.sendRequest({",
                 "  url: pm.environment.get('baseUrl') + '/operations/' + pm.environment.get('opId'),",
                 "  method: 'GET',",
                 "  header: { 'Authorization': 'Bearer ' + pm.environment.get('jwtProjectAdminA1') },",
                 "}, (err, res) => {",
                 "  let j = null; try { j = res.json(); } catch (e) {}",
                 "  if (j && j.done) {",
                 "    pm.test('v6 overlap rejected', () => pm.expect(!!j.error, JSON.stringify(j)).to.eql(true));",
                 "    pm.test('FailedPrecondition (9)', () => pm.expect(j.error.code).to.eql(9));",
                 "  } else if (++_t < 8) { setTimeout(_s, 500); }",
                 "  else { pm.test('op resolved', () => pm.expect.fail('timeout')); }",
                 "});",
                 "_s();",
             ]),
        Step(name="cleanup-sub1", method="DELETE", path="/vpc/v1/subnets/{{subId1}}",
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        _cleanup_net(),
        poll_operation_until_done(),
    ],
))


CASES.append(Case(
    id="SUB-LUA-CRUD-COUNT",
    title="Allocate 3 internal Address → ListUsedAddresses возвращает все 3",
    classes=["CRUD", "STATE"], priority="P1",
    steps=[
        *_make_net("luaCount"),
        Step(name="create-sub", method="POST", path="/vpc/v1/subnets",
             body={"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}",
                   "name": "sub-luac-{{runId}}", "zoneId": "{{existingZoneId}}",
                   "v4CidrBlocks": ["10.232.0.0/24"]},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.subnetId", "subId")]),
        poll_operation_until_done(),
        Step(name="addr1", method="POST", path="/vpc/v1/addresses",
             body={"projectId": "{{_suiteProjectId}}", "name": "luac-a1-{{runId}}",
                   "internalIpv4AddressSpec": {"subnetId": "{{subId}}"}},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.addressId", "addrId1")]),
        poll_operation_until_done(),
        Step(name="addr2", method="POST", path="/vpc/v1/addresses",
             body={"projectId": "{{_suiteProjectId}}", "name": "luac-a2-{{runId}}",
                   "internalIpv4AddressSpec": {"subnetId": "{{subId}}"}},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.addressId", "addrId2")]),
        poll_operation_until_done(),
        Step(name="addr3", method="POST", path="/vpc/v1/addresses",
             body={"projectId": "{{_suiteProjectId}}", "name": "luac-a3-{{runId}}",
                   "internalIpv4AddressSpec": {"subnetId": "{{subId}}"}},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.addressId", "addrId3")]),
        poll_operation_until_done(),
        Step(name="list-used", method="GET", path="/vpc/v1/subnets/{{subId}}:listUsedAddresses",
             test_script=[
                 *assert_status(200),
                 "const j = pm.response.json();",
                 "const used = j.addresses || [];",
                 "pm.test('ListUsedAddresses returns >= 3 entries (3 allocated)', () => pm.expect(used.length, JSON.stringify(used)).to.be.at.least(3));",
             ]),
        Step(name="del-a1", method="DELETE", path="/vpc/v1/addresses/{{addrId1}}",
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="del-a2", method="DELETE", path="/vpc/v1/addresses/{{addrId2}}",
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="del-a3", method="DELETE", path="/vpc/v1/addresses/{{addrId3}}",
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="cleanup-sub", method="DELETE", path="/vpc/v1/subnets/{{subId}}",
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        _cleanup_net(),
        poll_operation_until_done(),
    ],
))


CASES.append(Case(
    id="SUB-LUA-STATE-FRAGMENT",
    title="Allocate 5 → delete middle 3 → ListUsedAddresses возвращает только оставшиеся 2 (фрагментация)",
    classes=["STATE"], priority="P2",
    steps=[
        *_make_net("luaFrag"),
        Step(name="create-sub", method="POST", path="/vpc/v1/subnets",
             body={"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}",
                   "name": "sub-luaf-{{runId}}", "zoneId": "{{existingZoneId}}",
                   "v4CidrBlocks": ["10.233.0.0/24"]},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.subnetId", "subId")]),
        poll_operation_until_done(),
        # Allocate 5 sequential.
        *[s for i in range(5) for s in [
            Step(name=f"addr-{i}", method="POST", path="/vpc/v1/addresses",
                 body={"projectId": "{{_suiteProjectId}}", "name": f"luaf-{i}-{{{{runId}}}}",
                       "internalIpv4AddressSpec": {"subnetId": "{{subId}}"}},
                 test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                              *save_from_response("j.metadata && j.metadata.addressId", f"addrId{i}")]),
            poll_operation_until_done(),
        ]],
        Step(name="list-before-delete", method="GET", path="/vpc/v1/subnets/{{subId}}:listUsedAddresses",
             test_script=[
                 *assert_status(200),
                 "pm.environment.set('countBefore', String((pm.response.json().addresses || []).length));",
             ]),
        # Delete middle 3 (indices 1, 2, 3).
        Step(name="del-1", method="DELETE", path="/vpc/v1/addresses/{{addrId1}}",
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="del-2", method="DELETE", path="/vpc/v1/addresses/{{addrId2}}",
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="del-3", method="DELETE", path="/vpc/v1/addresses/{{addrId3}}",
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="list-after", method="GET", path="/vpc/v1/subnets/{{subId}}:listUsedAddresses",
             test_script=[
                 *assert_status(200),
                 "const after = (pm.response.json().addresses || []).length;",
                 "const before = parseInt(pm.environment.get('countBefore') || '0', 10);",
                 "pm.test('count decreased by exactly 3', () => pm.expect(before - after, `before=${before} after=${after}`).to.eql(3));",
             ]),
        Step(name="del-0", method="DELETE", path="/vpc/v1/addresses/{{addrId0}}",
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="del-4", method="DELETE", path="/vpc/v1/addresses/{{addrId4}}",
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="cleanup-sub", method="DELETE", path="/vpc/v1/subnets/{{subId}}",
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        _cleanup_net(),
        poll_operation_until_done(),
    ],
))


CASES.append(Case(
    id="SUB-CR-NEG-ROLLBACK-NO-RESOURCE-IN-GET",
    title="Failed Subnet.Create (parent network NF) → sync NotFound, ресурс НЕ создан/visible",
    classes=["NEG", "STATE"], priority="P1",
    steps=[
        # Parent-network existence is a SYNC precheck: a well-formed but non-existent
        # networkId is rejected synchronously with NotFound ("Network <id> not found"),
        # before any Operation is created or any subnet row is inserted. There is thus
        # no Operation to poll and nothing to roll back — the resource never comes into
        # being. (`net00000000000000000` is a well-formed, never-allocated network id.)
        Step(name="create-fail", method="POST", path="/vpc/v1/subnets",
             body={"projectId": "{{_suiteProjectId}}",
                   "networkId": "net00000000000000000",
                   "name": "sub-rollback-{{runId}}", "zoneId": "{{existingZoneId}}",
                   "v4CidrBlocks": ["10.234.0.0/24"]},
             test_script=[*assert_status(404), *assert_grpc_code(5, "NOT_FOUND")]),
        # List by project must not contain a subnet with the (unique) attempted name —
        # confirms no partial/leaked resource from the failed create.
        Step(name="list-not-include", method="GET",
             path="/vpc/v1/subnets?projectId={{_suiteProjectId}}&pageSize=1000",
             test_script=[
                 *assert_status(200),
                 "const subs = pm.response.json().subnets || [];",
                 "const failedName = 'sub-rollback-' + pm.environment.get('runId');",
                 "pm.test('List не содержит subnet с именем failed Create', () => pm.expect(subs.map(s => s.name), `name=${failedName}`).to.not.include(failedName));",
             ]),
    ],
))
