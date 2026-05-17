"""Case-set для OperationService.Get (один RPC)."""

CASES = []

CASES.append(Case(
    id="OP-GET-CRUD-OK",
    title="Get свежесозданной operation → done=true с response",
    classes=["CRUD"],
    priority="P1",
    steps=[
        Step(name="create-trigger", method="POST", path="/vpc/v1/networks",
             body={"projectId": "{{_suiteFolderId}}", "name": "opget-{{runId}}"},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.networkId", "netId")]),
        poll_operation_until_done(),
        Step(name="get-op", method="GET", path="/operations/{{opId}}",
             test_script=[*assert_status(200),
                          "const j = pm.response.json();",
                          "pm.test('done=true', () => pm.expect(j.done).to.eql(true));",
                          "pm.test('has response', () => pm.expect(j.response).to.be.an('object'));",
                          "pm.test('metadata has networkId', () => pm.expect(j.metadata && j.metadata.networkId).to.match(/^enp/));"]),
        Step(name="cleanup", method="DELETE", path="/vpc/v1/networks/{{netId}}",
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
    ],
))

CASES.append(Case(
    id="OP-GET-NEG-NF-INVALID-PREFIX",
    title="Get malformed opId → 400 InvalidArgument 'invalid operation id'",
    classes=["NEG"],
    priority="P0",
    steps=[
        Step(name="get-garbage", method="GET", path="/operations/{{garbageId}}",
             test_script=[
                 # OpsProxy api-gateway отвергает синтаксически невалидный/нераспознанный operation id.
                 # verbatim-YC (probe 2026-05-11): malformed id → 400 InvalidArgument "invalid operation id '<X>'".
                 # См. kacho-api-gateway#2: opsproxy выровнен под YC (раньше было "operation_id has unknown prefix").
                 *assert_status(400),
                 *assert_grpc_code(3, "INVALID_ARGUMENT"),
                 "pm.test('mentions invalid operation id', () => pm.expect(pm.response.json().message).to.include('invalid operation id'));",
             ]),
    ],
))

CASES.append(Case(
    id="OP-GET-NEG-NF-VALID-PREFIX",
    title="Get несуществующего opId с правильным префиксом → NotFound",
    classes=["NEG"],
    priority="P1",
    steps=[
        Step(name="get-vpc-garbage", method="GET", path="/operations/{{garbageVpcId}}",
             test_script=[*assert_status(404), *assert_grpc_code(5, "NOT_FOUND")]),
    ],
))

CASES.append(Case(
    # KAC-33: ListOperations no longer does a repo.Get precondition — operation
    # history must remain reachable after the resource is deleted (the operations
    # rows have no FK cascade). verifies subnet :listOperations after delete.
    id="OP-LIST-AFTER-DELETE-OK",
    title="ListOperations подсети после её удаления → 200, непустой список (Create + Delete)",
    classes=["STATE", "CRUD"], priority="P1",
    steps=[
        Step(name="cr-net", method="POST", path="/vpc/v1/networks",
             body={"projectId": "{{_suiteFolderId}}", "name": "oplistdel-net-{{runId}}"},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.networkId", "netId")]),
        poll_operation_until_done(),
        Step(name="cr-sub", method="POST", path="/vpc/v1/subnets",
             body={"projectId": "{{_suiteFolderId}}", "networkId": "{{netId}}",
                   "name": "oplistdel-sub-{{runId}}", "zoneId": "{{existingZoneId}}",
                   "v4CidrBlocks": ["10.249.7.0/24"]},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.subnetId", "subId")]),
        poll_operation_until_done(),
        Step(name="listops-before", method="GET", path="/vpc/v1/subnets/{{subId}}/operations",
             test_script=[*assert_status(200), "const j = pm.response.json();",
                          "pm.test('has Create op', () => pm.expect((j.operations||[]).length).to.be.at.least(1));"]),
        Step(name="del-sub", method="DELETE", path="/vpc/v1/subnets/{{subId}}",
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="listops-after-delete", method="GET", path="/vpc/v1/subnets/{{subId}}/operations",
             test_script=[
                 *assert_status(200), "const j = pm.response.json();",
                 "pm.test('history survives delete (Create + Delete)', () => pm.expect((j.operations||[]).length).to.be.at.least(2));",
             ]),
        Step(name="cleanup-net", method="DELETE", path="/vpc/v1/networks/{{netId}}",
             test_script=[*save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
    ],
))

# Расширение: CONF text
CASES.append(Case(
    id="OP-GET-CONF-NF-TEXT",
    title="Get несуществующего opId → verbatim 'Operation ... not found'",
    classes=["CONF", "NEG"], priority="P1",
    steps=[
        Step(name="get-vpc-garbage", method="GET", path="/operations/{{garbageVpcId}}",
             test_script=[
                 *assert_status(404), *assert_grpc_code(5, "NOT_FOUND"),
                 "pm.test('text matches', () => pm.expect(pm.response.json().message.toLowerCase()).to.include('not found'));",
             ]),
    ],
))
