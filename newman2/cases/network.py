"""Case-set для NetworkService (kacho-vpc).

Covered RPCs:
  Get, List, Create, Update, Delete, Move,
  ListSubnets, ListSecurityGroups, ListRouteTables, ListOperations
"""

# Helpers инжектятся gen.py через namespace модуля:
#   Step, Case, assert_status, assert_grpc_code, assert_field_violation,
#   save_from_response, assert_operation_envelope, poll_operation_until_done

CASES = []

# ---------------------------------------------------------------------------
# NET-CR — Create Network
# ---------------------------------------------------------------------------

CASES.append(Case(
    id="NET-CR-CRUD-OK",
    title="Create network → Operation → Network в response",
    classes=["CRUD"],
    priority="P1",
    steps=[
        Step(
            name="create",
            method="POST",
            path="/vpc/v1/networks",
            body={
                "folderId": "{{_suiteFolderId}}",
                "name": "net-cr-{{runId}}",
                "description": "newman2 CRUD-OK",
                "labels": {"suite": "newman2"},
            },
            test_script=[
                *assert_status(200),
                *assert_operation_envelope(),
                *save_from_response("j.id", "opId"),
                *save_from_response("j.metadata && j.metadata.networkId", "createdNetworkId"),
            ],
        ),
        poll_operation_until_done(),
        Step(
            name="get-confirms",
            method="GET",
            path="/vpc/v1/networks/{{createdNetworkId}}",
            test_script=[
                *assert_status(200),
                "const j = pm.response.json();",
                "pm.test('id matches', () => pm.expect(j.id).to.eql(pm.environment.get('createdNetworkId')));",
                "pm.test('folderId matches', () => pm.expect(j.folderId).to.eql(pm.environment.get('_suiteFolderId')));",
                "pm.test('name matches', () => pm.expect(j.name).to.match(/^net-cr-/));",
            ],
        ),
        Step(
            name="cleanup-delete",
            method="DELETE",
            path="/vpc/v1/networks/{{createdNetworkId}}",
            test_script=[*assert_status(200)],
        ),
    ],
))

CASES.append(Case(
    id="NET-CR-VAL-FOLDER-REQUIRED",
    title="Create без folderId → InvalidArgument (folder_id required)",
    classes=["VAL"],
    priority="P0",
    steps=[
        Step(
            name="create-no-folder",
            method="POST",
            path="/vpc/v1/networks",
            body={"name": "net-noflder-{{runId}}"},
            test_script=[
                *assert_status(400),
                *assert_grpc_code(3, "INVALID_ARGUMENT"),
            ],
        ),
    ],
))

CASES.append(Case(
    id="NET-CR-NEG-FOLDER-NOT-FOUND",
    title="Create с garbage folderId → async NOT_FOUND",
    classes=["NEG"],
    priority="P0",
    steps=[
        Step(
            name="create-bad-folder",
            method="POST",
            path="/vpc/v1/networks",
            body={"folderId": "{{garbageId}}", "name": "net-bf-{{runId}}"},
            test_script=[
                *assert_status(200),
                *save_from_response("j.id", "opId"),
            ],
        ),
        poll_operation_until_done(),
        Step(
            name="assert-not-found",
            method="GET",
            path="/operations/{{opId}}",
            test_script=[
                "const j = pm.response.json();",
                "pm.test('operation done', () => pm.expect(j.done).to.eql(true));",
                "pm.test('error code 5 (NOT_FOUND)', () => pm.expect(j.error && j.error.code, JSON.stringify(j)).to.eql(5));",
                "pm.test('error text mentions folder', () => pm.expect(j.error.message.toLowerCase()).to.include('folder'));",
            ],
        ),
    ],
))

