"""Case-set для GatewayService."""

CASES = []

CASES.append(Case(
    id="GW-CR-CRUD-OK",
    title="Create gateway + Get",
    classes=["CRUD"],
    priority="P1",
    steps=[
        Step(name="create", method="POST", path="/vpc/v1/gateways",
             body={"folderId": "{{_suiteFolderId}}", "name": "gw-cr-{{runId}}",
                   "sharedEgressGateway": {}},
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
             body={"name": "gw-nf-{{runId}}", "sharedEgressGateway": {}},
             test_script=[*assert_status(400), *assert_grpc_code(3, "INVALID_ARGUMENT")]),
    ],
))

CASES.append(Case(
    id="GW-GET-NEG-NF",
    title="Get garbage → 404",
    classes=["NEG"],
    priority="P0",
    steps=[
        Step(name="get-garbage", method="GET", path="/vpc/v1/gateways/{{garbageId}}",
             test_script=[*assert_status(404), *assert_grpc_code(5, "NOT_FOUND")]),
    ],
))

CASES.append(Case(
    id="GW-LST-CRUD-OK",
    title="List gateways",
    classes=["CRUD"],
    priority="P1",
    steps=[
        Step(name="list", method="GET", path="/vpc/v1/gateways?folderId={{_suiteFolderId}}",
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
             body={"folderId": "{{_suiteFolderId}}", "name": "gw-lop-{{runId}}",
                   "sharedEgressGateway": {}},
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
             body={"folderId": "{{_suiteFolderId}}", "name": "gw-mv-{{runId}}",
                   "sharedEgressGateway": {}},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.gatewayId", "gwId")]),
        poll_operation_until_done(),
        Step(name="move", method="POST", path="/vpc/v1/gateways/{{gwId}}:move",
             body={"destinationFolderId": "{{_suiteFolderCrossId}}"},
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
             body={"folderId": "{{_suiteFolderId}}", "name": "gw-upd-{{runId}}",
                   "sharedEgressGateway": {}},
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
             body={"destinationFolderId": "{{_suiteFolderId}}"},
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
             body={"folderId": "{{_suiteFolderId}}", "name": "gw-delok-{{runId}}",
                   "sharedEgressGateway": {}},
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
    title="Create Gateway в несуществующий folder → async NotFound",
    classes=["NEG", "CONF"], priority="P0",
    steps=[
        Step(name="create-bad-folder", method="POST", path="/vpc/v1/gateways",
             body={"folderId": "{{garbageId}}", "name": "gw-fnf-{{runId}}",
                   "sharedEgressGateway": {}},
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

CASES.extend(ecp_name_block("GW", "/vpc/v1/gateways", {"sharedEgressGateway": {}}))
CASES.extend(ecp_description_block("GW", "/vpc/v1/gateways", {"sharedEgressGateway": {}}))
CASES.extend(ecp_labels_block("GW", "/vpc/v1/gateways", {"sharedEgressGateway": {}}))
CASES.extend(updatemask_decision_table("GW", "/vpc/v1/gateways"))
CASES.extend(filter_syntax_block("GW", "/vpc/v1/gateways"))
CASES.append(pagination_roundtrip("GW", "/vpc/v1/gateways"))

CASES.extend(update_happy_per_field("GW", "/vpc/v1/gateways", "/vpc/v1/gateways",
    {"folderId": "{{_suiteFolderId}}", "sharedEgressGateway": {}}))
CASES.extend(perf_baseline_block("GW", "/vpc/v1/gateways"))
CASES.append(move_same_folder("GW", "/vpc/v1/gateways",
    {"folderId": "{{_suiteFolderId}}", "sharedEgressGateway": {}}))
CASES.extend(verbatim_text_pack("GW", "Gateway", "/vpc/v1/gateways"))
CASES.extend(authz_caller_headers_block("GW", "/vpc/v1/gateways"))

CASES.append(update_happy_multi_field("GW", "/vpc/v1/gateways", "/vpc/v1/gateways",
    {"folderId": "{{_suiteFolderId}}", "sharedEgressGateway": {}}))
CASES.append(cross_folder_resource_block("GW", "/vpc/v1/gateways", {"sharedEgressGateway": {}}))
CASES.append(list_filter_match_block("GW", "/vpc/v1/gateways",
    {"folderId": "{{_suiteFolderId}}", "sharedEgressGateway": {}}))
CASES.extend(neg_invalid_types_block("GW", "/vpc/v1/gateways",
    {"folderId": "{{_suiteFolderId}}", "sharedEgressGateway": {}}))
CASES.extend(http_method_not_allowed_block("GW", "/vpc/v1/gateways"))
CASES.extend(malformed_body_block("GW", "/vpc/v1/gateways"))

CASES.append(alreadyexists_dup_name_for("GW", "/vpc/v1/gateways",
    {"folderId": "{{_suiteFolderId}}", "sharedEgressGateway": {}}))
CASES.extend(update_mask_partial_block("GW", "/vpc/v1/gateways", "/vpc/v1/gateways",
    {"folderId": "{{_suiteFolderId}}", "sharedEgressGateway": {}}))
CASES.append(perf_baseline_get_block("GW", "/vpc/v1/gateways",
    {"folderId": "{{_suiteFolderId}}", "sharedEgressGateway": {}}))
CASES.extend(list_total_size_check_block("GW", "/vpc/v1/gateways"))
CASES.extend(headers_content_type_block("GW", "/vpc/v1/gateways",
    {"folderId": "{{_suiteFolderId}}", "sharedEgressGateway": {}}))

# v10 Gateway-specific
CASES.append(Case(
    id="GW-CR-VAL-MISSING-TYPE",
    title="Create Gateway без gateway type oneof → 400",
    classes=["VAL", "NEG"], priority="P1",
    steps=[Step(name="cr-notype", method="POST", path="/vpc/v1/gateways",
                body={"folderId": "{{_suiteFolderId}}", "name": "gw-nt-{{runId}}"},
                test_script=["pm.test('rejected', () => pm.expect(pm.response.code).to.be.oneOf([200, 400]));"])],
))

CASES.append(Case(
    id="GW-LST-FILTER-EMPTY",
    title="List Gateway с пустым filter expression → 200 (filter optional)",
    classes=["FILTER", "CRUD"], priority="P2",
    steps=[Step(name="lst-empty-filter", method="GET",
                path="/vpc/v1/gateways?folderId={{_suiteFolderId}}&filter=",
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
                path="/vpc/v1/gateways?folderId={{_suiteFolderId}}&filter=name%3D%22NONEXISTENT-UPPER%22",
                test_script=[*assert_status(200),
                             "pm.test('no matches', () => pm.expect((pm.response.json().gateways || []).length).to.eql(0));"])],
))

# v11 edge cases
CASES.append(Case(
    id="GW-LST-PAGE-NEGATIVE-SIZE",
    title="List с pageSize=-1 → 400 или 200",
    classes=["BVA", "VAL"], priority="P2",
    steps=[Step(name="lst-neg", method="GET",
                path="/vpc/v1/gateways?folderId={{_suiteFolderId}}&pageSize=-1",
                test_script=["pm.test('rejected or default', () => pm.expect(pm.response.code).to.be.oneOf([200, 400]));"])],
))

CASES.append(Case(
    id="GW-LST-FILTER-SPECIAL-CHARS",
    title="List с filter содержащим спец-символы → 400 или 200",
    classes=["FILTER", "VAL"], priority="P3",
    steps=[Step(name="lst-fsc", method="GET",
                path="/vpc/v1/gateways?folderId={{_suiteFolderId}}&filter=name%3D%22%21%40%23%24%25%22",
                test_script=["pm.test('handled', () => pm.expect(pm.response.code).to.be.oneOf([200, 400]));"])],
))

CASES.append(Case(
    id="GW-LST-PAGESIZE-EXACTLY-1000",
    title="List с pageSize=1000 (boundary max) → 200",
    classes=["BVA"], priority="P2",
    steps=[Step(name="lst-max", method="GET",
                path="/vpc/v1/gateways?folderId={{_suiteFolderId}}&pageSize=1000",
                test_script=[*assert_status(200)])],
))

CASES.append(Case(
    id="GW-LST-PAGESIZE-1001",
    title="List с pageSize=1001 (over max) → 400",
    classes=["BVA", "VAL"], priority="P1",
    steps=[Step(name="lst-1001", method="GET",
                path="/vpc/v1/gateways?folderId={{_suiteFolderId}}&pageSize=1001",
                test_script=[*assert_status(400), *assert_grpc_code(3, "INVALID_ARGUMENT")])],
))

CASES.append(Case(
    id="GW-LST-DOUBLE-FOLDER-PARAM",
    title="List с дубликатом folderId param → 200 (last wins) или 400",
    classes=["VAL"], priority="P3",
    steps=[Step(name="lst-dup", method="GET",
                path="/vpc/v1/gateways?folderId={{_suiteFolderId}}&folderId={{_suiteFolderCrossId}}&pageSize=10",
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
    {"folderId": "{{_suiteFolderId}}", "name": "gw-req-{{runId}}",
     "sharedEgressGateway": {}},
    ["folderId", "name"]))
CASES.extend(immutable_fields_matrix("GW", "/vpc/v1/gateways",
    ["folder_id"]))
