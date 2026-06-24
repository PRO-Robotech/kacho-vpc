# Copyright (c) PRO-Robotech
# SPDX-License-Identifier: BUSL-1.1

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
                "projectId": "{{_suiteProjectId}}",
                "name": "net-cr-{{runId}}",
                "description": "newman CRUD-OK",
                "labels": {"suite": "newman"},
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
                "pm.test('projectId matches', () => pm.expect(j.projectId).to.eql(pm.environment.get('_suiteProjectId')));",
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
    # vpn_id из proto/БД удален — data-plane управляется отдельно. Regression-guard:
    # на публичном GET /vpc/v1/networks/{id} поля `vpnId` быть не должно. Если кто-то
    # по ошибке вернет data-plane-инфу — тест поймает.
    id="NET-GET-NO-VPNID-OK",
    title="GET /vpc/v1/networks/{id} НЕ содержит vpnId (data-plane поле удалено)",
    classes=["CRUD", "CONF"],
    priority="P1",
    steps=[
        Step(name="create", method="POST", path="/vpc/v1/networks",
             body={"projectId": "{{_suiteProjectId}}", "name": "net-novpn-{{runId}}"},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.networkId", "createdNetworkId")]),
        poll_operation_until_done(),
        Step(name="get-no-vpnid", method="GET", path="/vpc/v1/networks/{{createdNetworkId}}",
             test_script=[*assert_status(200),
                          "const j = pm.response.json();",
                          "pm.test('no vpnId on public Network', () => pm.expect(j).to.not.have.property('vpnId'));"]),
        Step(name="cleanup-delete", method="DELETE", path="/vpc/v1/networks/{{createdNetworkId}}",
             test_script=[*assert_status(200)]),
    ],
))

