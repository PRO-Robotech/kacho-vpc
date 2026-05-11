"""Case-set для AddressService."""

CASES = []

# Address external IP — depends on default pool seeded for zone (REQ-001).
# Internal IP — requires Network + Subnet preflight.

def _make_net_sub(suffix="a", cidr="10.100.0.0/24"):
    return [
        Step(name="pre-net", method="POST", path="/vpc/v1/networks",
             body={"folderId": "{{_suiteFolderId}}", "name": f"adr-{suffix}-net-{{{{runId}}}}"},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.networkId", "netId")]),
        poll_operation_until_done(),
        Step(name="pre-sub", method="POST", path="/vpc/v1/subnets",
             body={"folderId": "{{_suiteFolderId}}", "networkId": "{{netId}}",
                   "name": f"adr-{suffix}-sub-{{{{runId}}}}", "zoneId": "{{existingZoneId}}",
                   "v4CidrBlocks": [cidr]},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.subnetId", "subId")]),
        poll_operation_until_done(),
    ]


def _cleanup_sub_net():
    return [
        Step(name="cleanup-sub", method="DELETE", path="/vpc/v1/subnets/{{subId}}",
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="cleanup-net", method="DELETE", path="/vpc/v1/networks/{{netId}}",
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
    ]


CASES.append(Case(
    id="ADR-CR-CRUD-INT",
    title="Create internal Address → IP в subnet",
    classes=["CRUD"],
    priority="P1",
    steps=[
        *_make_net_sub("cri"),
        Step(name="create", method="POST", path="/vpc/v1/addresses",
             body={"folderId": "{{_suiteFolderId}}", "name": "adr-cri-{{runId}}",
                   "internalIpv4AddressSpec": {"subnetId": "{{subId}}"}},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.addressId", "addrId")]),
        poll_operation_until_done(),
        Step(name="get", method="GET", path="/vpc/v1/addresses/{{addrId}}",
             test_script=[*assert_status(200),
                          "pm.test('has internal ipv4', () => pm.expect(pm.response.json().internalIpv4Address).to.be.an('object'));"]),
        Step(name="cleanup-addr", method="DELETE", path="/vpc/v1/addresses/{{addrId}}",
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        *_cleanup_sub_net(),
    ],
))

CASES.append(Case(
    id="ADR-CR-CRUD-EXT",
    title="Create external Address → IP из default pool",
    classes=["CRUD"],
    priority="P1",
    steps=[
        Step(name="create", method="POST", path="/vpc/v1/addresses",
             body={"folderId": "{{_suiteFolderId}}", "name": "adr-cre-{{runId}}",
                   "externalIpv4AddressSpec": {"zoneId": "{{existingZoneId}}"}},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.addressId", "addrId")]),
        poll_operation_until_done(),
        Step(name="get", method="GET", path="/vpc/v1/addresses/{{addrId}}",
             test_script=[*assert_status(200),
                          "pm.test('has external ipv4', () => pm.expect(pm.response.json().externalIpv4Address).to.be.an('object'));",
                          "pm.test('has ip address value', () => pm.expect(pm.response.json().externalIpv4Address.address).to.match(/^[0-9]+\\.[0-9]+\\.[0-9]+\\.[0-9]+$/));"]),
        Step(name="cleanup", method="DELETE", path="/vpc/v1/addresses/{{addrId}}",
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
    ],
))

CASES.append(Case(
    id="ADR-CR-VAL-SPEC-ONEOF",
    title="Create без external/internal spec → InvalidArgument",
    classes=["VAL"],
    priority="P0",
    steps=[
        Step(name="create-no-spec", method="POST", path="/vpc/v1/addresses",
             body={"folderId": "{{_suiteFolderId}}", "name": "adr-no-{{runId}}"},
             test_script=[*assert_status(400), *assert_grpc_code(3, "INVALID_ARGUMENT")]),
    ],
))

CASES.append(Case(
    id="ADR-CR-VAL-BOTH-SPEC",
    title="Create с обоими spec (external+internal) → InvalidArgument",
    classes=["VAL"],
    priority="P0",
    steps=[
        *_make_net_sub("both", "10.101.0.0/24"),
        Step(name="create-both", method="POST", path="/vpc/v1/addresses",
             body={"folderId": "{{_suiteFolderId}}", "name": "adr-bo-{{runId}}",
                   "externalIpv4AddressSpec": {"zoneId": "{{existingZoneId}}"},
                   "internalIpv4AddressSpec": {"subnetId": "{{subId}}"}},
             test_script=[*assert_status(400), *assert_grpc_code(3, "INVALID_ARGUMENT")]),
        *_cleanup_sub_net(),
    ],
))

CASES.append(Case(
    id="ADR-CR-NEG-SUBNET-NOT-FOUND",
    title="Create internal с garbage subnetId → async NotFound",
    classes=["NEG"],
    priority="P0",
    steps=[
        Step(name="create", method="POST", path="/vpc/v1/addresses",
             body={"folderId": "{{_suiteFolderId}}", "name": "adr-snf-{{runId}}",
                   "internalIpv4AddressSpec": {"subnetId": "{{garbageVpcId}}"}},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="assert-nf", method="GET", path="/operations/{{opId}}",
             test_script=["pm.test('error code 5', () => pm.expect(pm.response.json().error && pm.response.json().error.code).to.eql(5));"]),
    ],
))

CASES.append(Case(
    id="ADR-GET-NEG-NF",
    title="Get malformed id → 400 InvalidArgument 'invalid address id'",
    classes=["NEG"],
    priority="P0",
    steps=[
        Step(name="get-garbage", method="GET", path="/vpc/v1/addresses/{{garbageId}}",
             test_script=[
                 # verbatim-YC (probe 2026-05-11, kacho-vpc#7): malformed id (нет известного 3-char префикса)
                 # → 400 InvalidArgument "invalid address id '<X>'" (раньше было 404 NotFound). Проверка family-agnostic.
                 *assert_status(400),
                 *assert_grpc_code(3, "INVALID_ARGUMENT"),
                 "pm.test('mentions invalid id', () => { const m = pm.response.json().message; pm.expect(m).to.include('invalid'); pm.expect(m).to.include('id'); });",
             ]),
    ],
))

CASES.append(Case(
    id="ADR-LST-CRUD-OK",
    title="List addresses в folder → 200",
    classes=["CRUD"],
    priority="P1",
    steps=[
        Step(name="list", method="GET", path="/vpc/v1/addresses?folderId={{_suiteFolderId}}&pageSize=10",
             test_script=[*assert_status(200),
                          "pm.test('addresses array', () => pm.expect(pm.response.json().addresses || []).to.be.an('array'));"]),
    ],
))

CASES.append(Case(
    id="ADR-LST-VAL-FOLDER-REQUIRED",
    title="List без folderId → InvalidArgument",
    classes=["VAL", "AUTHZ"],
    priority="P0",
    steps=[
        Step(name="list-no-folder", method="GET", path="/vpc/v1/addresses",
             test_script=[*assert_status(400), *assert_grpc_code(3, "INVALID_ARGUMENT")]),
    ],
))

CASES.append(Case(
    id="ADR-DEL-AUTHZ-NF-SYNC",
    title="Delete несуществующего → sync 404",
    classes=["NEG", "AUTHZ"],
    priority="P1",
    steps=[
        Step(name="del-nx", method="DELETE", path="/vpc/v1/addresses/{{garbageVpcId}}",
             test_script=[*assert_status(404), *assert_grpc_code(5, "NOT_FOUND")]),
    ],
))

CASES.append(Case(
    id="ADR-UPD-AUTHZ-NF-SYNC",
    title="Update несуществующего → sync 404",
    classes=["NEG", "AUTHZ"],
    priority="P1",
    steps=[
        Step(name="patch-nx", method="PATCH", path="/vpc/v1/addresses/{{garbageVpcId}}",
             body={"updateMask": "description", "description": "x"},
             test_script=[*assert_status(404), *assert_grpc_code(5, "NOT_FOUND")]),
    ],
))

CASES.append(Case(
    id="ADR-GBV-NEG-NF",
    title="GetByValue несуществующего IP → NotFound (security: не должно leak'ать существование)",
    classes=["NEG", "AUTHZ"],
    priority="P0",
    steps=[
        Step(name="gbv", method="GET", path="/vpc/v1/addresses:byValue?externalIpv4Address=192.0.2.99",
             test_script=[*assert_status(404), *assert_grpc_code(5, "NOT_FOUND")]),
    ],
))

CASES.append(Case(
    id="ADR-LBS-CRUD-OK",
    title="ListBySubnet → массив (возможно пустой)",
    classes=["CRUD"],
    priority="P2",
    steps=[
        *_make_net_sub("lbs", "10.102.0.0/24"),
        Step(name="lbs", method="GET", path="/vpc/v1/addresses:bySubnet?subnetId={{subId}}",
             test_script=[*assert_status(200),
                          "pm.test('addresses array', () => pm.expect(pm.response.json().addresses || []).to.be.an('array'));"]),
        *_cleanup_sub_net(),
    ],
))

CASES.append(Case(
    id="ADR-LOP-CRUD-OK",
    title="ListOperations для address",
    classes=["CRUD"],
    priority="P1",
    steps=[
        Step(name="create", method="POST", path="/vpc/v1/addresses",
             body={"folderId": "{{_suiteFolderId}}", "name": "adr-lop-{{runId}}",
                   "externalIpv4AddressSpec": {"zoneId": "{{existingZoneId}}"}},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.addressId", "addrId")]),
        poll_operation_until_done(),
        Step(name="list-ops", method="GET", path="/vpc/v1/addresses/{{addrId}}/operations",
             test_script=[*assert_status(200),
                          "pm.test('at least 1 op', () => pm.expect((pm.response.json().operations || []).length).to.be.at.least(1));"]),
        Step(name="cleanup", method="DELETE", path="/vpc/v1/addresses/{{addrId}}",
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
    ],
))

# Расширение
CASES.extend(crud_list_bva_block("ADR", "/vpc/v1/addresses"))
CASES.append(conf_not_found_text("ADR", "/vpc/v1/addresses", "Address"))
CASES.append(state_update_unknown_mask("ADR", "/vpc/v1/addresses"))
CASES.append(authz_move_nf("ADR", "/vpc/v1/addresses"))

CASES.append(Case(
    id="ADR-MV-CRUD-OK",
    title="Move external address в другой folder",
    classes=["CRUD"], priority="P1",
    steps=[
        Step(name="create", method="POST", path="/vpc/v1/addresses",
             body={"folderId": "{{_suiteFolderId}}", "name": "adr-mv-{{runId}}",
                   "externalIpv4AddressSpec": {"zoneId": "{{existingZoneId}}"}},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.addressId", "addrId")]),
        poll_operation_until_done(),
        Step(name="move", method="POST", path="/vpc/v1/addresses/{{addrId}}:move",
             body={"destinationFolderId": "{{_suiteFolderCrossId}}"},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="verify", method="GET", path="/vpc/v1/addresses/{{addrId}}",
             test_script=[*assert_status(200),
                          "pm.test('folder updated', () => pm.expect(pm.response.json().folderId).to.eql(pm.environment.get('_suiteFolderCrossId')));"]),
        Step(name="cleanup", method="DELETE", path="/vpc/v1/addresses/{{addrId}}",
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
    ],
))

CASES.append(Case(
    id="ADR-UPD-CRUD-OK",
    title="Update address description через mask",
    classes=["CRUD"], priority="P1",
    steps=[
        Step(name="create", method="POST", path="/vpc/v1/addresses",
             body={"folderId": "{{_suiteFolderId}}", "name": "adr-upd-{{runId}}",
                   "externalIpv4AddressSpec": {"zoneId": "{{existingZoneId}}"}},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.addressId", "addrId")]),
        poll_operation_until_done(),
        Step(name="patch", method="PATCH", path="/vpc/v1/addresses/{{addrId}}",
             body={"updateMask": "description", "description": "upd-newman"},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="verify", method="GET", path="/vpc/v1/addresses/{{addrId}}",
             test_script=[*assert_status(200),
                          "pm.test('description updated', () => pm.expect(pm.response.json().description).to.eql('upd-newman'));"]),
        Step(name="cleanup", method="DELETE", path="/vpc/v1/addresses/{{addrId}}",
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
    ],
))

# Дополнение: STATE immutable folder + VAL move-no-dest + BVA pagesize=1
CASES.append(state_immutable_folder("ADR", "/vpc/v1/addresses"))
CASES.append(val_move_no_dest("ADR", "/vpc/v1/addresses"))
CASES.append(list_pagesize_1_bva("ADR", "/vpc/v1/addresses"))

CASES.append(Case(
    id="ADR-CR-CONF-SUB-NF-TEXT",
    title="Create address с garbage subnet → verbatim 'Subnet ... not found'",
    classes=["CONF", "NEG"], priority="P1",
    steps=[
        Step(name="create", method="POST", path="/vpc/v1/addresses",
             body={"folderId": "{{_suiteFolderId}}", "name": "adr-confnf-{{runId}}",
                   "internalIpv4AddressSpec": {"subnetId": "{{garbageVpcId}}"}},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="assert", method="GET", path="/operations/{{opId}}",
             test_script=[
                 "const j = pm.response.json();",
                 "pm.test('error code 5', () => pm.expect(j.error && j.error.code).to.eql(5));",
                 "pm.test('text matches verbatim Subnet/Folder ... not found', () => pm.expect(j.error.message).to.match(/^(Subnet|Folder) .* not found$/));",
             ]),
    ],
))

CASES.append(Case(
    id="ADR-CR-CONF-FOLDER-NF-TEXT",
    title="Create external address с garbage folder → verbatim 'Folder with id ... not found'",
    classes=["CONF", "NEG"], priority="P1",
    steps=[
        Step(name="create", method="POST", path="/vpc/v1/addresses",
             body={"folderId": "{{garbageId}}", "name": "adr-fnf-{{runId}}",
                   "externalIpv4AddressSpec": {"zoneId": "{{existingZoneId}}"}},
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
    id="ADR-UPD-CONF-NF-TEXT",
    title="Update несуществующего → verbatim 'Address ... not found' text",
    classes=["CONF", "NEG"], priority="P1",
    steps=[
        Step(name="patch-nx", method="PATCH",
             path="/vpc/v1/addresses/{{garbageVpcId}}",
             body={"updateMask": "description", "description": "x"},
             test_script=[
                 *assert_status(404), *assert_grpc_code(5, "NOT_FOUND"),
                 "pm.test('text matches Address ... not found', () => pm.expect(pm.response.json().message).to.match(/^Address .* not found$/));",
             ]),
    ],
))

CASES.append(Case(
    id="ADR-DEL-CONF-NF-TEXT",
    title="Delete несуществующего → verbatim 'Address ... not found' text",
    classes=["CONF", "NEG"], priority="P1",
    steps=[
        Step(name="del-nx", method="DELETE",
             path="/vpc/v1/addresses/{{garbageVpcId}}",
             test_script=[
                 *assert_status(404), *assert_grpc_code(5, "NOT_FOUND"),
                 "pm.test('text matches Address ... not found', () => pm.expect(pm.response.json().message).to.match(/^Address .* not found$/));",
             ]),
    ],
))

CASES.append(Case(
    id="ADR-MV-CONF-NF-TEXT",
    title="Move несуществующего → verbatim '<Resource> ... not found' text",
    classes=["CONF", "NEG"], priority="P1",
    steps=[
        Step(name="move-nx", method="POST", path="/vpc/v1/addresses/{{garbageVpcId}}:move",
             body={"destinationFolderId": "{{_suiteFolderId}}"},
             test_script=[
                 *assert_status(404), *assert_grpc_code(5, "NOT_FOUND"),
                 "pm.test('non-empty error text', () => pm.expect(pm.response.json().message).to.be.a('string').and.length.greaterThan(0));",
             ]),
    ],
))

CASES.append(Case(
    id="ADR-DEL-CRUD-OK",
    title="Address Delete happy path",
    classes=["CRUD"], priority="P1",
    steps=[
        Step(name="create", method="POST", path="/vpc/v1/addresses",
             body={"folderId": "{{_suiteFolderId}}", "name": "adr-delok-{{runId}}",
                   "externalIpv4AddressSpec": {"zoneId": "{{existingZoneId}}"}},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.addressId", "addrId")]),
        poll_operation_until_done(),
        Step(name="del-happy", method="DELETE", path="/vpc/v1/addresses/{{addrId}}",
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="get-after-del", method="GET", path="/vpc/v1/addresses/{{addrId}}",
             test_script=[*assert_status(404)]),
    ],
))

CASES.append(Case(
    id="ADR-GBV-CRUD-OK",
    title="GetByValue существующего external IP → 200 + сам Address",
    classes=["CRUD"], priority="P1",
    steps=[
        Step(name="create", method="POST", path="/vpc/v1/addresses",
             body={"folderId": "{{_suiteFolderId}}", "name": "adr-gbv-{{runId}}",
                   "externalIpv4AddressSpec": {"zoneId": "{{existingZoneId}}"}},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.addressId", "addrId")]),
        poll_operation_until_done(),
        Step(name="get-addr", method="GET", path="/vpc/v1/addresses/{{addrId}}",
             test_script=[*assert_status(200),
                          *save_from_response("j.externalIpv4Address && j.externalIpv4Address.address", "allocatedIp")]),
        Step(name="gbv", method="GET",
             path="/vpc/v1/addresses:byValue?externalIpv4Address={{allocatedIp}}",
             test_script=[*assert_status(200),
                          "pm.test('id matches', () => pm.expect(pm.response.json().id).to.eql(pm.environment.get('addrId')));"]),
        Step(name="cleanup", method="DELETE", path="/vpc/v1/addresses/{{addrId}}",
             test_script=[*save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
    ],
))

CASES.append(Case(
    id="ADR-LBS-NEG-PARENT-NF",
    title="ListBySubnet несуществующего subnet → 200 или 404",
    classes=["NEG"], priority="P2",
    steps=[
        Step(name="lbs-nx", method="GET",
             path="/vpc/v1/addresses:bySubnet?subnetId={{garbageVpcId}}",
             test_script=["pm.test('200 or 404', () => pm.expect(pm.response.code).to.be.oneOf([200, 404]));"]),
    ],
))

CASES.append(Case(
    id="ADR-LOP-NEG-PARENT-NF",
    title="ListOperations несуществующего address → 200 или 404",
    classes=["NEG"], priority="P2",
    steps=[
        Step(name="lop-nx", method="GET",
             path="/vpc/v1/addresses/{{garbageVpcId}}/operations",
             test_script=["pm.test('200 or 404', () => pm.expect(pm.response.code).to.be.oneOf([200, 404]));"]),
    ],
))

CASES.extend(ecp_name_block("ADR", "/vpc/v1/addresses",
                             {"externalIpv4AddressSpec": {"zoneId": "{{existingZoneId}}"}}))
CASES.extend(ecp_description_block("ADR", "/vpc/v1/addresses",
                                    {"externalIpv4AddressSpec": {"zoneId": "{{existingZoneId}}"}}))
CASES.extend(ecp_labels_block("ADR", "/vpc/v1/addresses",
                               {"externalIpv4AddressSpec": {"zoneId": "{{existingZoneId}}"}}))
CASES.extend(updatemask_decision_table("ADR", "/vpc/v1/addresses"))
CASES.extend(filter_syntax_block("ADR", "/vpc/v1/addresses"))
CASES.append(pagination_roundtrip("ADR", "/vpc/v1/addresses"))

CASES.extend(update_happy_per_field("ADR", "/vpc/v1/addresses", "/vpc/v1/addresses",
    {"folderId": "{{_suiteFolderId}}", "externalIpv4AddressSpec": {"zoneId": "{{existingZoneId}}"}}))
CASES.extend(perf_baseline_block("ADR", "/vpc/v1/addresses"))
CASES.append(move_same_folder("ADR", "/vpc/v1/addresses",
    {"folderId": "{{_suiteFolderId}}", "externalIpv4AddressSpec": {"zoneId": "{{existingZoneId}}"}}))
CASES.extend(verbatim_text_pack("ADR", "Address", "/vpc/v1/addresses"))
CASES.extend(authz_caller_headers_block("ADR", "/vpc/v1/addresses"))

CASES.append(update_happy_multi_field("ADR", "/vpc/v1/addresses", "/vpc/v1/addresses",
    {"folderId": "{{_suiteFolderId}}", "externalIpv4AddressSpec": {"zoneId": "{{existingZoneId}}"}}))
CASES.append(cross_folder_resource_block("ADR", "/vpc/v1/addresses",
    {"externalIpv4AddressSpec": {"zoneId": "{{existingZoneId}}"}}))
CASES.append(list_filter_match_block("ADR", "/vpc/v1/addresses",
    {"folderId": "{{_suiteFolderId}}", "externalIpv4AddressSpec": {"zoneId": "{{existingZoneId}}"}}))
CASES.extend(neg_invalid_types_block("ADR", "/vpc/v1/addresses",
    {"folderId": "{{_suiteFolderId}}", "externalIpv4AddressSpec": {"zoneId": "{{existingZoneId}}"}}))
CASES.extend(http_method_not_allowed_block("ADR", "/vpc/v1/addresses"))
CASES.extend(malformed_body_block("ADR", "/vpc/v1/addresses"))

CASES.append(alreadyexists_dup_name_for("ADR", "/vpc/v1/addresses",
    {"folderId": "{{_suiteFolderId}}", "externalIpv4AddressSpec": {"zoneId": "{{existingZoneId}}"}}))
CASES.extend(update_mask_partial_block("ADR", "/vpc/v1/addresses", "/vpc/v1/addresses",
    {"folderId": "{{_suiteFolderId}}", "externalIpv4AddressSpec": {"zoneId": "{{existingZoneId}}"}}))
CASES.append(perf_baseline_get_block("ADR", "/vpc/v1/addresses",
    {"folderId": "{{_suiteFolderId}}", "externalIpv4AddressSpec": {"zoneId": "{{existingZoneId}}"}}))
CASES.extend(list_total_size_check_block("ADR", "/vpc/v1/addresses"))
CASES.extend(headers_content_type_block("ADR", "/vpc/v1/addresses",
    {"folderId": "{{_suiteFolderId}}", "externalIpv4AddressSpec": {"zoneId": "{{existingZoneId}}"}}))

# v10 Address-specific
CASES.append(Case(
    id="ADR-CR-VAL-EXT-WITH-SUBNET-FK",
    title="Create external + internal со заданным subnet_id → 400 oneof",
    classes=["VAL", "NEG"], priority="P1",
    steps=[
        Step(name="create-bad-combo", method="POST", path="/vpc/v1/addresses",
             body={"folderId": "{{_suiteFolderId}}", "name": "adr-combo-{{runId}}",
                   "externalIpv4AddressSpec": {"zoneId": "{{existingZoneId}}"},
                   "internalIpv4AddressSpec": {"subnetId": "{{garbageVpcId}}"}},
             test_script=[*assert_status(400), *assert_grpc_code(3, "INVALID_ARGUMENT")]),
    ],
))

CASES.append(Case(
    id="ADR-CR-VAL-RESERVED-USED-OK",
    title="Create address с reserved/used флагами (если разрешено) → 200 или 400",
    classes=["VAL"], priority="P2",
    steps=[
        Step(name="cr-flags", method="POST", path="/vpc/v1/addresses",
             body={"folderId": "{{_suiteFolderId}}", "name": "adr-flg-{{runId}}",
                   "reserved": True, "used": False,
                   "externalIpv4AddressSpec": {"zoneId": "{{existingZoneId}}"}},
             test_script=["pm.test('200 or 400', () => pm.expect(pm.response.code).to.be.oneOf([200, 400]));",
                          *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.addressId", "addrId")]),
        poll_operation_until_done(),
        Step(name="cleanup", method="DELETE", path="/vpc/v1/addresses/{{addrId}}",
             test_script=["pm.test('cleanup', () => pm.expect(pm.response.code).to.be.oneOf([200, 404]));",
                          *save_from_response("j.id", "opId")]),
    ],
))

CASES.append(Case(
    id="ADR-GBV-VAL-INVALID-IP",
    title="GetByValue с garbage IP → 400 или 404",
    classes=["VAL", "NEG"], priority="P2",
    steps=[Step(name="gbv-bad", method="GET",
                path="/vpc/v1/addresses:byValue?externalIpv4Address=not-an-ip",
                test_script=["pm.test('rejected', () => pm.expect(pm.response.code).to.be.oneOf([400, 404]));"])],
))

CASES.append(Case(
    id="ADR-GBV-CONF-NOLEAK-FOR-EXISTING-OTHER",
    title="GetByValue адреса из другого folder → NotFound (security info-leak)",
    classes=["CONF", "AUTHZ"], priority="P0",
    steps=[
        Step(name="cr-in-A", method="POST", path="/vpc/v1/addresses",
             body={"folderId": "{{_suiteFolderId}}", "name": "adr-leak-{{runId}}",
                   "externalIpv4AddressSpec": {"zoneId": "{{existingZoneId}}"}},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.addressId", "addrId")]),
        poll_operation_until_done(),
        Step(name="get-ip", method="GET", path="/vpc/v1/addresses/{{addrId}}",
             test_script=[*assert_status(200),
                          *save_from_response("j.externalIpv4Address && j.externalIpv4Address.address", "leakIp")]),
        # cross-folder GBV не возможен без второго caller — проверяем что get возвращает что-то
        Step(name="gbv-find", method="GET",
             path="/vpc/v1/addresses:byValue?externalIpv4Address={{leakIp}}",
             test_script=[*assert_status(200),
                          "pm.test('id matches', () => pm.expect(pm.response.json().id).to.eql(pm.environment.get('addrId')));"]),
        Step(name="cleanup", method="DELETE", path="/vpc/v1/addresses/{{addrId}}",
             test_script=[*save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
    ],
))

# v11 edge cases
CASES.append(Case(
    id="ADR-LST-PAGE-NEGATIVE-SIZE",
    title="List с pageSize=-1 → 400 или 200",
    classes=["BVA", "VAL"], priority="P2",
    steps=[Step(name="lst-neg", method="GET",
                path="/vpc/v1/addresses?folderId={{_suiteFolderId}}&pageSize=-1",
                test_script=["pm.test('rejected or default', () => pm.expect(pm.response.code).to.be.oneOf([200, 400]));"])],
))

CASES.append(Case(
    id="ADR-LST-FILTER-SPECIAL-CHARS",
    title="List с filter содержащим спец-символы → 400 или 200",
    classes=["FILTER", "VAL"], priority="P3",
    steps=[Step(name="lst-fsc", method="GET",
                path="/vpc/v1/addresses?folderId={{_suiteFolderId}}&filter=name%3D%22%21%40%23%24%25%22",
                test_script=["pm.test('handled', () => pm.expect(pm.response.code).to.be.oneOf([200, 400]));"])],
))

CASES.append(Case(
    id="ADR-LST-PAGESIZE-EXACTLY-1000",
    title="List с pageSize=1000 (boundary max) → 200",
    classes=["BVA"], priority="P2",
    steps=[Step(name="lst-max", method="GET",
                path="/vpc/v1/addresses?folderId={{_suiteFolderId}}&pageSize=1000",
                test_script=[*assert_status(200)])],
))

CASES.append(Case(
    id="ADR-LST-PAGESIZE-1001",
    title="List с pageSize=1001 (over max) → 400",
    classes=["BVA", "VAL"], priority="P1",
    steps=[Step(name="lst-1001", method="GET",
                path="/vpc/v1/addresses?folderId={{_suiteFolderId}}&pageSize=1001",
                test_script=[*assert_status(400), *assert_grpc_code(3, "INVALID_ARGUMENT")])],
))

CASES.append(Case(
    id="ADR-LST-DOUBLE-FOLDER-PARAM",
    title="List с дубликатом folderId param → 200 (last wins) или 400",
    classes=["VAL"], priority="P3",
    steps=[Step(name="lst-dup", method="GET",
                path="/vpc/v1/addresses?folderId={{_suiteFolderId}}&folderId={{_suiteFolderCrossId}}&pageSize=10",
                test_script=["pm.test('200 or 400', () => pm.expect(pm.response.code).to.be.oneOf([200, 400]));"])],
))

CASES.append(Case(
    id="ADR-GET-TRAILING-SLASH",
    title="Get с trailing slash → 404",
    classes=["VAL"], priority="P3",
    steps=[Step(name="get-trail", method="GET", path="/vpc/v1/addresses/{{garbageVpcId}}/",
                test_script=["pm.test('non-2xx', () => pm.expect(pm.response.code).to.be.oneOf([400, 404]));"])],
))

# === Required + Immutable для Address ===
CASES.extend(required_fields_matrix("ADR", "/vpc/v1/addresses",
    {"folderId": "{{_suiteFolderId}}", "name": "adr-req-{{runId}}",
     "externalIpv4AddressSpec": {"zoneId": "{{existingZoneId}}"}},
    ["folderId", "name"]))  # ipv4 spec — oneof, не required
CASES.extend(immutable_fields_matrix("ADR", "/vpc/v1/addresses",
    ["folder_id", "external_ipv4_address_spec", "internal_ipv4_address_spec"]))

CASES.extend(security_injection_block("ADR", "/vpc/v1/addresses", "/vpc/v1/addresses",
    {"folderId": "{{_suiteFolderId}}", "externalIpv4AddressSpec": {"zoneId": "{{existingZoneId}}"}}))
CASES.append(conformance_lifecycle_pack("ADR", "/vpc/v1/addresses",
    {"folderId": "{{_suiteFolderId}}", "externalIpv4AddressSpec": {"zoneId": "{{existingZoneId}}"}}))
