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
    title="Get garbage → 404",
    classes=["NEG"],
    priority="P0",
    steps=[
        Step(name="get-garbage", method="GET", path="/vpc/v1/endpoints/{{garbageId}}",
             test_script=[*assert_status(404), *assert_grpc_code(5, "NOT_FOUND")]),
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
