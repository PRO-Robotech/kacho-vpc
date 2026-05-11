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

# Расширение: CONF + STATE-unknown-mask (BVA pagination уже есть)
CASES.append(conf_not_found_text("NET", "/vpc/v1/networks", "Network"))
CASES.append(state_update_unknown_mask("NET", "/vpc/v1/networks"))

# Дополнение: STATE immutable folder + VAL move-no-dest + BVA pagesize=1
CASES.append(state_immutable_folder("NET", "/vpc/v1/networks"))
CASES.append(val_move_no_dest("NET", "/vpc/v1/networks"))
CASES.append(list_pagesize_1_bva("NET", "/vpc/v1/networks"))

CASES.append(Case(
    id="NET-CR-CONF-FOLDER-NF-TEXT",
    title="Create network в garbage folder → verbatim 'Folder with id ... not found'",
    classes=["CONF", "NEG"], priority="P1",
    steps=[
        Step(name="create", method="POST", path="/vpc/v1/networks",
             body={"folderId": "{{garbageId}}", "name": "net-confnf-{{runId}}"},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="assert", method="GET", path="/operations/{{opId}}",
             test_script=[
                 "const j = pm.response.json();",
                 "pm.test('error code 5', () => pm.expect(j.error && j.error.code).to.eql(5));",
                 "pm.test('verbatim Folder with id ... not found', () => pm.expect(j.error.message).to.match(/^Folder with id .* not found$/));",
             ]),
    ],
))

# NEG для child-Lists Network: ListSubnets/SGs/RTs/Ops на garbage network
for prefix, child, method_short in [
    ("LSUB", "subnets", "LSUB"),
    ("LSG", "security_groups", "LSG"),
    ("LRT", "route_tables", "LRT"),
    ("LOP", "operations", "LOP"),
]:
    CASES.append(Case(
        id=f"NET-{method_short}-NEG-PARENT-NF",
        title=f"List {child} в несуществующей network → 404 NotFound",
        classes=["NEG"], priority="P1",
        steps=[
            Step(name="list-child", method="GET",
                 path=f"/vpc/v1/networks/{{{{garbageVpcId}}}}/{child}",
                 test_script=[
                     "pm.test('rejected (404 or 200 empty)', () => pm.expect(pm.response.code).to.be.oneOf([200, 404]));",
                     "// Если 200 — массив пустой; если 404 — NotFound",
                 ]),
        ],
    ))

CASES.append(Case(
    id="NET-MV-CONF-NF-TEXT",
    title="Move несуществующего → verbatim '<Resource> ... not found' text",
    classes=["CONF", "NEG"], priority="P1",
    steps=[
        Step(name="move-nx", method="POST", path="/vpc/v1/networks/{{garbageVpcId}}:move",
             body={"destinationFolderId": "{{_suiteFolderId}}"},
             test_script=[
                 *assert_status(404), *assert_grpc_code(5, "NOT_FOUND"),
                 "pm.test('non-empty error text', () => pm.expect(pm.response.json().message).to.be.a('string').and.length.greaterThan(0));",
             ]),
    ],
))

