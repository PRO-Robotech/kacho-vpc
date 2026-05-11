"""Case-set для SecurityGroupService."""

CASES = []


def _net_steps(suffix="sg"):
    return [
        Step(name="pre-net", method="POST", path="/vpc/v1/networks",
             body={"folderId": "{{_suiteFolderId}}", "name": f"sg-{suffix}-net-{{{{runId}}}}"},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.networkId", "netId")]),
        poll_operation_until_done(),
    ]


def _cleanup_net():
    return Step(name="cleanup-net", method="DELETE", path="/vpc/v1/networks/{{netId}}",
                test_script=[*assert_status(200), *save_from_response("j.id", "opId")])


CASES.append(Case(
    id="SG-CR-CRUD-OK",
    title="Create SG + Get",
    classes=["CRUD"],
    priority="P1",
    steps=[
        *_net_steps("cr"),
        Step(name="create", method="POST", path="/vpc/v1/securityGroups",
             body={"folderId": "{{_suiteFolderId}}", "networkId": "{{netId}}",
                   "name": "sg-cr-{{runId}}", "ruleSpecs": []},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.securityGroupId", "sgId")]),
        poll_operation_until_done(),
        Step(name="get", method="GET", path="/vpc/v1/securityGroups/{{sgId}}",
             test_script=[*assert_status(200),
                          "pm.test('id matches', () => pm.expect(pm.response.json().id).to.eql(pm.environment.get('sgId')));"]),
        Step(name="cleanup-sg", method="DELETE", path="/vpc/v1/securityGroups/{{sgId}}",
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        _cleanup_net(),
    ],
))

CASES.append(Case(
    id="SG-CR-VAL-NETWORK-REQUIRED",
    title="Create без network_id → InvalidArgument",
    classes=["VAL"],
    priority="P0",
    steps=[
        Step(name="create-no-net", method="POST", path="/vpc/v1/securityGroups",
             body={"folderId": "{{_suiteFolderId}}", "name": "sg-nn-{{runId}}"},
             test_script=[*assert_status(400), *assert_grpc_code(3, "INVALID_ARGUMENT")]),
    ],
))

CASES.append(Case(
    id="SG-GET-NEG-NF",
    title="Get garbage → 404",
    classes=["NEG"],
    priority="P0",
    steps=[
        Step(name="get-garbage", method="GET", path="/vpc/v1/securityGroups/{{garbageId}}",
             test_script=[*assert_status(404), *assert_grpc_code(5, "NOT_FOUND")]),
    ],
))

CASES.append(Case(
    id="SG-LST-CRUD-OK",
    title="List SG в folder → 200",
    classes=["CRUD"],
    priority="P1",
    steps=[
        Step(name="list", method="GET", path="/vpc/v1/securityGroups?folderId={{_suiteFolderId}}",
             test_script=[*assert_status(200),
                          "pm.test('securityGroups array', () => pm.expect(pm.response.json().securityGroups || []).to.be.an('array'));"]),
    ],
))

CASES.append(Case(
    id="SG-LST-VAL-FOLDER-REQUIRED",
    title="List без folder → InvalidArgument",
    classes=["VAL", "AUTHZ"],
    priority="P0",
    steps=[
        Step(name="list-nofolder", method="GET", path="/vpc/v1/securityGroups",
             test_script=[*assert_status(400), *assert_grpc_code(3, "INVALID_ARGUMENT")]),
    ],
))

CASES.append(Case(
    id="SG-UPD-AUTHZ-NF-SYNC",
    title="Update несуществующего → sync 404",
    classes=["NEG", "AUTHZ"],
    priority="P1",
    steps=[
        Step(name="patch-nx", method="PATCH", path="/vpc/v1/securityGroups/{{garbageVpcId}}",
             body={"updateMask": "description", "description": "x"},
             test_script=[*assert_status(404), *assert_grpc_code(5, "NOT_FOUND")]),
    ],
))

CASES.append(Case(
    id="SG-DEL-AUTHZ-NF-SYNC",
    title="Delete несуществующего → sync 404",
    classes=["NEG", "AUTHZ"],
    priority="P1",
    steps=[
        Step(name="del-nx", method="DELETE", path="/vpc/v1/securityGroups/{{garbageVpcId}}",
             test_script=[*assert_status(404), *assert_grpc_code(5, "NOT_FOUND")]),
    ],
))

CASES.append(Case(
    id="SG-URL-CRUD-OK",
    title="UpdateRules: добавить правило",
    classes=["CRUD", "STATE"],
    priority="P1",
    steps=[
        *_net_steps("url"),
        Step(name="create-sg", method="POST", path="/vpc/v1/securityGroups",
             body={"folderId": "{{_suiteFolderId}}", "networkId": "{{netId}}",
                   "name": "sg-url-{{runId}}", "ruleSpecs": []},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.securityGroupId", "sgId")]),
        poll_operation_until_done(),
        Step(name="update-rules", method="PATCH", path="/vpc/v1/securityGroups/{{sgId}}/rules",
             body={
                 "additionRuleSpecs": [
                     {"description": "ingress-tcp-22",
                      "direction": "INGRESS",
                      "ports": {"fromPort": 22, "toPort": 22},
                      "protocolName": "tcp",
                      "cidrBlocks": {"v4CidrBlocks": ["0.0.0.0/0"]}}
                 ]
             },
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="get-sg", method="GET", path="/vpc/v1/securityGroups/{{sgId}}",
             test_script=[*assert_status(200),
                          "pm.test('has 1 rule', () => pm.expect((pm.response.json().rules || []).length).to.be.at.least(1));"]),
        Step(name="cleanup-sg", method="DELETE", path="/vpc/v1/securityGroups/{{sgId}}",
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        _cleanup_net(),
    ],
))

CASES.append(Case(
    id="SG-LOP-CRUD-OK",
    title="ListOperations SG",
    classes=["CRUD"],
    priority="P1",
    steps=[
        *_net_steps("lop"),
        Step(name="create-sg", method="POST", path="/vpc/v1/securityGroups",
             body={"folderId": "{{_suiteFolderId}}", "networkId": "{{netId}}",
                   "name": "sg-lop-{{runId}}", "ruleSpecs": []},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.securityGroupId", "sgId")]),
        poll_operation_until_done(),
        Step(name="list-ops", method="GET", path="/vpc/v1/securityGroups/{{sgId}}/operations",
             test_script=[*assert_status(200),
                          "pm.test('at least 1 op', () => pm.expect((pm.response.json().operations || []).length).to.be.at.least(1));"]),
        Step(name="cleanup-sg", method="DELETE", path="/vpc/v1/securityGroups/{{sgId}}",
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        _cleanup_net(),
    ],
))