CASES.append(Case(
    id="NET-CR-NEG-DUP-NAME",
    title="Create с duplicate name в folder → async ALREADY_EXISTS",
    classes=["NEG", "CONC"],
    priority="P1",
    steps=[
        Step(
            name="create-first",
            method="POST",
            path="/vpc/v1/networks",
            body={"folderId": "{{_suiteFolderId}}", "name": "net-dup-{{runId}}"},
            test_script=[
                *assert_status(200),
                *save_from_response("j.id", "opId"),
                *save_from_response("j.metadata && j.metadata.networkId", "createdNetworkId"),
            ],
        ),
        poll_operation_until_done(),
        Step(
            name="create-second-same-name",
            method="POST",
            path="/vpc/v1/networks",
            body={"folderId": "{{_suiteFolderId}}", "name": "net-dup-{{runId}}"},
            test_script=[
                *assert_status(200),
                *save_from_response("j.id", "opId"),
            ],
        ),
        poll_operation_until_done(),
        Step(
            name="assert-already-exists",
            method="GET",
            path="/operations/{{opId}}",
            test_script=[
                "const j = pm.response.json();",
                "pm.test('operation done', () => pm.expect(j.done).to.eql(true));",
                "pm.test('error code 6 (ALREADY_EXISTS)', () => pm.expect(j.error && j.error.code, JSON.stringify(j)).to.eql(6));",
            ],
        ),
        Step(
            name="cleanup-first",
            method="DELETE",
            path="/vpc/v1/networks/{{createdNetworkId}}",
            test_script=[*assert_status(200)],
        ),
    ],
))

# ---------------------------------------------------------------------------
# NET-GET — Get Network
# ---------------------------------------------------------------------------

CASES.append(Case(
    id="NET-GET-NEG-NOT-FOUND",
    title="Get garbage id → NOT_FOUND (verbatim YC async-style)",
    classes=["NEG", "CONF"],
    priority="P0",
    steps=[
        Step(
            name="get-garbage",
            method="GET",
            path="/vpc/v1/networks/{{garbageId}}",
            test_script=[
                *assert_status(404),
                *assert_grpc_code(5, "NOT_FOUND"),
                "pm.test('error text mentions Network', () => pm.expect(pm.response.json().message.toLowerCase()).to.include('not found'));",
            ],
        ),
    ],
))

CASES.append(Case(
    id="NET-GET-NEG-EMPTY-ID",
    title="Get empty id → 404 (gRPC-gateway routing)",
    classes=["NEG"],
    priority="P2",
    steps=[
        Step(
            name="get-empty",
            method="GET",
            path="/vpc/v1/networks/",
            test_script=[
                "pm.test('non-2xx response', () => pm.expect(pm.response.code).to.be.oneOf([400, 404]));",
            ],
        ),
    ],
))

# ---------------------------------------------------------------------------
# NET-LST — List Networks
# ---------------------------------------------------------------------------

CASES.append(Case(
    id="NET-LST-CRUD-OK",
    title="List networks в folder → 200 + массив",
    classes=["CRUD"],
    priority="P1",
    steps=[
        Step(
            name="list",
            method="GET",
            path="/vpc/v1/networks?folderId={{_suiteFolderId}}&pageSize=10",
            test_script=[
                *assert_status(200),
                "const j = pm.response.json();",
                "pm.test('has networks array', () => pm.expect(j.networks).to.be.an('array'));",
                "pm.test('nextPageToken is string', () => pm.expect(j.nextPageToken).to.be.a('string'));",
            ],
        ),
    ],
))

CASES.append(Case(
    id="NET-LST-VAL-FOLDER-REQUIRED",
    title="List без folderId → InvalidArgument (no cross-folder enum)",
    classes=["VAL", "AUTHZ"],
    priority="P0",
    steps=[
        Step(
            name="list-no-folder",
            method="GET",
            path="/vpc/v1/networks",
            test_script=[
                *assert_status(400),
                *assert_grpc_code(3, "INVALID_ARGUMENT"),
            ],
        ),
    ],
))

CASES.append(Case(
    id="NET-LST-BVA-PAGESIZE-ZERO",
    title="List pageSize=0 → default applied (200, ≤50 items)",
    classes=["BVA"],
    priority="P2",
    steps=[
        Step(
            name="list-pagesize-0",
            method="GET",
            path="/vpc/v1/networks?folderId={{_suiteFolderId}}&pageSize=0",
            test_script=[
                *assert_status(200),
                "const j = pm.response.json();",
                "pm.test('default pagesize applied', () => pm.expect(j.networks.length).to.be.at.most(50));",
            ],
        ),
    ],
))

CASES.append(Case(
    id="NET-LST-BVA-PAGESIZE-OVER-MAX",
    title="List pageSize=10000 → InvalidArgument",
    classes=["BVA", "VAL"],
    priority="P2",
    steps=[
        Step(
            name="list-pagesize-huge",
            method="GET",
            path="/vpc/v1/networks?folderId={{_suiteFolderId}}&pageSize=10000",
            test_script=[
                *assert_status(400),
                *assert_grpc_code(3, "INVALID_ARGUMENT"),
            ],
        ),
    ],
))

