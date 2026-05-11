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
