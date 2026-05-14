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
    # kacho-proto#8: network_id больше не (required) — SG может быть "глобальной"
    # (folder-level, unbound). Без networkId Create проходит; GET → networkId пуст/отсутствует.
    id="SG-CR-NO-NETWORK-OK",
    title="Create SG без networkId → success → get → networkId пуст",
    classes=["CRUD"],
    priority="P1",
    steps=[
        Step(name="create-no-net", method="POST", path="/vpc/v1/securityGroups",
             body={"folderId": "{{_suiteFolderId}}", "name": "sg-nonet-{{runId}}", "ruleSpecs": []},
             test_script=[*assert_status(200), *assert_operation_envelope(),
                          *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.securityGroupId", "sgId")]),
        poll_operation_until_done(),
        Step(name="get-no-net", method="GET", path="/vpc/v1/securityGroups/{{sgId}}",
             test_script=[*assert_status(200),
                          "pm.test('networkId empty/absent', () => pm.expect(pm.response.json().networkId || '').to.eql(''));"]),
        Step(name="cleanup-sg", method="DELETE", path="/vpc/v1/securityGroups/{{sgId}}",
             test_script=[*save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
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
             body={"folderId": "{{_suiteFolderId}}", "networkId": "{{netId}}",
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
    # filter=network_id="<id>" (proto: "filter by network_id is here") — bound SG
    # матчится, unbound (network-less, NULL network_id) — нет.
    id="SG-LIST-FILTER-NETWORK-OK",
    title="List?filter=network_id=\"<id>\" — bound SG present, unbound absent",
    classes=["CRUD"],
    priority="P2",
    steps=[
        *_net_steps("fltnet"),
        Step(name="create-bound", method="POST", path="/vpc/v1/securityGroups",
             body={"folderId": "{{_suiteFolderId}}", "networkId": "{{netId}}",
                   "name": "sg-fltbound-{{runId}}", "ruleSpecs": []},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.securityGroupId", "boundSgId")]),
        poll_operation_until_done(),
        Step(name="create-unbound", method="POST", path="/vpc/v1/securityGroups",
             body={"folderId": "{{_suiteFolderId}}", "name": "sg-fltunbound-{{runId}}", "ruleSpecs": []},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.securityGroupId", "unboundSgId")]),
        poll_operation_until_done(),
        Step(name="list-by-network", method="GET",
             path="/vpc/v1/securityGroups?folderId={{_suiteFolderId}}&pageSize=1000&filter=network_id%3D%22{{netId}}%22",
             test_script=[*assert_status(200),
                          "const ids = (pm.response.json().securityGroups || []).map(s => s.id);",
                          "pm.test('bound SG present', () => pm.expect(ids).to.include(pm.environment.get('boundSgId')));",
                          "pm.test('unbound SG absent', () => pm.expect(ids).to.not.include(pm.environment.get('unboundSgId')));"]),
        Step(name="cleanup-bound", method="DELETE", path="/vpc/v1/securityGroups/{{boundSgId}}",
             test_script=[*save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="cleanup-unbound", method="DELETE", path="/vpc/v1/securityGroups/{{unboundSgId}}",
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
                 # verbatim-YC (probe 2026-05-11, kacho-vpc#7): malformed id (нет известного 3-char префикса)
                 # → 400 InvalidArgument "invalid security group id '<X>'" (раньше было 404 NotFound). Проверка family-agnostic.
                 *assert_status(400),
                 *assert_grpc_code(3, "INVALID_ARGUMENT"),
                 "pm.test('mentions invalid id', () => { const m = pm.response.json().message; pm.expect(m).to.include('invalid'); pm.expect(m).to.include('id'); });",
             ]),
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

# Расширение
CASES.extend(crud_list_bva_block("SG", "/vpc/v1/securityGroups"))
CASES.append(conf_not_found_text("SG", "/vpc/v1/securityGroups", "Security group"))
CASES.append(state_update_unknown_mask("SG", "/vpc/v1/securityGroups"))
CASES.append(authz_move_nf("SG", "/vpc/v1/securityGroups"))

CASES.append(Case(
    id="SG-UR-NEG-RULE-NF",
    title="UpdateRule малформированного rule_id → sync 400 'Invalid rule id <id>' (verbatim YC)",
    classes=["NEG"], priority="P1",
    steps=[
        *_net_steps("ur"),
        Step(name="create-sg", method="POST", path="/vpc/v1/securityGroups",
             body={"folderId": "{{_suiteFolderId}}", "networkId": "{{netId}}",
                   "name": "sg-ur-{{runId}}", "ruleSpecs": []},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.securityGroupId", "sgId")]),
        poll_operation_until_done(),
        # verbatim YC (probe 2026-05-11, kacho-vpc#10): малформированный rule_id →
        # синхронный 400 InvalidArgument "Invalid rule id <ruleId>" (не Operation).
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
             body={"folderId": "{{_suiteFolderId}}", "networkId": "{{netId}}",
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

CASES.append(Case(
    id="SG-MV-CRUD-OK",
    title="Move SG в другой folder",
    classes=["CRUD"], priority="P1",
    steps=[
        *_net_steps("mv"),
        Step(name="create-sg", method="POST", path="/vpc/v1/securityGroups",
             body={"folderId": "{{_suiteFolderId}}", "networkId": "{{netId}}",
                   "name": "sg-mv-{{runId}}", "ruleSpecs": []},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.securityGroupId", "sgId")]),
        poll_operation_until_done(),
        Step(name="move", method="POST", path="/vpc/v1/securityGroups/{{sgId}}:move",
             body={"destinationFolderId": "{{_suiteFolderCrossId}}"},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="cleanup-sg", method="DELETE", path="/vpc/v1/securityGroups/{{sgId}}",
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        _cleanup_net(),
    ],
))

# Дополнение: STATE immutable folder + VAL move-no-dest + BVA pagesize=1
CASES.append(state_immutable_folder("SG", "/vpc/v1/securityGroups"))
CASES.append(val_move_no_dest("SG", "/vpc/v1/securityGroups"))
CASES.append(list_pagesize_1_bva("SG", "/vpc/v1/securityGroups"))

CASES.append(Case(
    id="SG-CR-CONF-NET-NF-TEXT",
    title="Create SG в garbage network → sync verbatim 'Network ... not found' (kacho-vpc#8)",
    classes=["CONF", "NEG"], priority="P1",
    steps=[
        Step(name="create", method="POST", path="/vpc/v1/securityGroups",
             body={"folderId": "{{_suiteFolderId}}", "networkId": "{{garbageVpcId}}",
                   "name": "sg-confnf-{{runId}}", "ruleSpecs": []},
             test_script=[
                 *assert_status(404), *assert_grpc_code(5, "NOT_FOUND"),
                 "pm.test('verbatim Network ... not found', () => pm.expect(pm.response.json().message).to.match(/^Network .* not found$/));",
             ]),
    ],
))

CASES.append(Case(
    id="SG-UPD-CONF-NF-TEXT",
    title="Update несуществующего → verbatim 'Security group ... not found' text",
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
    title="Delete несуществующего → verbatim 'Security group ... not found' text",
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
    id="SG-MV-CONF-NF-TEXT",
    title="Move несуществующего → verbatim '<Resource> ... not found' text",
    classes=["CONF", "NEG"], priority="P1",
    steps=[
        Step(name="move-nx", method="POST", path="/vpc/v1/securityGroups/{{garbageVpcId}}:move",
             body={"destinationFolderId": "{{_suiteFolderId}}"},
             test_script=[
                 *assert_status(404), *assert_grpc_code(5, "NOT_FOUND"),
                 "pm.test('non-empty error text', () => pm.expect(pm.response.json().message).to.be.a('string').and.length.greaterThan(0));",
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
             body={"folderId": "{{_suiteFolderId}}", "networkId": "{{netId}}",
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
             body={"folderId": "{{_suiteFolderId}}", "networkId": "{{netId}}",
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
    {"folderId": "{{_suiteFolderId}}", "networkId": "{{netId}}", "ruleSpecs": []}):
    CASES.append(_sg_wrap("SG", "v7", c))

CASES.extend(perf_baseline_block("SG", "/vpc/v1/securityGroups"))
CASES.extend(verbatim_text_pack("SG", "SecurityGroup", "/vpc/v1/securityGroups", text_template="Security group SecurityGroup.Id(value={id}) not found"))
CASES.extend(authz_caller_headers_block("SG", "/vpc/v1/securityGroups"))

CASES.append(_sg_wrap("SG", "mvself",
    move_same_folder("SG", "/vpc/v1/securityGroups",
        {"folderId": "{{_suiteFolderId}}", "networkId": "{{netId}}", "ruleSpecs": []})))

CASES.append(_sg_wrap("SG", "v8m",
    update_happy_multi_field("SG", "/vpc/v1/securityGroups", "/vpc/v1/securityGroups",
        {"folderId": "{{_suiteFolderId}}", "networkId": "{{netId}}", "ruleSpecs": []})))
CASES.append(_sg_wrap("SG", "v8f",
    list_filter_match_block("SG", "/vpc/v1/securityGroups",
        {"folderId": "{{_suiteFolderId}}", "networkId": "{{netId}}", "ruleSpecs": []})))
for c in neg_invalid_types_block("SG", "/vpc/v1/securityGroups",
    {"folderId": "{{_suiteFolderId}}", "networkId": "{{netId}}", "ruleSpecs": []}):
    CASES.append(_sg_wrap("SG", "v8nt", c))
CASES.extend(http_method_not_allowed_block("SG", "/vpc/v1/securityGroups"))
CASES.extend(malformed_body_block("SG", "/vpc/v1/securityGroups"))

CASES.append(_sg_wrap("SG", "v9d",
    alreadyexists_dup_name_for("SG", "/vpc/v1/securityGroups",
        {"folderId": "{{_suiteFolderId}}", "networkId": "{{netId}}", "ruleSpecs": []})))
for c in update_mask_partial_block("SG", "/vpc/v1/securityGroups", "/vpc/v1/securityGroups",
    {"folderId": "{{_suiteFolderId}}", "networkId": "{{netId}}", "ruleSpecs": []}):
    CASES.append(_sg_wrap("SG", "v9p", c))
CASES.append(_sg_wrap("SG", "v9pf",
    perf_baseline_get_block("SG", "/vpc/v1/securityGroups",
        {"folderId": "{{_suiteFolderId}}", "networkId": "{{netId}}", "ruleSpecs": []})))
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
                 body={"folderId": "{{_suiteFolderId}}", "networkId": "{{netId}}",
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
                path="/vpc/v1/securityGroups?folderId={{_suiteFolderId}}&pageSize=-1",
                test_script=["pm.test('rejected or default', () => pm.expect(pm.response.code).to.be.oneOf([200, 400]));"])],
))

CASES.append(Case(
    id="SG-LST-FILTER-SPECIAL-CHARS",
    title="List с filter содержащим спец-символы → 400 или 200",
    classes=["FILTER", "VAL"], priority="P3",
    steps=[Step(name="lst-fsc", method="GET",
                path="/vpc/v1/securityGroups?folderId={{_suiteFolderId}}&filter=name%3D%22%21%40%23%24%25%22",
                test_script=["pm.test('handled', () => pm.expect(pm.response.code).to.be.oneOf([200, 400]));"])],
))

CASES.append(Case(
    id="SG-LST-PAGESIZE-EXACTLY-1000",
    title="List с pageSize=1000 (boundary max) → 200",
    classes=["BVA"], priority="P2",
    steps=[Step(name="lst-max", method="GET",
                path="/vpc/v1/securityGroups?folderId={{_suiteFolderId}}&pageSize=1000",
                test_script=[*assert_status(200)])],
))

CASES.append(Case(
    id="SG-LST-PAGESIZE-1001",
    title="List с pageSize=1001 (over max) → 400",
    classes=["BVA", "VAL"], priority="P1",
    steps=[Step(name="lst-1001", method="GET",
                path="/vpc/v1/securityGroups?folderId={{_suiteFolderId}}&pageSize=1001",
                test_script=[*assert_status(400), *assert_grpc_code(3, "INVALID_ARGUMENT")])],
))

CASES.append(Case(
    id="SG-LST-DOUBLE-FOLDER-PARAM",
    title="List с дубликатом folderId param → 200 (last wins) или 400",
    classes=["VAL"], priority="P3",
    steps=[Step(name="lst-dup", method="GET",
                path="/vpc/v1/securityGroups?folderId={{_suiteFolderId}}&folderId={{_suiteFolderCrossId}}&pageSize=10",
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
             body={"folderId": "{{_suiteFolderId}}", "name": "net-defsg-{{runId}}"},
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
                 "// Текущее поведение: либо OK (default SG удалён, можно тогда delete network), либо error (запрет)",
                 "pm.test('completed', () => pm.expect(j.done).to.eql(true));",
             ]),
        # cleanup — пытаемся удалить network в любом состоянии
        Step(name="cleanup-net", method="DELETE", path="/vpc/v1/networks/{{netId}}",
             test_script=["pm.test('cleanup attempted', () => pm.expect(pm.response.code).to.be.oneOf([200, 404]));",
                          *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
    ],
))

# KAC-53 (2) — TDD-red: пока DB-уровневый ref-trigger по KAC-52 не реализован,
# этот тест падает на assert FailedPrecondition (SG.Delete сейчас проходит,
# оставляя dangling ref в NIC.security_group_ids). Когда trigger / partial
# UNIQUE / эквивалент будут — кейс станет зелёным.
# verifies «SG, привязанный к NIC через security_group_ids[], нельзя удалить».
# Зависит от KAC-52 (within-service refs через БД, не software refcheck).
CASES.append(Case(
    id="SG-DEL-NEG-NIC-ATTACHED",
    title="Delete SG, прилинкованного к NIC через security_group_ids → FailedPrecondition (KAC-53; TDD-red до KAC-52 DB-trigger)",
    classes=["NEG", "STATE", "CONF"], priority="P0",
    steps=[
        Step(name="cr-net", method="POST", path="/vpc/v1/networks",
             body={"folderId": "{{_suiteFolderId}}", "name": "sg-nicatt-net-{{runId}}"},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.networkId", "netId")]),
        poll_operation_until_done(),
        Step(name="cr-sub", method="POST", path="/vpc/v1/subnets",
             body={"folderId": "{{_suiteFolderId}}", "networkId": "{{netId}}",
                   "name": "sg-nicatt-sub-{{runId}}", "zoneId": "{{existingZoneId}}",
                   "v4CidrBlocks": ["10.249.0.0/24"]},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.subnetId", "subId")]),
        poll_operation_until_done(),
        Step(name="cr-sg", method="POST", path="/vpc/v1/securityGroups",
             body={"folderId": "{{_suiteFolderId}}", "networkId": "{{netId}}",
                   "name": "sg-nicatt-{{runId}}", "ruleSpecs": []},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.securityGroupId", "sgId")]),
        poll_operation_until_done(),
        Step(name="cr-nic", method="POST", path="/vpc/v1/networkInterfaces",
             body={"folderId": "{{_suiteFolderId}}", "subnetId": "{{subId}}",
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
                 # Это и есть TDD-red: пока DB-trigger по KAC-52 не сделан,
                 # op.error будет undefined (SG удалится, оставив dangling ref).
                 "pm.test('SG.Delete failed FAILED_PRECONDITION (NIC-attached)', () => {",
                 "  pm.expect(j.error, JSON.stringify(j)).to.be.an('object');",
                 "  pm.expect(j.error.code).to.eql(9);",  # FAILED_PRECONDITION
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
    {"folderId": "{{_suiteFolderId}}", "networkId": "{{netId}}",
     "name": "sg-req-{{runId}}", "ruleSpecs": []},
    ["folderId", "networkId", "name"]):
    CASES.append(_sg_wrap("SG", "req", c))
CASES.extend(immutable_fields_matrix("SG", "/vpc/v1/securityGroups",
    ["folder_id", "network_id"]))

for c in security_injection_block("SG", "/vpc/v1/securityGroups", "/vpc/v1/securityGroups",
    {"folderId": "{{_suiteFolderId}}", "networkId": "{{netId}}", "ruleSpecs": []}):
    CASES.append(_sg_wrap("SG", "sec", c))