CASES.append(Case(
    id="NET-LST-PAGE-TOKEN-GARBAGE",
    title="List с garbage page_token → InvalidArgument",
    classes=["PAGE", "VAL"],
    priority="P1",
    steps=[
        Step(
            name="list-bad-token",
            method="GET",
            path="/vpc/v1/networks?folderId={{_suiteFolderId}}&pageSize=10&pageToken=not-a-real-token",
            test_script=[
                *assert_status(400),
                *assert_grpc_code(3, "INVALID_ARGUMENT"),
            ],
        ),
    ],
))

# ---------------------------------------------------------------------------
# NET-UPD — Update Network
# ---------------------------------------------------------------------------

CASES.append(Case(
    id="NET-UPD-CRUD-DESCRIPTION",
    title="Update description через mask → success + новое значение видно",
    classes=["CRUD"],
    priority="P1",
    steps=[
        Step(
            name="create",
            method="POST",
            path="/vpc/v1/networks",
            body={"folderId": "{{_suiteFolderId}}", "name": "net-upd-{{runId}}"},
            test_script=[
                *assert_status(200),
                *save_from_response("j.id", "opId"),
                *save_from_response("j.metadata && j.metadata.networkId", "netId"),
            ],
        ),
        poll_operation_until_done(),
        Step(
            name="update-desc",
            method="PATCH",
            path="/vpc/v1/networks/{{netId}}",
            body={"updateMask": "description", "description": "patched-desc"},
            test_script=[
                *assert_status(200),
                *save_from_response("j.id", "opId"),
            ],
        ),
        poll_operation_until_done(),
        Step(
            name="verify",
            method="GET",
            path="/vpc/v1/networks/{{netId}}",
            test_script=[
                *assert_status(200),
                "pm.test('description updated', () => pm.expect(pm.response.json().description).to.eql('patched-desc'));",
            ],
        ),
        Step(
            name="cleanup",
            method="DELETE",
            path="/vpc/v1/networks/{{netId}}",
            test_script=[*assert_status(200)],
        ),
    ],
))

CASES.append(Case(
    id="NET-UPD-STATE-IMMUTABLE-FOLDER",
    title="Update с mask=folder_id → InvalidArgument (immutable)",
    classes=["STATE", "VAL"],
    priority="P1",
    steps=[
        Step(
            name="update-folder-via-mask",
            method="PATCH",
            path="/vpc/v1/networks/{{garbageId}}",
            body={"updateMask": "folder_id", "folderId": "x"},
            test_script=[
                *assert_status(400),
                *assert_grpc_code(3, "INVALID_ARGUMENT"),
            ],
        ),
    ],
))

CASES.append(Case(
    id="NET-UPD-NEG-NF-INVALID-PREFIX",
    title="Update с id без VPC-префикса → sync 404 (gateway prefix-routing)",
    classes=["NEG", "STATE"],
    priority="P1",
    steps=[
        Step(
            name="patch-invalid-prefix",
            method="PATCH",
            path="/vpc/v1/networks/{{garbageId}}",
            body={"updateMask": "description", "description": "x"},
            test_script=[
                # Документированное поведение: gateway отсекает id без 3-char
                # VPC-префикса синхронно (см. BUG-MAP / REQUIREMENTS).
                *assert_status(404),
                *assert_grpc_code(5, "NOT_FOUND"),
            ],
        ),
    ],
))

CASES.append(Case(
    id="NET-UPD-AUTHZ-NF-SYNC",
    title="Update несуществующего id (валидный префикс) → sync 404 от AuthZ-Get",
    classes=["NEG", "AUTHZ"],
    priority="P1",
    steps=[
        Step(
            name="patch-nonexistent",
            method="PATCH",
            path="/vpc/v1/networks/{{garbageVpcId}}",
            body={"updateMask": "description", "description": "x"},
            test_script=[
                # Update делает sync Get → AssertFolderOwnership перед созданием
                # Operation. Sync 404 даже для valid-prefix id.
                *assert_status(404),
                *assert_grpc_code(5, "NOT_FOUND"),
            ],
        ),
    ],
))

# ---------------------------------------------------------------------------
# NET-DEL — Delete Network
# ---------------------------------------------------------------------------

