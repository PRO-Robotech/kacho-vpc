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
    title="Create в несуществующую network → async NotFound",
    classes=["NEG"],
    priority="P0",
    steps=[
        Step(name="create", method="POST", path="/vpc/v1/routeTables",
             body={"folderId": "{{_suiteFolderId}}", "networkId": "{{garbageVpcId}}",
                   "name": "rt-nn-{{runId}}", "staticRoutes": []},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="assert-nf", method="GET", path="/operations/{{opId}}",
             test_script=["pm.test('error code 5', () => pm.expect(pm.response.json().error && pm.response.json().error.code).to.eql(5));"]),
    ],
))

CASES.append(Case(
    id="RT-GET-NEG-NF",
    title="Get garbage → 404",
    classes=["NEG"],
    priority="P0",
    steps=[
        Step(name="get-garbage", method="GET", path="/vpc/v1/routeTables/{{garbageId}}",
             test_script=[*assert_status(404), *assert_grpc_code(5, "NOT_FOUND")]),
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
