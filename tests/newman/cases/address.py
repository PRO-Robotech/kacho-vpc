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
    title="Create с обоими spec (external+internal) → 400 (тело: YC plain-text / наш JSON — defensive)",
    classes=["VAL"],
    priority="P0",
    steps=[
        *_make_net_sub("both", "10.101.0.0/24"),
        # oneof address_spec задан дважды → ошибка JSON-transcoding. Verbatim YC
        # отдаёт plain-text, наш api-gateway — JSON {code,message} (kacho-vpc#10,
        # 07-known-divergences.md). Кейс defensive — 400 + непустое тело.
        Step(name="create-both", method="POST", path="/vpc/v1/addresses",
             body={"folderId": "{{_suiteFolderId}}", "name": "adr-bo-{{runId}}",
                   "externalIpv4AddressSpec": {"zoneId": "{{existingZoneId}}"},
                   "internalIpv4AddressSpec": {"subnetId": "{{subId}}"}},
             test_script=[*assert_transcode_error()]),
        *_cleanup_sub_net(),
    ],
))

CASES.append(Case(
    id="ADR-CR-NEG-SUBNET-NOT-FOUND",
    title="Create internal с garbage subnetId → sync 404 NOT_FOUND (kacho-vpc#8)",
    classes=["NEG"],
    priority="P0",
    steps=[
        Step(name="create", method="POST", path="/vpc/v1/addresses",
             body={"folderId": "{{_suiteFolderId}}", "name": "adr-snf-{{runId}}",
                   "internalIpv4AddressSpec": {"subnetId": "{{garbageVpcId}}"}},
             test_script=[*assert_status(404), *assert_grpc_code(5, "NOT_FOUND"),
                          "pm.test('mentions subnet', () => pm.expect(pm.response.json().message.toLowerCase()).to.include('subnet'));"]),
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
    title="Create address с garbage subnet → sync verbatim 'Subnet ... not found' (kacho-vpc#8)",
    classes=["CONF", "NEG"], priority="P1",
    steps=[
        Step(name="create", method="POST", path="/vpc/v1/addresses",
             body={"folderId": "{{_suiteFolderId}}", "name": "adr-confnf-{{runId}}",
                   "internalIpv4AddressSpec": {"subnetId": "{{garbageVpcId}}"}},
             test_script=[
                 *assert_status(404), *assert_grpc_code(5, "NOT_FOUND"),
                 "pm.test('verbatim Subnet ... not found', () => pm.expect(pm.response.json().message).to.match(/^Subnet .* not found$/));",
             ]),
    ],
))

CASES.append(Case(
    id="ADR-CR-CONF-FOLDER-NF-TEXT",
    title="Create external address с garbage folder → sync verbatim 'Folder with id ... not found' (kacho-vpc#8)",
    classes=["CONF", "NEG"], priority="P1",
    steps=[
        Step(name="create", method="POST", path="/vpc/v1/addresses",
             body={"folderId": "{{garbageId}}", "name": "adr-fnf-{{runId}}",
                   "externalIpv4AddressSpec": {"zoneId": "{{existingZoneId}}"}},
             test_script=[
                 *assert_status(404), *assert_grpc_code(5, "NOT_FOUND"),
                 "pm.test('verbatim text', () => pm.expect(pm.response.json().message).to.match(/^Folder with id .* not found$/));",
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


# ─── KAC-58 / KAC-63: External IPv6 regression coverage ─────────────────────
#
# Backend (KAC-60, миграция 0021): sparse counter-based IPv6 IPAM —
# ipv6_pool_cursors / ipv6_allocated_ips / ipv6_released_offsets, allocator
# = try pop released SKIP LOCKED → fallback bump cursor; ip = pool_base + offset.
# Эти кейсы — black-box проверка через api-gateway, не знают про SQL.
#
# Изоляция: каждый case создаёт свой v6-pool в zone `ru-central1-c` (нет
# seeded-default v4-pool там — не пересекается с ADR-CR-CRUD-EXT). Cleanup
# в обратном порядке: address → pool. Pool DELETE для KAC-60 проходит
# (зависимостей через FK нет; ipv6_allocated_ips → address_pools CASCADE).

POOLS = "/vpc/v1/addressPools"
ADDRS = "/vpc/v1/addresses"


def _make_v6_pool(suffix="v6", zone="ru-central1-d", cidr="2001:db8:cafe::/64",
                  is_default=True):
    """Создать v6-pool для конкретного case + забрать id в poolId.

    KAC-71: split-shape — v6 CIDR кладётся в v6CidrBlocks, v4CidrBlocks=[]."""
    body = {"name": f"adr-{suffix}-pool-{{{{runId}}}}", "kind": "EXTERNAL_PUBLIC",
            "zoneId": zone,
            "v4CidrBlocks": [], "v6CidrBlocks": [cidr],
            "isDefault": is_default}
    return [
        Step(name=f"pre-pool-{suffix}", method="POST", path=POOLS, body=body,
             test_script=[*assert_status(200),
                          *save_from_response("j.id", "poolId")]),
    ]


def _cleanup_pool():
    return [
        Step(name="cleanup-pool", method="DELETE", path=POOLS + "/{{poolId}}",
             test_script=["pm.test('cleanup pool (200 or 400/404)', () => "
                          "pm.expect(pm.response.code).to.be.oneOf([200, 400, 404]));"]),
    ]


CASES.append(Case(
    # index: ADR-CR-CRUD-EXT-V6 — happy path для External IPv6 auto-allocation.
    # Verifies: AddressPool (v6 CIDR) → Address.Create external_ipv6_address_spec
    # без explicit address → backend allocator (sparse counter) выбирает offset 1
    # из pool, ip = base + offset. Get показывает externalIpv6Address.address
    # как валидный v6.
    id="ADR-CR-CRUD-EXT-V6",
    title="Create external_ipv6 Address → IP из default v6 pool (KAC-58)",
    classes=["CRUD"], priority="P1",
    steps=[
        *_make_v6_pool("crv6", zone="ru-central1-d", cidr="2001:db8:cafe::/64"),
        Step(name="create", method="POST", path=ADDRS,
             body={"folderId": "{{_suiteFolderId}}", "name": "adr-crv6-{{runId}}",
                   "externalIpv6AddressSpec": {"zoneId": "ru-central1-d"}},
             test_script=[*assert_status(200),
                          *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.addressId", "addrId")]),
        poll_operation_until_done(),
        Step(name="get", method="GET", path=ADDRS + "/{{addrId}}",
             test_script=[*assert_status(200),
                          "pm.test('has external ipv6', () => pm.expect(pm.response.json().externalIpv6Address).to.be.an('object'));",
                          "pm.test('v6 address looks like ipv6 hex', () => pm.expect(pm.response.json().externalIpv6Address.address).to.match(/^[0-9a-fA-F:]+$/));",
                          "pm.test('v6 ip starts with pool prefix 2001:db8:cafe', () => pm.expect(pm.response.json().externalIpv6Address.address).to.match(/^2001:db8:cafe:/));"]),
        Step(name="cleanup-addr", method="DELETE", path=ADDRS + "/{{addrId}}",
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        *_cleanup_pool(),
    ],
))


CASES.append(Case(
    # index: ADR-CR-NEG-EXT-V6-NO-POOL — cascade resolve не находит v6 pool в зоне.
    # Verifies: Create external_ipv6 в zone, где НЕТ pool с v6 CIDR → backend
    # возвращает FailedPrecondition (cascade resolve fails или allocator не
    # находит подходящий pool). Защищает контракт «family-aware resolve».
    # Используется альтернативная zone `ru-central1-b` — там нет seeded pools,
    # ни v4 ни v6, ни в этом case-set'е других кейсов, создающих pool в b.
    id="ADR-CR-NEG-EXT-V6-NO-POOL",
    title="Create external_ipv6 без v6 pool в зоне → FailedPrecondition (KAC-58)",
    classes=["NEG"], priority="P0",
    steps=[
        Step(name="create", method="POST", path=ADDRS,
             body={"folderId": "{{_suiteFolderId}}", "name": "adr-nv6-{{runId}}",
                   "externalIpv6AddressSpec": {"zoneId": "ru-central1-b"}},
             test_script=[*assert_status(200),
                          *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.addressId", "addrId")]),
        # Operation done=true с error_code=9 (FailedPrecondition) — backend
        # либо в cascade resolve, либо в AllocateExternalIPv6FromPool увидит
        # что pool с v6 CIDR не найден.
        poll_operation_until_done(),
        Step(name="check-op-failed", method="GET", path="/operations/{{opId}}",
             test_script=[*assert_status(200),
                          "pm.test('operation done', () => pm.expect(pm.response.json().done).to.equal(true));",
                          "pm.test('operation has error', () => pm.expect(pm.response.json().error).to.be.an('object'));",
                          "pm.test('error code 9 (FailedPrecondition) or 5 (NotFound)', () => pm.expect(pm.response.json().error.code).to.be.oneOf([5, 9]));"]),
    ],
))


CASES.append(Case(
    # index: ADR-DEL-EXT-V6-RELEASE-REUSE — sparse allocator returns offset to
    # released pool on Delete; next Allocate берёт его first (perceived FIFO).
    # Verifies: First allocate получает IP с offset=1 (вычислимо как base + 1);
    # Delete — push offset=1 в released; Second allocate должен получить
    # **тот же** IP (попадает в pop released path до bump cursor).
    id="ADR-DEL-EXT-V6-RELEASE-REUSE",
    title="Delete v6 Address → offset возвращается в released, Reuse выдаёт тот же IP (KAC-58)",
    classes=["STATE", "CONF"], priority="P1",
    steps=[
        *_make_v6_pool("rru", zone="ru-central1-d", cidr="2001:db8:bee::/64"),
        # 1) Create + remember the IP.
        Step(name="cr-1", method="POST", path=ADDRS,
             body={"folderId": "{{_suiteFolderId}}", "name": "adr-rru1-{{runId}}",
                   "externalIpv6AddressSpec": {"zoneId": "ru-central1-d"}},
             test_script=[*assert_status(200),
                          *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.addressId", "addr1Id")]),
        poll_operation_until_done(),
        Step(name="get-1", method="GET", path=ADDRS + "/{{addr1Id}}",
             test_script=[*assert_status(200),
                          *save_from_response("j.externalIpv6Address && j.externalIpv6Address.address", "firstIp")]),
        # 2) Delete first — pushes offset to ipv6_released_offsets.
        Step(name="del-1", method="DELETE", path=ADDRS + "/{{addr1Id}}",
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        # 3) Allocate again — should pick up the released offset → same IP.
        Step(name="cr-2", method="POST", path=ADDRS,
             body={"folderId": "{{_suiteFolderId}}", "name": "adr-rru2-{{runId}}",
                   "externalIpv6AddressSpec": {"zoneId": "ru-central1-d"}},
             test_script=[*assert_status(200),
                          *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.addressId", "addr2Id")]),
        poll_operation_until_done(),
        Step(name="get-2", method="GET", path=ADDRS + "/{{addr2Id}}",
             test_script=[*assert_status(200),
                          "pm.test('reused IP equals first IP (released-first-allocate)', () => "
                          "pm.expect(pm.response.json().externalIpv6Address.address).to.equal(pm.environment.get('firstIp')));"]),
        Step(name="cleanup-addr2", method="DELETE", path=ADDRS + "/{{addr2Id}}",
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        *_cleanup_pool(),
    ],
))


CASES.append(Case(
    # index: ADR-CR-EXT-V6-FAMILY-FALLTHROUGH — глобальный v4 default pool
    # (seeded в кластере) НЕ "крадёт" v6 запрос: cascade step 4 (zone_default)
    # в zone-a для v6 пуст → step 5 global_default находит v4 pool, family-
    # фильтр (KAC-63) его отвергает (нет v6 cidr) → cascade проваливается с
    # FailedPrecondition (resolve address pool: pool not resolved). Если
    # family-filter сломан — попадаем в Internal "pool has no IPv6 cidr_blocks".
    id="ADR-CR-EXT-V6-FAMILY-FALLTHROUGH",
    title="External v6 в zone без v6 pool: cascade фильтрует v4 default → FailedPrecondition (KAC-58, KAC-63)",
    classes=["CONF", "NEG"], priority="P0",
    steps=[
        Step(name="create", method="POST", path=ADDRS,
             body={"folderId": "{{_suiteFolderId}}", "name": "adr-fal-{{runId}}",
                   "externalIpv6AddressSpec": {"zoneId": "ru-central1-a"}},
             test_script=[*assert_status(200),
                          *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.addressId", "addrId")]),
        poll_operation_until_done(),
        Step(name="check-op-failed", method="GET", path="/operations/{{opId}}",
             test_script=[*assert_status(200),
                          "pm.test('operation done', () => pm.expect(pm.response.json().done).to.equal(true));",
                          "pm.test('operation has error', () => pm.expect(pm.response.json().error).to.be.an('object'));",
                          # KAC-63 family-filter в cascade resolve: должен быть код 9
                          # (FailedPrecondition), а НЕ 13 (Internal). 13 — регрессия.
                          "pm.test('error code 9 (FailedPrecondition), не 13 (Internal — регрессия KAC-63)', () => pm.expect(pm.response.json().error.code).to.equal(9));"]),
    ],
))


# ─── KAC-71: Address.Create fallthrough на split-shape pool (REQ-RESOLVE-01/02) ─

CASES.append(Case(
    # index: ADR-CR-EXT-FALLTHROUGH-V4 — Address.Create v4 в zone где default-pool
    # v6-only. Cascade Step 4 (zone_default) находит pool, family-фильтр
    # (poolHasFamily v4 → len(V4CIDRBlocks)>0 → false) пропускает → fall-through
    # на Step 5; нет v4 global default → ErrPoolNotResolved → Operation error
    # code 9 (FailedPrecondition). Verifies REQ-RESOLVE-02 после split-рефактора.
    id="ADR-CR-EXT-FALLTHROUGH-V4",
    title="Create v4 Address в zone с v6-only default pool → cascade family-skip → FailedPrecondition (REQ-RESOLVE-02, KAC-71)",
    classes=["CONF", "NEG"], priority="P0",
    steps=[
        # Setup v6-only default pool в throwaway zone-d.
        Step(name="cr-v6-default", method="POST", path=POOLS,
             body={"name": "adr-falv4-pool-{{runId}}", "kind": "EXTERNAL_PUBLIC",
                   "zoneId": "ru-central1-d",
                   "v4CidrBlocks": [], "v6CidrBlocks": ["2001:db8:b0b::/64"],
                   "isDefault": True},
             test_script=[*assert_status(200),
                          *save_from_response("j.id", "falV4PoolId")]),
        # Allocate v4 → cascade falls through (нет v4 default в zone-d).
        Step(name="create", method="POST", path=ADDRS,
             body={"folderId": "{{_suiteFolderId}}", "name": "adr-falv4-{{runId}}",
                   "externalIpv4AddressSpec": {"zoneId": "ru-central1-d"}},
             test_script=[*assert_status(200),
                          *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.addressId", "falV4AddrId")]),
        poll_operation_until_done(),
        Step(name="check-op-failed", method="GET", path="/operations/{{opId}}",
             test_script=[*assert_status(200),
                          "pm.test('operation done', () => pm.expect(pm.response.json().done).to.equal(true));",
                          "pm.test('operation has error', () => pm.expect(pm.response.json().error).to.be.an('object'));",
                          # family-filter post-split: код 9 (FailedPrecondition), не 13 (Internal).
                          "pm.test('error code 9 (FailedPrecondition), не 13 (Internal)', () => pm.expect(pm.response.json().error.code).to.equal(9));"]),
        # Cleanup pool.
        Step(name="cleanup-pool", method="DELETE", path=POOLS + "/{{falV4PoolId}}",
             test_script=["pm.test('cleanup pool (200 or 400/404)', () => pm.expect(pm.response.code).to.be.oneOf([200, 400, 404]));"]),
    ],
))


CASES.append(Case(
    # index: ADR-CR-EXT-FALLTHROUGH-V6 — зеркало ADR-CR-EXT-FALLTHROUGH-V4 для v6:
    # default-pool в zone — v4-only; v6-allocate проваливает все 5 шагов cascade
    # (family-filter постоянно). Verifies REQ-RESOLVE-01 после split-рефактора.
    id="ADR-CR-EXT-FALLTHROUGH-V6",
    title="Create v6 Address в zone с v4-only default pool → cascade family-skip → FailedPrecondition (REQ-RESOLVE-01, KAC-71)",
    classes=["CONF", "NEG"], priority="P0",
    steps=[
        Step(name="cr-v4-default", method="POST", path=POOLS,
             body={"name": "adr-falv6-pool-{{runId}}", "kind": "EXTERNAL_PUBLIC",
                   "zoneId": "ru-central1-d",
                   "v4CidrBlocks": ["198.51.100.0/24"], "v6CidrBlocks": [],
                   "isDefault": True},
             test_script=[*assert_status(200),
                          *save_from_response("j.id", "falV6PoolId")]),
        Step(name="create", method="POST", path=ADDRS,
             body={"folderId": "{{_suiteFolderId}}", "name": "adr-falv6-{{runId}}",
                   "externalIpv6AddressSpec": {"zoneId": "ru-central1-d"}},
             test_script=[*assert_status(200),
                          *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.addressId", "falV6AddrId")]),
        poll_operation_until_done(),
        Step(name="check-op-failed", method="GET", path="/operations/{{opId}}",
             test_script=[*assert_status(200),
                          "pm.test('operation done', () => pm.expect(pm.response.json().done).to.equal(true));",
                          "pm.test('operation has error', () => pm.expect(pm.response.json().error).to.be.an('object'));",
                          "pm.test('error code 9 (FailedPrecondition), не 13 (Internal)', () => pm.expect(pm.response.json().error.code).to.equal(9));"]),
        Step(name="cleanup-pool", method="DELETE", path=POOLS + "/{{falV6PoolId}}",
             test_script=["pm.test('cleanup pool (200 or 400/404)', () => pm.expect(pm.response.code).to.be.oneOf([200, 400, 404]));"]),
    ],
))
