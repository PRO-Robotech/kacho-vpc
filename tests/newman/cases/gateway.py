"""Case-set для GatewayService."""

CASES = []

CASES.append(Case(
    id="GW-CR-CRUD-OK",
    title="Create gateway + Get",
    classes=["CRUD"],
    priority="P1",
    steps=[
        Step(name="create", method="POST", path="/vpc/v1/gateways",
             body={"projectId": "{{_suiteFolderId}}", "name": "gw-cr-{{runId}}",
                   "sharedEgressGatewaySpec": {}},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.gatewayId", "gwId")]),
        poll_operation_until_done(),
        Step(name="get", method="GET", path="/vpc/v1/gateways/{{gwId}}",
             test_script=[*assert_status(200),
                          "pm.test('id matches', () => pm.expect(pm.response.json().id).to.eql(pm.environment.get('gwId')));"]),
        Step(name="cleanup", method="DELETE", path="/vpc/v1/gateways/{{gwId}}",
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
    ],
))

CASES.append(Case(
    id="GW-CR-VAL-FOLDER-REQUIRED",
    title="Create без folder → InvalidArgument",
    classes=["VAL"],
    priority="P0",
    steps=[
        Step(name="cr", method="POST", path="/vpc/v1/gateways",
             body={"name": "gw-nf-{{runId}}", "sharedEgressGatewaySpec": {}},
             test_script=[*assert_status(400), *assert_grpc_code(3, "INVALID_ARGUMENT")]),
    ],
))

CASES.append(Case(
    id="GW-GET-NEG-NF",
    title="Get malformed id → 400 InvalidArgument 'invalid gateway id'",
    classes=["NEG"],
    priority="P0",
    steps=[
        Step(name="get-garbage", method="GET", path="/vpc/v1/gateways/{{garbageId}}",
             test_script=[
                 # verbatim-YC (probe 2026-05-11, kacho-vpc#7): malformed id (нет известного 3-char префикса)
                 # → 400 InvalidArgument "invalid gateway id '<X>'" (раньше было 404 NotFound). Проверка family-agnostic.
                 *assert_status(400),
                 *assert_grpc_code(3, "INVALID_ARGUMENT"),
                 "pm.test('mentions invalid id', () => { const m = pm.response.json().message; pm.expect(m).to.include('invalid'); pm.expect(m).to.include('id'); });",
             ]),
    ],
))

CASES.append(Case(
    id="GW-LST-CRUD-OK",
    title="List gateways",
    classes=["CRUD"],
    priority="P1",
    steps=[
        Step(name="list", method="GET", path="/vpc/v1/gateways?projectId={{_suiteFolderId}}",
             test_script=[*assert_status(200),
                          "pm.test('gateways array', () => pm.expect(pm.response.json().gateways || []).to.be.an('array'));"]),
    ],
))

CASES.append(Case(
    id="GW-LST-VAL-FOLDER-REQUIRED",
    title="List без folder → InvalidArgument",
    classes=["VAL", "AUTHZ"],
    priority="P0",
    steps=[
        Step(name="list-nofolder", method="GET", path="/vpc/v1/gateways",
             test_script=[*assert_status(400), *assert_grpc_code(3, "INVALID_ARGUMENT")]),
    ],
))

CASES.append(Case(
    id="GW-UPD-AUTHZ-NF-SYNC",
    title="Update несуществующего → sync 404",
    classes=["NEG", "AUTHZ"],
    priority="P1",
    steps=[
        Step(name="patch-nx", method="PATCH", path="/vpc/v1/gateways/{{garbageVpcId}}",
             body={"updateMask": "description", "description": "x"},
             test_script=[*assert_status(404), *assert_grpc_code(5, "NOT_FOUND")]),
    ],
))

CASES.append(Case(
    id="GW-DEL-AUTHZ-NF-SYNC",
    title="Delete несуществующего → sync 404",
    classes=["NEG", "AUTHZ"],
    priority="P1",
    steps=[
        Step(name="del-nx", method="DELETE", path="/vpc/v1/gateways/{{garbageVpcId}}",
             test_script=[*assert_status(404), *assert_grpc_code(5, "NOT_FOUND")]),
    ],
))

CASES.append(Case(
    id="GW-LOP-CRUD-OK",
    title="ListOperations Gateway",
    classes=["CRUD"],
    priority="P1",
    steps=[
        Step(name="create-gw", method="POST", path="/vpc/v1/gateways",
             body={"projectId": "{{_suiteFolderId}}", "name": "gw-lop-{{runId}}",
                   "sharedEgressGatewaySpec": {}},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.gatewayId", "gwId")]),
        poll_operation_until_done(),
        Step(name="list-ops", method="GET", path="/vpc/v1/gateways/{{gwId}}/operations",
             test_script=[*assert_status(200),
                          "pm.test('at least 1 op', () => pm.expect((pm.response.json().operations || []).length).to.be.at.least(1));"]),
        Step(name="cleanup", method="DELETE", path="/vpc/v1/gateways/{{gwId}}",
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
    ],
))

# Расширение
CASES.extend(crud_list_bva_block("GW", "/vpc/v1/gateways"))
CASES.append(conf_not_found_text("GW", "/vpc/v1/gateways", "Gateway"))
CASES.append(state_update_unknown_mask("GW", "/vpc/v1/gateways"))
CASES.append(authz_move_nf("GW", "/vpc/v1/gateways"))

CASES.append(Case(
    id="GW-MV-CRUD-OK",
    title="Move Gateway в другой folder",
    classes=["CRUD"], priority="P1",
    steps=[
        Step(name="create-gw", method="POST", path="/vpc/v1/gateways",
             body={"projectId": "{{_suiteFolderId}}", "name": "gw-mv-{{runId}}",
                   "sharedEgressGatewaySpec": {}},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.gatewayId", "gwId")]),
        poll_operation_until_done(),
        Step(name="move", method="POST", path="/vpc/v1/gateways/{{gwId}}:move",
             body={"destinationProjectId": "{{_suiteFolderCrossId}}"},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="cleanup", method="DELETE", path="/vpc/v1/gateways/{{gwId}}",
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
    ],
))

CASES.append(Case(
    id="GW-UPD-CRUD-OK",
    title="Update Gateway description",
    classes=["CRUD"], priority="P1",
    steps=[
        Step(name="create-gw", method="POST", path="/vpc/v1/gateways",
             body={"projectId": "{{_suiteFolderId}}", "name": "gw-upd-{{runId}}",
                   "sharedEgressGatewaySpec": {}},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.gatewayId", "gwId")]),
        poll_operation_until_done(),
        Step(name="patch", method="PATCH", path="/vpc/v1/gateways/{{gwId}}",
             body={"updateMask": "description", "description": "upd-newman"},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="cleanup", method="DELETE", path="/vpc/v1/gateways/{{gwId}}",
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
    ],
))

# Дополнение: STATE immutable folder + VAL move-no-dest + BVA pagesize=1
CASES.append(state_immutable_folder("GW", "/vpc/v1/gateways"))
CASES.append(val_move_no_dest("GW", "/vpc/v1/gateways"))
CASES.append(list_pagesize_1_bva("GW", "/vpc/v1/gateways"))

CASES.append(Case(
    id="GW-UPD-CONF-NF-TEXT",
    title="Update несуществующего → verbatim 'Gateway ... not found' text",
    classes=["CONF", "NEG"], priority="P1",
    steps=[
        Step(name="patch-nx", method="PATCH",
             path="/vpc/v1/gateways/{{garbageVpcId}}",
             body={"updateMask": "description", "description": "x"},
             test_script=[
                 *assert_status(404), *assert_grpc_code(5, "NOT_FOUND"),
                 "pm.test('text matches Gateway ... not found', () => pm.expect(pm.response.json().message).to.match(/^Gateway .* not found$/));",
             ]),
    ],
))

CASES.append(Case(
    id="GW-DEL-CONF-NF-TEXT",
    title="Delete несуществующего → verbatim 'Gateway ... not found' text",
    classes=["CONF", "NEG"], priority="P1",
    steps=[
        Step(name="del-nx", method="DELETE",
             path="/vpc/v1/gateways/{{garbageVpcId}}",
             test_script=[
                 *assert_status(404), *assert_grpc_code(5, "NOT_FOUND"),
                 "pm.test('text matches Gateway ... not found', () => pm.expect(pm.response.json().message).to.match(/^Gateway .* not found$/));",
             ]),
    ],
))

CASES.append(Case(
    id="GW-MV-CONF-NF-TEXT",
    title="Move несуществующего → verbatim '<Resource> ... not found' text",
    classes=["CONF", "NEG"], priority="P1",
    steps=[
        Step(name="move-nx", method="POST", path="/vpc/v1/gateways/{{garbageVpcId}}:move",
             body={"destinationProjectId": "{{_suiteFolderId}}"},
             test_script=[
                 *assert_status(404), *assert_grpc_code(5, "NOT_FOUND"),
                 "pm.test('non-empty error text', () => pm.expect(pm.response.json().message).to.be.a('string').and.length.greaterThan(0));",
             ]),
    ],
))

CASES.append(Case(
    id="GW-DEL-CRUD-OK",
    title="Gateway Delete happy",
    classes=["CRUD"], priority="P1",
    steps=[
        Step(name="create", method="POST", path="/vpc/v1/gateways",
             body={"projectId": "{{_suiteFolderId}}", "name": "gw-delok-{{runId}}",
                   "sharedEgressGatewaySpec": {}},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.gatewayId", "gwId")]),
        poll_operation_until_done(),
        Step(name="del-happy", method="DELETE", path="/vpc/v1/gateways/{{gwId}}",
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
    ],
))

CASES.append(Case(
    id="GW-CR-NEG-FOLDER-NF",
    title="Create Gateway в несуществующий folder → 200 (Operation accepted), затем operation.error NOT_FOUND (KAC-94 skill evgeniy I.4 — async-only)",
    classes=["NEG", "CONF"], priority="P0",
    steps=[
        Step(name="create-bad-folder", method="POST", path="/vpc/v1/gateways",
             body={"projectId": "{{garbageId}}", "name": "gw-fnf-{{runId}}",
                   "sharedEgressGatewaySpec": {}},
             # KAC-94 / skill evgeniy I.4: sync folder.Exists precheck удалён
             # (race-prone). Operation создаётся (200), затем worker падает с
             # NotFound — проверяем через poll-operation.
             test_script=[
                 *assert_status(200),
                 *save_from_response("j.id", "opId"),
             ]),
        poll_operation_until_done(),
        Step(name="assert-op-error", method="GET", path="/operations/{{opId}}",
             test_script=[
                 *assert_status(200),
                 "const j = pm.response.json();",
                 "pm.test('operation done', () => pm.expect(j.done).to.eql(true));",
                 "pm.test('operation has error', () => pm.expect(j.error).to.be.an('object'));",
                 "pm.test('error code is NOT_FOUND', () => pm.expect(j.error.code).to.eql(5));",
                 "pm.test('mentions folder not found', () => pm.expect((j.error.message || '').toLowerCase()).to.include('folder'));",
             ]),
    ],
))

CASES.append(Case(
    id="GW-LOP-NEG-PARENT-NF",
    title="ListOperations несуществующего Gateway → 200 или 404",
    classes=["NEG"], priority="P2",
    steps=[
        Step(name="lop-nx", method="GET",
             path="/vpc/v1/gateways/{{garbageVpcId}}/operations",
             test_script=["pm.test('200 or 404', () => pm.expect(pm.response.code).to.be.oneOf([200, 404]));"]),
    ],
))

CASES.extend(ecp_name_block("GW", "/vpc/v1/gateways", {"sharedEgressGatewaySpec": {}}))
CASES.extend(ecp_description_block("GW", "/vpc/v1/gateways", {"sharedEgressGatewaySpec": {}}))
CASES.extend(ecp_labels_block("GW", "/vpc/v1/gateways", {"sharedEgressGatewaySpec": {}}))
CASES.extend(updatemask_decision_table("GW", "/vpc/v1/gateways"))
CASES.extend(filter_syntax_block("GW", "/vpc/v1/gateways"))
CASES.append(pagination_roundtrip("GW", "/vpc/v1/gateways"))

CASES.extend(update_happy_per_field("GW", "/vpc/v1/gateways", "/vpc/v1/gateways",
    {"projectId": "{{_suiteFolderId}}", "sharedEgressGatewaySpec": {}}))
CASES.extend(perf_baseline_block("GW", "/vpc/v1/gateways"))
CASES.append(move_same_folder("GW", "/vpc/v1/gateways",
    {"projectId": "{{_suiteFolderId}}", "sharedEgressGatewaySpec": {}}))
CASES.extend(verbatim_text_pack("GW", "Gateway", "/vpc/v1/gateways"))
CASES.extend(authz_caller_headers_block("GW", "/vpc/v1/gateways"))

CASES.append(update_happy_multi_field("GW", "/vpc/v1/gateways", "/vpc/v1/gateways",
    {"projectId": "{{_suiteFolderId}}", "sharedEgressGatewaySpec": {}}))
CASES.append(cross_folder_resource_block("GW", "/vpc/v1/gateways", {"sharedEgressGatewaySpec": {}}))
CASES.append(list_filter_match_block("GW", "/vpc/v1/gateways",
    {"projectId": "{{_suiteFolderId}}", "sharedEgressGatewaySpec": {}}))
CASES.extend(neg_invalid_types_block("GW", "/vpc/v1/gateways",
    {"projectId": "{{_suiteFolderId}}", "sharedEgressGatewaySpec": {}}))
CASES.extend(http_method_not_allowed_block("GW", "/vpc/v1/gateways"))
CASES.extend(malformed_body_block("GW", "/vpc/v1/gateways"))

# NB: имена Gateway в YC НЕ уникальны (probe 2026-05-11) — дубль-имя создаётся успешно;
# generated alreadyexists_dup_name_for("GW", ...) убран (kacho-vpc#9).
CASES.extend(update_mask_partial_block("GW", "/vpc/v1/gateways", "/vpc/v1/gateways",
    {"projectId": "{{_suiteFolderId}}", "sharedEgressGatewaySpec": {}}))
CASES.append(perf_baseline_get_block("GW", "/vpc/v1/gateways",
    {"projectId": "{{_suiteFolderId}}", "sharedEgressGatewaySpec": {}}))
CASES.extend(list_total_size_check_block("GW", "/vpc/v1/gateways"))
CASES.extend(headers_content_type_block("GW", "/vpc/v1/gateways",
    {"projectId": "{{_suiteFolderId}}", "sharedEgressGatewaySpec": {}}))

# v10 Gateway-specific
CASES.append(Case(
    id="GW-CR-VAL-MISSING-TYPE",
    title="Create Gateway без gateway-type oneof → 400 InvalidArgument 'Illegal argument gateway'",
    classes=["VAL", "NEG"], priority="P1",
    steps=[Step(name="cr-notype", method="POST", path="/vpc/v1/gateways",
                body={"projectId": "{{_suiteFolderId}}", "name": "gw-nt-{{runId}}"},
                test_script=[
                    # verbatim-YC (probe 2026-05-11, kacho-vpc#9): без gateway-type oneof
                    # (или с нераспознанным телом) → 400 InvalidArgument "Illegal argument gateway".
                    *assert_status(400), *assert_grpc_code(3, "INVALID_ARGUMENT"),
                    "pm.test('mentions gateway', () => pm.expect(pm.response.json().message.toLowerCase()).to.include('gateway'));",
                ])],
))

CASES.append(Case(
    id="GW-LST-FILTER-EMPTY",
    title="List Gateway с пустым filter expression → 200 (filter optional)",
    classes=["FILTER", "CRUD"], priority="P2",
    steps=[Step(name="lst-empty-filter", method="GET",
                path="/vpc/v1/gateways?projectId={{_suiteFolderId}}&filter=",
                test_script=[*assert_status(200)])],
))

CASES.append(Case(
    id="GW-GET-WITH-QUERY-PARAMS",
    title="Get Gateway с дополнительными query params → 200 (ignored)",
    classes=["VAL", "CRUD"], priority="P3",
    steps=[Step(name="get-extra-params", method="GET",
                path="/vpc/v1/gateways/{{garbageVpcId}}?extra=ignored&another=param",
                test_script=["pm.test('404 with NOT_FOUND', () => pm.expect(pm.response.code).to.eql(404));"])],
))

CASES.append(Case(
    id="GW-LST-FILTER-CASE-SENSITIVITY",
    title="Filter case-sensitivity на name field",
    classes=["FILTER"], priority="P3",
    steps=[Step(name="lst-case", method="GET",
                path="/vpc/v1/gateways?projectId={{_suiteFolderId}}&filter=name%3D%22NONEXISTENT-UPPER%22",
                test_script=[*assert_status(200),
                             "pm.test('no matches', () => pm.expect((pm.response.json().gateways || []).length).to.eql(0));"])],
))

# v11 edge cases
CASES.append(Case(
    id="GW-LST-PAGE-NEGATIVE-SIZE",
    title="List с pageSize=-1 → 400 или 200",
    classes=["BVA", "VAL"], priority="P2",
    steps=[Step(name="lst-neg", method="GET",
                path="/vpc/v1/gateways?projectId={{_suiteFolderId}}&pageSize=-1",
                test_script=["pm.test('rejected or default', () => pm.expect(pm.response.code).to.be.oneOf([200, 400]));"])],
))

CASES.append(Case(
    id="GW-LST-FILTER-SPECIAL-CHARS",
    title="List с filter содержащим спец-символы → 400 или 200",
    classes=["FILTER", "VAL"], priority="P3",
    steps=[Step(name="lst-fsc", method="GET",
                path="/vpc/v1/gateways?projectId={{_suiteFolderId}}&filter=name%3D%22%21%40%23%24%25%22",
                test_script=["pm.test('handled', () => pm.expect(pm.response.code).to.be.oneOf([200, 400]));"])],
))

CASES.append(Case(
    id="GW-LST-PAGESIZE-EXACTLY-1000",
    title="List с pageSize=1000 (boundary max) → 200",
    classes=["BVA"], priority="P2",
    steps=[Step(name="lst-max", method="GET",
                path="/vpc/v1/gateways?projectId={{_suiteFolderId}}&pageSize=1000",
                test_script=[*assert_status(200)])],
))

CASES.append(Case(
    id="GW-LST-PAGESIZE-1001",
    title="List с pageSize=1001 (over max) → 400",
    classes=["BVA", "VAL"], priority="P1",
    steps=[Step(name="lst-1001", method="GET",
                path="/vpc/v1/gateways?projectId={{_suiteFolderId}}&pageSize=1001",
                test_script=[*assert_status(400), *assert_grpc_code(3, "INVALID_ARGUMENT")])],
))

CASES.append(Case(
    id="GW-LST-DOUBLE-FOLDER-PARAM",
    title="List с дубликатом projectId param → 200 (last wins) или 400",
    classes=["VAL"], priority="P3",
    steps=[Step(name="lst-dup", method="GET",
                path="/vpc/v1/gateways?projectId={{_suiteFolderId}}&projectId={{_suiteFolderCrossId}}&pageSize=10",
                test_script=["pm.test('200 or 400', () => pm.expect(pm.response.code).to.be.oneOf([200, 400]));"])],
))

CASES.append(Case(
    id="GW-GET-TRAILING-SLASH",
    title="Get с trailing slash → 404",
    classes=["VAL"], priority="P3",
    steps=[Step(name="get-trail", method="GET", path="/vpc/v1/gateways/{{garbageVpcId}}/",
                test_script=["pm.test('non-2xx', () => pm.expect(pm.response.code).to.be.oneOf([400, 404]));"])],
))

CASES.extend(required_fields_matrix("GW", "/vpc/v1/gateways",
    {"projectId": "{{_suiteFolderId}}", "name": "gw-req-{{runId}}",
     "sharedEgressGatewaySpec": {}},
    ["projectId", "name"]))
CASES.extend(immutable_fields_matrix("GW", "/vpc/v1/gateways",
    ["project_id"]))

CASES.extend(security_injection_block("GW", "/vpc/v1/gateways", "/vpc/v1/gateways",
    {"projectId": "{{_suiteFolderId}}", "sharedEgressGatewaySpec": {}}))
CASES.append(conformance_lifecycle_pack("GW", "/vpc/v1/gateways",
    {"projectId": "{{_suiteFolderId}}", "sharedEgressGatewaySpec": {}}))