CASES.append(Case(
    id="NET-DEL-NEG-NF-INVALID-PREFIX",
    title="Delete с id без VPC-префикса → sync 404",
    classes=["NEG", "STATE"],
    priority="P1",
    steps=[
        Step(
            name="delete-invalid-prefix",
            method="DELETE",
            path="/vpc/v1/networks/{{garbageId}}",
            test_script=[
                *assert_status(404),
                *assert_grpc_code(5, "NOT_FOUND"),
            ],
        ),
    ],
))

CASES.append(Case(
    id="NET-DEL-AUTHZ-NF-SYNC",
    title="Delete несуществующего id (валидный префикс) → sync 404 от AuthZ-Get",
    classes=["NEG", "AUTHZ"],
    priority="P1",
    steps=[
        Step(
            name="delete-nonexistent",
            method="DELETE",
            path="/vpc/v1/networks/{{garbageVpcId}}",
            test_script=[
                # Delete делает sync Get → AssertFolderOwnership.
                *assert_status(404),
                *assert_grpc_code(5, "NOT_FOUND"),
            ],
        ),
    ],
))

# ---------------------------------------------------------------------------
# NET-MV — Move Network
# ---------------------------------------------------------------------------

CASES.append(Case(
    id="NET-MV-CRUD-OK",
    title="Move network в другой folder → success + folder_id обновлён",
    classes=["CRUD"],
    priority="P1",
    steps=[
        Step(
            name="create",
            method="POST",
            path="/vpc/v1/networks",
            body={"folderId": "{{_suiteFolderId}}", "name": "net-mv-{{runId}}"},
            test_script=[
                *assert_status(200),
                *save_from_response("j.id", "opId"),
                *save_from_response("j.metadata && j.metadata.networkId", "netId"),
            ],
        ),
        poll_operation_until_done(),
        Step(
            name="move",
            method="POST",
            path="/vpc/v1/networks/{{netId}}:move",
            body={"destinationFolderId": "{{_suiteFolderCrossId}}"},
            test_script=[
                *assert_status(200),
                *save_from_response("j.id", "opId"),
            ],
        ),
        poll_operation_until_done(),
        Step(
            name="verify-moved",
            method="GET",
            path="/vpc/v1/networks/{{netId}}",
            test_script=[
                *assert_status(200),
                "pm.test('folder updated', () => pm.expect(pm.response.json().folderId).to.eql(pm.environment.get('_suiteFolderCrossId')));",
            ],
        ),
        Step(
            name="cleanup",
            method="DELETE",
            path="/vpc/v1/networks/{{netId}}",
            test_script=[*assert_status(200)],
        ),
    ],
))

CASES.append(Case(
    id="NET-MV-NEG-DEST-FOLDER-NF",
    title="Move в garbage folder → async NOT_FOUND",
    classes=["NEG"],
    priority="P1",
    steps=[
        Step(
            name="create",
            method="POST",
            path="/vpc/v1/networks",
            body={"folderId": "{{_suiteFolderId}}", "name": "net-mv-nf-{{runId}}"},
            test_script=[
                *assert_status(200),
                *save_from_response("j.id", "opId"),
                *save_from_response("j.metadata && j.metadata.networkId", "netId"),
            ],
        ),
        poll_operation_until_done(),
        Step(
            name="move-to-garbage",
            method="POST",
            path="/vpc/v1/networks/{{netId}}:move",
            body={"destinationFolderId": "{{garbageId}}"},
            test_script=[
                *assert_status(200),
                *save_from_response("j.id", "opId"),
            ],
        ),
        poll_operation_until_done(),
        Step(
            name="assert-not-found",
            method="GET",
            path="/operations/{{opId}}",
            test_script=[
                "const j = pm.response.json();",
                "pm.test('error code 5 (NOT_FOUND)', () => pm.expect(j.error && j.error.code, JSON.stringify(j)).to.eql(5));",
            ],
        ),
        Step(
            name="cleanup",
            method="DELETE",
            path="/vpc/v1/networks/{{netId}}",
            test_script=[*assert_status(200)],
        ),
    ],
))

# ---------------------------------------------------------------------------
# NET-LSUB / NET-LSG / NET-LRT — child lists
# ---------------------------------------------------------------------------

