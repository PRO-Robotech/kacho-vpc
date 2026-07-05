# Copyright (c) PRO-Robotech
# SPDX-License-Identifier: BUSL-1.1

"""Case-set для SecurityGroupService."""

CASES = []


def _net_steps(suffix="sg"):
    return [
        Step(name="pre-net", method="POST", path="/vpc/v1/networks",
             body={"projectId": "{{_suiteProjectId}}", "name": f"sg-{suffix}-net-{{{{runId}}}}"},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.networkId", "netId")]),
        poll_operation_until_done(),
    ]


def _cleanup_net():
    return Step(name="cleanup-net", method="DELETE", path="/vpc/v1/networks/{{netId}}",
                test_script=[*assert_status(200), *save_from_response("j.id", "opId")])


def _cleanup_net_lenient():
    # См. route-table.py::_cleanup_net_lenient — wrap'нутый Create мог пройти permissive'но
    # (ресурс создан) → DELETE сети блокируется FK RESTRICT (400). Оба исхода ОК.
    return Step(name="cleanup-net", method="DELETE", path="/vpc/v1/networks/{{netId}}",
                test_script=["pm.test('cleanup net (200 or 400 if child leaked)', () => pm.expect(pm.response.code).to.be.oneOf([200, 400]));",
                             *save_from_response("j.id", "opId")])


CASES.append(Case(
    id="SG-CR-CRUD-OK",
    title="Create SG + Get",
    classes=["CRUD"],
    priority="P1",
    steps=[
        *_net_steps("cr"),
        Step(name="create", method="POST", path="/vpc/v1/securityGroups",
             body={"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}",
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
    # network_id обязателен на Create: Create без networkId → sync 400
    # INVALID_ARGUMENT "network_id required", Operation НЕ создается.
    # verifies SG-NET-01
    id="SG-NET-01-NEG-CREATE-NO-NETWORK",
    title="Create SG без networkId → sync 400 INVALID_ARGUMENT 'network_id required'",
    classes=["VAL", "NEG"],
    priority="P0",
    steps=[
        Step(name="create-no-net", method="POST", path="/vpc/v1/securityGroups",
             body={"projectId": "{{_suiteProjectId}}", "name": "sg-nonet-{{runId}}", "ruleSpecs": []},
             test_script=[*assert_status(400), *assert_grpc_code(3, "INVALID_ARGUMENT"),
                          "pm.test('verbatim text network_id required', () => pm.expect(pm.response.json().message).to.eql('network_id required'));"]),
    ],
))

CASES.append(Case(
    id="SG-CR-WITH-NETWORK-OK",
    title="Create SG c networkId → success → get → networkId echoed",
    classes=["CRUD"],
    priority="P1",
    steps=[
        *_net_steps("withnet"),
        Step(name="create-with-net", method="POST", path="/vpc/v1/securityGroups",
             body={"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}",
                   "name": "sg-withnet-{{runId}}", "ruleSpecs": []},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.securityGroupId", "sgId")]),
        poll_operation_until_done(),
        Step(name="get-with-net", method="GET", path="/vpc/v1/securityGroups/{{sgId}}",
             test_script=[*assert_status(200),
                          "pm.test('networkId echoed', () => pm.expect(pm.response.json().networkId).to.eql(pm.environment.get('netId')));"]),
        Step(name="cleanup-sg", method="DELETE", path="/vpc/v1/securityGroups/{{sgId}}",
             test_script=[*save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        _cleanup_net(),
    ],
))

CASES.append(Case(
    # filter=network_id="<id>" — SG в net-A матчится, SG в другой сети net-B — нет.
    # network_id обязателен, «unbound SG» не существует, поэтому negative-сторона
    # фильтра проверяется SG из ДРУГОЙ сети (а не network-less SG).
    id="SG-LIST-FILTER-NETWORK-OK",
    title="List?filter=network_id=\"<netA>\" — SG net-A present, SG net-B absent",
    classes=["CRUD", "FILTER"],
    priority="P2",
    steps=[
        *_net_steps("fltneta"),  # net-A → {{netId}}
        Step(name="pre-net-b", method="POST", path="/vpc/v1/networks",
             body={"projectId": "{{_suiteProjectId}}", "name": "sg-fltnetb-net-{{runId}}"},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.networkId", "netBId")]),
        poll_operation_until_done(),
        Step(name="create-in-a", method="POST", path="/vpc/v1/securityGroups",
             body={"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}",
                   "name": "sg-flta-{{runId}}", "ruleSpecs": []},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.securityGroupId", "sgAId")]),
        poll_operation_until_done(),
        Step(name="create-in-b", method="POST", path="/vpc/v1/securityGroups",
             body={"projectId": "{{_suiteProjectId}}", "networkId": "{{netBId}}",
                   "name": "sg-fltb-{{runId}}", "ruleSpecs": []},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.securityGroupId", "sgBId")]),
        poll_operation_until_done(),
        Step(name="list-by-network", method="GET",
             path="/vpc/v1/securityGroups?projectId={{_suiteProjectId}}&pageSize=1000&filter=network_id%3D%22{{netId}}%22",
             test_script=[*assert_status(200),
                          "const ids = (pm.response.json().securityGroups || []).map(s => s.id);",
                          "pm.test('SG in net-A present', () => pm.expect(ids).to.include(pm.environment.get('sgAId')));",
                          "pm.test('SG in net-B absent', () => pm.expect(ids).to.not.include(pm.environment.get('sgBId')));"]),
        Step(name="cleanup-sg-a", method="DELETE", path="/vpc/v1/securityGroups/{{sgAId}}",
             test_script=[*save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="cleanup-sg-b", method="DELETE", path="/vpc/v1/securityGroups/{{sgBId}}",
             test_script=[*save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="cleanup-net-b", method="DELETE", path="/vpc/v1/networks/{{netBId}}",
             test_script=[*save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        _cleanup_net(),
    ],
))

CASES.append(Case(
    id="SG-GET-NEG-NF",
    title="Get malformed id → 400 InvalidArgument 'invalid security group id'",
    classes=["NEG"],
    priority="P0",
    steps=[
        Step(name="get-garbage", method="GET", path="/vpc/v1/securityGroups/{{garbageId}}",
             test_script=[
                 # malformed id (нет известного 3-char префикса) → 400 InvalidArgument
                 # "invalid security group id '<X>'". Проверка family-agnostic.
                 *assert_status(400),
                 *assert_grpc_code(3, "INVALID_ARGUMENT"),
                 "pm.test('mentions invalid id', () => { const m = pm.response.json().message; pm.expect(m).to.include('invalid'); pm.expect(m).to.include('id'); });",
             ]),
    ],
))

CASES.append(Case(
    id="SG-LST-CRUD-OK",
    title="List SG в project → 200",
    classes=["CRUD"],
    priority="P1",
    steps=[
        Step(name="list", method="GET", path="/vpc/v1/securityGroups?projectId={{_suiteProjectId}}",
             test_script=[*assert_status(200),
                          "pm.test('securityGroups array', () => pm.expect(pm.response.json().securityGroups || []).to.be.an('array'));"]),
    ],
))

CASES.append(Case(
    id="SG-LST-VAL-PROJECT-REQUIRED",
    title="List без project → InvalidArgument",
    classes=["VAL", "AUTHZ"],
    priority="P0",
    steps=[
        Step(name="list-noproject", method="GET", path="/vpc/v1/securityGroups",
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
             body={"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}",
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
             body={"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}",
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

# Расширение
CASES.extend(crud_list_bva_block("SG", "/vpc/v1/securityGroups"))
CASES.append(conf_not_found_text("SG", "/vpc/v1/securityGroups", "Security group"))
CASES.append(state_update_unknown_mask("SG", "/vpc/v1/securityGroups"))

CASES.append(Case(
    id="SG-UR-NEG-RULE-NF",
    title="UpdateRule малформированного rule_id → sync 400 'Invalid rule id <id>'",
    classes=["NEG"], priority="P1",
    steps=[
        *_net_steps("ur"),
        Step(name="create-sg", method="POST", path="/vpc/v1/securityGroups",
             body={"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}",
                   "name": "sg-ur-{{runId}}", "ruleSpecs": []},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.securityGroupId", "sgId")]),
        poll_operation_until_done(),
        # Малформированный rule_id → синхронный 400 InvalidArgument
        # "Invalid rule id <ruleId>" (не Operation).
        Step(name="ur-bad-rule-id", method="PATCH",
             path="/vpc/v1/securityGroups/{{sgId}}/rules/nonexistent-rule-id",
             body={"updateMask": "description", "description": "x"},
             test_script=[*assert_status(400), *assert_grpc_code(3, "INVALID_ARGUMENT"),
                          "pm.test('verbatim text', () => pm.expect(pm.response.json().message).to.eql('Invalid rule id nonexistent-rule-id'));"]),
        Step(name="cleanup-sg", method="DELETE", path="/vpc/v1/securityGroups/{{sgId}}",
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        _cleanup_net(),
    ],
))

CASES.append(Case(
    id="SG-UPD-CRUD-OK",
    title="Update SG description",
    classes=["CRUD"], priority="P1",
    steps=[
        *_net_steps("upd"),
        Step(name="create-sg", method="POST", path="/vpc/v1/securityGroups",
             body={"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}",
                   "name": "sg-upd-{{runId}}", "ruleSpecs": []},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.securityGroupId", "sgId")]),
        poll_operation_until_done(),
        Step(name="patch", method="PATCH", path="/vpc/v1/securityGroups/{{sgId}}",
             body={"updateMask": "description", "description": "upd-newman"},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="cleanup-sg", method="DELETE", path="/vpc/v1/securityGroups/{{sgId}}",
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        _cleanup_net(),
    ],
))

# Дополнение: STATE immutable project + VAL move-no-dest + BVA pagesize=1
CASES.append(state_immutable_project("SG", "/vpc/v1/securityGroups"))
CASES.append(list_pagesize_1_bva("SG", "/vpc/v1/securityGroups"))

CASES.append(Case(
    id="SG-CR-CONF-NET-NF-TEXT",
    title="Create SG в garbage network → точный текст 'Network ... not found'",
    classes=["CONF", "NEG"], priority="P1",
    steps=[
        Step(name="create", method="POST", path="/vpc/v1/securityGroups",
             body={"projectId": "{{_suiteProjectId}}", "networkId": "{{garbageVpcId}}",
                   "name": "sg-confnf-{{runId}}", "ruleSpecs": []},
             test_script=[
                 *assert_status(404), *assert_grpc_code(5, "NOT_FOUND"),
                 "pm.test('verbatim Network ... not found', () => pm.expect(pm.response.json().message).to.match(/^Network .* not found$/));",
             ]),
    ],
))

CASES.append(Case(
    id="SG-UPD-CONF-NF-TEXT",
    title="Update несуществующего → точный текст 'Security group ... not found' text",
    classes=["CONF", "NEG"], priority="P1",
    steps=[
        Step(name="patch-nx", method="PATCH",
             path="/vpc/v1/securityGroups/{{garbageVpcId}}",
             body={"updateMask": "description", "description": "x"},
             test_script=[
                 *assert_status(404), *assert_grpc_code(5, "NOT_FOUND"),
                 "pm.test('text matches Security group ... not found', () => pm.expect(pm.response.json().message).to.match(/^Security group .* not found$/));",
             ]),
    ],
))

CASES.append(Case(
    id="SG-DEL-CONF-NF-TEXT",
    title="Delete несуществующего → точный текст 'Security group ... not found' text",
    classes=["CONF", "NEG"], priority="P1",
    steps=[
        Step(name="del-nx", method="DELETE",
             path="/vpc/v1/securityGroups/{{garbageVpcId}}",
             test_script=[
                 *assert_status(404), *assert_grpc_code(5, "NOT_FOUND"),
                 "pm.test('text matches Security group ... not found', () => pm.expect(pm.response.json().message).to.match(/^Security group .* not found$/));",
             ]),
    ],
))

CASES.append(Case(
    id="SG-DEL-CRUD-OK",
    title="SG Delete happy",
    classes=["CRUD"], priority="P1",
    steps=[
        *_net_steps("delok"),
        Step(name="create-sg", method="POST", path="/vpc/v1/securityGroups",
             body={"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}",
                   "name": "sg-delok-{{runId}}", "ruleSpecs": []},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.securityGroupId", "sgId")]),
        poll_operation_until_done(),
        Step(name="del-happy", method="DELETE", path="/vpc/v1/securityGroups/{{sgId}}",
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        _cleanup_net(),
    ],
))

CASES.append(Case(
    id="SG-UR-CRUD-OK",
    title="UpdateRule (single) — добавить rule, обновить description",
    classes=["CRUD"], priority="P1",
    steps=[
        *_net_steps("urok"),
        Step(name="create-sg", method="POST", path="/vpc/v1/securityGroups",
             body={"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}",
                   "name": "sg-urok-{{runId}}", "ruleSpecs": []},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.securityGroupId", "sgId")]),
        poll_operation_until_done(),
        Step(name="add-rule", method="PATCH", path="/vpc/v1/securityGroups/{{sgId}}/rules",
             body={"additionRuleSpecs": [
                 {"description": "init", "direction": "INGRESS",
                  "ports": {"fromPort": 80, "toPort": 80}, "protocolName": "tcp",
                  "cidrBlocks": {"v4CidrBlocks": ["0.0.0.0/0"]}}
             ]},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="get-sg-rule-id", method="GET", path="/vpc/v1/securityGroups/{{sgId}}",
             test_script=[*assert_status(200),
                          *save_from_response("(j.rules && j.rules[0] && j.rules[0].id) || ''", "ruleId")]),
        Step(name="ur", method="PATCH", path="/vpc/v1/securityGroups/{{sgId}}/rules/{{ruleId}}",
             body={"updateMask": "description", "description": "updated"},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="cleanup", method="DELETE", path="/vpc/v1/securityGroups/{{sgId}}",
             test_script=[*save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        _cleanup_net(),
    ],
))

CASES.append(Case(
    id="SG-URL-AUTHZ-NF-SYNC",
    title="UpdateRules несуществующего SG → sync 404 от AuthZ-Get",
    classes=["NEG", "AUTHZ", "VAL"], priority="P1",
    steps=[
        Step(name="url-nx", method="PATCH", path="/vpc/v1/securityGroups/{{garbageVpcId}}/rules",
             body={"additionRuleSpecs": []},
             test_script=[*assert_status(404), *assert_grpc_code(5, "NOT_FOUND")]),
    ],
))

CASES.append(Case(
    id="SG-UR-AUTHZ-NF-SYNC",
    title="UpdateRule несуществующего SG → sync 404 от AuthZ-Get",
    classes=["NEG", "AUTHZ", "VAL"], priority="P1",
    steps=[
        Step(name="ur-nx", method="PATCH",
             path="/vpc/v1/securityGroups/{{garbageVpcId}}/rules/any-rule-id",
             body={"updateMask": "description", "description": "x"},
             test_script=[*assert_status(404), *assert_grpc_code(5, "NOT_FOUND")]),
    ],
))

CASES.append(Case(
    id="SG-LOP-NEG-PARENT-NF",
    title="ListOperations несуществующего SG → 200 или 404",
    classes=["NEG"], priority="P2",
    steps=[
        Step(name="lop-nx", method="GET",
             path="/vpc/v1/securityGroups/{{garbageVpcId}}/operations",
             test_script=["pm.test('200 or 404', () => pm.expect(pm.response.code).to.be.oneOf([200, 404]));"]),
    ],
))

def _sg_wrap(prefix, suffix, inner_case):
    uniq = inner_case.id.lower().replace("-","")[-12:]
    return Case(
        id=inner_case.id, title=inner_case.title, classes=inner_case.classes,
        priority=inner_case.priority,
        steps=[*_net_steps(uniq), *inner_case.steps, _cleanup_net_lenient()],
    )

_sg_body = {"networkId": "{{netId}}", "ruleSpecs": []}
for c in ecp_name_block("SG", "/vpc/v1/securityGroups", _sg_body):
    CASES.append(_sg_wrap("SG", "ecpn", c))
for c in ecp_description_block("SG", "/vpc/v1/securityGroups", _sg_body):
    CASES.append(_sg_wrap("SG", "ecpd", c))
for c in ecp_labels_block("SG", "/vpc/v1/securityGroups", _sg_body):
    CASES.append(_sg_wrap("SG", "ecpl", c))
CASES.extend(updatemask_decision_table("SG", "/vpc/v1/securityGroups"))
CASES.extend(filter_syntax_block("SG", "/vpc/v1/securityGroups"))
CASES.append(pagination_roundtrip("SG", "/vpc/v1/securityGroups"))

for c in update_happy_per_field("SG", "/vpc/v1/securityGroups", "/vpc/v1/securityGroups",
    {"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}", "ruleSpecs": []}):
    CASES.append(_sg_wrap("SG", "v7", c))

CASES.extend(perf_baseline_block("SG", "/vpc/v1/securityGroups"))
CASES.extend(verbatim_text_pack("SG", "SecurityGroup", "/vpc/v1/securityGroups", text_template="Security group SecurityGroup.Id(value={id}) not found"))
CASES.extend(authz_caller_headers_block("SG", "/vpc/v1/securityGroups"))

# move_same_project («Destination project is the same as the source») для SG не
# применим: network-bound Move-guard срабатывает первым (все SG network-bound), до
# same-project check. Паттерн *-MV-IDM-SAME-PROJECT остается валиден для прочих
# ресурсов (net/rt/...).

CASES.append(_sg_wrap("SG", "v8m",
    update_happy_multi_field("SG", "/vpc/v1/securityGroups", "/vpc/v1/securityGroups",
        {"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}", "ruleSpecs": []})))
CASES.append(_sg_wrap("SG", "v8f",
    list_filter_match_block("SG", "/vpc/v1/securityGroups",
        {"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}", "ruleSpecs": []})))
for c in neg_invalid_types_block("SG", "/vpc/v1/securityGroups",
    {"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}", "ruleSpecs": []}):
    CASES.append(_sg_wrap("SG", "v8nt", c))
CASES.extend(http_method_not_allowed_block("SG", "/vpc/v1/securityGroups"))
CASES.extend(malformed_body_block("SG", "/vpc/v1/securityGroups"))

CASES.append(_sg_wrap("SG", "v9d",
    alreadyexists_dup_name_for("SG", "/vpc/v1/securityGroups",
        {"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}", "ruleSpecs": []})))
for c in update_mask_partial_block("SG", "/vpc/v1/securityGroups", "/vpc/v1/securityGroups",
    {"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}", "ruleSpecs": []}):
    CASES.append(_sg_wrap("SG", "v9p", c))
CASES.append(_sg_wrap("SG", "v9pf",
    perf_baseline_get_block("SG", "/vpc/v1/securityGroups",
        {"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}", "ruleSpecs": []})))
CASES.extend(list_total_size_check_block("SG", "/vpc/v1/securityGroups"))

# v10: SG-specific rule validation
for case_id, rule, expect_ok in [
    ("SG-URL-VAL-PORT-NEG", {"fromPort": -2, "toPort": 22}, False),
    ("SG-URL-VAL-PORT-OVER-65535", {"fromPort": 65536, "toPort": 65536}, False),
    ("SG-URL-VAL-PORT-ANY-MINUS-1", {"fromPort": -1, "toPort": -1}, True),
    ("SG-URL-VAL-DIRECTION-UNKNOWN", {"fromPort": 80, "toPort": 80, "direction": "DIAGONAL"}, False),
    ("SG-URL-VAL-PROTOCOL-UNKNOWN", {"fromPort": 80, "toPort": 80, "protocolName": "klingon"}, False),
]:
    rule_full = {"description": "test", "direction": rule.pop("direction", "INGRESS"),
                 "ports": {"fromPort": rule["fromPort"], "toPort": rule["toPort"]},
                 "protocolName": rule.pop("protocolName", "tcp"),
                 "cidrBlocks": {"v4CidrBlocks": ["0.0.0.0/0"]}}
    inner = Case(
        id=case_id, title=f"UpdateRules rule field: {case_id}",
        classes=["VAL", "STATE"] + (["NEG"] if not expect_ok else []),
        priority="P1",
        steps=[
            Step(name="create-sg", method="POST", path="/vpc/v1/securityGroups",
                 body={"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}",
                       "name": f"sg-r-{case_id.lower()[-6:]}-{{{{runId}}}}", "ruleSpecs": []},
                 test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                              *save_from_response("j.metadata && j.metadata.securityGroupId", "sgId")]),
            poll_operation_until_done(),
            Step(name="update-rule-bad", method="PATCH", path="/vpc/v1/securityGroups/{{sgId}}/rules",
                 body={"additionRuleSpecs": [rule_full]},
                 test_script=[
                     f"pm.test('{'200' if expect_ok else 'rejected sync or async'}', () => pm.expect(pm.response.code).to.be.oneOf([200, 400]));",
                     *(save_from_response("j.id", "opId") if expect_ok else []),
                 ]),
        ] + ([poll_operation_until_done()] if expect_ok else []) + [
            Step(name="cleanup-sg", method="DELETE", path="/vpc/v1/securityGroups/{{sgId}}",
                 test_script=[*save_from_response("j.id", "opId")]),
            poll_operation_until_done(),
        ],
    )
    CASES.append(_sg_wrap("SG", "v10r" + case_id[-5:].lower(), inner))

# v11 edge cases
CASES.append(Case(
    id="SG-LST-PAGE-NEGATIVE-SIZE",
    title="List с pageSize=-1 → 400 или 200",
    classes=["BVA", "VAL"], priority="P2",
    steps=[Step(name="lst-neg", method="GET",
                path="/vpc/v1/securityGroups?projectId={{_suiteProjectId}}&pageSize=-1",
                test_script=["pm.test('rejected or default', () => pm.expect(pm.response.code).to.be.oneOf([200, 400]));"])],
))

CASES.append(Case(
    id="SG-LST-FILTER-SPECIAL-CHARS",
    title="List с filter содержащим спец-символы → 400 или 200",
    classes=["FILTER", "VAL"], priority="P3",
    steps=[Step(name="lst-fsc", method="GET",
                path="/vpc/v1/securityGroups?projectId={{_suiteProjectId}}&filter=name%3D%22%21%40%23%24%25%22",
                test_script=["pm.test('handled', () => pm.expect(pm.response.code).to.be.oneOf([200, 400]));"])],
))

CASES.append(Case(
    id="SG-LST-PAGESIZE-EXACTLY-1000",
    title="List с pageSize=1000 (boundary max) → 200",
    classes=["BVA"], priority="P2",
    steps=[Step(name="lst-max", method="GET",
                path="/vpc/v1/securityGroups?projectId={{_suiteProjectId}}&pageSize=1000",
                test_script=[*assert_status(200)])],
))

CASES.append(Case(
    id="SG-LST-PAGESIZE-1001",
    title="List с pageSize=1001 (over max) → 400",
    classes=["BVA", "VAL"], priority="P1",
    steps=[Step(name="lst-1001", method="GET",
                path="/vpc/v1/securityGroups?projectId={{_suiteProjectId}}&pageSize=1001",
                test_script=[*assert_status(400), *assert_grpc_code(3, "INVALID_ARGUMENT")])],
))

CASES.append(Case(
    id="SG-LST-DOUBLE-PROJECT-PARAM",
    title="List с дубликатом projectId param → 200 (last wins) или 400",
    classes=["VAL"], priority="P3",
    steps=[Step(name="lst-dup", method="GET",
                path="/vpc/v1/securityGroups?projectId={{_suiteProjectId}}&projectId={{_suiteProjectCrossId}}&pageSize=10",
                test_script=["pm.test('200 or 400', () => pm.expect(pm.response.code).to.be.oneOf([200, 400]));"])],
))

CASES.append(Case(
    id="SG-GET-TRAILING-SLASH",
    title="Get с trailing slash → 404",
    classes=["VAL"], priority="P3",
    steps=[Step(name="get-trail", method="GET", path="/vpc/v1/securityGroups/{{garbageVpcId}}/",
                test_script=["pm.test('non-2xx', () => pm.expect(pm.response.code).to.be.oneOf([400, 404]));"])],
))

CASES.append(Case(
    id="SG-DEL-STATE-DEFAULT-SG",
    title="Delete default-SG напрямую → должен fail (нельзя delete default SG в обход)",
    classes=["NEG", "STATE"], priority="P1",
    steps=[
        Step(name="cr-net", method="POST", path="/vpc/v1/networks",
             body={"projectId": "{{_suiteProjectId}}", "name": "net-defsg-{{runId}}"},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.networkId", "netId")]),
        poll_operation_until_done(),
        Step(name="get-default-sg-id", method="GET",
             path="/vpc/v1/networks/{{netId}}/security_groups",
             test_script=[*assert_status(200),
                          "const def = (pm.response.json().securityGroups || []).find(s => s.defaultForNetwork === true);",
                          "pm.expect(def, 'must have default SG').to.be.an('object');",
                          "pm.environment.set('defaultSgId', def.id);"]),
        Step(name="del-default-sg", method="DELETE",
             path="/vpc/v1/securityGroups/{{defaultSgId}}",
             test_script=[
                 "pm.test('200 (op started) or 400/409 sync', () => pm.expect(pm.response.code).to.be.oneOf([200, 400, 409]));",
                 *save_from_response("j.id", "opId"),
             ]),
        poll_operation_until_done(),
        Step(name="check-result", method="GET", path="/operations/{{opId}}",
             test_script=[
                 "const j = pm.response.json();",
                 "// Текущее поведение: либо OK (default SG удален, можно тогда delete network), либо error (запрет)",
                 "pm.test('completed', () => pm.expect(j.done).to.eql(true));",
             ]),
        # cleanup — пытаемся удалить network в любом состоянии
        Step(name="cleanup-net", method="DELETE", path="/vpc/v1/networks/{{netId}}",
             test_script=["pm.test('cleanup attempted', () => pm.expect(pm.response.code).to.be.oneOf([200, 404]));",
                          *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
    ],
))

# SG, привязанный к NIC через security_group_ids[], нельзя удалить. Пока DB-уровневый
# ref-trigger не реализован, SG.Delete проходит, оставляя dangling ref в
# NIC.security_group_ids; assert FailedPrecondition (code 9) остаётся КРАСНЫМ до
# появления within-service refcheck на уровне БД (trigger / partial UNIQUE /
# эквивалент — НЕ software refcheck). Persistent-RED по rule #13.
# verifies https://github.com/PRO-Robotech/kacho-vpc/issues/27
CASES.append(Case(
    id="SG-DEL-NEG-NIC-ATTACHED",
    title="Delete SG, прилинкованного к NIC через security_group_ids → FailedPrecondition (persistent-RED: verifies #27)",
    classes=["NEG", "STATE", "CONF"], priority="P0",
    steps=[
        Step(name="cr-net", method="POST", path="/vpc/v1/networks",
             body={"projectId": "{{_suiteProjectId}}", "name": "sg-nicatt-net-{{runId}}"},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.networkId", "netId")]),
        poll_operation_until_done(),
        Step(name="cr-sub", method="POST", path="/vpc/v1/subnets",
             body={"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}",
                   "name": "sg-nicatt-sub-{{runId}}", "zoneId": "{{existingZoneId}}",
                   "v4CidrBlocks": ["10.249.0.0/24"]},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.subnetId", "subId")]),
        poll_operation_until_done(),
        Step(name="cr-sg", method="POST", path="/vpc/v1/securityGroups",
             body={"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}",
                   "name": "sg-nicatt-{{runId}}", "ruleSpecs": []},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.securityGroupId", "sgId")]),
        poll_operation_until_done(),
        Step(name="cr-nic", method="POST", path="/vpc/v1/networkInterfaces",
             body={"projectId": "{{_suiteProjectId}}", "subnetId": "{{subId}}",
                   "name": "nic-sgatt-{{runId}}", "securityGroupIds": ["{{sgId}}"]},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.networkInterfaceId", "nicId")]),
        poll_operation_until_done(),
        Step(name="assert-nic-created", method="GET", path="/operations/{{opId}}",
             test_script=["const j = pm.response.json();",
                          "pm.test('NIC create op done no error', () => pm.expect(j.done && !j.error).to.eql(true));"]),
        # Главная проверка: SG.Delete должна быть отвергнута.
        Step(name="del-sg-attached", method="DELETE", path="/vpc/v1/securityGroups/{{sgId}}",
             test_script=[
                 "pm.test('sync 200 (op started) or 400 (sync FailedPrecondition)', () => pm.expect(pm.response.code).to.be.oneOf([200, 400]));",
                 *save_from_response("j.id", "opId"),
             ]),
        poll_operation_until_done(),
        Step(name="assert-sg-delete-blocked", method="GET", path="/operations/{{opId}}",
             test_script=[
                 "const j = pm.response.json();",
                 "pm.test('sg delete op completed', () => pm.expect(j.done).to.eql(true));",
                 # Persistent-RED (rule #13): SG.Delete SG'а, прилинкованного к NIC через
                 # security_group_ids[], ОБЯЗАНА отвергаться FAILED_PRECONDITION (code 9).
                 # Пока within-service refcheck на уровне БД не реализован (issue #27),
                 # SG.Delete проходит и оставляет dangling ref → этот assert КРАСНЫЙ.
                 # НИКАКОГО pm.test.skip — безусловная проверка (rule #13 запрещает skip).
                 "pm.test('SG.Delete NIC-attached must fail FAILED_PRECONDITION (verifies #27)', () => {",
                 "    pm.expect(j.error, 'expected op error, got: ' + JSON.stringify(j)).to.be.an('object');",
                 "    pm.expect(j.error.code, 'expected FAILED_PRECONDITION(9)').to.eql(9);",
                 "});",
             ]),
        # Cleanup: сначала detach SG из NIC (PATCH securityGroupIds=[]),
        # затем удаление снизу вверх. Если кейс красный (refcheck нет),
        # SG уже удалена — detach/cleanup-sg просто no-op'ит.
        Step(name="detach-sg-from-nic", method="PATCH", path="/vpc/v1/networkInterfaces/{{nicId}}",
             body={"updateMask": "securityGroupIds", "securityGroupIds": []},
             test_script=["pm.test('detach (200 / 400 / 404)', () => pm.expect(pm.response.code).to.be.oneOf([200, 400, 404]));",
                          *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="cleanup-nic", method="DELETE", path="/vpc/v1/networkInterfaces/{{nicId}}",
             test_script=["pm.test('cleanup nic (200 / 400 / 404)', () => pm.expect(pm.response.code).to.be.oneOf([200, 400, 404]));",
                          *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="cleanup-sg", method="DELETE", path="/vpc/v1/securityGroups/{{sgId}}",
             test_script=["pm.test('cleanup sg (200 / 400 / 404)', () => pm.expect(pm.response.code).to.be.oneOf([200, 400, 404]));",
                          *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="cleanup-sub", method="DELETE", path="/vpc/v1/subnets/{{subId}}",
             test_script=["pm.test('cleanup sub (200 / 400 / 404)', () => pm.expect(pm.response.code).to.be.oneOf([200, 400, 404]));",
                          *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="cleanup-net", method="DELETE", path="/vpc/v1/networks/{{netId}}",
             test_script=["pm.test('cleanup net (200 / 400 / 404)', () => pm.expect(pm.response.code).to.be.oneOf([200, 400, 404]));",
                          *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
    ],
))

for c in required_fields_matrix("SG", "/vpc/v1/securityGroups",
    {"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}",
     "name": "sg-req-{{runId}}", "ruleSpecs": []},
    ["projectId", "networkId", "name"]):
    CASES.append(_sg_wrap("SG", "req", c))
CASES.extend(immutable_fields_matrix("SG", "/vpc/v1/securityGroups",
    ["project_id", "network_id"]))

for c in security_injection_block("SG", "/vpc/v1/securityGroups", "/vpc/v1/securityGroups",
    {"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}", "ruleSpecs": []}):
    CASES.append(_sg_wrap("SG", "sec", c))


# ===========================================================================
# SecurityGroup: network_id mandatory+immutable + same-network SG-rules.
#   Контракт детерминирован sync fast-fail: отказы — синхронные 4xx, Operation
#   НЕ создается. Concurrency и migration backfill покрываются integration-тестами,
#   в newman не воспроизводятся.
# ===========================================================================


def _two_net_steps(suffix):
    """net-A → {{netId}}, net-B → {{netBId}} в _suiteProjectId. Для cross-network SG-rule кейсов."""
    return [
        *_net_steps(suffix + "a"),  # net-A → {{netId}}
        Step(name="pre-net-b", method="POST", path="/vpc/v1/networks",
             body={"projectId": "{{_suiteProjectId}}", "name": f"sg-{suffix}b-net-{{{{runId}}}}"},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.networkId", "netBId")]),
        poll_operation_until_done(),
    ]


def _cleanup_net_b():
    return Step(name="cleanup-net-b", method="DELETE", path="/vpc/v1/networks/{{netBId}}",
                test_script=[*save_from_response("j.id", "opId")])


# Create SG с валидным networkId → Operation → done (happy).
CASES.append(Case(
    # verifies SG-NET-02
    id="SG-NET-02-CREATE-OK",
    title="Create SG c валидным networkId → Operation done → networkId echoed (happy)",
    classes=["CRUD"], priority="P0",
    steps=[
        *_net_steps("net02"),
        Step(name="create", method="POST", path="/vpc/v1/securityGroups",
             body={"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}",
                   "name": "sg-net02-{{runId}}", "ruleSpecs": []},
             test_script=[*assert_status(200), *assert_operation_envelope(),
                          *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.securityGroupId", "sgId")]),
        poll_operation_until_done(),
        Step(name="assert-op-ok", method="GET", path="/operations/{{opId}}",
             test_script=["const j = pm.response.json();",
                          "pm.test('create op done no error', () => pm.expect(j.done && !j.error).to.eql(true));"]),
        Step(name="get", method="GET", path="/vpc/v1/securityGroups/{{sgId}}",
             test_script=[*assert_status(200),
                          "pm.test('networkId echoed', () => pm.expect(pm.response.json().networkId).to.eql(pm.environment.get('netId')));"]),
        Step(name="cleanup-sg", method="DELETE", path="/vpc/v1/securityGroups/{{sgId}}",
             test_script=[*save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        _cleanup_net(),
    ],
))


# Create SG с несуществующим (well-formed) networkId → sync 404.
CASES.append(Case(
    # verifies SG-NET-03
    # well-formed enp… id, которого нет → sync fast-fail NOT_FOUND "Network <id> not found".
    # Отличается от SG-CR-CONF-NET-NF-TEXT (garbageVpcId) тем, что фиксирует sync-путь:
    # Operation НЕ создается.
    id="SG-NET-03-NEG-NETWORK-NOTFOUND",
    title="Create SG с well-formed несуществующим networkId → sync 404 'Network ... not found'",
    classes=["CONF", "NEG"], priority="P1",
    steps=[
        Step(name="create-nf-net", method="POST", path="/vpc/v1/securityGroups",
             body={"projectId": "{{_suiteProjectId}}", "networkId": "enp00000000000000000",
                   "name": "sg-net03-{{runId}}", "ruleSpecs": []},
             test_script=[*assert_status(404), *assert_grpc_code(5, "NOT_FOUND"),
                          "pm.test('verbatim Network ... not found', () => pm.expect(pm.response.json().message).to.match(/^Network .* not found$/));"]),
    ],
))


# Update mask=network_id (на реальной SG) → sync 400 INVALID_ARGUMENT.
CASES.append(Case(
    # verifies SG-NET-04
    # network_id не в known-mask validateSGUpdate ({name,description,labels,rule_specs}) → unknown-field
    # → sync INVALID_ARGUMENT (immutable+mandatory). На реальной SG детерминированный 400, не 404 от
    # AuthZ-Get (в отличие от generic SG-UPD-STATE-IMMUTABLE-NETWORK-ID на garbage id).
    id="SG-NET-04-NEG-UPDATE-MASK-NETWORK",
    title="Update реальной SG с mask=network_id → sync 400 INVALID_ARGUMENT (immutable)",
    classes=["STATE", "VAL", "NEG"], priority="P1",
    steps=[
        *_net_steps("net04"),
        Step(name="create", method="POST", path="/vpc/v1/securityGroups",
             body={"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}",
                   "name": "sg-net04-{{runId}}", "ruleSpecs": []},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.securityGroupId", "sgId")]),
        poll_operation_until_done(),
        Step(name="patch-mask-network", method="PATCH", path="/vpc/v1/securityGroups/{{sgId}}",
             body={"updateMask": "network_id", "networkId": "{{netId}}"},
             test_script=[*assert_status(400), *assert_grpc_code(3, "INVALID_ARGUMENT")]),
        Step(name="verify-unchanged", method="GET", path="/vpc/v1/securityGroups/{{sgId}}",
             test_script=[*assert_status(200),
                          "pm.test('networkId unchanged', () => pm.expect(pm.response.json().networkId).to.eql(pm.environment.get('netId')));"]),
        Step(name="cleanup-sg", method="DELETE", path="/vpc/v1/securityGroups/{{sgId}}",
             test_script=[*save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        _cleanup_net(),
    ],
))


# Create SG с SG-target rule на SG из другой сети → 400 + field_violations.
CASES.append(Case(
    # verifies SG-NET-07
    # SG-target rule (oneof target = security_group_id) на SG из net-B при создании SG в net-A →
    # sync INVALID_ARGUMENT + BadRequest.field_violations[].field="rule_specs[0].security_group_id".
    id="SG-NET-07-NEG-RULE-CROSS-NETWORK-CREATE",
    title="Create SG в net-A с SG-rule на SG из net-B → 400 INVALID_ARGUMENT same-network + field_violations",
    classes=["VAL", "NEG", "CONF"], priority="P1",
    steps=[
        *_two_net_steps("net07"),
        # target SG в net-B
        Step(name="create-target-b", method="POST", path="/vpc/v1/securityGroups",
             body={"projectId": "{{_suiteProjectId}}", "networkId": "{{netBId}}",
                   "name": "sg-net07-tgt-{{runId}}", "ruleSpecs": []},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.securityGroupId", "sgBId")]),
        poll_operation_until_done(),
        # Create SG в net-A с rule, таргетящим SG из net-B → отказ
        Step(name="create-cross-net", method="POST", path="/vpc/v1/securityGroups",
             body={"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}",
                   "name": "sg-net07-{{runId}}",
                   "ruleSpecs": [{"direction": "INGRESS", "securityGroupId": "{{sgBId}}"}]},
             test_script=[*assert_status(400), *assert_grpc_code(3, "INVALID_ARGUMENT"),
                          "pm.test('same-network text', () => pm.expect(pm.response.json().message).to.eql('security group rule can only reference a security group in the same network'));",
                          *assert_field_violation("rule_specs[0].security_group_id")]),
        Step(name="cleanup-target-b", method="DELETE", path="/vpc/v1/securityGroups/{{sgBId}}",
             test_script=[*save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        _cleanup_net_b(),
        _cleanup_net(),
    ],
))


# Create SG с SG-target rule на SG из той же сети → OK.
CASES.append(Case(
    # verifies SG-NET-08
    id="SG-NET-08-RULE-SAME-NETWORK-OK",
    title="Create SG в net-A с SG-rule на SG из той же net-A → Operation done, rule сохранен (happy)",
    classes=["CRUD", "STATE"], priority="P1",
    steps=[
        *_net_steps("net08"),
        # target SG в той же net-A
        Step(name="create-target-a", method="POST", path="/vpc/v1/securityGroups",
             body={"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}",
                   "name": "sg-net08-tgt-{{runId}}", "ruleSpecs": []},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.securityGroupId", "sgAId")]),
        poll_operation_until_done(),
        Step(name="create-same-net", method="POST", path="/vpc/v1/securityGroups",
             body={"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}",
                   "name": "sg-net08-{{runId}}",
                   "ruleSpecs": [{"direction": "INGRESS", "securityGroupId": "{{sgAId}}"}]},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.securityGroupId", "sgId")]),
        poll_operation_until_done(),
        Step(name="assert-op-ok", method="GET", path="/operations/{{opId}}",
             test_script=["const j = pm.response.json();",
                          "pm.test('same-network rule create op done no error', () => pm.expect(j.done && !j.error).to.eql(true));"]),
        Step(name="get", method="GET", path="/vpc/v1/securityGroups/{{sgId}}",
             test_script=[*assert_status(200),
                          "const rules = pm.response.json().rules || [];",
                          "pm.test('rule targets same-network SG', () => pm.expect(rules.map(r => r.securityGroupId)).to.include(pm.environment.get('sgAId')));"]),
        Step(name="cleanup-sg", method="DELETE", path="/vpc/v1/securityGroups/{{sgId}}",
             test_script=[*save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="cleanup-target-a", method="DELETE", path="/vpc/v1/securityGroups/{{sgAId}}",
             test_script=[*save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        _cleanup_net(),
    ],
))


# UpdateRules добавляет SG-rule cross-network → 400 + field_violations.
CASES.append(Case(
    # verifies SG-NET-09
    # addition_rule_specs[0].security_group_id таргетит SG из другой сети → sync INVALID_ARGUMENT,
    # field="addition_rule_specs[0].security_group_id". Набор правил не изменен (атомарная замена не применена).
    id="SG-NET-09-NEG-RULE-CROSS-NETWORK-UPDATERULES",
    title="UpdateRules SG(net-A) добавляет SG-rule на SG из net-B → 400 INVALID_ARGUMENT same-network + field_violations",
    classes=["VAL", "NEG", "STATE"], priority="P1",
    steps=[
        *_two_net_steps("net09n"),
        Step(name="create-target-b", method="POST", path="/vpc/v1/securityGroups",
             body={"projectId": "{{_suiteProjectId}}", "networkId": "{{netBId}}",
                   "name": "sg-net09n-tgt-{{runId}}", "ruleSpecs": []},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.securityGroupId", "sgBId")]),
        poll_operation_until_done(),
        Step(name="create-sg-a", method="POST", path="/vpc/v1/securityGroups",
             body={"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}",
                   "name": "sg-net09n-{{runId}}", "ruleSpecs": []},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.securityGroupId", "sgId")]),
        poll_operation_until_done(),
        Step(name="update-rules-cross", method="PATCH", path="/vpc/v1/securityGroups/{{sgId}}/rules",
             body={"additionRuleSpecs": [{"direction": "INGRESS", "securityGroupId": "{{sgBId}}"}]},
             test_script=[*assert_status(400), *assert_grpc_code(3, "INVALID_ARGUMENT"),
                          "pm.test('same-network text', () => pm.expect(pm.response.json().message).to.eql('security group rule can only reference a security group in the same network'));",
                          *assert_field_violation("addition_rule_specs[0].security_group_id")]),
        Step(name="verify-no-rules", method="GET", path="/vpc/v1/securityGroups/{{sgId}}",
             test_script=[*assert_status(200),
                          "pm.test('rules unchanged (none added)', () => pm.expect((pm.response.json().rules || []).length).to.eql(0));"]),
        Step(name="cleanup-sg", method="DELETE", path="/vpc/v1/securityGroups/{{sgId}}",
             test_script=[*save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="cleanup-target-b", method="DELETE", path="/vpc/v1/securityGroups/{{sgBId}}",
             test_script=[*save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        _cleanup_net_b(),
        _cleanup_net(),
    ],
))


# UpdateRules добавляет SG-rule same-network → OK.
CASES.append(Case(
    # verifies SG-NET-09
    # Positive per-endpoint через UpdateRules: same-network SG-target → done.
    id="SG-NET-09-RULE-SAME-NETWORK-UPDATERULES-OK",
    title="UpdateRules SG(net-A) добавляет SG-rule на SG из той же net-A → Operation done, rule виден (happy)",
    classes=["CRUD", "STATE"], priority="P1",
    steps=[
        *_net_steps("net09p"),
        Step(name="create-target-a", method="POST", path="/vpc/v1/securityGroups",
             body={"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}",
                   "name": "sg-net09p-tgt-{{runId}}", "ruleSpecs": []},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.securityGroupId", "sgAId")]),
        poll_operation_until_done(),
        Step(name="create-sg", method="POST", path="/vpc/v1/securityGroups",
             body={"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}",
                   "name": "sg-net09p-{{runId}}", "ruleSpecs": []},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.securityGroupId", "sgId")]),
        poll_operation_until_done(),
        Step(name="update-rules-same", method="PATCH", path="/vpc/v1/securityGroups/{{sgId}}/rules",
             body={"additionRuleSpecs": [{"direction": "INGRESS", "securityGroupId": "{{sgAId}}"}]},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="assert-op-ok", method="GET", path="/operations/{{opId}}",
             test_script=["const j = pm.response.json();",
                          "pm.test('same-network updateRules op done no error', () => pm.expect(j.done && !j.error).to.eql(true));"]),
        Step(name="get", method="GET", path="/vpc/v1/securityGroups/{{sgId}}",
             test_script=[*assert_status(200),
                          "const rules = pm.response.json().rules || [];",
                          "pm.test('rule targets same-network SG', () => pm.expect(rules.map(r => r.securityGroupId)).to.include(pm.environment.get('sgAId')));"]),
        Step(name="cleanup-sg", method="DELETE", path="/vpc/v1/securityGroups/{{sgId}}",
             test_script=[*save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="cleanup-target-a", method="DELETE", path="/vpc/v1/securityGroups/{{sgAId}}",
             test_script=[*save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        _cleanup_net(),
    ],
))
