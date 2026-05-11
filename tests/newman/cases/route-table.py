"""Case-set для RouteTableService."""

CASES = []


def _net_steps(suffix="rt"):
    return [
        Step(name="pre-net", method="POST", path="/vpc/v1/networks",
             body={"folderId": "{{_suiteFolderId}}", "name": f"rt-{suffix}-net-{{{{runId}}}}"},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.networkId", "netId")]),
        poll_operation_until_done(),
    ]


def _cleanup_net():
    return Step(name="cleanup-net", method="DELETE", path="/vpc/v1/networks/{{netId}}",
                test_script=[*assert_status(200), *save_from_response("j.id", "opId")])


CASES.append(Case(
    id="RT-CR-CRUD-OK",
    title="Create RouteTable + Get",
    classes=["CRUD"],
    priority="P1",
    steps=[
        *_net_steps("cr"),
        Step(name="create", method="POST", path="/vpc/v1/routeTables",
             body={"folderId": "{{_suiteFolderId}}", "networkId": "{{netId}}",
                   "name": "rt-cr-{{runId}}", "staticRoutes": []},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.routeTableId", "rtId")]),
        poll_operation_until_done(),
        Step(name="get", method="GET", path="/vpc/v1/routeTables/{{rtId}}",
             test_script=[*assert_status(200),
                          "pm.test('id matches', () => pm.expect(pm.response.json().id).to.eql(pm.environment.get('rtId')));"]),
        Step(name="del-rt", method="DELETE", path="/vpc/v1/routeTables/{{rtId}}",
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        _cleanup_net(),
    ],
))

CASES.append(Case(
    id="RT-CR-VAL-NETWORK-REQUIRED",
    title="Create без network_id → InvalidArgument",
    classes=["VAL"],
    priority="P0",
    steps=[
        Step(name="create-no-network", method="POST", path="/vpc/v1/routeTables",
             body={"folderId": "{{_suiteFolderId}}", "name": "rt-nn-{{runId}}"},
             test_script=[*assert_status(400), *assert_grpc_code(3, "INVALID_ARGUMENT")]),
    ],
))

CASES.append(Case(
    id="RT-CR-NEG-NETWORK-NF",
    title="Create в несуществующую network → sync 404 NOT_FOUND (kacho-vpc#8)",
    classes=["NEG"],
    priority="P0",
    steps=[
        Step(name="create", method="POST", path="/vpc/v1/routeTables",
             body={"folderId": "{{_suiteFolderId}}", "networkId": "{{garbageVpcId}}",
                   "name": "rt-nn-{{runId}}", "staticRoutes": []},
             test_script=[*assert_status(404), *assert_grpc_code(5, "NOT_FOUND"),
                          "pm.test('mentions network', () => pm.expect(pm.response.json().message.toLowerCase()).to.include('network'));"]),
    ],
))

CASES.append(Case(
    id="RT-GET-NEG-NF",
    title="Get malformed id → 400 InvalidArgument 'invalid route table id'",
    classes=["NEG"],
    priority="P0",
    steps=[
        Step(name="get-garbage", method="GET", path="/vpc/v1/routeTables/{{garbageId}}",
             test_script=[
                 # verbatim-YC (probe 2026-05-11, kacho-vpc#7): malformed id (нет известного 3-char префикса)
                 # → 400 InvalidArgument "invalid route table id '<X>'" (раньше было 404 NotFound). Проверка family-agnostic.
                 *assert_status(400),
                 *assert_grpc_code(3, "INVALID_ARGUMENT"),
                 "pm.test('mentions invalid id', () => { const m = pm.response.json().message; pm.expect(m).to.include('invalid'); pm.expect(m).to.include('id'); });",
             ]),
    ],
))

CASES.append(Case(
    id="RT-LST-CRUD-OK",
    title="List route tables",
    classes=["CRUD"],
    priority="P1",
    steps=[
        Step(name="list", method="GET", path="/vpc/v1/routeTables?folderId={{_suiteFolderId}}",
             test_script=[*assert_status(200),
                          "pm.test('routeTables array', () => pm.expect(pm.response.json().routeTables || []).to.be.an('array'));"]),
    ],
))

CASES.append(Case(
    id="RT-LST-VAL-FOLDER-REQUIRED",
    title="List без folderId → InvalidArgument",
    classes=["VAL", "AUTHZ"],
    priority="P0",
    steps=[
        Step(name="list-nofolder", method="GET", path="/vpc/v1/routeTables",
             test_script=[*assert_status(400), *assert_grpc_code(3, "INVALID_ARGUMENT")]),
    ],
))

CASES.append(Case(
    id="RT-UPD-AUTHZ-NF-SYNC",
    title="Update несуществующего → sync 404",
    classes=["NEG", "AUTHZ"],
    priority="P1",
    steps=[
        Step(name="patch-nx", method="PATCH", path="/vpc/v1/routeTables/{{garbageVpcId}}",
             body={"updateMask": "description", "description": "x"},
             test_script=[*assert_status(404), *assert_grpc_code(5, "NOT_FOUND")]),
    ],
))

CASES.append(Case(
    id="RT-DEL-AUTHZ-NF-SYNC",
    title="Delete несуществующего → sync 404",
    classes=["NEG", "AUTHZ"],
    priority="P1",
    steps=[
        Step(name="del-nx", method="DELETE", path="/vpc/v1/routeTables/{{garbageVpcId}}",
             test_script=[*assert_status(404), *assert_grpc_code(5, "NOT_FOUND")]),
    ],
))

CASES.append(Case(
    id="RT-LOP-CRUD-OK",
    title="ListOperations RouteTable",
    classes=["CRUD"],
    priority="P1",
    steps=[
        *_net_steps("lop"),
        Step(name="create-rt", method="POST", path="/vpc/v1/routeTables",
             body={"folderId": "{{_suiteFolderId}}", "networkId": "{{netId}}",
                   "name": "rt-lop-{{runId}}", "staticRoutes": []},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.routeTableId", "rtId")]),
        poll_operation_until_done(),
        Step(name="list-ops", method="GET", path="/vpc/v1/routeTables/{{rtId}}/operations",
             test_script=[*assert_status(200),
                          "pm.test('at least 1 op', () => pm.expect((pm.response.json().operations || []).length).to.be.at.least(1));"]),
        Step(name="cleanup-rt", method="DELETE", path="/vpc/v1/routeTables/{{rtId}}",
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        _cleanup_net(),
    ],
))

# Расширение
CASES.extend(crud_list_bva_block("RT", "/vpc/v1/routeTables"))
CASES.append(conf_not_found_text("RT", "/vpc/v1/routeTables", "Route table"))
CASES.append(state_update_unknown_mask("RT", "/vpc/v1/routeTables"))
CASES.append(authz_move_nf("RT", "/vpc/v1/routeTables"))

CASES.append(Case(
    id="RT-MV-CRUD-OK",
    title="Move RouteTable в другой folder",
    classes=["CRUD"], priority="P1",
    steps=[
        *_net_steps("mv"),
        Step(name="create-rt", method="POST", path="/vpc/v1/routeTables",
             body={"folderId": "{{_suiteFolderId}}", "networkId": "{{netId}}",
                   "name": "rt-mv-{{runId}}", "staticRoutes": []},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.routeTableId", "rtId")]),
        poll_operation_until_done(),
        Step(name="move", method="POST", path="/vpc/v1/routeTables/{{rtId}}:move",
             body={"destinationFolderId": "{{_suiteFolderCrossId}}"},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="cleanup-rt", method="DELETE", path="/vpc/v1/routeTables/{{rtId}}",
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        _cleanup_net(),
    ],
))

CASES.append(Case(
    id="RT-UPD-CRUD-OK",
    title="Update RouteTable description",
    classes=["CRUD"], priority="P1",
    steps=[
        *_net_steps("upd"),
        Step(name="create-rt", method="POST", path="/vpc/v1/routeTables",
             body={"folderId": "{{_suiteFolderId}}", "networkId": "{{netId}}",
                   "name": "rt-upd-{{runId}}", "staticRoutes": []},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.routeTableId", "rtId")]),
        poll_operation_until_done(),
        Step(name="patch", method="PATCH", path="/vpc/v1/routeTables/{{rtId}}",
             body={"updateMask": "description", "description": "upd-newman"},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="cleanup-rt", method="DELETE", path="/vpc/v1/routeTables/{{rtId}}",
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        _cleanup_net(),
    ],
))

# Дополнение: STATE immutable folder + VAL move-no-dest + BVA pagesize=1
CASES.append(state_immutable_folder("RT", "/vpc/v1/routeTables"))
CASES.append(val_move_no_dest("RT", "/vpc/v1/routeTables"))
CASES.append(list_pagesize_1_bva("RT", "/vpc/v1/routeTables"))

CASES.append(Case(
    id="RT-CR-CONF-NET-NF-TEXT",
    title="Create RT в garbage network → sync verbatim 'Network ... not found' (kacho-vpc#8)",
    classes=["CONF", "NEG"], priority="P1",
    steps=[
        Step(name="create", method="POST", path="/vpc/v1/routeTables",
             body={"folderId": "{{_suiteFolderId}}", "networkId": "{{garbageVpcId}}",
                   "name": "rt-confnf-{{runId}}", "staticRoutes": []},
             test_script=[
                 *assert_status(404), *assert_grpc_code(5, "NOT_FOUND"),
                 "pm.test('verbatim Network ... not found', () => pm.expect(pm.response.json().message).to.match(/^Network .* not found$/));",
             ]),
    ],
))

CASES.append(Case(
    id="RT-UPD-CONF-NF-TEXT",
    title="Update несуществующего → verbatim 'Route table ... not found' text",
    classes=["CONF", "NEG"], priority="P1",
    steps=[
        Step(name="patch-nx", method="PATCH",
             path="/vpc/v1/routeTables/{{garbageVpcId}}",
             body={"updateMask": "description", "description": "x"},
             test_script=[
                 *assert_status(404), *assert_grpc_code(5, "NOT_FOUND"),
                 "pm.test('text matches Route table ... not found', () => pm.expect(pm.response.json().message).to.match(/^Route table .* not found$/));",
             ]),
    ],
))

CASES.append(Case(
    id="RT-DEL-CONF-NF-TEXT",
    title="Delete несуществующего → verbatim 'Route table ... not found' text",
    classes=["CONF", "NEG"], priority="P1",
    steps=[
        Step(name="del-nx", method="DELETE",
             path="/vpc/v1/routeTables/{{garbageVpcId}}",
             test_script=[
                 *assert_status(404), *assert_grpc_code(5, "NOT_FOUND"),
                 "pm.test('text matches Route table ... not found', () => pm.expect(pm.response.json().message).to.match(/^Route table .* not found$/));",
             ]),
    ],
))

CASES.append(Case(
    id="RT-MV-CONF-NF-TEXT",
    title="Move несуществующего → verbatim '<Resource> ... not found' text",
    classes=["CONF", "NEG"], priority="P1",
    steps=[
        Step(name="move-nx", method="POST", path="/vpc/v1/routeTables/{{garbageVpcId}}:move",
             body={"destinationFolderId": "{{_suiteFolderId}}"},
             test_script=[
                 *assert_status(404), *assert_grpc_code(5, "NOT_FOUND"),
                 "pm.test('non-empty error text', () => pm.expect(pm.response.json().message).to.be.a('string').and.length.greaterThan(0));",
             ]),
    ],
))

CASES.append(Case(
    id="RT-DEL-CRUD-OK",
    title="RouteTable Delete happy",
    classes=["CRUD"], priority="P1",
    steps=[
        *_net_steps("delok"),
        Step(name="create-rt", method="POST", path="/vpc/v1/routeTables",
             body={"folderId": "{{_suiteFolderId}}", "networkId": "{{netId}}",
                   "name": "rt-delok-{{runId}}", "staticRoutes": []},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.routeTableId", "rtId")]),
        poll_operation_until_done(),
        Step(name="del-happy", method="DELETE", path="/vpc/v1/routeTables/{{rtId}}",
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        _cleanup_net(),
    ],
))

CASES.append(Case(
    id="RT-LOP-NEG-PARENT-NF",
    title="ListOperations несуществующего routeTable → 200 или 404",
    classes=["NEG"], priority="P2",
    steps=[
        Step(name="lop-nx", method="GET",
             path="/vpc/v1/routeTables/{{garbageVpcId}}/operations",
             test_script=["pm.test('200 or 404', () => pm.expect(pm.response.code).to.be.oneOf([200, 404]));"]),
    ],
))

# RT нужен parent network — упаковываем через _wrap_with_net аналогично subnet
def _rt_wrap(prefix, suffix, inner_case):
    uniq = inner_case.id.lower().replace("-","")[-12:]
    return Case(
        id=inner_case.id, title=inner_case.title, classes=inner_case.classes,
        priority=inner_case.priority,
        steps=[*_net_steps(uniq), *inner_case.steps, _cleanup_net()],
    )

_rt_body = {"networkId": "{{netId}}", "staticRoutes": []}
for c in ecp_name_block("RT", "/vpc/v1/routeTables", _rt_body):
    CASES.append(_rt_wrap("RT", "ecpn", c))
for c in ecp_description_block("RT", "/vpc/v1/routeTables", _rt_body):
    CASES.append(_rt_wrap("RT", "ecpd", c))
for c in ecp_labels_block("RT", "/vpc/v1/routeTables", _rt_body):
    CASES.append(_rt_wrap("RT", "ecpl", c))
CASES.extend(updatemask_decision_table("RT", "/vpc/v1/routeTables"))
CASES.extend(filter_syntax_block("RT", "/vpc/v1/routeTables"))
CASES.append(pagination_roundtrip("RT", "/vpc/v1/routeTables"))

for c in update_happy_per_field("RT", "/vpc/v1/routeTables", "/vpc/v1/routeTables",
    {"folderId": "{{_suiteFolderId}}", "networkId": "{{netId}}", "staticRoutes": []}):
    CASES.append(_rt_wrap("RT", "v7", c))

CASES.extend(perf_baseline_block("RT", "/vpc/v1/routeTables"))
CASES.extend(verbatim_text_pack("RT", "Route table", "/vpc/v1/routeTables"))
CASES.extend(authz_caller_headers_block("RT", "/vpc/v1/routeTables"))

CASES.append(_rt_wrap("RT", "mvself",
    move_same_folder("RT", "/vpc/v1/routeTables",
        {"folderId": "{{_suiteFolderId}}", "networkId": "{{netId}}", "staticRoutes": []})))

CASES.append(_rt_wrap("RT", "v8m",
    update_happy_multi_field("RT", "/vpc/v1/routeTables", "/vpc/v1/routeTables",
        {"folderId": "{{_suiteFolderId}}", "networkId": "{{netId}}", "staticRoutes": []})))
CASES.append(_rt_wrap("RT", "v8f",
    list_filter_match_block("RT", "/vpc/v1/routeTables",
        {"folderId": "{{_suiteFolderId}}", "networkId": "{{netId}}", "staticRoutes": []})))
for c in neg_invalid_types_block("RT", "/vpc/v1/routeTables",
    {"folderId": "{{_suiteFolderId}}", "networkId": "{{netId}}", "staticRoutes": []}):
    CASES.append(_rt_wrap("RT", "v8nt", c))
CASES.extend(http_method_not_allowed_block("RT", "/vpc/v1/routeTables"))
CASES.extend(malformed_body_block("RT", "/vpc/v1/routeTables"))

CASES.append(_rt_wrap("RT", "v9d",
    alreadyexists_dup_name_for("RT", "/vpc/v1/routeTables",
        {"folderId": "{{_suiteFolderId}}", "networkId": "{{netId}}", "staticRoutes": []})))
for c in update_mask_partial_block("RT", "/vpc/v1/routeTables", "/vpc/v1/routeTables",
    {"folderId": "{{_suiteFolderId}}", "networkId": "{{netId}}", "staticRoutes": []}):
    CASES.append(_rt_wrap("RT", "v9p", c))
CASES.append(_rt_wrap("RT", "v9pf",
    perf_baseline_get_block("RT", "/vpc/v1/routeTables",
        {"folderId": "{{_suiteFolderId}}", "networkId": "{{netId}}", "staticRoutes": []})))
CASES.extend(list_total_size_check_block("RT", "/vpc/v1/routeTables"))

# v10: RT-specific static_routes validation
for case_id, route, expect_ok in [
    ("RT-CR-VAL-ROUTE-OK", {"destinationPrefix": "0.0.0.0/0", "nextHopAddress": "10.0.0.1"}, True),
    ("RT-CR-VAL-ROUTE-INVALID-PREFIX", {"destinationPrefix": "not-a-cidr", "nextHopAddress": "10.0.0.1"}, False),
    ("RT-CR-VAL-ROUTE-INVALID-HOP", {"destinationPrefix": "0.0.0.0/0", "nextHopAddress": "999.999.999.999"}, False),
    ("RT-CR-VAL-ROUTE-EMPTY-PREFIX", {"destinationPrefix": "", "nextHopAddress": "10.0.0.1"}, False),
    ("RT-CR-VAL-ROUTE-EMPTY-HOP", {"destinationPrefix": "10.0.0.0/24", "nextHopAddress": ""}, False),
]:
    inner = Case(
        id=case_id, title=f"static_routes validation: {case_id}",
        classes=["VAL"] + (["NEG"] if not expect_ok else ["CRUD"]),
        priority="P1",
        steps=[
            Step(name="cr-route", method="POST", path="/vpc/v1/routeTables",
                 body={"folderId": "{{_suiteFolderId}}", "networkId": "{{netId}}",
                       "name": f"rt-r-{case_id.lower()[-6:]}-{{{{runId}}}}",
                       "staticRoutes": [route]},
                 test_script=[
                     f"pm.test('{'200' if expect_ok else 'rejected'}', () => pm.expect(pm.response.code).to.be.oneOf([200, 400]));",
                     *(save_from_response("j.id", "opId") if expect_ok else []),
                     *(save_from_response("j.metadata && j.metadata.routeTableId", "rtId") if expect_ok else []),
                 ]),
        ] + ([poll_operation_until_done(),
              Step(name="cleanup-rt", method="DELETE", path="/vpc/v1/routeTables/{{rtId}}",
                   test_script=[*save_from_response("j.id", "opId")]),
              poll_operation_until_done()] if expect_ok else []),
    )
    CASES.append(_rt_wrap("RT", "v10r" + case_id[-5:].lower(), inner))

# v11 edge cases
CASES.append(Case(
    id="RT-LST-PAGE-NEGATIVE-SIZE",
    title="List с pageSize=-1 → 400 или 200",
    classes=["BVA", "VAL"], priority="P2",
    steps=[Step(name="lst-neg", method="GET",
                path="/vpc/v1/routeTables?folderId={{_suiteFolderId}}&pageSize=-1",
                test_script=["pm.test('rejected or default', () => pm.expect(pm.response.code).to.be.oneOf([200, 400]));"])],
))

CASES.append(Case(
    id="RT-LST-FILTER-SPECIAL-CHARS",
    title="List с filter содержащим спец-символы → 400 или 200",
    classes=["FILTER", "VAL"], priority="P3",
    steps=[Step(name="lst-fsc", method="GET",
                path="/vpc/v1/routeTables?folderId={{_suiteFolderId}}&filter=name%3D%22%21%40%23%24%25%22",
                test_script=["pm.test('handled', () => pm.expect(pm.response.code).to.be.oneOf([200, 400]));"])],
))

CASES.append(Case(
    id="RT-LST-PAGESIZE-EXACTLY-1000",
    title="List с pageSize=1000 (boundary max) → 200",
    classes=["BVA"], priority="P2",
    steps=[Step(name="lst-max", method="GET",
                path="/vpc/v1/routeTables?folderId={{_suiteFolderId}}&pageSize=1000",
                test_script=[*assert_status(200)])],
))

CASES.append(Case(
    id="RT-LST-PAGESIZE-1001",
    title="List с pageSize=1001 (over max) → 400",
    classes=["BVA", "VAL"], priority="P1",
    steps=[Step(name="lst-1001", method="GET",
                path="/vpc/v1/routeTables?folderId={{_suiteFolderId}}&pageSize=1001",
                test_script=[*assert_status(400), *assert_grpc_code(3, "INVALID_ARGUMENT")])],
))

CASES.append(Case(
    id="RT-LST-DOUBLE-FOLDER-PARAM",
    title="List с дубликатом folderId param → 200 (last wins) или 400",
    classes=["VAL"], priority="P3",
    steps=[Step(name="lst-dup", method="GET",
                path="/vpc/v1/routeTables?folderId={{_suiteFolderId}}&folderId={{_suiteFolderCrossId}}&pageSize=10",
                test_script=["pm.test('200 or 400', () => pm.expect(pm.response.code).to.be.oneOf([200, 400]));"])],
))

CASES.append(Case(
    id="RT-GET-TRAILING-SLASH",
    title="Get с trailing slash → 404",
    classes=["VAL"], priority="P3",
    steps=[Step(name="get-trail", method="GET", path="/vpc/v1/routeTables/{{garbageVpcId}}/",
                test_script=["pm.test('non-2xx', () => pm.expect(pm.response.code).to.be.oneOf([400, 404]));"])],
))

for c in required_fields_matrix("RT", "/vpc/v1/routeTables",
    {"folderId": "{{_suiteFolderId}}", "networkId": "{{netId}}",
     "name": "rt-req-{{runId}}", "staticRoutes": []},
    ["folderId", "networkId", "name"]):
    CASES.append(_rt_wrap("RT", "req", c))
CASES.extend(immutable_fields_matrix("RT", "/vpc/v1/routeTables",
    ["folder_id", "network_id"]))

for c in security_injection_block("RT", "/vpc/v1/routeTables", "/vpc/v1/routeTables",
    {"folderId": "{{_suiteFolderId}}", "networkId": "{{netId}}", "staticRoutes": []}):
    CASES.append(_rt_wrap("RT", "sec", c))
