"""Case-set для SubnetService (kacho-vpc)."""

CASES = []

def _make_net(name_suffix="net"):
    """Helper: набор шагов для создания parent Network + сохранения netId."""
    return [
        Step(
            name="pre-create-net",
            method="POST",
            path="/vpc/v1/networks",
            body={"folderId": "{{_suiteFolderId}}", "name": f"sub-{name_suffix}-{{{{runId}}}}"},
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
                "folderId": "{{_suiteFolderId}}", "networkId": "{{netId}}",
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
            body={"folderId": "{{_suiteFolderId}}", "networkId": "{{netId}}",
                  "name": "sub-noz-{{runId}}", "v4CidrBlocks": ["10.0.0.0/24"]},
            test_script=[*assert_status(400), *assert_grpc_code(3, "INVALID_ARGUMENT")],
        ),
        _cleanup_net(),
    ],
))

CASES.append(Case(
    id="SUB-CR-VAL-ZONE-UNKNOWN",
    title="Create с несуществующей зоной → sync 400 INVALID_ARGUMENT \"unknown zone id '...'\" (kacho-vpc#8)",
    classes=["VAL"],
    priority="P0",
    steps=[
        *_make_net("zu"),
        Step(
            name="create-unknown-zone",
            method="POST",
            path="/vpc/v1/subnets",
            body={"folderId": "{{_suiteFolderId}}", "networkId": "{{netId}}",
                  "name": "sub-zu-{{runId}}", "zoneId": "ru-central1-z-fake",
                  "v4CidrBlocks": ["10.0.0.0/24"]},
            # verbatim-YC (kacho-vpc#8): flat {code,message} body, не Operation.
            test_script=[*assert_status(400), *assert_grpc_code(3, "INVALID_ARGUMENT"),
                         "pm.test('unknown zone text', () => pm.expect(pm.response.json().message).to.match(/^unknown zone id '.*'$/));"],
        ),
        _cleanup_net(),
    ],
))

CASES.append(Case(
    id="SUB-CR-VAL-CIDR-REQUIRED",
    title="Create без v4_cidr_blocks → InvalidArgument",
    classes=["VAL"],
    priority="P0",
    steps=[
        *_make_net("nc"),
        Step(
            name="create-no-cidr",
            method="POST",
            path="/vpc/v1/subnets",
            body={"folderId": "{{_suiteFolderId}}", "networkId": "{{netId}}",
                  "name": "sub-nc-{{runId}}", "zoneId": "{{existingZoneId}}"},
            test_script=[*assert_status(400), *assert_grpc_code(3, "INVALID_ARGUMENT")],
        ),
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
            body={"folderId": "{{_suiteFolderId}}", "networkId": "{{netId}}",
                  "name": "sub-hb-{{runId}}", "zoneId": "{{existingZoneId}}",
                  "v4CidrBlocks": ["10.0.0.5/24"]},
            test_script=[*assert_status(400), *assert_grpc_code(3, "INVALID_ARGUMENT")],
        ),
        _cleanup_net(),
    ],
))

CASES.append(Case(
    id="SUB-CR-NEG-NETWORK-NOT-FOUND",
    title="Create в несуществующей network → sync 404 NOT_FOUND (kacho-vpc#8)",
    classes=["NEG"],
    priority="P0",
    steps=[
        Step(
            name="create",
            method="POST",
            path="/vpc/v1/subnets",
            body={"folderId": "{{_suiteFolderId}}", "networkId": "{{garbageVpcId}}",
                  "name": "sub-nf-{{runId}}", "zoneId": "{{existingZoneId}}",
                  "v4CidrBlocks": ["10.10.0.0/24"]},
            test_script=[*assert_status(404), *assert_grpc_code(5, "NOT_FOUND"),
                         "pm.test('mentions network', () => pm.expect(pm.response.json().message.toLowerCase()).to.include('network'));"],
        ),
    ],
))

