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
    title="Get garbage → 404",
    classes=["NEG"],
    priority="P0",
    steps=[
        Step(name="get-garbage", method="GET", path="/vpc/v1/addresses/{{garbageId}}",
             test_script=[*assert_status(404), *assert_grpc_code(5, "NOT_FOUND")]),
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
