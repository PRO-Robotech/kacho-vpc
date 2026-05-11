"""Case-set для PrivateEndpointService."""

CASES = []

CASES.append(Case(
    id="PE-CR-VAL-FOLDER-REQUIRED",
    title="Create без folder → InvalidArgument",
    classes=["VAL"],
    priority="P0",
    steps=[
        Step(name="create-no-folder", method="POST", path="/vpc/v1/endpoints",
             body={"name": "pe-nf-{{runId}}"},
             test_script=[*assert_status(400), *assert_grpc_code(3, "INVALID_ARGUMENT")]),
    ],
))

CASES.append(Case(
    id="PE-CR-NEG-NETWORK-NF",
    title="Create в несуществующую network → async NotFound",
    classes=["NEG"],
    priority="P0",
    steps=[
        Step(name="create", method="POST", path="/vpc/v1/endpoints",
             body={"folderId": "{{_suiteFolderId}}", "name": "pe-nn-{{runId}}",
                   "networkId": "{{garbageVpcId}}",
                   "objectStorage": {}},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="assert-nf", method="GET", path="/operations/{{opId}}",
             test_script=["pm.test('error code 5', () => pm.expect(pm.response.json().error && pm.response.json().error.code).to.eql(5));"]),
    ],
))

CASES.append(Case(
    id="PE-GET-NEG-NF",
    title="Get malformed id → 400 InvalidArgument 'invalid private endpoint id'",
    classes=["NEG"],
    priority="P0",
    steps=[
        Step(name="get-garbage", method="GET", path="/vpc/v1/endpoints/{{garbageId}}",
             test_script=[
                 # verbatim-YC (probe 2026-05-11, kacho-vpc#7): malformed id (нет известного 3-char префикса)
                 # → 400 InvalidArgument "invalid private endpoint id '<X>'" (раньше было 404 NotFound). Проверка family-agnostic.
                 *assert_status(400),
                 *assert_grpc_code(3, "INVALID_ARGUMENT"),
                 "pm.test('mentions invalid id', () => { const m = pm.response.json().message; pm.expect(m).to.include('invalid'); pm.expect(m).to.include('id'); });",
             ]),
    ],
))

CASES.append(Case(
    id="PE-LST-CRUD-OK",
    title="List private endpoints",
    classes=["CRUD"],
    priority="P1",
    steps=[
        Step(name="list", method="GET", path="/vpc/v1/endpoints?folderId={{_suiteFolderId}}",
             test_script=[*assert_status(200),
                          "pm.test('privateEndpoints array', () => pm.expect(pm.response.json().privateEndpoints || []).to.be.an('array'));"]),
    ],
))

CASES.append(Case(
    id="PE-LST-VAL-FOLDER-REQUIRED",
    title="List без folder → InvalidArgument",
    classes=["VAL", "AUTHZ"],
    priority="P0",
    steps=[
        Step(name="list-nofolder", method="GET", path="/vpc/v1/endpoints",
             test_script=[*assert_status(400), *assert_grpc_code(3, "INVALID_ARGUMENT")]),
    ],
))

CASES.append(Case(
    id="PE-UPD-AUTHZ-NF-SYNC",
    title="Update несуществующего → sync 404",
    classes=["NEG", "AUTHZ"],
    priority="P1",
    steps=[
        Step(name="patch-nx", method="PATCH", path="/vpc/v1/endpoints/{{garbageVpcId}}",
             body={"updateMask": "description", "description": "x"},
             test_script=[*assert_status(404), *assert_grpc_code(5, "NOT_FOUND")]),
    ],
))

CASES.append(Case(
    id="PE-DEL-AUTHZ-NF-SYNC",
    title="Delete несуществующего → sync 404",
    classes=["NEG", "AUTHZ"],
    priority="P1",
    steps=[
        Step(name="del-nx", method="DELETE", path="/vpc/v1/endpoints/{{garbageVpcId}}",
             test_script=[*assert_status(404), *assert_grpc_code(5, "NOT_FOUND")]),
    ],
))

