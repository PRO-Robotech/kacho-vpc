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
    title="Create с несуществующей зоной → InvalidArgument (dynamic whitelist)",
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
            test_script=[*assert_status(400), *assert_grpc_code(3, "INVALID_ARGUMENT"),
                         "pm.test('mentions zone whitelist', () => pm.expect(pm.response.json().details && JSON.stringify(pm.response.json())).to.include('zone_id'));"],
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
    title="Create в несуществующей network → async NOT_FOUND",
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
            test_script=[*assert_status(200), *save_from_response("j.id", "opId")],
        ),
        poll_operation_until_done(),
        Step(
            name="assert-nf",
            method="GET",
            path="/operations/{{opId}}",
            test_script=["pm.test('error code 5', () => pm.expect(pm.response.json().error && pm.response.json().error.code).to.eql(5));"],
        ),
    ],
))

CASES.append(Case(
    id="SUB-CR-NEG-CIDR-OVERLAP",
    title="Create двух subnet с пересекающимися CIDR → второй FailedPrecondition",
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
            test_script=[*assert_status(200), *save_from_response("j.id", "opId")],
        ),
        poll_operation_until_done(),
        Step(
            name="assert-failed-precondition",
            method="GET",
            path="/operations/{{opId}}",
            test_script=[
                "const j = pm.response.json();",
                "pm.test('error code 9 (FAILED_PRECONDITION)', () => pm.expect(j.error && j.error.code, JSON.stringify(j)).to.eql(9));",
                "pm.test('text mentions overlap', () => pm.expect(j.error.message.toLowerCase()).to.match(/overlap|cidr/));",
            ],
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
    title="Get garbage → 404",
    classes=["NEG"],
    priority="P0",
    steps=[
        Step(
            name="get-garbage",
            method="GET",
            path="/vpc/v1/subnets/{{garbageId}}",
            test_script=[*assert_status(404), *assert_grpc_code(5, "NOT_FOUND")],
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
    title="Update с mask=v4_cidr_blocks → InvalidArgument (immutable)",
    classes=["STATE", "VAL"],
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
        Step(name="patch-cidr-via-mask", method="PATCH", path="/vpc/v1/subnets/{{subId}}",
             body={"updateMask": "v4_cidr_blocks", "v4CidrBlocks": ["10.31.0.0/24"]},
             test_script=[*assert_status(400), *assert_grpc_code(3, "INVALID_ARGUMENT")]),
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
    title="Relocate subnet с Address-ом → FailedPrecondition 'Invalid subnet state'",
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
        Step(name="relocate", method="POST", path="/vpc/v1/subnets/{{subId}}:relocate",
             body={"destinationZoneId": "{{existingZoneAltId}}"},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="assert-failed-precondition", method="GET", path="/operations/{{opId}}",
             test_script=[
                 "const j = pm.response.json();",
                 "pm.test('error code 9 (FAILED_PRECONDITION)', () => pm.expect(j.error && j.error.code, JSON.stringify(j)).to.eql(9));",
                 "pm.test('text \"Invalid subnet state\"', () => pm.expect(j.error.message).to.include('Invalid subnet state'));",
             ]),
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
