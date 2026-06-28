# Copyright (c) PRO-Robotech
# SPDX-License-Identifier: BUSL-1.1

"""Case-set для OperationService.Get (один RPC)."""

CASES = []

CASES.append(Case(
    id="OP-GET-CRUD-OK",
    title="Get свежесозданной operation → done=true с response",
    classes=["CRUD"],
    priority="P1",
    steps=[
        Step(name="create-trigger", method="POST", path="/vpc/v1/networks",
             body={"projectId": "{{_suiteProjectId}}", "name": "opget-{{runId}}"},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.networkId", "netId")]),
        poll_operation_until_done(),
        Step(name="get-op", method="GET", path="/operations/{{opId}}",
             test_script=[*assert_status(200),
                          "const j = pm.response.json();",
                          "pm.test('done=true', () => pm.expect(j.done).to.eql(true));",
                          "pm.test('has response', () => pm.expect(j.response).to.be.an('object'));",
                          "pm.test('metadata has networkId', () => pm.expect(j.metadata && j.metadata.networkId).to.match(/^net/));"]),
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
                 # OpsProxy api-gateway отвергает синтаксически невалидный/нераспознанный operation id:
                 # malformed id → 400 InvalidArgument "invalid operation id '<X>'".
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
    # ListOperations не делает repo.Get precondition — история операций остается
    # доступной после удаления ресурса (строки operations не имеют FK cascade).
    id="OP-LIST-AFTER-DELETE-OK",
    title="ListOperations подсети после ее удаления → 200, непустой список (Create + Delete)",
    classes=["STATE", "CRUD"], priority="P1",
    steps=[
        Step(name="cr-net", method="POST", path="/vpc/v1/networks",
             body={"projectId": "{{_suiteProjectId}}", "name": "oplistdel-net-{{runId}}"},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.networkId", "netId")]),
        poll_operation_until_done(),
        Step(name="cr-sub", method="POST", path="/vpc/v1/subnets",
             body={"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}",
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


# ---------------------------------------------------------------------------
# Форма ответа failed Operation
# ---------------------------------------------------------------------------

CASES.append(Case(
    id="OP-GET-ASYNC-FAILURE-RESPONSE",
    title="Failed Operation: error.code/message заполнены, response пуст (REQ-OPS-01)",
    classes=["STATE", "CONF"], priority="P1",
    steps=[
        # Trigger guaranteed async failure: NetworkInterface.Create references a
        # well-formed but non-existent subnet. NIC.Create performs ALL ref-validation
        # in its async worker (unlike Subnet.Create, which prechecks its parent network
        # synchronously and would reject with a sync NotFound), so the parent-subnet
        # NotFound surfaces as a FAILED Operation — exactly the shape this case asserts.
        Step(name="trigger-fail", method="POST", path="/vpc/v1/networkInterfaces",
             body={"projectId": "{{_suiteProjectId}}",
                   "subnetId": "sub00000000000000000",
                   "name": "op-fail-shape-{{runId}}"},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        Step(name="poll-and-verify-shape", method="GET", path="/operations/{{opId}}",
             test_script=[
                 "let _t = 0; const _MAX = 8;",
                 "const _step = () => pm.sendRequest({",
                 "  url: pm.environment.get('baseUrl') + '/operations/' + pm.environment.get('opId'),",
                 "  method: 'GET',",
                 "  header: { 'Authorization': 'Bearer ' + pm.environment.get('jwtProjectAdminA1') },",
                 "}, (err, res) => {",
                 "  let j = null; try { j = res.json(); } catch (e) {}",
                 "  if (j && j.done) {",
                 "    pm.test('done=true', () => pm.expect(j.done).to.eql(true));",
                 "    pm.test('error.code populated (gRPC code)', () => {",
                 "      pm.expect(j.error, JSON.stringify(j)).to.be.an('object');",
                 "      pm.expect(j.error.code).to.be.a('number');",
                 "    });",
                 "    pm.test('error.message non-empty', () => pm.expect(j.error.message, j.error.message).to.be.a('string').and.not.eql(''));",
                 "    pm.test('response NOT set on failure', () => pm.expect(j.response || null, JSON.stringify(j.response)).to.eql(null));",
                 "    pm.test('metadata still present (resource_id для tracing)', () => {",
                 "      pm.expect(j.metadata, JSON.stringify(j)).to.be.an('object');",
                 "    });",
                 "  } else if (++_t < _MAX) { setTimeout(_step, 500); }",
                 "  else { pm.test('op resolved in time', () => pm.expect.fail('timeout')); }",
                 "});",
                 "_step();",
             ]),
    ],
))