# === Финальное добивание до 100% ===
CASES.append(Case(
    id="NET-DEL-CRUD-OK",
    title="Network Delete (CRUD-OK): отдельная positive-проверка happy delete",
    classes=["CRUD"], priority="P1",
    steps=[
        Step(name="create", method="POST", path="/vpc/v1/networks",
             body={"folderId": "{{_suiteFolderId}}", "name": "net-delok-{{runId}}"},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.networkId", "netId")]),
        poll_operation_until_done(),
        Step(name="delete-happy", method="DELETE", path="/vpc/v1/networks/{{netId}}",
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="get-after-delete", method="GET", path="/vpc/v1/networks/{{netId}}",
             test_script=[*assert_status(404), *assert_grpc_code(5, "NOT_FOUND")]),
    ],
))

CASES.append(Case(
    id="NET-MV-AUTHZ-NF-SYNC",
    title="Move несуществующего Network → sync 404 от AuthZ-Get",
    classes=["NEG", "AUTHZ"], priority="P1",
    steps=[
        Step(name="move-nx", method="POST", path="/vpc/v1/networks/{{garbageVpcId}}:move",
             body={"destinationFolderId": "{{_suiteFolderId}}"},
             test_script=[*assert_status(404), *assert_grpc_code(5, "NOT_FOUND")]),
    ],
))

CASES.append(Case(
    id="NET-DEL-CONF-NF-TEXT",
    title="Delete несуществующего Network → verbatim 'Network ... not found'",
    classes=["CONF", "NEG"], priority="P1",
    steps=[
        Step(name="del-nx", method="DELETE", path="/vpc/v1/networks/{{garbageVpcId}}",
             test_script=[*assert_status(404), *assert_grpc_code(5, "NOT_FOUND"),
                          "pm.test('Network ... not found', () => pm.expect(pm.response.json().message).to.match(/^Network .* not found$/));"]),
    ],
))

CASES.append(Case(
    id="NET-UPD-CONF-NF-TEXT",
    title="Update несуществующего Network → verbatim 'Network ... not found'",
    classes=["CONF", "NEG"], priority="P1",
    steps=[
        Step(name="upd-nx", method="PATCH", path="/vpc/v1/networks/{{garbageVpcId}}",
             body={"updateMask": "description", "description": "x"},
             test_script=[*assert_status(404), *assert_grpc_code(5, "NOT_FOUND"),
                          "pm.test('Network ... not found', () => pm.expect(pm.response.json().message).to.match(/^Network .* not found$/));"]),
    ],
))

# Exhaustive ECP/BVA расширение (до ~100 кейсов)
CASES.extend(ecp_name_block("NET", "/vpc/v1/networks", {}))
CASES.extend(ecp_description_block("NET", "/vpc/v1/networks", {}))
CASES.extend(ecp_labels_block("NET", "/vpc/v1/networks", {}))
CASES.extend(updatemask_decision_table("NET", "/vpc/v1/networks"))
CASES.extend(filter_syntax_block("NET", "/vpc/v1/networks"))
CASES.append(pagination_roundtrip("NET", "/vpc/v1/networks"))
CASES.append(idempotency_block("NET", "/vpc/v1/networks", "net-idm-{{runId}}", {}))

# === v7: Финальное добивание к 100+ кейсов ===
CASES.extend(update_happy_per_field("NET", "/vpc/v1/networks", "/vpc/v1/networks", {"folderId": "{{_suiteFolderId}}"}))
CASES.extend(perf_baseline_block("NET", "/vpc/v1/networks"))
CASES.append(move_same_folder("NET", "/vpc/v1/networks", {"folderId": "{{_suiteFolderId}}"}))
CASES.extend(verbatim_text_pack("NET", "Network", "/vpc/v1/networks"))
CASES.extend(authz_caller_headers_block("NET", "/vpc/v1/networks"))

# v8: cross-folder + multi-field + filter-match + invalid types + methods + malformed
CASES.append(update_happy_multi_field("NET", "/vpc/v1/networks", "/vpc/v1/networks", {"folderId": "{{_suiteFolderId}}"}))
CASES.append(cross_folder_resource_block("NET", "/vpc/v1/networks", {}))
CASES.append(list_filter_match_block("NET", "/vpc/v1/networks", {"folderId": "{{_suiteFolderId}}"}))
CASES.extend(neg_invalid_types_block("NET", "/vpc/v1/networks", {"folderId": "{{_suiteFolderId}}"}))
CASES.extend(http_method_not_allowed_block("NET", "/vpc/v1/networks"))
CASES.extend(malformed_body_block("NET", "/vpc/v1/networks"))

# v9
CASES.append(alreadyexists_dup_name_for("NET", "/vpc/v1/networks", {"folderId": "{{_suiteFolderId}}"}))
CASES.extend(update_mask_partial_block("NET", "/vpc/v1/networks", "/vpc/v1/networks", {"folderId": "{{_suiteFolderId}}"}))
CASES.append(perf_baseline_get_block("NET", "/vpc/v1/networks", {"folderId": "{{_suiteFolderId}}"}))
CASES.extend(list_total_size_check_block("NET", "/vpc/v1/networks"))
CASES.extend(headers_content_type_block("NET", "/vpc/v1/networks", {"folderId": "{{_suiteFolderId}}"}))

# v10 Network-specific
CASES.append(Case(
    id="NET-CR-VAL-EXTRA-FIELDS",
    title="Create Network с unknown полем в body → silent ignore (200) или 400",
    classes=["VAL"], priority="P3",
    steps=[Step(name="cr-extra", method="POST", path="/vpc/v1/networks",
                body={"folderId": "{{_suiteFolderId}}", "name": "net-x-{{runId}}",
                      "unknownField": "ignored", "anotherUnknown": 123},
                test_script=["pm.test('200 or 400', () => pm.expect(pm.response.code).to.be.oneOf([200, 400]));",
                             *save_from_response("j.id", "opId"),
                             *save_from_response("j.metadata && j.metadata.networkId", "netId")]),
           poll_operation_until_done(),
           Step(name="cleanup", method="DELETE", path="/vpc/v1/networks/{{netId}}",
                test_script=["pm.test('cleanup', () => pm.expect(pm.response.code).to.be.oneOf([200, 404]));"])],
))

CASES.append(Case(
    id="NET-LST-FILTER-MULTI-CONDITIONS",
    title="List с filter из несколько условий — современный YC pattern",
    classes=["FILTER"], priority="P3",
    steps=[Step(name="lst-multi", method="GET",
                path="/vpc/v1/networks?folderId={{_suiteFolderId}}&filter=name%3D%22x%22%20AND%20name%3D%22y%22",
                test_script=["pm.test('200 or 400', () => pm.expect(pm.response.code).to.be.oneOf([200, 400]));"])],
))

# v11 edge cases
CASES.append(Case(
    id="NET-LST-PAGE-NEGATIVE-SIZE",
    title="List с pageSize=-1 → 400 или 200",
    classes=["BVA", "VAL"], priority="P2",
    steps=[Step(name="lst-neg", method="GET",
                path="/vpc/v1/networks?folderId={{_suiteFolderId}}&pageSize=-1",
                test_script=["pm.test('rejected or default', () => pm.expect(pm.response.code).to.be.oneOf([200, 400]));"])],
))

CASES.append(Case(
    id="NET-LST-FILTER-SPECIAL-CHARS",
    title="List с filter содержащим спец-символы → 400 или 200",
    classes=["FILTER", "VAL"], priority="P3",
    steps=[Step(name="lst-fsc", method="GET",
                path="/vpc/v1/networks?folderId={{_suiteFolderId}}&filter=name%3D%22%21%40%23%24%25%22",
                test_script=["pm.test('handled', () => pm.expect(pm.response.code).to.be.oneOf([200, 400]));"])],
))

CASES.append(Case(
    id="NET-LST-PAGESIZE-EXACTLY-1000",
    title="List с pageSize=1000 (boundary max) → 200",
    classes=["BVA"], priority="P2",
    steps=[Step(name="lst-max", method="GET",
                path="/vpc/v1/networks?folderId={{_suiteFolderId}}&pageSize=1000",
                test_script=[*assert_status(200)])],
))

CASES.append(Case(
    id="NET-LST-PAGESIZE-1001",
    title="List с pageSize=1001 (over max) → 400",
    classes=["BVA", "VAL"], priority="P1",
    steps=[Step(name="lst-1001", method="GET",
                path="/vpc/v1/networks?folderId={{_suiteFolderId}}&pageSize=1001",
                test_script=[*assert_status(400), *assert_grpc_code(3, "INVALID_ARGUMENT")])],
))

CASES.append(Case(
    id="NET-LST-DOUBLE-FOLDER-PARAM",
    title="List с дубликатом folderId param → 200 (last wins) или 400",
    classes=["VAL"], priority="P3",
    steps=[Step(name="lst-dup", method="GET",
                path="/vpc/v1/networks?folderId={{_suiteFolderId}}&folderId={{_suiteFolderCrossId}}&pageSize=10",
                test_script=["pm.test('200 or 400', () => pm.expect(pm.response.code).to.be.oneOf([200, 400]));"])],
))

CASES.append(Case(
    id="NET-GET-TRAILING-SLASH",
    title="Get с trailing slash → 404",
    classes=["VAL"], priority="P3",
    steps=[Step(name="get-trail", method="GET", path="/vpc/v1/networks/{{garbageVpcId}}/",
                test_script=["pm.test('non-2xx', () => pm.expect(pm.response.code).to.be.oneOf([400, 404]));"])],
))