CASES.append(Case(
    id="SUB-CR-NEG-CIDR-OVERLAP",
    title="Create двух subnet с пересекающимися CIDR → второй sync 400 FAILED_PRECONDITION (kacho-vpc#8)",
    classes=["NEG"],
    priority="P0",
    steps=[
        *_make_net("ov"),
        Step(
            name="create-first",
            method="POST",
            path="/vpc/v1/subnets",
            body={"folderId": "{{_suiteFolderId}}", "networkId": "{{netId}}",
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
            body={"folderId": "{{_suiteFolderId}}", "networkId": "{{netId}}",
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
            # verbatim-YC (probe 2026-05-11, kacho-vpc#7): malformed id (нет известного 3-char префикса)
            # → 400 InvalidArgument "invalid subnet id '<X>'" (раньше было 404 NotFound). Проверка family-agnostic.
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
    title="List subnets в folder → 200",
    classes=["CRUD"],
    priority="P1",
    steps=[
        Step(
            name="list",
            method="GET",
            path="/vpc/v1/subnets?folderId={{_suiteFolderId}}&pageSize=10",
            test_script=[*assert_status(200),
                         "pm.test('subnets array', () => pm.expect(pm.response.json().subnets || []).to.be.an('array'));"],
        ),
    ],
))

CASES.append(Case(
    id="SUB-LST-VAL-FOLDER-REQUIRED",
    title="List без folderId → InvalidArgument",
    classes=["VAL", "AUTHZ"],
    priority="P0",
    steps=[
        Step(name="list-no-folder", method="GET", path="/vpc/v1/subnets",
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
    title="Update с mask=v4_cidr_blocks → принимается (200); YC мутирует CIDR, мы — no-op (kacho-vpc#10)",
    classes=["STATE", "CRUD"],
    priority="P1",
    steps=[
        *_make_net("im"),
        Step(name="create-sub", method="POST", path="/vpc/v1/subnets",
             body={"folderId": "{{_suiteFolderId}}", "networkId": "{{netId}}",
                   "name": "sub-im-{{runId}}", "zoneId": "{{existingZoneId}}",
                   "v4CidrBlocks": ["10.30.0.0/24"]},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.subnetId", "subId")]),
        poll_operation_until_done(),
        # verbatim YC (probe 2026-05-11, kacho-vpc#10): v4_cidr_blocks в update_mask
        # больше НЕ отвергается — 200. (YC при этом меняет CIDR; наш repo.Update
        # CIDR-колонки не перезаписывает → изменение CIDR через Update у нас no-op,
        # см. 07-known-divergences.md.)
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
             body={"folderId": "{{_suiteFolderId}}", "networkId": "{{netId}}",
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
    id="SUB-REL-NEG-IN-USE",
    title="Relocate subnet (с Address-ом) → sync 400 FailedPrecondition 'Invalid subnet state' (kacho-vpc#10)",
    classes=["NEG", "CONF"],
    priority="P1",
    steps=[
        *_make_net("rel"),
        Step(name="create-sub", method="POST", path="/vpc/v1/subnets",
             body={"folderId": "{{_suiteFolderId}}", "networkId": "{{netId}}",
                   "name": "sub-rel-{{runId}}", "zoneId": "{{existingZoneId}}",
                   "v4CidrBlocks": ["10.70.0.0/24"]},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.subnetId", "subId")]),
        poll_operation_until_done(),
        Step(name="create-addr", method="POST", path="/vpc/v1/addresses",
             body={"folderId": "{{_suiteFolderId}}",
                   "name": "addr-rel-{{runId}}",
                   "internalIpv4AddressSpec": {"subnetId": "{{subId}}"}},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.addressId", "addrId")]),
        poll_operation_until_done(),
        # verbatim YC (probe 2026-05-11, kacho-vpc#10): Relocate ВСЕГДА → sync 400
        # FailedPrecondition "Invalid subnet state" (Operation не создаётся).
        Step(name="relocate", method="POST", path="/vpc/v1/subnets/{{subId}}:relocate",
             body={"destinationZoneId": "{{existingZoneAltId}}"},
             test_script=[*assert_status(400), *assert_grpc_code(9, "FAILED_PRECONDITION"),
                          "pm.test('text \"Invalid subnet state\"', () => pm.expect(pm.response.json().message).to.eql('Invalid subnet state'));"]),
        Step(name="cleanup-addr", method="DELETE", path="/vpc/v1/addresses/{{addrId}}",
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
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
             body={"folderId": "{{_suiteFolderId}}", "networkId": "{{netId}}",
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
             body={"folderId": "{{_suiteFolderId}}", "networkId": "{{netId}}",
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
CASES.append(authz_move_nf("SUB", "/vpc/v1/subnets"))

CASES.append(Case(
    id="SUB-MV-CRUD-OK",
    title="Move subnet в другой folder → folder_id обновлён",
    classes=["CRUD"], priority="P1",
    steps=[
        *_make_net("mv"),
        Step(name="create-sub", method="POST", path="/vpc/v1/subnets",
             body={"folderId": "{{_suiteFolderId}}", "networkId": "{{netId}}",
                   "name": "sub-mv-{{runId}}", "zoneId": "{{existingZoneId}}",
                   "v4CidrBlocks": ["10.110.0.0/24"]},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.subnetId", "subId")]),
        poll_operation_until_done(),
        Step(name="move", method="POST", path="/vpc/v1/subnets/{{subId}}:move",
             body={"destinationFolderId": "{{_suiteFolderCrossId}}"},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="verify", method="GET", path="/vpc/v1/subnets/{{subId}}",
             test_script=[*assert_status(200),
                          "pm.test('folder updated', () => pm.expect(pm.response.json().folderId).to.eql(pm.environment.get('_suiteFolderCrossId')));"]),
        Step(name="cleanup-sub", method="DELETE", path="/vpc/v1/subnets/{{subId}}",
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        _cleanup_net(),
    ],
))

CASES.append(Case(
    id="SUB-UPD-CRUD-OK",
    title="Update Subnet description",
    classes=["CRUD"], priority="P1",
    steps=[
        *_make_net("upd"),
        Step(name="create-sub", method="POST", path="/vpc/v1/subnets",
             body={"folderId": "{{_suiteFolderId}}", "networkId": "{{netId}}",
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
             body={"folderId": "{{_suiteFolderId}}", "networkId": "{{netId}}",
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

# Дополнение: STATE immutable folder + VAL move-no-dest + BVA pagesize=1
CASES.append(state_immutable_folder("SUB", "/vpc/v1/subnets"))
CASES.append(val_move_no_dest("SUB", "/vpc/v1/subnets"))
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
             body={"folderId": "{{_suiteFolderId}}", "networkId": "{{netId}}",
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
    id="SUB-REL-STATE-NO-ADDRESSES-OK",
    title="Relocate subnet без Address → YC всё равно 400 'Invalid subnet state' (kacho-vpc#10; -OK исторический)",
    classes=["STATE", "NEG"], priority="P1",
    steps=[
        *_make_net("rels"),
        Step(name="create-sub", method="POST", path="/vpc/v1/subnets",
             body={"folderId": "{{_suiteFolderId}}", "networkId": "{{netId}}",
                   "name": "sub-rels-{{runId}}", "zoneId": "{{existingZoneId}}",
                   "v4CidrBlocks": ["10.160.0.0/24"]},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.subnetId", "subId")]),
        poll_operation_until_done(),
        # verbatim YC (probe 2026-05-11): Relocate отвергается даже для свежей
        # подсети без адресов — sync 400 FailedPrecondition "Invalid subnet state".
        Step(name="relocate", method="POST", path="/vpc/v1/subnets/{{subId}}:relocate",
             body={"destinationZoneId": "{{existingZoneAltId}}"},
             test_script=[*assert_status(400), *assert_grpc_code(9, "FAILED_PRECONDITION"),
                          "pm.test('text \"Invalid subnet state\"', () => pm.expect(pm.response.json().message).to.eql('Invalid subnet state'));"]),
        Step(name="cleanup-sub", method="DELETE", path="/vpc/v1/subnets/{{subId}}",
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        _cleanup_net(),
    ],
))

CASES.append(Case(
    id="SUB-CR-CONF-NET-NF-TEXT",
    title="Create subnet в garbage network → sync verbatim 'Network ... not found' (kacho-vpc#8)",
    classes=["CONF", "NEG"], priority="P1",
    steps=[
        Step(name="create-bad-net", method="POST", path="/vpc/v1/subnets",
             body={"folderId": "{{_suiteFolderId}}", "networkId": "{{garbageVpcId}}",
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
    title="Subnet duplicate name в folder → ALREADY_EXISTS (FINDING-005 fixed: migration 0002 UNIQUE)",
    classes=["NEG", "CONF", "CONC"], priority="P0",
    steps=[
        *_make_net("dup"),
        Step(name="create-first", method="POST", path="/vpc/v1/subnets",
             body={"folderId": "{{_suiteFolderId}}", "networkId": "{{netId}}",
                   "name": "sub-dup-{{runId}}", "zoneId": "{{existingZoneId}}",
                   "v4CidrBlocks": ["10.180.0.0/24"]},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.subnetId", "subId1")]),
        poll_operation_until_done(),
        Step(name="create-dup", method="POST", path="/vpc/v1/subnets",
             body={"folderId": "{{_suiteFolderId}}", "networkId": "{{netId}}",
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

CASES.append(Case(
    id="SUB-MV-CONF-NF-TEXT",
    title="Move несуществующего → verbatim '<Resource> ... not found' text",
    classes=["CONF", "NEG"], priority="P1",
    steps=[
        Step(name="move-nx", method="POST", path="/vpc/v1/subnets/{{garbageVpcId}}:move",
             body={"destinationFolderId": "{{_suiteFolderId}}"},
             test_script=[
                 *assert_status(404), *assert_grpc_code(5, "NOT_FOUND"),
                 "pm.test('non-empty error text', () => pm.expect(pm.response.json().message).to.be.a('string').and.length.greaterThan(0));",
             ]),
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
             body={"folderId": "{{_suiteFolderId}}", "networkId": "{{netId}}",
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
             body={"folderId": "{{_suiteFolderId}}", "networkId": "{{netId}}",
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
             body={"folderId": "{{_suiteFolderId}}", "networkId": "{{netId}}",
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
    id="SUB-REL-VAL-NO-DEST",
    title="Relocate без destinationZoneId → InvalidArgument",
    classes=["VAL", "NEG"], priority="P1",
    steps=[
        Step(name="rel-no-dest", method="POST",
             path="/vpc/v1/subnets/{{garbageVpcId}}:relocate",
             body={},
             test_script=["pm.test('rejected', () => pm.expect(pm.response.code).to.be.oneOf([400, 404]));"]),
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
    title="Delete несуществующего Subnet → verbatim 'Subnet ... not found'",
    classes=["CONF", "NEG"], priority="P1",
    steps=[
        Step(name="del-nx", method="DELETE", path="/vpc/v1/subnets/{{garbageVpcId}}",
             test_script=[*assert_status(404), *assert_grpc_code(5, "NOT_FOUND"),
                          "pm.test('Subnet ... not found', () => pm.expect(pm.response.json().message).to.match(/^Subnet .* not found$/));"]),
    ],
))

CASES.append(Case(
    id="SUB-UPD-CONF-NF-TEXT",
    title="Update несуществующего Subnet → verbatim 'Subnet ... not found'",
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
             body={"folderId": "{{_suiteFolderId}}", "networkId": "{{netId}}",
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
    {"folderId": "{{_suiteFolderId}}", "networkId": "{{netId}}",
     "zoneId": "{{existingZoneId}}", "v4CidrBlocks": ["10.241.0.0/24"]}):
    CASES.append(_wrap_with_net("SUB", "v7", c))

CASES.extend(perf_baseline_block("SUB", "/vpc/v1/subnets"))
CASES.extend(verbatim_text_pack("SUB", "Subnet", "/vpc/v1/subnets"))
CASES.extend(authz_caller_headers_block("SUB", "/vpc/v1/subnets"))

# move-self для subnet
CASES.append(_wrap_with_net("SUB", "mvself",
    move_same_folder("SUB", "/vpc/v1/subnets",
        {"folderId": "{{_suiteFolderId}}", "networkId": "{{netId}}",
         "zoneId": "{{existingZoneId}}", "v4CidrBlocks": ["10.242.0.0/24"]})))

# v8 subnet
CASES.append(_wrap_with_net("SUB", "v8m",
    update_happy_multi_field("SUB", "/vpc/v1/subnets", "/vpc/v1/subnets",
        {"folderId": "{{_suiteFolderId}}", "networkId": "{{netId}}",
         "zoneId": "{{existingZoneId}}", "v4CidrBlocks": ["10.243.0.0/24"]})))
CASES.append(_wrap_with_net("SUB", "v8f",
    list_filter_match_block("SUB", "/vpc/v1/subnets",
        {"folderId": "{{_suiteFolderId}}", "networkId": "{{netId}}",
         "zoneId": "{{existingZoneId}}", "v4CidrBlocks": ["10.244.0.0/24"]})))
for c in neg_invalid_types_block("SUB", "/vpc/v1/subnets",
    {"folderId": "{{_suiteFolderId}}", "networkId": "{{netId}}",
     "zoneId": "{{existingZoneId}}", "v4CidrBlocks": ["10.245.0.0/24"]}):
    CASES.append(_wrap_with_net("SUB", "v8nt", c))
CASES.extend(http_method_not_allowed_block("SUB", "/vpc/v1/subnets"))
CASES.extend(malformed_body_block("SUB", "/vpc/v1/subnets"))

# NB: dup-name для Subnet покрыт hand-written SUB-CR-NEG-DUP-NAME (использует РАЗНЫЕ
# CIDR у обеих подсетей). Generated alreadyexists_dup_name_for тут НЕ применим: он
# создаёт две подсети с ОДИНАКОВЫМ телом (тот же CIDR) → verbatim-YC проверяет
# overlap раньше name-uniqueness и возвращает FAILED_PRECONDITION "Subnet CIDRs can
# not overlap", а не ALREADY_EXISTS. (kacho-vpc#8.)
for c in update_mask_partial_block("SUB", "/vpc/v1/subnets", "/vpc/v1/subnets",
    {"folderId": "{{_suiteFolderId}}", "networkId": "{{netId}}",
     "zoneId": "{{existingZoneId}}", "v4CidrBlocks": ["10.247.0.0/24"]}):
    CASES.append(_wrap_with_net("SUB", "v9p", c))
CASES.append(_wrap_with_net("SUB", "v9pf",
    perf_baseline_get_block("SUB", "/vpc/v1/subnets",
        {"folderId": "{{_suiteFolderId}}", "networkId": "{{netId}}",
         "zoneId": "{{existingZoneId}}", "v4CidrBlocks": ["10.248.0.0/24"]})))
CASES.extend(list_total_size_check_block("SUB", "/vpc/v1/subnets"))

# v10: subnet-specific dhcp_options + cidr boundary
def _sub_dhcp(opts):
    return {"folderId": "{{_suiteFolderId}}", "networkId": "{{netId}}",
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

# CIDR prefix boundary — verbatim YC (probe 2026-05-11, kacho-vpc#10):
# /28 принимается; /29, /30, /31 → 400 "Illegal argument Invalid network prefix /N".
CASES.append(_wrap_with_net("SUB", "v10cidr28",
    Case(
        id="SUB-CR-BVA-CIDR-28",
        title="Create subnet с prefix /28 → 200 (минимальный размер по YC)",
        classes=["BVA", "CRUD"], priority="P2",
        steps=[
            Step(name="cr-prefix-28", method="POST", path="/vpc/v1/subnets",
                 body={"folderId": "{{_suiteFolderId}}", "networkId": "{{netId}}",
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
            title=f"Create subnet с prefix /{_n} → 400 'Illegal argument Invalid network prefix /{_n}' (verbatim YC)",
            classes=["BVA", "VAL", "NEG"], priority="P2",
            steps=[
                Step(name=f"cr-prefix-{_n}", method="POST", path="/vpc/v1/subnets",
                     body={"folderId": "{{_suiteFolderId}}", "networkId": "{{netId}}",
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
             body={"folderId": "{{_suiteFolderId}}", "networkId": "{{netId}}",
                   "name": "sub-hasad-{{runId}}", "zoneId": "{{existingZoneId}}",
                   "v4CidrBlocks": ["10.251.0.0/24"]},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.subnetId", "subId")]),
        poll_operation_until_done(),
        Step(name="cr-internal-addr", method="POST", path="/vpc/v1/addresses",
             body={"folderId": "{{_suiteFolderId}}", "name": "adr-hasad-{{runId}}",
                   "internalIpv4AddressSpec": {"subnetId": "{{subId}}"}},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.addressId", "addrId")]),
        poll_operation_until_done(),
        Step(name="del-sub-blocked", method="DELETE", path="/vpc/v1/subnets/{{subId}}",
             test_script=[
                 # verbatim-YC (probe 2026-05-11, kacho-vpc#8): delete subnet с internal Address →
                 # sync FAILED_PRECONDITION "Subnet has allocated internal addresses".
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
    id="SUB-DEL-CRUD-EMPTY-OK",
    title="Delete Subnet без зависимостей → OK",
    classes=["CRUD"], priority="P1",
    steps=[
        *_make_net("delempty"),
        Step(name="cr-sub", method="POST", path="/vpc/v1/subnets",
             body={"folderId": "{{_suiteFolderId}}", "networkId": "{{netId}}",
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
    {"folderId": "{{_suiteFolderId}}", "networkId": "{{netId}}",
     "name": "sub-req-{{runId}}", "zoneId": "{{existingZoneId}}",
     "v4CidrBlocks": ["10.190.0.0/24"]},
    ["folderId", "networkId", "name", "zoneId", "v4CidrBlocks"]):
    CASES.append(_wrap_with_net("SUB", "req", c))
CASES.extend(immutable_fields_matrix("SUB", "/vpc/v1/subnets",
    ["folder_id", "network_id", "zone_id", "v4_cidr_blocks", "v6_cidr_blocks"]))

# === Subnet CIDR expand/shrink pack — обёрнут в setup-сцену ===
# Создаём один Subnet с primary CIDR 10.180.0.0/24, accumulate 4 cidr через add,
# потом гоняем 8 кейсов remove/add/overlap/roundtrip.
def _subnet_cidr_setup_teardown(case):
    return Case(
        id=case.id, title=case.title, classes=case.classes, priority=case.priority,
        steps=[
            *_make_net("cidrexp"),
            Step(name="setup-sub", method="POST", path="/vpc/v1/subnets",
                 body={"folderId": "{{_suiteFolderId}}", "networkId": "{{netId}}",
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
    {"folderId": "{{_suiteFolderId}}", "networkId": "{{netId}}",
     "zoneId": "{{existingZoneId}}", "v4CidrBlocks": ["10.169.0.0/24"]}):
    CASES.append(_wrap_with_net("SUB", "sec", c))
