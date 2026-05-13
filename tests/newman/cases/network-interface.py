"""Case-set для NetworkInterfaceService (kacho-vpc) — first-class AWS-ENI-подобный
ресурс (эпик KAC-2). REST: /vpc/v1/networkInterfaces.

Контракт изоляции — как везде: каждый case внутри своего runId, suite работает
в pre-allocated existingFolderId. Имена ресурсов суффиксуются {{runId}}.

Публичная проекция NIC — lean: id/folderId/subnetId/v4AddressIds/v6AddressIds/
securityGroupIds/usedBy/status/name/labels. Инфра-чувствительные data-plane-поля
(vpnId, hvId, sid, hostIface, netns, gatewayIp, containerId, networkId, instanceId,
index) — НЕ на публичной поверхности (см. workspace CLAUDE.md §«Инфра-чувствительные данные»).
"""

CASES = []


def _net_subnet_steps(suffix, cidr="10.60.0.0/24"):
    """Helper: создаёт parent Network + Subnet, сохраняет netId/subId."""
    return [
        Step(name="pre-net", method="POST", path="/vpc/v1/networks",
             body={"folderId": "{{_suiteFolderId}}", "name": f"nic-{suffix}-net-{{{{runId}}}}"},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.networkId", "netId")]),
        poll_operation_until_done(),
        Step(name="pre-subnet", method="POST", path="/vpc/v1/subnets",
             body={"folderId": "{{_suiteFolderId}}", "networkId": "{{netId}}",
                   "name": f"nic-{suffix}-sub-{{{{runId}}}}", "zoneId": "{{existingZoneId}}",
                   "v4CidrBlocks": [cidr]},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.subnetId", "subId")]),
        poll_operation_until_done(),
    ]


def _cleanup_subnet():
    return Step(name="cleanup-subnet", method="DELETE", path="/vpc/v1/subnets/{{subId}}",
                test_script=["pm.test('cleanup subnet (200 or 400 if child leaked)', () => pm.expect(pm.response.code).to.be.oneOf([200, 400]));",
                             *save_from_response("j.id", "opId")])


def _cleanup_net():
    return Step(name="cleanup-net", method="DELETE", path="/vpc/v1/networks/{{netId}}",
                test_script=["pm.test('cleanup net (200 or 400 if child leaked)', () => pm.expect(pm.response.code).to.be.oneOf([200, 400]));",
                             *save_from_response("j.id", "opId")])


def _cleanup_nic(env="nicId"):
    return [
        Step(name="cleanup-nic", method="DELETE", path=f"/vpc/v1/networkInterfaces/{{{{{env}}}}}",
             test_script=["pm.test('cleanup nic (200 or 400)', () => pm.expect(pm.response.code).to.be.oneOf([200, 400]));",
                          *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
    ]


_LEAN_FORBIDDEN = ["vpnId", "hvId", "sid", "hostIface", "netns", "gatewayIp",
                   "containerId", "networkId", "instanceId", "index"]


def _assert_lean_projection():
    return [
        "const j = pm.response.json();",
        "pm.test('has id/folderId/subnetId/status', () => {",
        "  pm.expect(j.id, 'id').to.be.a('string');",
        "  pm.expect(j.folderId, 'folderId').to.be.a('string');",
        "  pm.expect(j.subnetId, 'subnetId').to.be.a('string');",
        "  pm.expect(j.status, 'status').to.be.a('string');",
        "});",
        f"pm.test('no infra-sensitive fields on public projection', () => {{",
        f"  const forbidden = {_LEAN_FORBIDDEN!r};",
        "  forbidden.forEach(k => pm.expect(j, 'leaked ' + k).to.not.have.property(k));",
        "});",
    ]


# ---------------------------------------------------------------------------
# NIC-CR
# ---------------------------------------------------------------------------

CASES.append(Case(
    id="NIC-CR-CRUD-OK",
    title="Create NIC в свежей network+subnet → Operation → poll → get → lean public projection",
    classes=["CRUD"],
    priority="P1",
    steps=[
        *_net_subnet_steps("cr"),
        Step(name="create-nic", method="POST", path="/vpc/v1/networkInterfaces",
             body={"folderId": "{{_suiteFolderId}}", "subnetId": "{{subId}}",
                   "name": "nic-cr-{{runId}}"},
             test_script=[*assert_status(200), *assert_operation_envelope(),
                          *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.networkInterfaceId", "nicId")]),
        poll_operation_until_done(),
        Step(name="get-nic", method="GET", path="/vpc/v1/networkInterfaces/{{nicId}}",
             test_script=[*assert_status(200), *_assert_lean_projection(),
                          "pm.test('subnetId matches', () => pm.expect(pm.response.json().subnetId).to.eql(pm.environment.get('subId')));"]),
        *_cleanup_nic(),
        _cleanup_subnet(),
        poll_operation_until_done(),
        _cleanup_net(),
        poll_operation_until_done(),
    ],
))

CASES.append(Case(
    id="NIC-CR-NEG-DUP-NAME",
    title="Create двух NIC с одинаковым name в одном folder → второй ALREADY_EXISTS (async op.error)",
    classes=["NEG", "CONF"],
    priority="P1",
    steps=[
        *_net_subnet_steps("dup"),
        Step(name="create-1", method="POST", path="/vpc/v1/networkInterfaces",
             body={"folderId": "{{_suiteFolderId}}", "subnetId": "{{subId}}", "name": "nic-dup-{{runId}}"},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.networkInterfaceId", "nicId")]),
        poll_operation_until_done(),
        Step(name="create-2-dup", method="POST", path="/vpc/v1/networkInterfaces",
             body={"folderId": "{{_suiteFolderId}}", "subnetId": "{{subId}}", "name": "nic-dup-{{runId}}"},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="assert-dup-failed", method="GET", path="/operations/{{opId}}",
             test_script=["const j = pm.response.json();",
                          "pm.test('2nd create failed (already exists)', () => {",
                          "  pm.expect(j.done).to.eql(true);",
                          "  pm.expect(j.error, JSON.stringify(j)).to.be.an('object');",
                          "  pm.expect(j.error.code).to.eql(6);",  # ALREADY_EXISTS
                          "});"]),
        *_cleanup_nic(),
        _cleanup_subnet(),
        poll_operation_until_done(),
        _cleanup_net(),
        poll_operation_until_done(),
    ],
))

CASES.append(Case(
    id="NIC-CR-NEG-BAD-SUBNET",
    title="Create NIC с несуществующим subnetId → NotFound (async op.error code=5)",
    classes=["NEG"],
    priority="P1",
    steps=[
        Step(name="create-bad-subnet", method="POST", path="/vpc/v1/networkInterfaces",
             body={"folderId": "{{_suiteFolderId}}", "subnetId": "{{garbageVpcId}}", "name": "nic-bs-{{runId}}"},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="assert-nf", method="GET", path="/operations/{{opId}}",
             test_script=["const j = pm.response.json();",
                          "pm.test('op failed NotFound', () => {",
                          "  pm.expect(j.done).to.eql(true);",
                          "  pm.expect(j.error, JSON.stringify(j)).to.be.an('object');",
                          "  pm.expect(j.error.code).to.eql(5);",  # NOT_FOUND
                          "});"]),
    ],
))

CASES.append(Case(
    id="NIC-LIST-OK",
    title="List NIC by folderId → 200, массив; созданный NIC присутствует",
    classes=["CRUD"],
    priority="P1",
    steps=[
        *_net_subnet_steps("lst"),
        Step(name="create-nic", method="POST", path="/vpc/v1/networkInterfaces",
             body={"folderId": "{{_suiteFolderId}}", "subnetId": "{{subId}}", "name": "nic-lst-{{runId}}"},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.networkInterfaceId", "nicId")]),
        poll_operation_until_done(),
        Step(name="list", method="GET", path="/vpc/v1/networkInterfaces?folderId={{_suiteFolderId}}&pageSize=1000",
             test_script=[*assert_status(200),
                          "const j = pm.response.json();",
                          "pm.test('networkInterfaces array', () => pm.expect(j.networkInterfaces || []).to.be.an('array'));",
                          "pm.test('created NIC present', () => pm.expect((j.networkInterfaces || []).map(n => n.id)).to.include(pm.environment.get('nicId')));"]),
        Step(name="list-no-folder", method="GET", path="/vpc/v1/networkInterfaces",
             test_script=[*assert_status(400), *assert_grpc_code(3, "INVALID_ARGUMENT")]),
        *_cleanup_nic(),
        _cleanup_subnet(),
        poll_operation_until_done(),
        _cleanup_net(),
        poll_operation_until_done(),
    ],
))

CASES.append(Case(
    id="NIC-UPD-OK",
    title="Update NIC description/labels/securityGroupIds → 200, изменения видны в GET",
    classes=["CRUD", "STATE"],
    priority="P1",
    steps=[
        *_net_subnet_steps("upd"),
        Step(name="get-net-for-sg", method="GET", path="/vpc/v1/networks/{{netId}}",
             test_script=[*assert_status(200), *save_from_response("j.defaultSecurityGroupId", "defSgId")]),
        Step(name="create-nic", method="POST", path="/vpc/v1/networkInterfaces",
             body={"folderId": "{{_suiteFolderId}}", "subnetId": "{{subId}}", "name": "nic-upd-{{runId}}"},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.networkInterfaceId", "nicId")]),
        poll_operation_until_done(),
        Step(name="patch-nic", method="PATCH", path="/vpc/v1/networkInterfaces/{{nicId}}",
             body={"updateMask": "description,labels,securityGroupIds",
                   "description": "nic-upd-desc-{{runId}}",
                   "labels": {"k": "v"},
                   "securityGroupIds": ["{{defSgId}}"]},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="get-after-upd", method="GET", path="/vpc/v1/networkInterfaces/{{nicId}}",
             test_script=[*assert_status(200),
                          "const j = pm.response.json();",
                          "pm.test('description updated', () => pm.expect(j.description).to.eql('nic-upd-desc-' + pm.environment.get('runId')));",
                          "pm.test('labels updated', () => pm.expect(j.labels && j.labels.k).to.eql('v'));",
                          "pm.test('securityGroupIds set', () => pm.expect(j.securityGroupIds || []).to.include(pm.environment.get('defSgId')));"]),
        *_cleanup_nic(),
        _cleanup_subnet(),
        poll_operation_until_done(),
        _cleanup_net(),
        poll_operation_until_done(),
    ],
))

CASES.append(Case(
    id="NIC-DEL-OK",
    title="Delete NIC (не приаттаченный) → Operation → poll done без ошибки → GET 404",
    classes=["CRUD"],
    priority="P1",
    steps=[
        *_net_subnet_steps("del"),
        Step(name="create-nic", method="POST", path="/vpc/v1/networkInterfaces",
             body={"folderId": "{{_suiteFolderId}}", "subnetId": "{{subId}}", "name": "nic-del-{{runId}}"},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.networkInterfaceId", "nicId")]),
        poll_operation_until_done(),
        Step(name="del-nic", method="DELETE", path="/vpc/v1/networkInterfaces/{{nicId}}",
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="assert-deleted", method="GET", path="/operations/{{opId}}",
             test_script=["const j = pm.response.json();",
                          "pm.test('delete op done no error', () => pm.expect(j.done && !j.error).to.eql(true));"]),
        Step(name="get-gone", method="GET", path="/vpc/v1/networkInterfaces/{{nicId}}",
             test_script=[*assert_status(404), *assert_grpc_code(5, "NOT_FOUND")]),
        _cleanup_subnet(),
        poll_operation_until_done(),
        _cleanup_net(),
        poll_operation_until_done(),
    ],
))

CASES.append(Case(
    # Delete NIC, приаттаченного к (фейковому) инстансу, → FailedPrecondition (async op.error code=9).
    id="NIC-DEL-NEG-ATTACHED",
    title="Delete приаттаченного NIC → op.error FAILED_PRECONDITION",
    classes=["NEG", "STATE", "CONF"],
    priority="P1",
    steps=[
        *_net_subnet_steps("delatt"),
        Step(name="create-nic", method="POST", path="/vpc/v1/networkInterfaces",
             body={"folderId": "{{_suiteFolderId}}", "subnetId": "{{subId}}", "name": "nic-delatt-{{runId}}"},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.networkInterfaceId", "nicId")]),
        poll_operation_until_done(),
        Step(name="attach", method="POST", path="/vpc/v1/networkInterfaces/{{nicId}}:attach",
             body={"instanceId": "fake-instance-{{runId}}"},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="del-attached", method="DELETE", path="/vpc/v1/networkInterfaces/{{nicId}}",
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="assert-del-failed", method="GET", path="/operations/{{opId}}",
             test_script=["const j = pm.response.json();",
                          "pm.test('delete op failed FailedPrecondition', () => {",
                          "  pm.expect(j.done).to.eql(true);",
                          "  pm.expect(j.error, JSON.stringify(j)).to.be.an('object');",
                          "  pm.expect(j.error.code).to.eql(9);",  # FAILED_PRECONDITION
                          "});"]),
        # detach + cleanup
        Step(name="detach", method="POST", path="/vpc/v1/networkInterfaces/{{nicId}}:detach",
             body={}, test_script=[*save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        *_cleanup_nic(),
        _cleanup_subnet(),
        poll_operation_until_done(),
        _cleanup_net(),
        poll_operation_until_done(),
    ],
))

CASES.append(Case(
    id="NIC-ATTACH-DETACH-OK",
    title="Attach NIC к инстансу → get → usedBy populated → detach → usedBy cleared",
    classes=["CRUD", "STATE"],
    priority="P1",
    steps=[
        *_net_subnet_steps("attdet"),
        Step(name="create-nic", method="POST", path="/vpc/v1/networkInterfaces",
             body={"folderId": "{{_suiteFolderId}}", "subnetId": "{{subId}}", "name": "nic-attdet-{{runId}}"},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.networkInterfaceId", "nicId")]),
        poll_operation_until_done(),
        Step(name="attach", method="POST", path="/vpc/v1/networkInterfaces/{{nicId}}:attach",
             body={"instanceId": "fake-inst-{{runId}}", "index": "0"},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="get-attached", method="GET", path="/vpc/v1/networkInterfaces/{{nicId}}",
             test_script=[*assert_status(200),
                          "const j = pm.response.json();",
                          "pm.test('usedBy populated', () => {",
                          "  pm.expect(j.usedBy, JSON.stringify(j)).to.be.an('object');",
                          "  pm.expect(j.usedBy.referrer && j.usedBy.referrer.id).to.eql('fake-inst-' + pm.environment.get('runId'));",
                          "});"]),
        Step(name="detach", method="POST", path="/vpc/v1/networkInterfaces/{{nicId}}:detach",
             body={}, test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="get-detached", method="GET", path="/vpc/v1/networkInterfaces/{{nicId}}",
             test_script=[*assert_status(200),
                          "const j = pm.response.json();",
                          "pm.test('usedBy cleared', () => {",
                          "  const r = j.usedBy && j.usedBy.referrer;",
                          "  pm.expect(!r || !r.id).to.eql(true);",
                          "});"]),
        *_cleanup_nic(),
        _cleanup_subnet(),
        poll_operation_until_done(),
        _cleanup_net(),
        poll_operation_until_done(),
    ],
))

CASES.append(Case(
    id="NIC-CR-WITH-ADDR-OK",
    title="Create internal_ipv4 Address в subnet → create NIC с этим address id в v4AddressIds → get → echoed",
    classes=["CRUD"],
    priority="P1",
    steps=[
        *_net_subnet_steps("waddr"),
        Step(name="create-addr", method="POST", path="/vpc/v1/addresses",
             body={"folderId": "{{_suiteFolderId}}", "name": "nic-waddr-addr-{{runId}}",
                   "internalIpv4AddressSpec": {"subnetId": "{{subId}}"}},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.addressId", "addrId")]),
        poll_operation_until_done(),
        Step(name="create-nic-with-addr", method="POST", path="/vpc/v1/networkInterfaces",
             body={"folderId": "{{_suiteFolderId}}", "subnetId": "{{subId}}",
                   "name": "nic-waddr-{{runId}}", "v4AddressIds": ["{{addrId}}"]},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.networkInterfaceId", "nicId")]),
        poll_operation_until_done(),
        Step(name="assert-create-ok", method="GET", path="/operations/{{opId}}",
             test_script=["const j = pm.response.json();",
                          "pm.test('NIC create op done no error', () => pm.expect(j.done && !j.error).to.eql(true));"]),
        Step(name="get-nic", method="GET", path="/vpc/v1/networkInterfaces/{{nicId}}",
             test_script=[*assert_status(200),
                          "pm.test('v4AddressIds echoed', () => pm.expect(pm.response.json().v4AddressIds || []).to.include(pm.environment.get('addrId')));"]),
        *_cleanup_nic(),
        Step(name="cleanup-addr", method="DELETE", path="/vpc/v1/addresses/{{addrId}}",
             test_script=["pm.test('cleanup addr (200 or 400)', () => pm.expect(pm.response.code).to.be.oneOf([200, 400]));",
                          *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        _cleanup_subnet(),
        poll_operation_until_done(),
        _cleanup_net(),
        poll_operation_until_done(),
    ],
))

CASES.append(Case(
    # KAC-33 (issue #2 surface): NIC referencing an internal_ipv6 Address — create
    # network + subnet (with v6 cidr) + internal_ipv6 Address + NIC with that addr
    # id in v6AddressIds → GET NIC echoes v6AddressIds.
    id="NIC-CR-WITH-V6-ADDR-OK",
    title="Create internal_ipv6 Address в subnet с v6 cidr → NIC с этим id в v6AddressIds → GET → echoed",
    classes=["CRUD"],
    priority="P2",
    steps=[
        Step(name="pre-net", method="POST", path="/vpc/v1/networks",
             body={"folderId": "{{_suiteFolderId}}", "name": "nic-v6addr-net-{{runId}}"},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.networkId", "netId")]),
        poll_operation_until_done(),
        Step(name="pre-subnet", method="POST", path="/vpc/v1/subnets",
             body={"folderId": "{{_suiteFolderId}}", "networkId": "{{netId}}",
                   "name": "nic-v6addr-sub-{{runId}}", "zoneId": "{{existingZoneId}}",
                   "v4CidrBlocks": ["10.61.0.0/24"], "v6CidrBlocks": ["fd00:cafe:f00d::/64"]},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.subnetId", "subId")]),
        poll_operation_until_done(),
        Step(name="create-v6-addr", method="POST", path="/vpc/v1/addresses",
             body={"folderId": "{{_suiteFolderId}}", "name": "nic-v6addr-addr-{{runId}}",
                   "internalIpv6AddressSpec": {"subnetId": "{{subId}}"}},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.addressId", "addrId")]),
        poll_operation_until_done(),
        Step(name="create-nic-with-v6-addr", method="POST", path="/vpc/v1/networkInterfaces",
             body={"folderId": "{{_suiteFolderId}}", "subnetId": "{{subId}}",
                   "name": "nic-v6addr-{{runId}}", "v6AddressIds": ["{{addrId}}"]},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.networkInterfaceId", "nicId")]),
        poll_operation_until_done(),
        Step(name="assert-create-ok", method="GET", path="/operations/{{opId}}",
             test_script=["const j = pm.response.json();",
                          "pm.test('NIC create op done no error', () => pm.expect(j.done && !j.error).to.eql(true));"]),
        Step(name="get-nic", method="GET", path="/vpc/v1/networkInterfaces/{{nicId}}",
             test_script=[*assert_status(200),
                          "pm.test('v6AddressIds echoed', () => pm.expect(pm.response.json().v6AddressIds || []).to.include(pm.environment.get('addrId')));"]),
        *_cleanup_nic(),
        Step(name="cleanup-addr", method="DELETE", path="/vpc/v1/addresses/{{addrId}}",
             test_script=["pm.test('cleanup addr (200 or 400)', () => pm.expect(pm.response.code).to.be.oneOf([200, 400]));",
                          *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        _cleanup_subnet(),
        poll_operation_until_done(),
        _cleanup_net(),
        poll_operation_until_done(),
    ],
))

CASES.append(Case(
    # KAC-31: Address, используемый NIC через v4AddressIds, нельзя удалить —
    # AddressService.Delete синхронно отвергает FAILED_PRECONDITION (409). После
    # удаления NIC адрес освобождается и удаляется.
    id="ADDR-DEL-NEG-USED-BY-NIC",
    title="Delete Address, который в использовании у NIC → 409 FailedPrecondition; после delete NIC → Address удаляется",
    classes=["NEG", "STATE", "CONF"],
    priority="P1",
    steps=[
        *_net_subnet_steps("delusedbynic"),
        Step(name="create-addr", method="POST", path="/vpc/v1/addresses",
             body={"folderId": "{{_suiteFolderId}}", "name": "nic-dubn-addr-{{runId}}",
                   "internalIpv4AddressSpec": {"subnetId": "{{subId}}"}},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.addressId", "addrId")]),
        poll_operation_until_done(),
        Step(name="create-nic-with-addr", method="POST", path="/vpc/v1/networkInterfaces",
             body={"folderId": "{{_suiteFolderId}}", "subnetId": "{{subId}}",
                   "name": "nic-dubn-{{runId}}", "v4AddressIds": ["{{addrId}}"]},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.networkInterfaceId", "nicId")]),
        poll_operation_until_done(),
        Step(name="assert-nic-created", method="GET", path="/operations/{{opId}}",
             test_script=["const j = pm.response.json();",
                          "pm.test('NIC create op done no error', () => pm.expect(j.done && !j.error).to.eql(true));"]),
        Step(name="del-addr-blocked", method="DELETE", path="/vpc/v1/addresses/{{addrId}}",
             # grpc-gateway маппит FAILED_PRECONDITION (9) → HTTP 400.
             test_script=[*assert_status(400), *assert_grpc_code(9, "FAILED_PRECONDITION"),
                          "pm.test('message mentions network interface', () => pm.expect(pm.response.json().message).to.include('network interface'));"]),
        # Удаляем NIC → адрес освобождается.
        Step(name="del-nic", method="DELETE", path="/vpc/v1/networkInterfaces/{{nicId}}",
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        # Теперь Address удаляется.
        Step(name="del-addr-ok", method="DELETE", path="/vpc/v1/addresses/{{addrId}}",
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="assert-addr-deleted", method="GET", path="/operations/{{opId}}",
             test_script=["const j = pm.response.json();",
                          "pm.test('addr delete op done no error', () => pm.expect(j.done && !j.error).to.eql(true));"]),
        Step(name="get-addr-gone", method="GET", path="/vpc/v1/addresses/{{addrId}}",
             test_script=[*assert_status(404), *assert_grpc_code(5, "NOT_FOUND")]),
        _cleanup_subnet(),
        poll_operation_until_done(),
        _cleanup_net(),
        poll_operation_until_done(),
    ],
))

CASES.append(Case(
    # Network-less (unbound, kacho-proto#8) SG, приаттаченная к NIC в той же folder.
    id="NIC-CR-WITH-UNBOUND-SG-OK",
    title="Create network-less SG → create NIC c этим SG в securityGroupIds → get → echoed",
    classes=["CRUD"],
    priority="P2",
    steps=[
        *_net_subnet_steps("wsg"),
        Step(name="create-unbound-sg", method="POST", path="/vpc/v1/securityGroups",
             body={"folderId": "{{_suiteFolderId}}", "name": "nic-wsg-sg-{{runId}}", "ruleSpecs": []},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.securityGroupId", "sgId")]),
        poll_operation_until_done(),
        Step(name="create-nic-with-sg", method="POST", path="/vpc/v1/networkInterfaces",
             body={"folderId": "{{_suiteFolderId}}", "subnetId": "{{subId}}",
                   "name": "nic-wsg-{{runId}}", "securityGroupIds": ["{{sgId}}"]},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.networkInterfaceId", "nicId")]),
        poll_operation_until_done(),
        Step(name="assert-create-ok", method="GET", path="/operations/{{opId}}",
             test_script=["const j = pm.response.json();",
                          "pm.test('NIC create with unbound SG done no error', () => pm.expect(j.done && !j.error).to.eql(true));"]),
        Step(name="get-nic", method="GET", path="/vpc/v1/networkInterfaces/{{nicId}}",
             test_script=[*assert_status(200),
                          "pm.test('securityGroupIds echoed', () => pm.expect(pm.response.json().securityGroupIds || []).to.include(pm.environment.get('sgId')));"]),
        *_cleanup_nic(),
        Step(name="cleanup-sg", method="DELETE", path="/vpc/v1/securityGroups/{{sgId}}",
             test_script=["pm.test('cleanup sg (200 or 400)', () => pm.expect(pm.response.code).to.be.oneOf([200, 400]));",
                          *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        _cleanup_subnet(),
        poll_operation_until_done(),
        _cleanup_net(),
        poll_operation_until_done(),
    ],
))