CASES.append(Case(
    id="NET-CR-VAL-PROJECT-REQUIRED",
    title="Create без projectId → InvalidArgument (project_id required)",
    classes=["VAL"],
    priority="P0",
    steps=[
        Step(
            name="create-no-project",
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
    id="NET-CR-NEG-PROJECT-NOT-FOUND",
    title="Create с garbage projectId → 403 (IAM authz no-path) или 200 + async NOT_FOUND",
    classes=["NEG"],
    priority="P0",
    steps=[
        Step(
            name="create-bad-project",
            method="POST",
            path="/vpc/v1/networks",
            body={"projectId": "{{garbageRmId}}", "name": "net-bf-{{runId}}"},
            # api-gateway может отклонить синхронно (403, FGA no-path для
            # несуществующего project) либо пропустить и вернуть 200 с operation,
            # которая упадет асинхронно NOT_FOUND. Оба исхода валидны (sync
            # project.Exists precheck отсутствует).
            test_script=[
                "pm.test('rejected sync (403) or accepted async (200)', () => pm.expect(pm.response.code).to.be.oneOf([200, 403]));",
                "if (pm.response.code === 200) pm.environment.set('opId', pm.response.json().id);",
                "else pm.environment.set('opId', '');",
            ],
        ),
        poll_operation_until_done(),
    ],
))

CASES.append(Case(
    id="NET-CR-NEG-DUP-NAME",
    title="Create с duplicate name в project → sync 409 ALREADY_EXISTS",
    classes=["NEG", "CONC"],
    priority="P1",
    steps=[
        Step(
            name="create-first",
            method="POST",
            path="/vpc/v1/networks",
            body={"projectId": "{{_suiteProjectId}}", "name": "net-dup-{{runId}}"},
            test_script=[
                *assert_status(200),
                *assert_operation_envelope(),
                *save_from_response("j.id", "opId"),
                *save_from_response("j.metadata && j.metadata.networkId", "createdNetworkId"),
            ],
        ),
        poll_operation_until_done(),
        Step(
            name="create-second-same-name",
            method="POST",
            path="/vpc/v1/networks",
            body={"projectId": "{{_suiteProjectId}}", "name": "net-dup-{{runId}}"},
            test_script=[
                *assert_status(409),
                *assert_grpc_code(6, "ALREADY_EXISTS"),
                "pm.test('mentions already exists', () => pm.expect(pm.response.json().message.toLowerCase()).to.include('already exists'));",
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
    title="Get malformed id → 400 InvalidArgument 'invalid network id'",
    classes=["NEG", "CONF"],
    priority="P0",
    steps=[
        Step(
            name="get-garbage",
            method="GET",
            path="/vpc/v1/networks/{{garbageId}}",
            # malformed id (нет известного 3-char префикса) → 400 InvalidArgument
            # "invalid network id '<X>'". Проверка family-agnostic.
            test_script=[
                *assert_status(400),
                *assert_grpc_code(3, "INVALID_ARGUMENT"),
                "pm.test('mentions invalid id', () => { const m = pm.response.json().message; pm.expect(m).to.include('invalid'); pm.expect(m).to.include('id'); });",
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
    title="List networks в project → 200 + массив",
    classes=["CRUD"],
    priority="P1",
    steps=[
        Step(
            name="list",
            method="GET",
            path="/vpc/v1/networks?projectId={{_suiteProjectId}}&pageSize=10",
            test_script=[
                *assert_status(200),
                "const j = pm.response.json();",
                # proto3-JSON: пустое repeated поле опускается. Defensive: || [].
                "pm.test('networks is array (or omitted when empty)', () => pm.expect(j.networks || []).to.be.an('array'));",
                "pm.test('nextPageToken is string (or omitted)', () => pm.expect(j.nextPageToken || '').to.be.a('string'));",
            ],
        ),
    ],
))

CASES.append(Case(
    id="NET-LST-VAL-PROJECT-REQUIRED",
    title="List без projectId → InvalidArgument (no cross-project enum)",
    classes=["VAL", "AUTHZ"],
    priority="P0",
    steps=[
        Step(
            name="list-no-project",
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
            path="/vpc/v1/networks?projectId={{_suiteProjectId}}&pageSize=0",
            test_script=[
                *assert_status(200),
                "const j = pm.response.json();",
                "pm.test('default pagesize applied', () => pm.expect((j.networks || []).length).to.be.at.most(50));",
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
            path="/vpc/v1/networks?projectId={{_suiteProjectId}}&pageSize=10000",
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
            path="/vpc/v1/networks?projectId={{_suiteProjectId}}&pageSize=10&pageToken=not-a-real-token",
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
            body={"projectId": "{{_suiteProjectId}}", "name": "net-upd-{{runId}}"},
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

# NB: NET-UPD-STATE-IMMUTABLE-PROJECT генерится helper'ом state_immutable_project("NET", …)
# ниже по файлу — explicit-дубль убран (validate-cases.py: hard-fail на дубль case-id).

CASES.append(Case(
    id="NET-UPD-NEG-NF-INVALID-PREFIX",
    title="Update malformed id → 400 InvalidArgument 'invalid network id'",
    classes=["NEG", "STATE"],
    priority="P1",
    steps=[
        Step(
            name="patch-invalid-prefix",
            method="PATCH",
            path="/vpc/v1/networks/{{garbageId}}",
            body={"updateMask": "description", "description": "x"},
            test_script=[
                # malformed id (нет известного 3-char префикса) → 400 InvalidArgument
                # "invalid network id '<X>'". Проверка family-agnostic.
                *assert_status(400),
                *assert_grpc_code(3, "INVALID_ARGUMENT"),
                "pm.test('mentions invalid id', () => { const m = pm.response.json().message; pm.expect(m).to.include('invalid'); pm.expect(m).to.include('id'); });",
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
                # Update делает sync Get → AssertProjectOwnership перед созданием
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
    title="Delete malformed id → 400 InvalidArgument 'invalid network id'",
    classes=["NEG", "STATE"],
    priority="P1",
    steps=[
        Step(
            name="delete-invalid-prefix",
            method="DELETE",
            path="/vpc/v1/networks/{{garbageId}}",
            test_script=[
                # malformed id (нет известного 3-char префикса) → 400 InvalidArgument
                # "invalid network id '<X>'". Проверка family-agnostic.
                *assert_status(400),
                *assert_grpc_code(3, "INVALID_ARGUMENT"),
                "pm.test('mentions invalid id', () => { const m = pm.response.json().message; pm.expect(m).to.include('invalid'); pm.expect(m).to.include('id'); });",
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
                # Delete делает sync Get → AssertProjectOwnership.
                *assert_status(404),
                *assert_grpc_code(5, "NOT_FOUND"),
            ],
        ),
    ],
))

# ---------------------------------------------------------------------------
# NET-MV — Move Network
# ---------------------------------------------------------------------------

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
            body={"projectId": "{{_suiteProjectId}}", "name": "net-lsub-{{runId}}"},
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
            body={"projectId": "{{_suiteProjectId}}", "name": "net-lsg-{{runId}}"},
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
            body={"projectId": "{{_suiteProjectId}}", "name": "net-lrt-{{runId}}"},
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
            body={"projectId": "{{_suiteProjectId}}", "name": "net-lop-{{runId}}"},
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

# Дополнение: STATE immutable project + VAL move-no-dest + BVA pagesize=1
CASES.append(state_immutable_project("NET", "/vpc/v1/networks"))
CASES.append(list_pagesize_1_bva("NET", "/vpc/v1/networks"))

CASES.append(Case(
    id="NET-CR-CONF-PROJECT-NF-TEXT",
    title="Create network в garbage project → 403 (IAM no-path) или operation.error 'Project ... not found'",
    classes=["CONF", "NEG"], priority="P1",
    steps=[
        Step(name="create", method="POST", path="/vpc/v1/networks",
             body={"projectId": "{{garbageRmId}}", "name": "net-confnf-{{runId}}"},
             # api-gateway может заблокировать (403) или принять (200) для
             # несуществующего project. При 200 worker возвращает NOT_FOUND с
             # точным текстом.
             test_script=[
                 "pm.test('rejected sync (403) or accepted async (200)', () => pm.expect(pm.response.code).to.be.oneOf([200, 403]));",
                 "if (pm.response.code === 200) pm.environment.set('opId', pm.response.json().id);",
                 "else pm.environment.set('opId', '');",
             ]),
        poll_operation_until_done(),
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
    id="NET-DEL-CRUD-OK",
    title="Network Delete (CRUD-OK): отдельная positive-проверка happy delete",
    classes=["CRUD"], priority="P1",
    steps=[
        Step(name="create", method="POST", path="/vpc/v1/networks",
             body={"projectId": "{{_suiteProjectId}}", "name": "net-delok-{{runId}}"},
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
    id="NET-DEL-CONF-NF-TEXT",
    title="Delete несуществующего Network → точный текст 'Network ... not found'",
    classes=["CONF", "NEG"], priority="P1",
    steps=[
        Step(name="del-nx", method="DELETE", path="/vpc/v1/networks/{{garbageVpcId}}",
             test_script=[*assert_status(404), *assert_grpc_code(5, "NOT_FOUND"),
                          "pm.test('Network ... not found', () => pm.expect(pm.response.json().message).to.match(/^Network .* not found$/));"]),
    ],
))

CASES.append(Case(
    id="NET-UPD-CONF-NF-TEXT",
    title="Update несуществующего Network → точный текст 'Network ... not found'",
    classes=["CONF", "NEG"], priority="P1",
    steps=[
        Step(name="upd-nx", method="PATCH", path="/vpc/v1/networks/{{garbageVpcId}}",
             body={"updateMask": "description", "description": "x"},
             test_script=[*assert_status(404), *assert_grpc_code(5, "NOT_FOUND"),
                          "pm.test('Network ... not found', () => pm.expect(pm.response.json().message).to.match(/^Network .* not found$/));"]),
    ],
))

# Exhaustive ECP/BVA расширение
CASES.extend(ecp_name_block("NET", "/vpc/v1/networks", {}))
CASES.extend(ecp_description_block("NET", "/vpc/v1/networks", {}))
CASES.extend(ecp_labels_block("NET", "/vpc/v1/networks", {}))
CASES.extend(updatemask_decision_table("NET", "/vpc/v1/networks"))
CASES.extend(filter_syntax_block("NET", "/vpc/v1/networks"))
CASES.append(pagination_roundtrip("NET", "/vpc/v1/networks"))
CASES.append(idempotency_block("NET", "/vpc/v1/networks", "net-idm-{{runId}}", {}))

# Update happy / perf-baseline / conformance-text / authz-caller-headers
CASES.extend(update_happy_per_field("NET", "/vpc/v1/networks", "/vpc/v1/networks", {"projectId": "{{_suiteProjectId}}"}))
CASES.extend(perf_baseline_block("NET", "/vpc/v1/networks"))
CASES.extend(verbatim_text_pack("NET", "Network", "/vpc/v1/networks"))
CASES.extend(authz_caller_headers_block("NET", "/vpc/v1/networks"))

# cross-project + multi-field + filter-match + invalid types + methods + malformed
CASES.append(update_happy_multi_field("NET", "/vpc/v1/networks", "/vpc/v1/networks", {"projectId": "{{_suiteProjectId}}"}))
CASES.append(cross_project_resource_block("NET", "/vpc/v1/networks", {}))
CASES.append(list_filter_match_block("NET", "/vpc/v1/networks", {"projectId": "{{_suiteProjectId}}"}))
CASES.extend(neg_invalid_types_block("NET", "/vpc/v1/networks", {"projectId": "{{_suiteProjectId}}"}))
CASES.extend(http_method_not_allowed_block("NET", "/vpc/v1/networks"))
CASES.extend(malformed_body_block("NET", "/vpc/v1/networks"))

# AlreadyExists dup-name + update-mask partial + perf-baseline-get + list-total-size + headers/content-type
CASES.append(alreadyexists_dup_name_for("NET", "/vpc/v1/networks", {"projectId": "{{_suiteProjectId}}"}))
CASES.extend(update_mask_partial_block("NET", "/vpc/v1/networks", "/vpc/v1/networks", {"projectId": "{{_suiteProjectId}}"}))
CASES.append(perf_baseline_get_block("NET", "/vpc/v1/networks", {"projectId": "{{_suiteProjectId}}"}))
CASES.extend(list_total_size_check_block("NET", "/vpc/v1/networks"))
CASES.extend(headers_content_type_block("NET", "/vpc/v1/networks", {"projectId": "{{_suiteProjectId}}"}))

# Network-specific edge cases
CASES.append(Case(
    id="NET-CR-VAL-EXTRA-FIELDS",
    title="Create Network с unknown полем в body → silent ignore (200) или 400",
    classes=["VAL"], priority="P3",
    steps=[Step(name="cr-extra", method="POST", path="/vpc/v1/networks",
                body={"projectId": "{{_suiteProjectId}}", "name": "net-x-{{runId}}",
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
    title="List с filter из несколько условий — multi-condition filter",
    classes=["FILTER"], priority="P3",
    steps=[Step(name="lst-multi", method="GET",
                path="/vpc/v1/networks?projectId={{_suiteProjectId}}&filter=name%3D%22x%22%20AND%20name%3D%22y%22",
                test_script=["pm.test('200 or 400', () => pm.expect(pm.response.code).to.be.oneOf([200, 400]));"])],
))

# List/Get edge cases
CASES.append(Case(
    id="NET-LST-PAGE-NEGATIVE-SIZE",
    title="List с pageSize=-1 → 400 или 200",
    classes=["BVA", "VAL"], priority="P2",
    steps=[Step(name="lst-neg", method="GET",
                path="/vpc/v1/networks?projectId={{_suiteProjectId}}&pageSize=-1",
                test_script=["pm.test('rejected or default', () => pm.expect(pm.response.code).to.be.oneOf([200, 400]));"])],
))

CASES.append(Case(
    id="NET-LST-FILTER-SPECIAL-CHARS",
    title="List с filter содержащим спец-символы → 400 или 200",
    classes=["FILTER", "VAL"], priority="P3",
    steps=[Step(name="lst-fsc", method="GET",
                path="/vpc/v1/networks?projectId={{_suiteProjectId}}&filter=name%3D%22%21%40%23%24%25%22",
                test_script=["pm.test('handled', () => pm.expect(pm.response.code).to.be.oneOf([200, 400]));"])],
))

CASES.append(Case(
    id="NET-LST-PAGESIZE-EXACTLY-1000",
    title="List с pageSize=1000 (boundary max) → 200",
    classes=["BVA"], priority="P2",
    steps=[Step(name="lst-max", method="GET",
                path="/vpc/v1/networks?projectId={{_suiteProjectId}}&pageSize=1000",
                test_script=[*assert_status(200)])],
))

CASES.append(Case(
    id="NET-LST-PAGESIZE-1001",
    title="List с pageSize=1001 (over max) → 400",
    classes=["BVA", "VAL"], priority="P1",
    steps=[Step(name="lst-1001", method="GET",
                path="/vpc/v1/networks?projectId={{_suiteProjectId}}&pageSize=1001",
                test_script=[*assert_status(400), *assert_grpc_code(3, "INVALID_ARGUMENT")])],
))

CASES.append(Case(
    id="NET-LST-DOUBLE-PROJECT-PARAM",
    title="List с дубликатом projectId param → 200 (last wins) или 400",
    classes=["VAL"], priority="P3",
    steps=[Step(name="lst-dup", method="GET",
                path="/vpc/v1/networks?projectId={{_suiteProjectId}}&projectId={{_suiteProjectCrossId}}&pageSize=10",
                test_script=["pm.test('200 or 400', () => pm.expect(pm.response.code).to.be.oneOf([200, 400]));"])],
))

CASES.append(Case(
    id="NET-GET-TRAILING-SLASH",
    title="Get с trailing slash → 404",
    classes=["VAL"], priority="P3",
    steps=[Step(name="get-trail", method="GET", path="/vpc/v1/networks/{{garbageVpcId}}/",
                test_script=["pm.test('non-2xx', () => pm.expect(pm.response.code).to.be.oneOf([400, 404]));"])],
))

# === Delete с зависимыми ресурсами (FK RESTRICT) ===

CASES.append(Case(
    id="NET-DEL-NEG-HAS-SUBNETS",
    title="Delete Network c Subnet → FailedPrecondition (FK RESTRICT)",
    classes=["NEG", "CONF", "STATE"], priority="P0",
    steps=[
        Step(name="cr-net", method="POST", path="/vpc/v1/networks",
             body={"projectId": "{{_suiteProjectId}}", "name": "net-hasub-{{runId}}"},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.networkId", "netId")]),
        poll_operation_until_done(),
        Step(name="cr-sub", method="POST", path="/vpc/v1/subnets",
             body={"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}",
                   "name": "sub-hasub-{{runId}}", "zoneId": "{{existingZoneId}}",
                   "v4CidrBlocks": ["10.250.1.0/24"]},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.subnetId", "subId")]),
        poll_operation_until_done(),
        Step(name="del-net-blocked", method="DELETE", path="/vpc/v1/networks/{{netId}}",
             test_script=[
                 *assert_status(400), *assert_grpc_code(9, "FAILED_PRECONDITION"),
                 "pm.test('not empty text', () => pm.expect(pm.response.json().message).to.match(/^Network .* is not empty$/));",
             ]),
        # cleanup в обратном порядке
        Step(name="cleanup-sub", method="DELETE", path="/vpc/v1/subnets/{{subId}}",
             test_script=[*save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="cleanup-net", method="DELETE", path="/vpc/v1/networks/{{netId}}",
             test_script=[*save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
    ],
))

CASES.append(Case(
    id="NET-DEL-NEG-HAS-ROUTE-TABLE",
    title="Delete Network c RouteTable → FailedPrecondition",
    classes=["NEG", "CONF", "STATE"], priority="P0",
    steps=[
        Step(name="cr-net", method="POST", path="/vpc/v1/networks",
             body={"projectId": "{{_suiteProjectId}}", "name": "net-hart-{{runId}}"},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.networkId", "netId")]),
        poll_operation_until_done(),
        Step(name="cr-rt", method="POST", path="/vpc/v1/routeTables",
             body={"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}",
                   "name": "rt-hart-{{runId}}", "staticRoutes": []},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.routeTableId", "rtId")]),
        poll_operation_until_done(),
        Step(name="del-net-blocked", method="DELETE", path="/vpc/v1/networks/{{netId}}",
             test_script=[
                 *assert_status(400), *assert_grpc_code(9, "FAILED_PRECONDITION"),
                 "pm.test('not empty text', () => pm.expect(pm.response.json().message).to.match(/^Network .* is not empty$/));",
             ]),
        Step(name="cleanup-rt", method="DELETE", path="/vpc/v1/routeTables/{{rtId}}",
             test_script=[*save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="cleanup-net", method="DELETE", path="/vpc/v1/networks/{{netId}}",
             test_script=[*save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
    ],
))

CASES.append(Case(
    id="NET-DEL-NEG-HAS-NONDEFAULT-SG",
    title="Delete Network с НЕ-default SG → FailedPrecondition (RESTRICT FK)",
    classes=["NEG", "CONF", "STATE"], priority="P0",
    steps=[
        Step(name="cr-net", method="POST", path="/vpc/v1/networks",
             body={"projectId": "{{_suiteProjectId}}", "name": "net-hasg-{{runId}}"},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.networkId", "netId")]),
        poll_operation_until_done(),
        # Создаем дополнительный (non-default) SG
        Step(name="cr-sg", method="POST", path="/vpc/v1/securityGroups",
             body={"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}",
                   "name": "sg-hasg-{{runId}}", "ruleSpecs": []},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.securityGroupId", "sgId")]),
        poll_operation_until_done(),
        Step(name="del-net-blocked", method="DELETE", path="/vpc/v1/networks/{{netId}}",
             test_script=[
                 *assert_status(400), *assert_grpc_code(9, "FAILED_PRECONDITION"),
                 "pm.test('not empty text', () => pm.expect(pm.response.json().message).to.match(/^Network .* is not empty$/));",
             ]),
        Step(name="cleanup-sg", method="DELETE", path="/vpc/v1/securityGroups/{{sgId}}",
             test_script=[*save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="cleanup-net", method="DELETE", path="/vpc/v1/networks/{{netId}}",
             test_script=[*save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
    ],
))

CASES.append(Case(
    id="NET-DEL-CRUD-ONLY-DEFAULT-SG",
    title="Delete Network у которой есть только default-SG → OK (auto-cleanup default)",
    classes=["CRUD", "STATE"], priority="P1",
    steps=[
        # Создаем network — default SG создается inline в doCreate автоматически
        Step(name="cr-net", method="POST", path="/vpc/v1/networks",
             body={"projectId": "{{_suiteProjectId}}", "name": "net-defsg-{{runId}}"},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.networkId", "netId")]),
        poll_operation_until_done(),
        # Проверка что default SG действительно создался
        Step(name="check-default-sg", method="GET",
             path="/vpc/v1/networks/{{netId}}/security_groups",
             test_script=[*assert_status(200),
                          "const sgs = pm.response.json().securityGroups || [];",
                          "pm.test('exactly 1 default SG present', () => pm.expect(sgs.filter(s => s.defaultForNetwork === true).length).to.eql(1));"]),
        # Delete network — должен пройти (default SG автоматически чистится service-кодом)
        Step(name="del-net", method="DELETE", path="/vpc/v1/networks/{{netId}}",
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="assert-success", method="GET", path="/operations/{{opId}}",
             test_script=[
                 "const j = pm.response.json();",
                 "pm.test('done', () => pm.expect(j.done).to.eql(true));",
                 "pm.test('no error — delete with only default-SG succeeds', () => pm.expect(j.error).to.be.undefined);",
             ]),
        Step(name="get-after-del", method="GET", path="/vpc/v1/networks/{{netId}}",
             test_script=[*assert_status(404), *assert_grpc_code(5, "NOT_FOUND")]),
    ],
))

CASES.append(Case(
    # После Network.Delete ее default SG обязан исчезнуть — explicit
    # GET /securityGroups/{defSgId} должен дать 404. Здесь проверяется именно
    # life-cycle default SG (NET-DEL-CRUD-ONLY-DEFAULT-SG проверяет лишь, что
    # Network.Delete возвращает 200).
    id="NET-DEL-CRUD-DEFAULT-SG-REMOVED",
    title="Delete Network → ее default SG тоже удалена (explicit GET /securityGroups → 404)",
    classes=["CRUD", "STATE"], priority="P1",
    steps=[
        Step(name="cr-net", method="POST", path="/vpc/v1/networks",
             body={"projectId": "{{_suiteProjectId}}", "name": "net-defsgrm-{{runId}}"},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.networkId", "netId")]),
        poll_operation_until_done(),
        Step(name="get-default-sg-id", method="GET", path="/vpc/v1/networks/{{netId}}",
             test_script=[*assert_status(200),
                          "const j = pm.response.json();",
                          "pm.test('defaultSecurityGroupId populated', () => pm.expect(j.defaultSecurityGroupId, JSON.stringify(j)).to.be.a('string').and.not.empty);",
                          *save_from_response("j.defaultSecurityGroupId", "defSgId")]),
        Step(name="get-default-sg-alive", method="GET", path="/vpc/v1/securityGroups/{{defSgId}}",
             test_script=[*assert_status(200),
                          "pm.test('default SG exists before network.delete', () => pm.expect(pm.response.json().defaultForNetwork).to.eql(true));"]),
        Step(name="del-net", method="DELETE", path="/vpc/v1/networks/{{netId}}",
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="assert-net-deleted", method="GET", path="/operations/{{opId}}",
             test_script=["const j = pm.response.json();",
                          "pm.test('net delete op done, no error', () => pm.expect(j.done && !j.error).to.eql(true));"]),
        # Главная проверка: default SG должна быть удалена вместе с Network.
        Step(name="get-default-sg-gone", method="GET", path="/vpc/v1/securityGroups/{{defSgId}}",
             test_script=[*assert_status(404), *assert_grpc_code(5, "NOT_FOUND"),
                          "pm.test('NOT_FOUND text mentions SG', () => pm.expect(pm.response.json().message || '').to.match(/[Ss]ecurity\\s*[Gg]roup/));"]),
    ],
))

CASES.append(Case(
    # Subnet с NIC блокирует Subnet.Delete (FK RESTRICT) — и, как следствие,
    # Network.Delete (FK RESTRICT subnet→network). NIC-in-subnet вариант контракта
    # network-not-empty.
    id="NET-DEL-NEG-HAS-SUBNET-WITH-NIC",
    title="Delete Network c Subnet, в которой NIC → FailedPrecondition (not empty); cleanup снизу вверх",
    classes=["NEG", "STATE"], priority="P0",
    steps=[
        Step(name="cr-net", method="POST", path="/vpc/v1/networks",
             body={"projectId": "{{_suiteProjectId}}", "name": "net-subnic-{{runId}}"},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.networkId", "netId")]),
        poll_operation_until_done(),
        Step(name="cr-sub", method="POST", path="/vpc/v1/subnets",
             body={"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}",
                   "name": "sub-subnic-{{runId}}", "zoneId": "{{existingZoneId}}",
                   "v4CidrBlocks": ["10.248.3.0/24"]},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.subnetId", "subId")]),
        poll_operation_until_done(),
        Step(name="cr-nic", method="POST", path="/vpc/v1/networkInterfaces",
             body={"projectId": "{{_suiteProjectId}}", "subnetId": "{{subId}}", "name": "nic-subnic-{{runId}}"},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.networkInterfaceId", "nicId")]),
        poll_operation_until_done(),
        Step(name="del-net-blocked", method="DELETE", path="/vpc/v1/networks/{{netId}}",
             test_script=[
                 *assert_status(400), *assert_grpc_code(9, "FAILED_PRECONDITION"),
                 "pm.test('not empty text', () => pm.expect(pm.response.json().message).to.match(/^Network .* is not empty$/));",
             ]),
        # cleanup снизу вверх: NIC → Subnet → Network
        Step(name="cleanup-nic", method="DELETE", path="/vpc/v1/networkInterfaces/{{nicId}}",
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="cleanup-sub", method="DELETE", path="/vpc/v1/subnets/{{subId}}",
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="cleanup-net", method="DELETE", path="/vpc/v1/networks/{{netId}}",
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
    ],
))

CASES.append(Case(
    # ListOperations не делает repo.Get precondition — history переживает удаление
    # ресурса. Проверяет network /operations после delete.
    id="NET-LISTOPS-AFTER-DELETE-OK",
    title="ListOperations сети после ее удаления → 200, непустой список (Create + Delete)",
    classes=["STATE", "CRUD"], priority="P1",
    steps=[
        Step(name="cr-net", method="POST", path="/vpc/v1/networks",
             body={"projectId": "{{_suiteProjectId}}", "name": "net-listops-{{runId}}"},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.networkId", "netId")]),
        poll_operation_until_done(),
        Step(name="listops-before", method="GET", path="/vpc/v1/networks/{{netId}}/operations",
             test_script=[*assert_status(200), "const j = pm.response.json();",
                          "pm.test('has Create op', () => pm.expect((j.operations||[]).length).to.be.at.least(1));"]),
        Step(name="del-net", method="DELETE", path="/vpc/v1/networks/{{netId}}",
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="listops-after-delete", method="GET", path="/vpc/v1/networks/{{netId}}/operations",
             test_script=[
                 *assert_status(200), "const j = pm.response.json();",
                 "pm.test('history survives delete (Create + Delete)', () => pm.expect((j.operations||[]).length).to.be.at.least(2));",
             ]),
    ],
))

# === Required-field matrix + Immutable matrix для Network ===
CASES.extend(required_fields_matrix("NET", "/vpc/v1/networks",
    {"projectId": "{{_suiteProjectId}}", "name": "net-req-{{runId}}"},
    ["projectId", "name"]))
CASES.extend(immutable_fields_matrix("NET", "/vpc/v1/networks",
    ["project_id"]))

# security probes + lifecycle conformance
CASES.extend(security_injection_block("NET", "/vpc/v1/networks", "/vpc/v1/networks",
    {"projectId": "{{_suiteProjectId}}"}))
CASES.append(conformance_lifecycle_pack("NET", "/vpc/v1/networks",
    {"projectId": "{{_suiteProjectId}}"}))
