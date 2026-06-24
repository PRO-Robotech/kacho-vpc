# Copyright (c) PRO-Robotech
# SPDX-License-Identifier: BUSL-1.1

"""Case-set для NetworkInterfaceService (kacho-vpc) — first-class NIC-ресурс.
REST: /vpc/v1/networkInterfaces.

Контракт изоляции — как везде: каждый case внутри своего runId, suite работает
в pre-allocated existingProjectId. Имена ресурсов суффиксуются {{runId}}.

Проекция NIC — lean, control-plane only: id/projectId/subnetId/v4AddressIds/v6AddressIds/
securityGroupIds/usedBy/status/name/labels. Инфра/data-plane-полей у kacho-vpc нет (появятся
на стороне будущего kacho-vpc-implement). Денилист `_LEAN_FORBIDDEN` ниже — регрессионный guard:
эти инфра-чувствительные поля НИКОГДА не должны появиться на публичной поверхности.
"""

CASES = []


def _net_subnet_steps(suffix, cidr="10.60.0.0/24"):
    """Helper: создает parent Network + Subnet, сохраняет netId/subId."""
    return [
        Step(name="pre-net", method="POST", path="/vpc/v1/networks",
             body={"projectId": "{{_suiteProjectId}}", "name": f"nic-{suffix}-net-{{{{runId}}}}"},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.networkId", "netId")]),
        poll_operation_until_done(),
        Step(name="pre-subnet", method="POST", path="/vpc/v1/subnets",
             body={"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}",
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
        "pm.test('has id/projectId/subnetId/status', () => {",
        "  pm.expect(j.id, 'id').to.be.a('string');",
        "  pm.expect(j.projectId, 'projectId').to.be.a('string');",
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
             body={"projectId": "{{_suiteProjectId}}", "subnetId": "{{subId}}",
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
    id="NIC-CR-MAC-OK",
    title="Create NIC → macAddress в lean-проекции, формат 0e:xx:xx:xx:xx:xx, стабилен при Update name",
    classes=["CRUD"],
    priority="P1",
    steps=[
        *_net_subnet_steps("mac"),
        Step(name="create-nic", method="POST", path="/vpc/v1/networkInterfaces",
             body={"projectId": "{{_suiteProjectId}}", "subnetId": "{{subId}}",
                   "name": "nic-mac-{{runId}}"},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.networkInterfaceId", "nicId")]),
        poll_operation_until_done(),
        Step(name="get-nic-mac", method="GET", path="/vpc/v1/networkInterfaces/{{nicId}}",
             test_script=[*assert_status(200),
                          "const j = pm.response.json();",
                          "pm.test('macAddress на публичной проекции', () => pm.expect(j.macAddress, 'macAddress').to.be.a('string'));",
                          "pm.test('macAddress в Kachō-формате 0e:xx:xx:xx:xx:xx (lowercase hex)', () => pm.expect(j.macAddress).to.match(/^0e:[0-9a-f]{2}:[0-9a-f]{2}:[0-9a-f]{2}:[0-9a-f]{2}:[0-9a-f]{2}$/));",
                          "pm.environment.set('savedMac', j.macAddress);"]),
        Step(name="update-nic-name", method="PATCH", path="/vpc/v1/networkInterfaces/{{nicId}}",
             body={"updateMask": "name", "name": "nic-mac-renamed-{{runId}}"},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="get-nic-mac-stable", method="GET", path="/vpc/v1/networkInterfaces/{{nicId}}",
             test_script=[*assert_status(200),
                          "const j = pm.response.json();",
                          "pm.test('macAddress не меняется при Update (stable NIC MAC)', () => pm.expect(j.macAddress).to.eql(pm.environment.get('savedMac')));"]),
        *_cleanup_nic(),
        _cleanup_subnet(),
        poll_operation_until_done(),
        _cleanup_net(),
        poll_operation_until_done(),
    ],
))

CASES.append(Case(
    id="NIC-CR-NEG-DUP-NAME",
    title="Create двух NIC с одинаковым name в одном project → второй ALREADY_EXISTS (async op.error)",
    classes=["NEG", "CONF"],
    priority="P1",
    steps=[
        *_net_subnet_steps("dup"),
        Step(name="create-1", method="POST", path="/vpc/v1/networkInterfaces",
             body={"projectId": "{{_suiteProjectId}}", "subnetId": "{{subId}}", "name": "nic-dup-{{runId}}"},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.networkInterfaceId", "nicId")]),
        poll_operation_until_done(),
        Step(name="create-2-dup", method="POST", path="/vpc/v1/networkInterfaces",
             body={"projectId": "{{_suiteProjectId}}", "subnetId": "{{subId}}", "name": "nic-dup-{{runId}}"},
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
             body={"projectId": "{{_suiteProjectId}}", "subnetId": "{{garbageVpcId}}", "name": "nic-bs-{{runId}}"},
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
    title="List NIC by projectId → 200, массив; созданный NIC присутствует",
    classes=["CRUD"],
    priority="P1",
    steps=[
        *_net_subnet_steps("lst"),
        Step(name="create-nic", method="POST", path="/vpc/v1/networkInterfaces",
             body={"projectId": "{{_suiteProjectId}}", "subnetId": "{{subId}}", "name": "nic-lst-{{runId}}"},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.networkInterfaceId", "nicId")]),
        poll_operation_until_done(),
        Step(name="list", method="GET", path="/vpc/v1/networkInterfaces?projectId={{_suiteProjectId}}&pageSize=1000",
             test_script=[*assert_status(200),
                          "const j = pm.response.json();",
                          "pm.test('networkInterfaces array', () => pm.expect(j.networkInterfaces || []).to.be.an('array'));",
                          "pm.test('created NIC present', () => pm.expect((j.networkInterfaces || []).map(n => n.id)).to.include(pm.environment.get('nicId')));"]),
        Step(name="list-no-project", method="GET", path="/vpc/v1/networkInterfaces",
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
             body={"projectId": "{{_suiteProjectId}}", "subnetId": "{{subId}}", "name": "nic-upd-{{runId}}"},
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
             body={"projectId": "{{_suiteProjectId}}", "subnetId": "{{subId}}", "name": "nic-del-{{runId}}"},
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
    id="NIC-CR-WITH-ADDR-OK",
    title="Create internal_ipv4 Address в subnet → create NIC с этим address id в v4AddressIds → get → echoed",
    classes=["CRUD"],
    priority="P1",
    steps=[
        *_net_subnet_steps("waddr"),
        Step(name="create-addr", method="POST", path="/vpc/v1/addresses",
             body={"projectId": "{{_suiteProjectId}}", "name": "nic-waddr-addr-{{runId}}",
                   "internalIpv4AddressSpec": {"subnetId": "{{subId}}"}},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.addressId", "addrId")]),
        poll_operation_until_done(),
        Step(name="create-nic-with-addr", method="POST", path="/vpc/v1/networkInterfaces",
             body={"projectId": "{{_suiteProjectId}}", "subnetId": "{{subId}}",
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
    # NIC, ссылающийся на internal_ipv6 Address: создаем network + subnet (с v6 cidr)
    # + internal_ipv6 Address + NIC с этим addr id в v6AddressIds → GET NIC отдает
    # v6AddressIds.
    id="NIC-CR-WITH-V6-ADDR-OK",
    title="Create internal_ipv6 Address в subnet с v6 cidr → NIC с этим id в v6AddressIds → GET → echoed",
    classes=["CRUD"],
    priority="P2",
    steps=[
        Step(name="pre-net", method="POST", path="/vpc/v1/networks",
             body={"projectId": "{{_suiteProjectId}}", "name": "nic-v6addr-net-{{runId}}"},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.networkId", "netId")]),
        poll_operation_until_done(),
        Step(name="pre-subnet", method="POST", path="/vpc/v1/subnets",
             body={"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}",
                   "name": "nic-v6addr-sub-{{runId}}", "zoneId": "{{existingZoneId}}",
                   "v4CidrBlocks": ["10.61.0.0/24"], "v6CidrBlocks": ["fd00:cafe:f00d::/64"]},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.subnetId", "subId")]),
        poll_operation_until_done(),
        Step(name="create-v6-addr", method="POST", path="/vpc/v1/addresses",
             body={"projectId": "{{_suiteProjectId}}", "name": "nic-v6addr-addr-{{runId}}",
                   "internalIpv6AddressSpec": {"subnetId": "{{subId}}"}},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.addressId", "addrId")]),
        poll_operation_until_done(),
        Step(name="create-nic-with-v6-addr", method="POST", path="/vpc/v1/networkInterfaces",
             body={"projectId": "{{_suiteProjectId}}", "subnetId": "{{subId}}",
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
    # При линковке адреса к интерфейсу v4_address_ids или v6_address_ids (или оба
    # вместе) могут быть заполнены. Проверяем, что одновременная линковка v4 + v6
    # при создании NIC работает.
    id="NIC-CR-WITH-BOTH-ADDR-OK",
    title="Create NIC с v4_address_ids И v6_address_ids одновременно → 200, оба address привязаны",
    classes=["CRUD"], priority="P1",
    steps=[
        # Network + Subnet с v4 и v6 cidr.
        Step(name="pre-net", method="POST", path="/vpc/v1/networks",
             body={"projectId": "{{_suiteProjectId}}", "name": "nic-both-net-{{runId}}"},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.networkId", "netId")]),
        poll_operation_until_done(),
        Step(name="pre-subnet", method="POST", path="/vpc/v1/subnets",
             body={"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}",
                   "name": "nic-both-sub-{{runId}}", "zoneId": "{{existingZoneId}}",
                   "v4CidrBlocks": ["10.62.0.0/24"], "v6CidrBlocks": ["fd00:cafe:b00b::/64"]},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.subnetId", "subId")]),
        poll_operation_until_done(),
        # v4 + v6 addresses
        Step(name="cr-v4-addr", method="POST", path="/vpc/v1/addresses",
             body={"projectId": "{{_suiteProjectId}}", "name": "nic-both-v4-{{runId}}",
                   "internalIpv4AddressSpec": {"subnetId": "{{subId}}"}},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.addressId", "v4AddrId")]),
        poll_operation_until_done(),
        Step(name="cr-v6-addr", method="POST", path="/vpc/v1/addresses",
             body={"projectId": "{{_suiteProjectId}}", "name": "nic-both-v6-{{runId}}",
                   "internalIpv6AddressSpec": {"subnetId": "{{subId}}"}},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.addressId", "v6AddrId")]),
        poll_operation_until_done(),
        # NIC create — оба address-id одновременно.
        Step(name="cr-nic-both", method="POST", path="/vpc/v1/networkInterfaces",
             body={"projectId": "{{_suiteProjectId}}", "subnetId": "{{subId}}",
                   "name": "nic-both-{{runId}}",
                   "v4AddressIds": ["{{v4AddrId}}"],
                   "v6AddressIds": ["{{v6AddrId}}"]},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.networkInterfaceId", "nicId")]),
        poll_operation_until_done(),
        Step(name="assert-nic-ok", method="GET", path="/operations/{{opId}}",
             test_script=["const j = pm.response.json();",
                          "pm.test('NIC.Create op done, no error', () => pm.expect(j.done && !j.error).to.eql(true));"]),
        Step(name="get-nic", method="GET", path="/vpc/v1/networkInterfaces/{{nicId}}",
             test_script=[*assert_status(200),
                          "const j = pm.response.json();",
                          "pm.test('v4AddressIds linked', () => pm.expect(j.v4AddressIds || []).to.include(pm.environment.get('v4AddrId')));",
                          "pm.test('v6AddressIds linked', () => pm.expect(j.v6AddressIds || []).to.include(pm.environment.get('v6AddrId')));"]),
        # Cleanup снизу вверх: NIC → addresses → subnet → network.
        *_cleanup_nic(),
        Step(name="cleanup-v4-addr", method="DELETE", path="/vpc/v1/addresses/{{v4AddrId}}",
             test_script=["pm.test('cleanup v4 addr (200 or 400)', () => pm.expect(pm.response.code).to.be.oneOf([200, 400]));",
                          *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="cleanup-v6-addr", method="DELETE", path="/vpc/v1/addresses/{{v6AddrId}}",
             test_script=["pm.test('cleanup v6 addr (200 or 400)', () => pm.expect(pm.response.code).to.be.oneOf([200, 400]));",
                          *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        _cleanup_subnet(),
        poll_operation_until_done(),
        _cleanup_net(),
        poll_operation_until_done(),
    ],
))

CASES.append(Case(
    # Address, используемый NIC через v4AddressIds, нельзя удалить —
    # AddressService.Delete синхронно отвергает FAILED_PRECONDITION (409). После
    # удаления NIC адрес освобождается и удаляется.
    id="ADDR-DEL-NEG-USED-BY-NIC",
    title="Delete Address, который в использовании у NIC → 409 FailedPrecondition; после delete NIC → Address удаляется",
    classes=["NEG", "STATE", "CONF"],
    priority="P1",
    steps=[
        *_net_subnet_steps("delusedbynic"),
        Step(name="create-addr", method="POST", path="/vpc/v1/addresses",
             body={"projectId": "{{_suiteProjectId}}", "name": "nic-dubn-addr-{{runId}}",
                   "internalIpv4AddressSpec": {"subnetId": "{{subId}}"}},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.addressId", "addrId")]),
        poll_operation_until_done(),
        Step(name="create-nic-with-addr", method="POST", path="/vpc/v1/networkInterfaces",
             body={"projectId": "{{_suiteProjectId}}", "subnetId": "{{subId}}",
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
    # network_id у SG обязателен — «network-less» SG не существует. SG создается
    # привязанной к сети NIC'а ({{netId}}); кейс проверяет, что NIC ссылается на SG
    # через securityGroupIds[].
    id="NIC-CR-WITH-UNBOUND-SG-OK",
    title="Create SG (bound к сети NIC) → create NIC c этим SG в securityGroupIds → get → echoed",
    classes=["CRUD"],
    priority="P2",
    steps=[
        *_net_subnet_steps("wsg"),
        Step(name="create-sg", method="POST", path="/vpc/v1/securityGroups",
             body={"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}",
                   "name": "nic-wsg-sg-{{runId}}", "ruleSpecs": []},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.securityGroupId", "sgId")]),
        poll_operation_until_done(),
        Step(name="create-nic-with-sg", method="POST", path="/vpc/v1/networkInterfaces",
             body={"projectId": "{{_suiteProjectId}}", "subnetId": "{{subId}}",
                   "name": "nic-wsg-{{runId}}", "securityGroupIds": ["{{sgId}}"]},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.networkInterfaceId", "nicId")]),
        poll_operation_until_done(),
        Step(name="assert-create-ok", method="GET", path="/operations/{{opId}}",
             test_script=["const j = pm.response.json();",
                          "pm.test('NIC create with SG done no error', () => pm.expect(j.done && !j.error).to.eql(true));"]),
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


# ---------------------------------------------------------------------------
# NIC negative + ephemeral lifecycle
# ---------------------------------------------------------------------------

def _make_addr(name_suffix, ip_field="internalIpv4AddressSpec"):
    """Helper: создает internal Address в subnet, сохраняет в env addrId<suffix>."""
    env_var = f"addrId{name_suffix}"
    return [
        Step(name=f"create-addr-{name_suffix}", method="POST", path="/vpc/v1/addresses",
             body={"projectId": "{{_suiteProjectId}}",
                   "name": f"nic-multi-addr-{name_suffix}-{{{{runId}}}}",
                   ip_field: {"subnetId": "{{subId}}"}},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.addressId", env_var)]),
        poll_operation_until_done(),
    ]


CASES.append(Case(
    id="NIC-CR-NEG-MULTI-V4-ADDR",
    title="Create NIC с 2× v4_address_ids → InvalidArgument (cardinality CHECK constraint, REQ-NIC-04)",
    classes=["NEG", "VAL"], priority="P0",
    steps=[
        *_net_subnet_steps("m4"),
        *_make_addr("A"),
        *_make_addr("B"),
        Step(name="create-nic-2v4", method="POST", path="/vpc/v1/networkInterfaces",
             body={"projectId": "{{_suiteProjectId}}", "subnetId": "{{subId}}",
                   "name": "nic-m4-{{runId}}",
                   "v4AddressIds": ["{{addrIdA}}", "{{addrIdB}}"]},
             test_script=[
                 # Может быть sync InvalidArgument (service validateNICAddressCardinality)
                 # либо async через DB CHECK (миграция 0018) → Operation Failed.
                 "pm.test('400 sync OR 200 sync (Operation потом fails)', () => pm.expect(pm.response.code).to.be.oneOf([200, 400]));",
                 "if (pm.response.code === 400) {",
                 "  pm.test('grpc INVALID_ARGUMENT', () => { const j = pm.response.json(); pm.expect(j.code, JSON.stringify(j)).to.eql(3); });",
                 "  pm.environment.unset('opId');",
                 "} else {",
                 "  const j = pm.response.json(); pm.environment.set('opId', j.id || '');",
                 "}",
             ]),
        Step(name="poll-and-assert-fail", method="GET", path="/operations/{{opId}}",
             test_script=[
                 "if (!pm.environment.get('opId')) return;",
                 "// Worker должен failed'ить с InvalidArgument (cardinality CHECK).",
                 "let _tries = 0; const _MAX = 8;",
                 "const _step = () => {",
                 "  pm.sendRequest({",
                 "    url: pm.environment.get('baseUrl') + '/operations/' + pm.environment.get('opId'),",
                 "    method: 'GET',",
                 "    header: { 'Authorization': 'Bearer ' + pm.environment.get('jwtProjectAdminA1') },",
                 "  }, (err, res) => {",
                 "    let j = null; try { j = res.json(); } catch (e) {}",
                 "    if (j && j.done) {",
                 "      pm.test('Operation failed: cardinality v4≤1 violated', () => pm.expect(!!j.error, JSON.stringify(j)).to.eql(true));",
                 "      pm.test('error code InvalidArgument (3) or FailedPrecondition (9)', () => pm.expect(j.error.code).to.be.oneOf([3, 9]));",
                 "    } else if (++_tries < _MAX) { setTimeout(_step, 500); }",
                 "    else { pm.test('op resolved in time', () => pm.expect.fail('op did not finish')); }",
                 "  });",
                 "};",
                 "_step();",
             ]),
        # Cleanup addresses (NIC не создалось → не блокирует).
        Step(name="del-addrA", method="DELETE", path="/vpc/v1/addresses/{{addrIdA}}",
             test_script=["pm.test('del addr A 200|400', () => pm.expect(pm.response.code).to.be.oneOf([200, 400]));",
                          *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="del-addrB", method="DELETE", path="/vpc/v1/addresses/{{addrIdB}}",
             test_script=["pm.test('del addr B 200|400', () => pm.expect(pm.response.code).to.be.oneOf([200, 400]));",
                          *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        _cleanup_subnet(),
        poll_operation_until_done(),
        _cleanup_net(),
        poll_operation_until_done(),
    ],
))


CASES.append(Case(
    id="NIC-CR-NEG-MULTI-V6-ADDR",
    title="Create NIC с 2× v6_address_ids → InvalidArgument/FailedPrecondition (cardinality v6≤1, REQ-NIC-04)",
    classes=["NEG", "VAL"], priority="P0",
    steps=[
        *_net_subnet_steps("m6"),
        # Subnet с v6-CIDR — иначе нельзя allocate v6 Address.
        Step(name="add-v6-cidr", method="POST", path="/vpc/v1/subnets/{{subId}}:add-cidr-blocks",
             body={"v6CidrBlocks": ["fd12:3456:78aa::/64"]},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        # Два v6 Address.
        Step(name="create-addrA-v6", method="POST", path="/vpc/v1/addresses",
             body={"projectId": "{{_suiteProjectId}}", "name": "nic-m6-A-{{runId}}",
                   "internalIpv6AddressSpec": {"subnetId": "{{subId}}"}},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.addressId", "addrIdA")]),
        poll_operation_until_done(),
        Step(name="create-addrB-v6", method="POST", path="/vpc/v1/addresses",
             body={"projectId": "{{_suiteProjectId}}", "name": "nic-m6-B-{{runId}}",
                   "internalIpv6AddressSpec": {"subnetId": "{{subId}}"}},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.addressId", "addrIdB")]),
        poll_operation_until_done(),
        Step(name="create-nic-2v6", method="POST", path="/vpc/v1/networkInterfaces",
             body={"projectId": "{{_suiteProjectId}}", "subnetId": "{{subId}}",
                   "name": "nic-m6-{{runId}}",
                   "v6AddressIds": ["{{addrIdA}}", "{{addrIdB}}"]},
             test_script=[
                 "pm.test('400 sync OR 200 sync (Operation fails)', () => pm.expect(pm.response.code).to.be.oneOf([200, 400]));",
                 "if (pm.response.code === 400) { pm.environment.unset('opId'); }",
                 "else { const j = pm.response.json(); pm.environment.set('opId', j.id || ''); }",
             ]),
        Step(name="poll-fail", method="GET", path="/operations/{{opId}}",
             test_script=[
                 "if (!pm.environment.get('opId')) return;",
                 "let _t = 0;",
                 "const _s = () => pm.sendRequest({",
                 "  url: pm.environment.get('baseUrl') + '/operations/' + pm.environment.get('opId'),",
                 "  method: 'GET',",
                 "  header: { 'Authorization': 'Bearer ' + pm.environment.get('jwtProjectAdminA1') },",
                 "}, (err, res) => {",
                 "  let j = null; try { j = res.json(); } catch (e) {}",
                 "  if (j && j.done) {",
                 "    pm.test('op failed cardinality v6≤1', () => pm.expect(!!j.error).to.eql(true));",
                 "    pm.test('code 3 or 9', () => pm.expect(j.error.code).to.be.oneOf([3, 9]));",
                 "  } else if (++_t < 8) { setTimeout(_s, 500); }",
                 "  else { pm.test('op resolved', () => pm.expect.fail('timeout')); }",
                 "});",
                 "_s();",
             ]),
        Step(name="del-addrA-v6", method="DELETE", path="/vpc/v1/addresses/{{addrIdA}}",
             test_script=["pm.test('del 200|400', () => pm.expect(pm.response.code).to.be.oneOf([200, 400]));",
                          *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="del-addrB-v6", method="DELETE", path="/vpc/v1/addresses/{{addrIdB}}",
             test_script=["pm.test('del 200|400', () => pm.expect(pm.response.code).to.be.oneOf([200, 400]));",
                          *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        _cleanup_subnet(),
        poll_operation_until_done(),
        _cleanup_net(),
        poll_operation_until_done(),
    ],
))




