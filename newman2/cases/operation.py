"""Case-set для OperationService.Get (один RPC)."""

CASES = []

CASES.append(Case(
    id="OP-GET-CRUD-OK",
    title="Get свежесозданной operation → done=true с response",
    classes=["CRUD"],
    priority="P1",
    steps=[
        Step(name="create-trigger", method="POST", path="/vpc/v1/networks",
             body={"folderId": "{{_suiteFolderId}}", "name": "opget-{{runId}}"},
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
    title="Get opId без 3-char domain-prefix → 400 InvalidArgument 'unknown prefix'",
    classes=["NEG"],
    priority="P0",
    steps=[
        Step(name="get-garbage", method="GET", path="/operations/{{garbageId}}",
             test_script=[
                 # OpsProxy в api-gateway отвергает id без known 3-char prefix.
                 # Документированное поведение: prefix не из {b1g, enp, bpf} → 400 InvalidArgument.
                 *assert_status(400),
                 *assert_grpc_code(3, "INVALID_ARGUMENT"),
                 "pm.test('mentions unknown prefix', () => pm.expect(pm.response.json().message).to.include('prefix'));",
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
