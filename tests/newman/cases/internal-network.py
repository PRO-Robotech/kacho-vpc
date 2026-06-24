# Copyright (c) PRO-Robotech
# SPDX-License-Identifier: BUSL-1.1

"""Case-set для InternalNetworkService.GetNetwork.

internal/admin-only RPC, проброшенный через api-gateway cluster-internal mux на
GET /vpc/v1/networks/{id}:internal. Возвращает GetInternalNetworkResponse
{ network, vrfId } — инфра-чувствительный vrf_id (SRv6 VRF id). Проверки:
vrf_id присутствует ТОЛЬКО на internal-пути, НЕ на public GET/List.

⚠️ REST gateway body — camelCase JSON.

Helpers инжектятся gen.py: Step, Case, assert_status, assert_grpc_code,
save_from_response, assert_operation_envelope, poll_operation_until_done.
"""

CASES = []

NETWORKS = "/vpc/v1/networks"


# ---------------------------------------------------------------------------
# internal GetNetwork отдает vrfId; public Get его НЕ содержит
# ---------------------------------------------------------------------------
CASES.append(Case(
    id="CIL0-NET-INTERNAL-VRFID-OK",
    title="Internal GetNetwork → vrfId>=1; public Get НЕ содержит vrfId",
    classes=["CRUD", "CONF"], priority="P0",
    steps=[
        Step(name="create", method="POST", path=NETWORKS,
             body={"projectId": "{{_suiteProjectId}}", "name": "net-cil0-{{runId}}"},
             test_script=[*assert_status(200), *assert_operation_envelope(),
                          *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.networkId", "createdNetworkId")]),
        poll_operation_until_done(),
        Step(name="get-internal", method="GET", path=NETWORKS + "/{{createdNetworkId}}:internal", auth="jwtBootstrap",
             test_script=[*assert_status(200),
                          "const j = pm.response.json();",
                          "pm.test('network.id matches', () => pm.expect(j.network.id).to.eql(pm.environment.get('createdNetworkId')));",
                          "pm.test('vrfId is a number', () => pm.expect(j.vrfId).to.be.a('number'));",
                          "pm.test('vrfId allocated (>=1, 0 reserved)', () => pm.expect(j.vrfId).to.be.at.least(1));",
                          "pm.test('vrfId not leaked into nested network', () => pm.expect(j.network).to.not.have.property('vrfId'));"]),
        Step(name="get-public-no-vrfid", method="GET", path=NETWORKS + "/{{createdNetworkId}}",
             test_script=[*assert_status(200),
                          "const j = pm.response.json();",
                          "pm.test('public Get has no vrfId', () => pm.expect(j).to.not.have.property('vrfId'));",
                          "pm.test('public Get has no vrf_id', () => pm.expect(j).to.not.have.property('vrf_id'));"]),
        Step(name="cleanup-delete", method="DELETE", path=NETWORKS + "/{{createdNetworkId}}", auth="jwtAccountAdminA",
             test_script=[*assert_status(200)]),
    ],
))


# ---------------------------------------------------------------------------
# public List НЕ содержит vrfId ни в одном элементе
# ---------------------------------------------------------------------------
CASES.append(Case(
    id="CIL0-NET-LIST-NO-VRFID",
    title="public List networks — ни один элемент не содержит vrfId",
    classes=["CRUD", "CONF"], priority="P1",
    steps=[
        Step(name="create", method="POST", path=NETWORKS,
             body={"projectId": "{{_suiteProjectId}}", "name": "net-cil0l-{{runId}}"},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.networkId", "createdNetworkId")]),
        poll_operation_until_done(),
        Step(name="list-no-vrfid", method="GET", path=NETWORKS + "?projectId={{_suiteProjectId}}",
             test_script=[*assert_status(200),
                          "const nets = pm.response.json().networks || [];",
                          "pm.test('list non-empty', () => pm.expect(nets.length).to.be.at.least(1));",
                          "pm.test('no element has vrfId', () => nets.forEach(n => pm.expect(n).to.not.have.property('vrfId')));"]),
        Step(name="cleanup-delete", method="DELETE", path=NETWORKS + "/{{createdNetworkId}}", auth="jwtAccountAdminA",
             test_script=[*assert_status(200)]),
    ],
))


# ---------------------------------------------------------------------------
# internal GetNetwork для несуществующего well-formed id → NotFound
# ---------------------------------------------------------------------------
CASES.append(Case(
    id="CIL0-NET-INTERNAL-NOTFOUND",
    title="Internal GetNetwork well-formed-но-нет → NotFound",
    classes=["VAL"], priority="P1",
    steps=[
        Step(name="get-internal-nx", method="GET", auth="jwtBootstrap",
             path=NETWORKS + "/netaaaaaaaaaaaaaaaaa:internal",
             test_script=[*assert_status(404), *assert_grpc_code(5, "NOT_FOUND")]),
    ],
))


# ---------------------------------------------------------------------------
# internal GetNetwork с malformed id → InvalidArgument
# ---------------------------------------------------------------------------
CASES.append(Case(
    id="CIL0-NET-INTERNAL-MALFORMED",
    title="Internal GetNetwork malformed id → InvalidArgument 'invalid network id'",
    classes=["VAL"], priority="P1",
    steps=[
        Step(name="get-internal-garbage", method="GET", auth="jwtBootstrap",
             path=NETWORKS + "/garbage:internal",
             test_script=[*assert_status(400), *assert_grpc_code(3, "INVALID_ARGUMENT"),
                          "pm.test('msg mentions invalid network id', () => pm.expect(pm.response.json().message).to.match(/invalid network id/));"]),
    ],
))


# ---------------------------------------------------------------------------
# vrfId стабилен при Update (rename)
# ---------------------------------------------------------------------------
CASES.append(Case(
    id="CIL0-NET-VRFID-STABLE-UPDATE",
    title="vrfId неизменен после Update (rename)",
    classes=["CRUD", "CONF"], priority="P1",
    steps=[
        Step(name="create", method="POST", path=NETWORKS,
             body={"projectId": "{{_suiteProjectId}}", "name": "net-cil0s-{{runId}}"},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.networkId", "createdNetworkId")]),
        poll_operation_until_done(),
        Step(name="get-internal-before", method="GET", path=NETWORKS + "/{{createdNetworkId}}:internal", auth="jwtBootstrap",
             test_script=[*assert_status(200),
                          *save_from_response("j.vrfId", "cil0VrfId")]),
        Step(name="update-name", method="PATCH", path=NETWORKS + "/{{createdNetworkId}}",
             body={"updateMask": "name", "name": "net-cil0s2-{{runId}}"},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="get-internal-after", method="GET", path=NETWORKS + "/{{createdNetworkId}}:internal", auth="jwtBootstrap",
             test_script=[*assert_status(200),
                          "pm.test('vrfId unchanged after update', () => pm.expect(String(pm.response.json().vrfId)).to.eql(pm.environment.get('cil0VrfId')));"]),
        Step(name="cleanup-delete", method="DELETE", path=NETWORKS + "/{{createdNetworkId}}", auth="jwtAccountAdminA",
             test_script=[*assert_status(200)]),
    ],
))


# ---------------------------------------------------------------------------
# updateMask=vrfId → InvalidArgument (vrfId не в known-set Network)
# ---------------------------------------------------------------------------
CASES.append(Case(
    id="CIL0-NET-UPDATE-VRFID-INVALID",
    title="public Update updateMask=vrfId → InvalidArgument (unknown field)",
    classes=["VAL"], priority="P1",
    steps=[
        Step(name="create", method="POST", path=NETWORKS,
             body={"projectId": "{{_suiteProjectId}}", "name": "net-cil0u-{{runId}}"},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.networkId", "createdNetworkId")]),
        poll_operation_until_done(),
        Step(name="update-vrfid-rejected", method="PATCH", path=NETWORKS + "/{{createdNetworkId}}",
             body={"updateMask": "vrfId", "name": "x"},
             test_script=[*assert_status(400), *assert_grpc_code(3, "INVALID_ARGUMENT")]),
        Step(name="cleanup-delete", method="DELETE", path=NETWORKS + "/{{createdNetworkId}}", auth="jwtAccountAdminA",
             test_script=[*assert_status(200)]),
    ],
))