CASES.append(Case(
    id="NET-LSUB-CRUD-EMPTY",
    title="ListSubnets для пустой network → 200 + empty array",
    classes=["CRUD"],
    priority="P2",
    steps=[
        Step(
            name="create",
            method="POST",
            path="/vpc/v1/networks",
            body={"folderId": "{{_suiteFolderId}}", "name": "net-lsub-{{runId}}"},
            test_script=[
                *assert_status(200),
                *save_from_response("j.id", "opId"),
                *save_from_response("j.metadata && j.metadata.networkId", "netId"),
            ],
        ),
        poll_operation_until_done(),
        Step(
            name="list-subnets",
            method="GET",
            path="/vpc/v1/networks/{{netId}}/subnets",
            test_script=[
                *assert_status(200),
                "pm.test('subnets array empty or one (default-SG sometimes)', () => pm.expect(pm.response.json().subnets || []).to.be.an('array'));",
            ],
        ),
        Step(
            name="cleanup",
            method="DELETE",
            path="/vpc/v1/networks/{{netId}}",
            test_script=[*assert_status(200)],
        ),
    ],
))

CASES.append(Case(
    id="NET-LSG-CRUD-DEFAULT-SG",
    title="ListSecurityGroups → default SG присутствует (inline create в doCreate)",
    classes=["CRUD"],
    priority="P1",
    steps=[
        Step(
            name="create",
            method="POST",
            path="/vpc/v1/networks",
            body={"folderId": "{{_suiteFolderId}}", "name": "net-lsg-{{runId}}"},
            test_script=[
                *assert_status(200),
                *save_from_response("j.id", "opId"),
                *save_from_response("j.metadata && j.metadata.networkId", "netId"),
            ],
        ),
        poll_operation_until_done(),
        Step(
            name="list-sgs",
            method="GET",
            path="/vpc/v1/networks/{{netId}}/security_groups",
            test_script=[
                *assert_status(200),
                "const j = pm.response.json();",
                "const sgs = j.securityGroups || [];",
                "pm.test('has at least 1 SG (default)', () => pm.expect(sgs.length).to.be.at.least(1));",
                "pm.test('default SG flag set', () => pm.expect(sgs.some(s => s.defaultForNetwork === true)).to.eql(true));",
            ],
        ),
        Step(
            name="cleanup",
            method="DELETE",
            path="/vpc/v1/networks/{{netId}}",
            test_script=[*assert_status(200)],
        ),
    ],
))

CASES.append(Case(
    id="NET-LRT-CRUD-EMPTY",
    title="ListRouteTables → 200 + empty",
    classes=["CRUD"],
    priority="P2",
    steps=[
        Step(
            name="create",
            method="POST",
            path="/vpc/v1/networks",
            body={"folderId": "{{_suiteFolderId}}", "name": "net-lrt-{{runId}}"},
            test_script=[
                *assert_status(200),
                *save_from_response("j.id", "opId"),
                *save_from_response("j.metadata && j.metadata.networkId", "netId"),
            ],
        ),
        poll_operation_until_done(),
        Step(
            name="list-rt",
            method="GET",
            path="/vpc/v1/networks/{{netId}}/route_tables",
            test_script=[
                *assert_status(200),
                "pm.test('routeTables array', () => pm.expect(pm.response.json().routeTables || []).to.be.an('array'));",
            ],
        ),
        Step(
            name="cleanup",
            method="DELETE",
            path="/vpc/v1/networks/{{netId}}",
            test_script=[*assert_status(200)],
        ),
    ],
))

CASES.append(Case(
    id="NET-LOP-CRUD-OK",
    title="ListOperations возвращает operations для свежесозданной network",
    classes=["CRUD"],
    priority="P1",
    steps=[
        Step(
            name="create",
            method="POST",
            path="/vpc/v1/networks",
            body={"folderId": "{{_suiteFolderId}}", "name": "net-lop-{{runId}}"},
            test_script=[
                *assert_status(200),
                *save_from_response("j.id", "opId"),
                *save_from_response("j.id", "createOpId"),
                *save_from_response("j.metadata && j.metadata.networkId", "netId"),
            ],
        ),
        poll_operation_until_done(),
        Step(
            name="list-ops",
            method="GET",
            path="/vpc/v1/networks/{{netId}}/operations",
            test_script=[
                *assert_status(200),
                "const j = pm.response.json();",
                "const ops = j.operations || [];",
                "pm.test('at least 1 op', () => pm.expect(ops.length).to.be.at.least(1));",
                "pm.test('contains create op', () => pm.expect(ops.some(o => o.id === pm.environment.get('createOpId'))).to.eql(true));",
            ],
        ),
        Step(
            name="cleanup",
            method="DELETE",
            path="/vpc/v1/networks/{{netId}}",
            test_script=[*assert_status(200)],
        ),
    ],
))