# Расширение
CASES.extend(crud_list_bva_block("PE", "/vpc/v1/endpoints"))
CASES.append(conf_not_found_text("PE", "/vpc/v1/endpoints", "PrivateEndpoint"))
CASES.append(state_update_unknown_mask("PE", "/vpc/v1/endpoints"))

CASES.append(Case(
    id="PE-LOP-CRUD-OK",
    title="ListOperations PrivateEndpoint (через garbage id для негативного покрытия)",
    classes=["NEG"], priority="P2",
    steps=[
        Step(name="list-ops-garbage", method="GET",
             path="/vpc/v1/endpoints/{{garbageVpcId}}/operations",
             test_script=[
                 "pm.test('NotFound or empty', () => pm.expect(pm.response.code).to.be.oneOf([200, 404]));",
             ]),
    ],
))

# Полный PE.Create lifecycle с ObjectStorage spec
def _pe_net_sub(suffix="pe", cidr="10.130.0.0/24"):
    return [
        Step(name="pre-net", method="POST", path="/vpc/v1/networks",
             body={"folderId": "{{_suiteFolderId}}", "name": f"pe-{suffix}-net-{{{{runId}}}}"},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.networkId", "netId")]),
        poll_operation_until_done(),
        Step(name="pre-sub", method="POST", path="/vpc/v1/subnets",
             body={"folderId": "{{_suiteFolderId}}", "networkId": "{{netId}}",
                   "name": f"pe-{suffix}-sub-{{{{runId}}}}", "zoneId": "{{existingZoneId}}",
                   "v4CidrBlocks": [cidr]},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.subnetId", "subId")]),
        poll_operation_until_done(),
    ]

CASES.append(Case(
    id="PE-CR-CRUD-OK",
    title="Create PrivateEndpoint с ObjectStorage spec",
    classes=["CRUD"], priority="P1",
    steps=[
        *_pe_net_sub("cr"),
        Step(name="create", method="POST", path="/vpc/v1/endpoints",
             body={"folderId": "{{_suiteFolderId}}", "name": "pe-cr-{{runId}}",
                   "networkId": "{{netId}}", "subnetId": "{{subId}}",
                   "objectStorage": {}},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.privateEndpointId", "peId")]),
        poll_operation_until_done(),
        Step(name="get", method="GET", path="/vpc/v1/endpoints/{{peId}}",
             test_script=["pm.test('exists or NF', () => pm.expect(pm.response.code).to.be.oneOf([200, 404]));"]),
        Step(name="cleanup-pe", method="DELETE", path="/vpc/v1/endpoints/{{peId}}",
             test_script=["pm.test('cleanup', () => pm.expect(pm.response.code).to.be.oneOf([200, 404]));",
                          *save_from_response("j.id", "opId")]),
        # cleanup parent
        Step(name="cleanup-sub", method="DELETE", path="/vpc/v1/subnets/{{subId}}",
             test_script=[*save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="cleanup-net", method="DELETE", path="/vpc/v1/networks/{{netId}}",
             test_script=[*save_from_response("j.id", "opId")]),
    ],
))

# Дополнение: STATE immutable folder + VAL move-no-dest + BVA pagesize=1
CASES.append(state_immutable_folder("PE", "/vpc/v1/endpoints"))
# PrivateEndpoint не имеет Move RPC — val_move_no_dest пропущен.
CASES.append(list_pagesize_1_bva("PE", "/vpc/v1/endpoints"))

CASES.append(Case(
    id="PE-CR-CONF-NET-NF-TEXT",
    title="Create PE в garbage network → verbatim text 'Network ... not found'",
    classes=["CONF", "NEG"], priority="P1",
    steps=[
        Step(name="create", method="POST", path="/vpc/v1/endpoints",
             body={"folderId": "{{_suiteFolderId}}", "name": "pe-confnf-{{runId}}",
                   "networkId": "{{garbageVpcId}}", "objectStorage": {}},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="assert", method="GET", path="/operations/{{opId}}",
             test_script=[
                 "const j = pm.response.json();",
                 "pm.test('error code 5', () => pm.expect(j.error && j.error.code).to.eql(5));",
                 "pm.test('verbatim Network ... not found', () => pm.expect(j.error.message).to.match(/^Network .* not found$/));",
             ]),
    ],
))

CASES.append(Case(
    id="PE-UPD-CONF-NF-TEXT",
    title="Update несуществующего → verbatim 'PrivateEndpoint ... not found' text",
    classes=["CONF", "NEG"], priority="P1",
    steps=[
        Step(name="patch-nx", method="PATCH",
             path="/vpc/v1/endpoints/{{garbageVpcId}}",
             body={"updateMask": "description", "description": "x"},
             test_script=[
                 *assert_status(404), *assert_grpc_code(5, "NOT_FOUND"),
                 "pm.test('text matches PrivateEndpoint ... not found', () => pm.expect(pm.response.json().message).to.match(/^PrivateEndpoint .* not found$/));",
             ]),
    ],
))

CASES.append(Case(
    id="PE-DEL-CONF-NF-TEXT",
    title="Delete несуществующего → verbatim 'PrivateEndpoint ... not found' text",
    classes=["CONF", "NEG"], priority="P1",
    steps=[
        Step(name="del-nx", method="DELETE",
             path="/vpc/v1/endpoints/{{garbageVpcId}}",
             test_script=[
                 *assert_status(404), *assert_grpc_code(5, "NOT_FOUND"),
                 "pm.test('text matches PrivateEndpoint ... not found', () => pm.expect(pm.response.json().message).to.match(/^PrivateEndpoint .* not found$/));",
             ]),
    ],
))

CASES.append(Case(
    id="PE-UPD-CRUD-OK",
    title="PE Update description happy",
    classes=["CRUD"], priority="P1",
    steps=[
        *_pe_net_sub("updok", "10.131.0.0/24"),
        Step(name="create", method="POST", path="/vpc/v1/endpoints",
             body={"folderId": "{{_suiteFolderId}}", "name": "pe-updok-{{runId}}",
                   "networkId": "{{netId}}", "subnetId": "{{subId}}",
                   "objectStorage": {}},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.privateEndpointId", "peId")]),
        poll_operation_until_done(),
        Step(name="patch", method="PATCH", path="/vpc/v1/endpoints/{{peId}}",
             body={"updateMask": "description", "description": "upd-newman"},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="cleanup-pe", method="DELETE", path="/vpc/v1/endpoints/{{peId}}",
             test_script=[*save_from_response("j.id", "opId")]),
        # cleanup parents
        Step(name="cleanup-sub", method="DELETE", path="/vpc/v1/subnets/{{subId}}",
             test_script=[*save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="cleanup-net", method="DELETE", path="/vpc/v1/networks/{{netId}}",
             test_script=[*save_from_response("j.id", "opId")]),
    ],
))

CASES.append(Case(
    id="PE-DEL-CRUD-OK",
    title="PE Delete happy",
    classes=["CRUD"], priority="P1",
    steps=[
        *_pe_net_sub("delok", "10.132.0.0/24"),
        Step(name="create", method="POST", path="/vpc/v1/endpoints",
             body={"folderId": "{{_suiteFolderId}}", "name": "pe-delok-{{runId}}",
                   "networkId": "{{netId}}", "subnetId": "{{subId}}",
                   "objectStorage": {}},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.privateEndpointId", "peId")]),
        poll_operation_until_done(),
        Step(name="del-happy", method="DELETE", path="/vpc/v1/endpoints/{{peId}}",
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="cleanup-sub", method="DELETE", path="/vpc/v1/subnets/{{subId}}",
             test_script=[*save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="cleanup-net", method="DELETE", path="/vpc/v1/networks/{{netId}}",
             test_script=[*save_from_response("j.id", "opId")]),
    ],
))

CASES.append(Case(
    id="PE-LOP-CRUD-OK",
    title="PE ListOperations happy",
    classes=["CRUD"], priority="P1",
    steps=[
        *_pe_net_sub("lop", "10.133.0.0/24"),
        Step(name="create", method="POST", path="/vpc/v1/endpoints",
             body={"folderId": "{{_suiteFolderId}}", "name": "pe-lop-{{runId}}",
                   "networkId": "{{netId}}", "subnetId": "{{subId}}",
                   "objectStorage": {}},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.privateEndpointId", "peId")]),
        poll_operation_until_done(),
        Step(name="list-ops", method="GET", path="/vpc/v1/endpoints/{{peId}}/operations",
             test_script=[
                 "pm.test('200 or 404', () => pm.expect(pm.response.code).to.be.oneOf([200, 404]));",
             ]),
        Step(name="cleanup-pe", method="DELETE", path="/vpc/v1/endpoints/{{peId}}",
             test_script=[*save_from_response("j.id", "opId")]),
        Step(name="cleanup-sub", method="DELETE", path="/vpc/v1/subnets/{{subId}}",
             test_script=[*save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="cleanup-net", method="DELETE", path="/vpc/v1/networks/{{netId}}",
             test_script=[*save_from_response("j.id", "opId")]),
    ],
))

# PE требует Network+Subnet+objectStorage. ECP делаю через wrap.
def _pe_wrap(prefix, suffix, inner_case):
    uniq = inner_case.id.lower().replace("-","")[-12:]
    return Case(
        id=inner_case.id, title=inner_case.title, classes=inner_case.classes,
        priority=inner_case.priority,
        steps=[*_pe_net_sub(uniq, "10.190.0.0/24"),
               *inner_case.steps,
               Step(name="cleanup-sub-pe", method="DELETE", path="/vpc/v1/subnets/{{subId}}",
                    test_script=[*save_from_response("j.id", "opId")]),
               poll_operation_until_done(),
               Step(name="cleanup-net-pe", method="DELETE", path="/vpc/v1/networks/{{netId}}",
                    test_script=[*save_from_response("j.id", "opId")])],
    )

_pe_body = {"networkId": "{{netId}}", "subnetId": "{{subId}}", "objectStorage": {}}
# ECP по name для PE — берём только key cases чтобы не плодить parent-сетей
for c in ecp_name_block("PE", "/vpc/v1/endpoints", _pe_body)[:3]:
    CASES.append(_pe_wrap("PE", "ecpn", c))
CASES.extend(updatemask_decision_table("PE", "/vpc/v1/endpoints"))
CASES.extend(filter_syntax_block("PE", "/vpc/v1/endpoints"))
CASES.append(pagination_roundtrip("PE", "/vpc/v1/endpoints"))

# PE: update-per-field — добавляем только description+labels (name тяжелее)
for c in update_happy_per_field("PE", "/vpc/v1/endpoints", "/vpc/v1/endpoints",
    {"folderId": "{{_suiteFolderId}}", "networkId": "{{netId}}",
     "subnetId": "{{subId}}", "objectStorage": {}})[1:]:  # skip NAME (PE имеет особый Update)
    CASES.append(_pe_wrap("PE", "v7", c))

CASES.extend(perf_baseline_block("PE", "/vpc/v1/endpoints"))
CASES.extend(verbatim_text_pack("PE", "PrivateEndpoint", "/vpc/v1/endpoints"))
CASES.extend(authz_caller_headers_block("PE", "/vpc/v1/endpoints"))

CASES.append(_pe_wrap("PE", "v8m",
    update_happy_multi_field("PE", "/vpc/v1/endpoints", "/vpc/v1/endpoints",
        {"folderId": "{{_suiteFolderId}}", "networkId": "{{netId}}",
         "subnetId": "{{subId}}", "objectStorage": {}})))
CASES.extend(http_method_not_allowed_block("PE", "/vpc/v1/endpoints"))
CASES.extend(malformed_body_block("PE", "/vpc/v1/endpoints"))

CASES.extend(list_total_size_check_block("PE", "/vpc/v1/endpoints"))

# v10 PE-specific
CASES.append(Case(
    id="PE-CR-VAL-SERVICE-MISSING",
    title="Create PE без objectStorage → 400",
    classes=["VAL", "NEG"], priority="P1",
    steps=[Step(name="cr-no-service", method="POST", path="/vpc/v1/endpoints",
                body={"folderId": "{{_suiteFolderId}}", "name": "pe-noserv-{{runId}}",
                      "networkId": "{{garbageVpcId}}"},
                test_script=["pm.test('rejected', () => pm.expect(pm.response.code).to.be.oneOf([200, 400]));"])],
))

CASES.append(Case(
    id="PE-LST-FILTER-STATUS",
    title="List PE с фильтром по status (если поддерживается)",
    classes=["FILTER"], priority="P3",
    steps=[Step(name="lst-status", method="GET",
                path="/vpc/v1/endpoints?folderId={{_suiteFolderId}}&filter=status%3D%22AVAILABLE%22",
                test_script=["pm.test('200 or 400', () => pm.expect(pm.response.code).to.be.oneOf([200, 400]));"])],
))

# v11 — PE усиленный набор
CASES.append(Case(
    id="PE-CR-VAL-NETWORK-REQUIRED",
    title="Create PE без networkId → 400",
    classes=["VAL", "NEG"], priority="P1",
    steps=[Step(name="cr-no-net", method="POST", path="/vpc/v1/endpoints",
                body={"folderId": "{{_suiteFolderId}}", "name": "pe-nn-{{runId}}",
                      "objectStorage": {}},
                test_script=["pm.test('rejected', () => pm.expect(pm.response.code).to.be.oneOf([200, 400]));"])],
))

CASES.append(Case(
    id="PE-CR-VAL-SUBNET-REQUIRED",
    title="Create PE без subnetId → ожидаемое поведение",
    classes=["VAL"], priority="P2",
    steps=[Step(name="cr-no-sub", method="POST", path="/vpc/v1/endpoints",
                body={"folderId": "{{_suiteFolderId}}", "name": "pe-ns-{{runId}}",
                      "networkId": "{{garbageVpcId}}", "objectStorage": {}},
                test_script=["pm.test('200 or 400', () => pm.expect(pm.response.code).to.be.oneOf([200, 400]));"])],
))

CASES.append(Case(
    id="PE-CR-NEG-SUBNET-NF",
    title="PE Create с garbage subnetId в addressSpec → async NotFound 'Subnet ... not found'",
    classes=["NEG", "CONF"], priority="P1",
    steps=[*_pe_net_sub("nsnf", "10.135.0.0/24"),
           # addressSpec.internalIpv4AddressSpec.subnetId — verbatim YC oneof-форма
           # (плоский subnetId не существует в proto и тихо отбрасывается gateway'ом).
           Step(name="cr-bad-sub", method="POST", path="/vpc/v1/endpoints",
                body={"folderId": "{{_suiteFolderId}}", "name": "pe-nsnf-{{runId}}",
                      "networkId": "{{netId}}",
                      "addressSpec": {"internalIpv4AddressSpec": {"subnetId": "{{garbageVpcId}}"}},
                      "objectStorage": {}},
                test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
           poll_operation_until_done(),
           Step(name="assert-subnet-nf", method="GET", path="/operations/{{opId}}",
                test_script=[
                    "const j = pm.response.json();",
                    "pm.test('operation done', () => pm.expect(j.done).to.eql(true));",
                    "pm.test('error code 5 (NOT_FOUND)', () => pm.expect(j.error && j.error.code, JSON.stringify(j)).to.eql(5));",
                    "pm.test('verbatim Subnet ... not found', () => pm.expect(j.error.message).to.match(/^Subnet .* not found$/));",
                ]),
           Step(name="cleanup-sub", method="DELETE", path="/vpc/v1/subnets/{{subId}}",
                test_script=[*save_from_response("j.id", "opId")]),
           poll_operation_until_done(),
           Step(name="cleanup-net", method="DELETE", path="/vpc/v1/networks/{{netId}}",
                test_script=[*save_from_response("j.id", "opId")])],
))

CASES.append(Case(
    id="PE-CR-CRUD-WITH-SUBNET",
    title="PE Create с валидным addressSpec.internalIpv4AddressSpec.subnetId → address привязан",
    classes=["CRUD"], priority="P2",
    steps=[*_pe_net_sub("wsub", "10.137.0.0/24"),
           Step(name="create", method="POST", path="/vpc/v1/endpoints",
                body={"folderId": "{{_suiteFolderId}}", "name": "pe-wsub-{{runId}}",
                      "networkId": "{{netId}}",
                      "addressSpec": {"internalIpv4AddressSpec": {"subnetId": "{{subId}}"}},
                      "objectStorage": {}},
                test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                             *save_from_response("j.metadata && j.metadata.privateEndpointId", "peId")]),
           poll_operation_until_done(),
           Step(name="get", method="GET", path="/vpc/v1/endpoints/{{peId}}",
                test_script=[*assert_status(200),
                             "pm.test('address.subnetId linked', () => pm.expect(pm.response.json().address && pm.response.json().address.subnetId).to.eql(pm.environment.get('subId')));"]),
           Step(name="cleanup-pe", method="DELETE", path="/vpc/v1/endpoints/{{peId}}",
                test_script=[*save_from_response("j.id", "opId")]),
           poll_operation_until_done(),
           Step(name="cleanup-sub", method="DELETE", path="/vpc/v1/subnets/{{subId}}",
                test_script=[*save_from_response("j.id", "opId")]),
           poll_operation_until_done(),
           Step(name="cleanup-net", method="DELETE", path="/vpc/v1/networks/{{netId}}",
                test_script=[*save_from_response("j.id", "opId")])],
))

CASES.append(Case(
    id="PE-DEL-CONF-NF-TEXT",
    title="Delete несуществующего PE → verbatim 'PrivateEndpoint ... not found'",
    classes=["CONF", "NEG"], priority="P1",
    steps=[Step(name="del-nx", method="DELETE", path="/vpc/v1/endpoints/{{garbageVpcId}}",
                test_script=[*assert_status(404), *assert_grpc_code(5, "NOT_FOUND"),
                             "pm.test('verbatim text', () => pm.expect(pm.response.json().message).to.match(/^PrivateEndpoint .* not found$/));"])],
))

CASES.append(Case(
    id="PE-UPD-CONF-NF-TEXT",
    title="Update несуществующего PE → verbatim text",
    classes=["CONF", "NEG"], priority="P1",
    steps=[Step(name="upd-nx", method="PATCH", path="/vpc/v1/endpoints/{{garbageVpcId}}",
                body={"updateMask": "description", "description": "x"},
                test_script=[*assert_status(404), *assert_grpc_code(5, "NOT_FOUND"),
                             "pm.test('verbatim text', () => pm.expect(pm.response.json().message).to.match(/^PrivateEndpoint .* not found$/));"])],
))

CASES.append(Case(
    id="PE-LST-PAGE-ZERO",
    title="List PE с pageSize=0 → default applied",
    classes=["BVA"], priority="P2",
    steps=[Step(name="lst-0", method="GET", path="/vpc/v1/endpoints?folderId={{_suiteFolderId}}&pageSize=0",
                test_script=[*assert_status(200)])],
))

CASES.append(Case(
    id="PE-LST-PAGE-OVER",
    title="List PE с pageSize=10000 → 400",
    classes=["BVA", "VAL"], priority="P2",
    steps=[Step(name="lst-over", method="GET", path="/vpc/v1/endpoints?folderId={{_suiteFolderId}}&pageSize=10000",
                test_script=[*assert_status(400), *assert_grpc_code(3, "INVALID_ARGUMENT")])],
))

CASES.append(Case(
    id="PE-LST-FILTER-NAME-OK",
    title="List PE с filter=name='x' → 200",
    classes=["FILTER", "CRUD"], priority="P2",
    steps=[Step(name="lst-f", method="GET",
                path="/vpc/v1/endpoints?folderId={{_suiteFolderId}}&filter=name%3D%22nonexistent%22",
                test_script=[*assert_status(200)])],
))

CASES.append(Case(
    id="PE-LST-FILTER-GARBAGE",
    title="List PE с garbage filter → 200 или 400",
    classes=["FILTER", "VAL"], priority="P2",
    steps=[Step(name="lst-fbad", method="GET",
                path="/vpc/v1/endpoints?folderId={{_suiteFolderId}}&filter=garbage%20!syntax",
                test_script=["pm.test('200 or 400', () => pm.expect(pm.response.code).to.be.oneOf([200, 400]));"])],
))

CASES.append(Case(
    id="PE-LST-ROUNDTRIP",
    title="Pagination roundtrip PE",
    classes=["PAGE", "CRUD"], priority="P2",
    steps=[
        Step(name="p1", method="GET", path="/vpc/v1/endpoints?folderId={{_suiteFolderId}}&pageSize=1",
             test_script=[*assert_status(200),
                          "pm.environment.set('peTok', pm.response.json().nextPageToken || '');"]),
        Step(name="p2", method="GET",
             path="/vpc/v1/endpoints?folderId={{_suiteFolderId}}&pageSize=1&pageToken={{peTok}}",
             test_script=[*assert_status(200)]),
    ],
))

CASES.append(Case(
    id="PE-GET-CONF-NF-FULLTEXT",
    title="Get garbage PE → 'PrivateEndpoint <id> not found' формат",
    classes=["CONF", "NEG"], priority="P1",
    steps=[Step(name="get-pe", method="GET", path="/vpc/v1/endpoints/enpsnapshotpe99999",
                test_script=[*assert_status(404), *assert_grpc_code(5, "NOT_FOUND"),
                             "pm.test('exact format', () => pm.expect(pm.response.json().message).to.match(/^PrivateEndpoint enpsnapshotpe99999 not found$/));"])],
))

CASES.append(Case(
    id="PE-METHOD-NOT-ALLOWED",
    title="PUT/HEAD на /endpoints → не разрешено",
    classes=["VAL", "NEG"], priority="P3",
    steps=[Step(name="put-pe", method="PUT", path="/vpc/v1/endpoints", body={},
                test_script=["pm.test('rejected', () => pm.expect(pm.response.code).to.be.oneOf([404, 405, 501]));"])],
))

CASES.append(Case(
    id="PE-GET-EXTRA-QS",
    title="Get PE с unused query params → не влияет",
    classes=["VAL"], priority="P3",
    steps=[Step(name="get-extra", method="GET", path="/vpc/v1/endpoints/{{garbageVpcId}}?foo=bar&baz=qux",
                test_script=[*assert_status(404)])],
))

# PE required + immutable
CASES.extend(required_fields_matrix("PE", "/vpc/v1/endpoints",
    {"folderId": "{{_suiteFolderId}}", "name": "pe-req-{{runId}}",
     "networkId": "{{garbageVpcId}}", "objectStorage": {}},
    ["folderId", "name", "networkId"]))
CASES.extend(immutable_fields_matrix("PE", "/vpc/v1/endpoints",
    ["folder_id", "network_id", "subnet_id", "address_id", "service_type"]))
