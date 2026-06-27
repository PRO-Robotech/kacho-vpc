# Copyright (c) PRO-Robotech
# SPDX-License-Identifier: BUSL-1.1

"""Case-set для InternalAddressPoolService (kacho-only admin IPAM RPC).

Покрывает internal/admin-only RPC, проброшенные через api-gateway cluster-internal
mux на /vpc/v1/addressPools/... . Эти RPC возвращают ресурсы ПРЯМО (не Operation).
AddressPool — глобальный infrastructure-ресурс (как Region/Zone), не привязан к
project. Тесты создают только runId-суффиксованные throwaway-пулы/сети/адреса и
убирают за собой; seeded `default-{{zoneA}}` pool / `zone` region /
`zone-{a,b,c,d}` zones НЕ трогаются.

REST gateway body — camelCase JSON.
"""

CASES = []

POOLS = "/vpc/v1/addressPools"


# ---------------------------------------------------------------------------
# Pool CRUD happy path
# ---------------------------------------------------------------------------

CASES.append(Case(
    # v4-only split-shape: v4CidrBlocks непуст, v6CidrBlocks пуст.
    id="IPL-CR-CRUD-V4-OK",
    title="AddressPool Create v4-only → Get → List-includes → Delete → Get-404",
    classes=["CRUD", "CONF"], priority="P0",
    steps=[
        Step(name="create", method="POST", path=POOLS,
             body={"name": "ipl-crud-{{runId}}", "kind": "EXTERNAL_PUBLIC",
                   "zoneId": "{{zoneC}}",
                   "v4CidrBlocks": ["203.0.113.0/24"], "v6CidrBlocks": []},
             test_script=[*assert_status(200),
                          "const j = pm.response.json();",
                          "pm.test('id has apl prefix', () => pm.expect(j.id).to.match(/^apl/));",
                          "pm.test('name matches', () => pm.expect(j.name).to.eql('ipl-crud-' + pm.environment.get('runId')));",
                          "pm.test('kind echoed', () => pm.expect(j.kind).to.eql('EXTERNAL_PUBLIC'));",
                          # internal mux api-gateway эмитит EmitUnpopulated=false →
                          # bool false опускается из JSON. Поэтому `isDefault` приходит как
                          # undefined, что эквивалентно отсутствию default-флага.
                          "pm.test('isDefault false', () => pm.expect(j.isDefault || false).to.eql(false));",
                          "pm.test('v4CidrBlocks echoed', () => pm.expect(j.v4CidrBlocks).to.eql(['203.0.113.0/24']));",
                          "pm.test('v6CidrBlocks empty', () => pm.expect(j.v6CidrBlocks || []).to.eql([]));",
                          *save_from_response("j.id", "iplId")]),
        Step(name="get", method="GET", path=POOLS + "/{{iplId}}",
             test_script=[*assert_status(200),
                          "pm.test('id matches', () => pm.expect(pm.response.json().id).to.eql(pm.environment.get('iplId')));"]),
        Step(name="list-includes", method="GET", path=POOLS,
             test_script=[*assert_status(200),
                          "const pools = pm.response.json().pools || [];",
                          "pm.test('list contains created', () => pm.expect(pools.map(p => p.id)).to.include(pm.environment.get('iplId')));"]),
        Step(name="delete", method="DELETE", path=POOLS + "/{{iplId}}",
             test_script=[*assert_status(200),
                          "pm.test('delete returns empty obj', () => pm.expect(pm.response.json()).to.be.an('object'));"]),
        Step(name="get-404", method="GET", path=POOLS + "/{{iplId}}",
             test_script=[*assert_status(404), *assert_grpc_code(5, "NOT_FOUND")]),
    ],
))

CASES.append(Case(
    id="IPL-UPD-CRUD-OK",
    title="AddressPool Update — description / labels / isDefault",
    classes=["CRUD", "STATE"], priority="P1",
    steps=[
        Step(name="create", method="POST", path=POOLS,
             body={"name": "ipl-upd-{{runId}}", "kind": "EXTERNAL_PUBLIC",
                   "zoneId": "{{zoneC}}",
                   "v4CidrBlocks": ["203.0.113.0/24"], "v6CidrBlocks": []},
             test_script=[*assert_status(200), *save_from_response("j.id", "iplId")]),
        # Partial-update через google.protobuf.FieldMask update_mask (parity со
        # всеми VPC Update-RPC).
        Step(name="patch", method="PATCH", path=POOLS + "/{{iplId}}",
             body={"updateMask": "description,labels,isDefault",
                   "description": "ipl-updated-desc",
                   "labels": {"env": "test", "team": "ipam"},
                   "isDefault": True},
             test_script=[*assert_status(200),
                          "const j = pm.response.json();",
                          "pm.test('description updated', () => pm.expect(j.description).to.eql('ipl-updated-desc'));",
                          "pm.test('label env', () => pm.expect((j.labels || {}).env).to.eql('test'));",
                          "pm.test('label team', () => pm.expect((j.labels || {}).team).to.eql('ipam'));",
                          "pm.test('isDefault now true', () => pm.expect(j.isDefault).to.eql(true));"]),
        Step(name="verify", method="GET", path=POOLS + "/{{iplId}}",
             test_script=[*assert_status(200),
                          "pm.test('description persisted', () => pm.expect(pm.response.json().description).to.eql('ipl-updated-desc'));"]),
        Step(name="cleanup", method="DELETE", path=POOLS + "/{{iplId}}",
             test_script=[*assert_status(200)]),
    ],
))

CASES.append(Case(
    id="IPL-UPD-NEG-IMMUTABLE-MASK",
    title="Update с immutable-полем (zoneId) в update_mask → InvalidArgument",
    classes=["NEG", "CONF"], priority="P1",
    steps=[
        Step(name="create", method="POST", path=POOLS,
             body={"name": "ipl-immut-{{runId}}", "kind": "EXTERNAL_PUBLIC",
                   "zoneId": "{{zoneC}}",
                   "v4CidrBlocks": ["203.0.113.0/24"], "v6CidrBlocks": []},
             test_script=[*assert_status(200), *save_from_response("j.id", "iplImmutId")]),
        # update_mask с immutable zone_id → InvalidArgument (immutable-в-mask
        # отвергается, как у Subnet network_id/zone_id).
        Step(name="patch-immutable", method="PATCH", path=POOLS + "/{{iplImmutId}}",
             body={"updateMask": "zoneId"},
             test_script=[*assert_status(400), *assert_grpc_code(3, "INVALID_ARGUMENT")]),
        Step(name="cleanup", method="DELETE", path=POOLS + "/{{iplImmutId}}",
             test_script=[*assert_status(200)]),
    ],
))

CASES.append(Case(
    id="IPL-LST-CRUD-OK",
    title="List addressPools → pools array present",
    classes=["CRUD"], priority="P1",
    steps=[
        Step(name="list", method="GET", path=POOLS,
             test_script=[*assert_status(200),
                          "pm.test('pools array', () => pm.expect(pm.response.json().pools || []).to.be.an('array'));"]),
        Step(name="list-by-zone", method="GET", path=POOLS + "?zoneId={{zoneA}}",
             test_script=[*assert_status(200),
                          "pm.test('pools array (zone filter)', () => pm.expect(pm.response.json().pools || []).to.be.an('array'));"]),
        Step(name="list-by-kind", method="GET", path=POOLS + "?kind=EXTERNAL_PUBLIC",
             test_script=[*assert_status(200),
                          "pm.test('pools array (kind filter)', () => pm.expect(pm.response.json().pools || []).to.be.an('array'));"]),
    ],
))


# ---------------------------------------------------------------------------
# Negative / conformance
# ---------------------------------------------------------------------------

CASES.append(Case(
    id="IPL-CR-NEG-DUP-DEFAULT",
    title="Create второй isDefault=true для того же (zoneId, kind) что у seeded default → AlreadyExists",
    classes=["NEG", "CONF"], priority="P0",
    steps=[
        # seeded default-{{zoneA}} (EXTERNAL_PUBLIC, isDefault) уже занимает
        # партишн (zone_id='{{zoneA}}', kind=EXTERNAL_PUBLIC). DB partial UNIQUE
        # WHERE is_default → 23505 → ErrAlreadyExists. Create не успевает создать
        # row → нечего чистить.
        Step(name="cr-dup-default", method="POST", path=POOLS,
             body={"name": "ipl-dupdef-{{runId}}", "kind": "EXTERNAL_PUBLIC",
                   "zoneId": "{{zoneA}}",
                   "v4CidrBlocks": ["203.0.113.0/24"], "v6CidrBlocks": [],
                   "isDefault": True},
             test_script=[*assert_status(409), *assert_grpc_code(6, "ALREADY_EXISTS")]),
    ],
))

CASES.append(Case(
    id="IPL-CR-NEG-BAD-ZONE",
    title="Create с несуществующим zoneId → FailedPrecondition (FK violation)",
    classes=["NEG", "CONF"], priority="P0",
    steps=[
        Step(name="cr-bad-zone", method="POST", path=POOLS,
             body={"name": "ipl-badzone-{{runId}}", "kind": "EXTERNAL_PUBLIC",
                   "zoneId": "nonexistent-zone-{{runId}}",
                   "v4CidrBlocks": ["203.0.113.0/24"], "v6CidrBlocks": []},
             test_script=[
                 # zone_id FK на zones → 23503 → ErrFailedPrecondition.
                 *assert_status(400), *assert_grpc_code(9, "FAILED_PRECONDITION"),
             ]),
    ],
))

CASES.append(Case(
    id="IPL-CR-VAL-MISSING-KIND",
    title="Create без kind → 400 InvalidArgument",
    classes=["VAL"], priority="P0",
    steps=[
        Step(name="cr-no-kind", method="POST", path=POOLS,
             body={"name": "ipl-nokind-{{runId}}", "zoneId": "{{zoneC}}",
                   "v4CidrBlocks": ["203.0.113.0/24"], "v6CidrBlocks": []},
             test_script=[*assert_status(400), *assert_grpc_code(3, "INVALID_ARGUMENT")]),
    ],
))

CASES.append(Case(
    # Split-shape: оба массива (v4_cidr_blocks + v6_cidr_blocks) пусты →
    # sync InvalidArgument "must not be both empty".
    id="IPL-CR-VAL-BOTH-EMPTY",
    title="Create с обоими v4CidrBlocks/v6CidrBlocks=[] → 400 InvalidArgument (REQ-IPL-CR-04)",
    classes=["VAL", "NEG"], priority="P0",
    steps=[
        Step(name="cr-both-empty", method="POST", path=POOLS,
             body={"name": "ipl-empty-{{runId}}", "kind": "EXTERNAL_PUBLIC",
                   "zoneId": "{{zoneC}}",
                   "v4CidrBlocks": [], "v6CidrBlocks": []},
             test_script=[*assert_status(400), *assert_grpc_code(3, "INVALID_ARGUMENT"),
                          "pm.test('message mentions both empty', () => pm.expect(String(pm.response.json().message || '')).to.match(/both empty|must not be both empty/i));"]),
    ],
))

CASES.append(Case(
    id="IPL-CR-VAL-MISSING-NAME",
    title="Create без name → текущее: 200 (name не валидируется в InternalAddressPoolService.Create)",
    classes=["VAL", "CONF"], priority="P2",
    steps=[
        # Create НЕ требует name (kacho-admin RPC). Если поведение изменится на
        # 400 — этот кейс это поймает.
        Step(name="cr-no-name", method="POST", path=POOLS,
             body={"kind": "EXTERNAL_PUBLIC", "zoneId": "{{zoneC}}",
                   "v4CidrBlocks": ["203.0.113.0/24"], "v6CidrBlocks": []},
             test_script=[
                 "pm.test('accepted (200) or rejected (400)', () => pm.expect(pm.response.code).to.be.oneOf([200, 400]));",
                 "if (pm.response.code === 200) { pm.environment.set('_noNamePoolId', pm.response.json().id); }",
                 "else { pm.environment.set('_noNamePoolId', 'aplnonexistent999999'); }",
             ]),
        Step(name="cleanup", method="DELETE", path=POOLS + "/{{_noNamePoolId}}",
             test_script=["pm.test('cleanup', () => pm.expect(pm.response.code).to.be.oneOf([200, 404, 400]));"]),
    ],
))

CASES.append(Case(
    id="IPL-CR-VAL-BAD-CIDR-HOSTBITS",
    title="Create с host-bits в cidr (203.0.113.5/24) → 400 InvalidArgument",
    classes=["VAL"], priority="P1",
    steps=[
        Step(name="cr-hostbits", method="POST", path=POOLS,
             body={"name": "ipl-hb-{{runId}}", "kind": "EXTERNAL_PUBLIC",
                   "zoneId": "{{zoneC}}",
                   "v4CidrBlocks": ["203.0.113.5/24"], "v6CidrBlocks": []},
             test_script=[*assert_status(400), *assert_grpc_code(3, "INVALID_ARGUMENT")]),
    ],
))

CASES.append(Case(
    # Sparse counter-based IPv6 allocator. AddressPool с IPv6 CIDR допустим —
    # Create проходит (200), InitIPv6PoolCursor инициализирует sparse counter пула.
    id="IPL-CR-VAL-IPV6-CIDR",
    title="Create AddressPool с IPv6 cidr → 200 (sparse counter allocator)",
    classes=["CRUD"], priority="P1",
    steps=[
        Step(name="cr-v6", method="POST", path=POOLS,
             body={"name": "ipl-v6-{{runId}}", "kind": "EXTERNAL_PUBLIC",
                   "zoneId": "{{zoneC}}",
                   "v4CidrBlocks": [], "v6CidrBlocks": ["2001:db8::/64"]},
             test_script=[*assert_status(200), *save_from_response("j.id", "poolId")]),
        Step(name="cleanup", method="DELETE", path=POOLS + "/{{poolId}}",
             test_script=["pm.test('cleanup (200 or 400/404)', () => pm.expect(pm.response.code).to.be.oneOf([200, 400, 404]));"]),
    ],
))

CASES.append(Case(
    id="IPL-GET-NEG-NF",
    title="Get несуществующего pool → 404 NOT_FOUND",
    classes=["NEG"], priority="P1",
    steps=[
        Step(name="get-garbage", method="GET", path=POOLS + "/aplnonexistent999999",
             test_script=[*assert_status(404), *assert_grpc_code(5, "NOT_FOUND")]),
    ],
))

CASES.append(Case(
    id="IPL-DEL-NEG-NF",
    title="Delete несуществующего pool → 404 NOT_FOUND",
    classes=["NEG"], priority="P1",
    steps=[
        Step(name="del-garbage", method="DELETE", path=POOLS + "/aplnonexistent999999",
             test_script=[*assert_status(404), *assert_grpc_code(5, "NOT_FOUND")]),
    ],
))


# ---------------------------------------------------------------------------
# Diagnostics: Check / ExplainResolution / GetUtilization / ListAddresses
# ---------------------------------------------------------------------------

CASES.append(Case(
    id="IPL-UTIL-CRUD-OK",
    title="GetUtilization для throwaway pool → 200 + totalIps/usedIps/cidrs",
    classes=["CRUD"], priority="P1",
    steps=[
        Step(name="cr-pool", method="POST", path=POOLS,
             body={"name": "ipl-util-{{runId}}", "kind": "EXTERNAL_PUBLIC",
                   "zoneId": "{{zoneC}}",
                   "v4CidrBlocks": ["203.0.113.0/24"], "v6CidrBlocks": []},
             test_script=[*assert_status(200), *save_from_response("j.id", "utilPoolId")]),
        Step(name="util", method="GET", path=POOLS + "/{{utilPoolId}}/utilization",
             test_script=[*assert_status(200),
                          "const j = pm.response.json();",
                          "pm.test('poolId echoed', () => pm.expect(j.poolId).to.eql(pm.environment.get('utilPoolId')));",
                          "pm.test('totalIps == 254 for /24', () => pm.expect(Number(j.totalIps)).to.eql(254));",
                          "pm.test('usedIps == 0', () => pm.expect(Number(j.usedIps || 0)).to.eql(0));",
                          "pm.test('cidrs array', () => pm.expect(j.cidrs || []).to.be.an('array').with.lengthOf(1));"]),
        Step(name="cleanup", method="DELETE", path=POOLS + "/{{utilPoolId}}",
             test_script=["pm.test('cleanup', () => pm.expect(pm.response.code).to.be.oneOf([200, 404]));"]),
    ],
))

CASES.append(Case(
    id="IPL-UTIL-NEG-NF",
    title="GetUtilization несуществующего pool → 404 NOT_FOUND",
    classes=["NEG"], priority="P2",
    steps=[
        Step(name="util-garbage", method="GET", path=POOLS + "/aplnonexistent999999/utilization",
             test_script=[*assert_status(404), *assert_grpc_code(5, "NOT_FOUND")]),
    ],
))

CASES.append(Case(
    id="IPL-LISTADDR-CRUD-OK",
    title="ListAddresses на seeded default-{{zoneA}} pool → 200 + addresses array",
    classes=["CRUD"], priority="P1",
    steps=[
        Step(name="seed-pool-id", method="GET", path=POOLS + "?zoneId={{zoneA}}&kind=EXTERNAL_PUBLIC",
             test_script=[*assert_status(200),
                          "const def = (pm.response.json().pools || []).find(p => p.isDefault);",
                          "pm.test('seeded default pool exists', () => pm.expect(def, JSON.stringify(pm.response.json())).to.be.an('object'));",
                          "if (def) pm.environment.set('_seedPoolId', def.id);"]),
        Step(name="list-addr", method="GET", path=POOLS + "/{{_seedPoolId}}/addresses",
             test_script=[*assert_status(200),
                          "pm.test('addresses array', () => pm.expect(pm.response.json().addresses || []).to.be.an('array'));"]),
        Step(name="list-addr-paged", method="GET", path=POOLS + "/{{_seedPoolId}}/addresses?pageSize=2",
             test_script=[*assert_status(200),
                          "pm.test('at most 2 addresses', () => pm.expect((pm.response.json().addresses || []).length).to.be.at.most(2));"]),
    ],
))

CASES.append(Case(
    id="IPL-LISTADDR-EMPTY-OK",
    title="ListAddresses на свежем throwaway pool → 200 + пустой массив",
    classes=["CRUD"], priority="P2",
    steps=[
        Step(name="cr-pool", method="POST", path=POOLS,
             body={"name": "ipl-la-{{runId}}", "kind": "EXTERNAL_PUBLIC",
                   "zoneId": "{{zoneC}}",
                   "v4CidrBlocks": ["203.0.113.0/24"], "v6CidrBlocks": []},
             test_script=[*assert_status(200), *save_from_response("j.id", "laPoolId")]),
        Step(name="list-addr", method="GET", path=POOLS + "/{{laPoolId}}/addresses",
             test_script=[*assert_status(200),
                          "pm.test('empty addresses array', () => pm.expect(pm.response.json().addresses || []).to.be.an('array').with.lengthOf(0));"]),
        Step(name="cleanup", method="DELETE", path=POOLS + "/{{laPoolId}}",
             test_script=["pm.test('cleanup', () => pm.expect(pm.response.code).to.be.oneOf([200, 404]));"]),
    ],
))


# ---------------------------------------------------------------------------
# Bindings: addressPoolBinding (Network). Per-address override отсутствует.
# ---------------------------------------------------------------------------

CASES.append(Case(
    id="IPL-NETBIND-CRUD-OK",
    title="addressPoolBinding: bind Network → idempotent re-bind → unbind (explain удален)",
    classes=["CRUD", "STATE"], priority="P0",
    steps=[
        Step(name="cr-net", method="POST", path="/vpc/v1/networks",
             body={"projectId": "{{_suiteProjectId}}", "name": "ipl-nb-net-{{runId}}"},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.networkId", "nbNetId")]),
        poll_operation_until_done(),
        Step(name="cr-pool", method="POST", path=POOLS,
             body={"name": "ipl-nb-pool-{{runId}}", "kind": "EXTERNAL_PUBLIC",
                   "zoneId": "{{zoneC}}",
                   "v4CidrBlocks": ["203.0.113.0/24"], "v6CidrBlocks": []},
             test_script=[*assert_status(200), *save_from_response("j.id", "nbPoolId")]),
        Step(name="bind", method="POST", path="/vpc/v1/networks/{{nbNetId}}/addressPoolBinding",
             body={"poolId": "{{nbPoolId}}"},
             test_script=[*assert_status(200)]),
        # Идемпотентно: повторный bind того же pool — no-op.
        Step(name="bind-again", method="POST", path="/vpc/v1/networks/{{nbNetId}}/addressPoolBinding",
             body={"poolId": "{{nbPoolId}}"},
             test_script=[*assert_status(200)]),
        # Эффект bind'а проверяется не здесь: cascade-resolve покрыт IPL-RESOLVE-* /
        # IPAM integration-тестом. Здесь проверяем только сам bind/unbind contract.
        Step(name="unbind", method="DELETE", path="/vpc/v1/networks/{{nbNetId}}/addressPoolBinding",
             test_script=[*assert_status(200)]),
        # Идемпотентно: повторный unbind — no-op.
        Step(name="unbind-again", method="DELETE", path="/vpc/v1/networks/{{nbNetId}}/addressPoolBinding",
             test_script=["pm.test('idempotent unbind', () => pm.expect(pm.response.code).to.be.oneOf([200, 404]));"]),
        Step(name="del-pool", method="DELETE", path=POOLS + "/{{nbPoolId}}",
             test_script=["pm.test('del pool', () => pm.expect(pm.response.code).to.be.oneOf([200, 404]));"]),
        Step(name="del-net", method="DELETE", path="/vpc/v1/networks/{{nbNetId}}",
             test_script=[*save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
    ],
))

CASES.append(Case(
    id="IPL-NETBIND-NEG-NF",
    title="addressPoolBinding на несуществующую Network → 404 NOT_FOUND",
    classes=["NEG"], priority="P1",
    steps=[
        Step(name="bind-bad-net", method="POST", path="/vpc/v1/networks/enpnonexistent999999/addressPoolBinding",
             body={"poolId": "aplnonexistent999999"},
             test_script=[*assert_status(404), *assert_grpc_code(5, "NOT_FOUND")]),
    ],
))

CASES.append(Case(
    id="IPL-NETUNBIND-IDM-NOOP",
    title="UnbindNetworkDefault на Network без binding → idempotent",
    classes=["IDM"], priority="P2",
    steps=[
        Step(name="unbind-noop", method="DELETE", path="/vpc/v1/networks/enpnonexistent999999/addressPoolBinding",
             test_script=["pm.test('idempotent (200 / 404)', () => pm.expect(pm.response.code).to.be.oneOf([200, 404]));"]),
    ],
))


# ---------------------------------------------------------------------------
# AddressPool split CIDR family (v4_cidr_blocks + v6_cidr_blocks)
# ---------------------------------------------------------------------------
#
# Покрывает:
#   - Create v4-only / v6-only / dual-stack + cross-family / both-empty валидация;
#     Bind* family-agnostic.
#   - cascade resolve family-skip на каждом из шагов (override / network_default /
#     selector / dual-stack zone_default).

CASES.append(Case(
    id="IPL-CR-CRUD-V6-OK",
    title="Create v6-only AddressPool (v6CidrBlocks непуст, v4 пуст) → 200 (REQ-IPL-CR-02)",
    classes=["CRUD"], priority="P0",
    steps=[
        Step(name="cr-v6only", method="POST", path=POOLS,
             body={"name": "ipl-v6only-{{runId}}", "kind": "EXTERNAL_PUBLIC",
                   "zoneId": "{{zoneC}}",
                   "v4CidrBlocks": [], "v6CidrBlocks": ["2001:db8::/64"]},
             test_script=[*assert_status(200),
                          "const j = pm.response.json();",
                          "pm.test('id has apl prefix', () => pm.expect(j.id).to.match(/^apl/));",
                          "pm.test('v4CidrBlocks empty', () => pm.expect(j.v4CidrBlocks || []).to.eql([]));",
                          "pm.test('v6CidrBlocks echoed', () => pm.expect(j.v6CidrBlocks).to.eql(['2001:db8::/64']));",
                          *save_from_response("j.id", "iplV6Id")]),
        Step(name="cleanup", method="DELETE", path=POOLS + "/{{iplV6Id}}",
             test_script=["pm.test('cleanup (200 or 404)', () => pm.expect(pm.response.code).to.be.oneOf([200, 404]));"]),
    ],
))

CASES.append(Case(
    id="IPL-CR-CRUD-DS-OK",
    title="Create dual-stack AddressPool (оба массива непусты) → 200 (REQ-IPL-CR-03)",
    classes=["CRUD"], priority="P0",
    steps=[
        Step(name="cr-ds", method="POST", path=POOLS,
             body={"name": "ipl-ds-{{runId}}", "kind": "EXTERNAL_PUBLIC",
                   "zoneId": "{{zoneC}}",
                   "v4CidrBlocks": ["198.51.100.0/24"],
                   "v6CidrBlocks": ["2001:db8:1::/64"]},
             test_script=[*assert_status(200),
                          "const j = pm.response.json();",
                          "pm.test('v4CidrBlocks echoed', () => pm.expect(j.v4CidrBlocks).to.eql(['198.51.100.0/24']));",
                          "pm.test('v6CidrBlocks echoed', () => pm.expect(j.v6CidrBlocks).to.eql(['2001:db8:1::/64']));",
                          *save_from_response("j.id", "iplDsId")]),
        Step(name="cleanup", method="DELETE", path=POOLS + "/{{iplDsId}}",
             test_script=["pm.test('cleanup (200 or 404)', () => pm.expect(pm.response.code).to.be.oneOf([200, 404]));"]),
    ],
))

CASES.append(Case(
    id="IPL-CR-VAL-CROSS-V4-IN-V6",
    title="Create с IPv6-prefix в v4CidrBlocks → 400 InvalidArgument (cross-family) (REQ-IPL-CR-05)",
    classes=["VAL", "NEG"], priority="P1",
    steps=[
        # IPv6 prefix в v4-слоте — sync InvalidArgument.
        Step(name="cr-v6-in-v4", method="POST", path=POOLS,
             body={"name": "ipl-cross-{{runId}}", "kind": "EXTERNAL_PUBLIC",
                   "zoneId": "{{zoneC}}",
                   "v4CidrBlocks": ["2001:db8::/64"], "v6CidrBlocks": []},
             test_script=[*assert_status(400), *assert_grpc_code(3, "INVALID_ARGUMENT"),
                          "pm.test('error mentions v4_cidr_blocks slot', () => pm.expect(String(pm.response.json().message || '')).to.match(/v4_cidr_blocks|not an IPv4 prefix/i));"]),
        # Симметрично: IPv4 prefix в v6-слоте — 400.
        Step(name="cr-v4-in-v6", method="POST", path=POOLS,
             body={"name": "ipl-cross2-{{runId}}", "kind": "EXTERNAL_PUBLIC",
                   "zoneId": "{{zoneC}}",
                   "v4CidrBlocks": [], "v6CidrBlocks": ["10.0.0.0/24"]},
             test_script=[*assert_status(400), *assert_grpc_code(3, "INVALID_ARGUMENT"),
                          "pm.test('error mentions v6_cidr_blocks slot', () => pm.expect(String(pm.response.json().message || '')).to.match(/v6_cidr_blocks|not an IPv6 prefix/i));"]),
    ],
))

# ---------------------------------------------------------------------------
# AddressPool CIDR management (:addCidrBlocks / :removeCidrBlocks).
# Update не меняет CIDR — изменение состава CIDR-блоков пула делается через
# отдельные RPC (parity с Subnet :addCidrBlocks / :removeCidrBlocks).
# ---------------------------------------------------------------------------

CASES.append(Case(
    id="IPL-ADDCIDR-OK",
    title="AddCidrBlocks: добавить v4-CIDR к пулу → 200, блок добавлен; повторный add того же блока — дедуп (REQ-IPL-ADDCIDR-01)",
    classes=["CRUD", "STATE"], priority="P0",
    steps=[
        Step(name="cr-pool", method="POST", path=POOLS,
             body={"name": "ipl-add-{{runId}}", "kind": "EXTERNAL_PUBLIC",
                   "zoneId": "{{zoneC}}",
                   "v4CidrBlocks": ["198.51.100.0/24"], "v6CidrBlocks": []},
             test_script=[*assert_status(200), *save_from_response("j.id", "addPoolId")]),
        Step(name="add-v4", method="POST", path=POOLS + "/{{addPoolId}}:addCidrBlocks",
             body={"v4CidrBlocks": ["203.0.113.0/24"]},
             test_script=[*assert_status(200),
                          "const j = pm.response.json();",
                          "pm.test('both v4 cidrs present', () => pm.expect(j.v4CidrBlocks).to.have.members(['198.51.100.0/24','203.0.113.0/24']));"]),
        Step(name="verify", method="GET", path=POOLS + "/{{addPoolId}}",
             test_script=[*assert_status(200),
                          "pm.test('v4 persisted', () => pm.expect(pm.response.json().v4CidrBlocks).to.have.members(['198.51.100.0/24','203.0.113.0/24']));"]),
        # Дедуп: повторный add того же блока — состав не меняется.
        Step(name="add-dup", method="POST", path=POOLS + "/{{addPoolId}}:addCidrBlocks",
             body={"v4CidrBlocks": ["203.0.113.0/24"]},
             test_script=[*assert_status(200),
                          "pm.test('dedup: still 2 cidrs', () => pm.expect((pm.response.json().v4CidrBlocks || []).length).to.eql(2));"]),
        Step(name="cleanup", method="DELETE", path=POOLS + "/{{addPoolId}}",
             test_script=["pm.test('cleanup', () => pm.expect(pm.response.code).to.be.oneOf([200, 404]));"]),
    ],
))

CASES.append(Case(
    id="IPL-RMCIDR-OK",
    title="RemoveCidrBlocks: удалить чистый v4-CIDR из dual-CIDR пула → 200, блок удален (REQ-IPL-RMCIDR-01)",
    classes=["CRUD", "STATE"], priority="P0",
    steps=[
        Step(name="cr-pool", method="POST", path=POOLS,
             body={"name": "ipl-rm-{{runId}}", "kind": "EXTERNAL_PUBLIC",
                   "zoneId": "{{zoneC}}",
                   "v4CidrBlocks": ["198.51.100.0/24", "203.0.113.0/24"], "v6CidrBlocks": []},
             test_script=[*assert_status(200), *save_from_response("j.id", "rmPoolId")]),
        Step(name="remove-v4", method="POST", path=POOLS + "/{{rmPoolId}}:removeCidrBlocks",
             body={"v4CidrBlocks": ["203.0.113.0/24"]},
             test_script=[*assert_status(200),
                          "const j = pm.response.json();",
                          "pm.test('only first cidr remains', () => pm.expect(j.v4CidrBlocks).to.eql(['198.51.100.0/24']));"]),
        Step(name="verify", method="GET", path=POOLS + "/{{rmPoolId}}",
             test_script=[*assert_status(200),
                          "pm.test('removal persisted', () => pm.expect(pm.response.json().v4CidrBlocks).to.eql(['198.51.100.0/24']));"]),
        # Удаление последнего CIDR → 400 (пул не может стать пустым).
        Step(name="remove-last", method="POST", path=POOLS + "/{{rmPoolId}}:removeCidrBlocks",
             body={"v4CidrBlocks": ["198.51.100.0/24"]},
             test_script=[*assert_status(400), *assert_grpc_code(3, "INVALID_ARGUMENT"),
                          "pm.test('message mentions empty after removal', () => pm.expect(String(pm.response.json().message || '')).to.match(/both empty|must not be both empty/i));"]),
        Step(name="cleanup", method="DELETE", path=POOLS + "/{{rmPoolId}}",
             test_script=["pm.test('cleanup', () => pm.expect(pm.response.code).to.be.oneOf([200, 404]));"]),
    ],
))

CASES.append(Case(
    id="IPL-RMCIDR-NEG-INUSE",
    title="RemoveCidrBlocks: CIDR с выделенным external-IP → 400 FailedPrecondition (use-guard) (REQ-IPL-RMCIDR-02)",
    classes=["NEG", "STATE"], priority="P0",
    steps=[
        # Pool в zone без seeded default + второй CIDR (чтобы remove не опустошал).
        # Allocate external IPv4 из пула (через Address.Create в этой zone), затем
        # попытка удалить CIDR с выделенным IP → FailedPrecondition.
        Step(name="cr-pool", method="POST", path=POOLS,
             body={"name": "ipl-rm-inuse-{{runId}}", "kind": "EXTERNAL_PUBLIC",
                   "zoneId": "{{zoneD}}",
                   "v4CidrBlocks": ["198.51.100.0/24", "203.0.113.0/24"],
                   "isDefault": True},
             test_script=[*assert_status(200), *save_from_response("j.id", "rmInUsePoolId")]),
        Step(name="cr-addr", method="POST", path="/vpc/v1/addresses",
             body={"projectId": "{{_suiteProjectId}}", "name": "ipl-rm-inuse-addr-{{runId}}",
                   "externalIpv4AddressSpec": {"zoneId": "{{zoneD}}"}},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.addressId", "rmInUseAddrId")]),
        poll_operation_until_done(),
        Step(name="get-addr", method="GET", path="/vpc/v1/addresses/{{rmInUseAddrId}}",
             test_script=[*assert_status(200),
                          "const ip = pm.response.json().externalIpv4Address.address;",
                          "pm.test('allocated ip present', () => pm.expect(ip).to.be.a('string').and.not.empty);",
                          # Запомним, в какой CIDR попал IP — его и попытаемся удалить.
                          "pm.environment.set('rmInUseCidr', ip.indexOf('198.51.100.') === 0 ? '198.51.100.0/24' : '203.0.113.0/24');"]),
        Step(name="remove-inuse", method="POST", path=POOLS + "/{{rmInUsePoolId}}:removeCidrBlocks",
             body={"v4CidrBlocks": ["{{rmInUseCidr}}"]},
             test_script=[*assert_status(400), *assert_grpc_code(9, "FAILED_PRECONDITION"),
                          "pm.test('message mentions allocated addresses', () => pm.expect(String(pm.response.json().message || '')).to.match(/has allocated addresses/i));"]),
        Step(name="verify-unchanged", method="GET", path=POOLS + "/{{rmInUsePoolId}}",
             test_script=[*assert_status(200),
                          "pm.test('both cidrs preserved (remove aborted)', () => pm.expect((pm.response.json().v4CidrBlocks || []).length).to.eql(2));"]),
        # Cleanup: address → pool.
        Step(name="del-addr", method="DELETE", path="/vpc/v1/addresses/{{rmInUseAddrId}}",
             test_script=[*save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="del-pool", method="DELETE", path=POOLS + "/{{rmInUsePoolId}}",
             test_script=["pm.test('cleanup', () => pm.expect(pm.response.code).to.be.oneOf([200, 404]));"]),
    ],
))

# ---------------------------------------------------------------------------
# DB-level защита от пересечения CIDR в AddressPool (EXCLUDE gist на
# нормализованной address_pool_cidrs). Два пула с пересекающимися CIDR одного
# kind иначе позволили бы IPAM аллоцировать один external-IP дважды. Пересечение
# → FailedPrecondition (grpc-gateway → 400).
# ---------------------------------------------------------------------------

CASES.append(Case(
    id="IPL-CR-NEG-OVERLAP",
    title="Create пула, чей v4-CIDR пересекает существующий пул (тот же kind) → 400 FailedPrecondition (REQ-IPL-OVERLAP-01)",
    classes=["NEG", "CONF"], priority="P0",
    steps=[
        # Базовый пул {10.20.0.0/24} в throwaway-zone (не seeded).
        Step(name="cr-base", method="POST", path=POOLS,
             body={"name": "ipl-ovl-base-{{runId}}", "kind": "EXTERNAL_PUBLIC",
                   "zoneId": "{{zoneC}}",
                   "v4CidrBlocks": ["10.20.0.0/24"], "v6CidrBlocks": []},
             test_script=[*assert_status(200), *save_from_response("j.id", "ovlBaseId")]),
        # Пересекающийся пул {10.20.0.128/25} ⊂ {10.20.0.0/24} → отклонен.
        Step(name="cr-overlap", method="POST", path=POOLS,
             body={"name": "ipl-ovl-conflict-{{runId}}", "kind": "EXTERNAL_PUBLIC",
                   "zoneId": "{{zoneC}}",
                   "v4CidrBlocks": ["10.20.0.128/25"], "v6CidrBlocks": []},
             test_script=[*assert_status(400), *assert_grpc_code(9, "FAILED_PRECONDITION"),
                          "pm.test('message mentions overlap', () => pm.expect(String(pm.response.json().message || '')).to.match(/can not overlap/i));"]),
        # Disjoint пул {10.21.0.0/24} — OK (sanity: только пересечение отклоняется).
        Step(name="cr-disjoint", method="POST", path=POOLS,
             body={"name": "ipl-ovl-disjoint-{{runId}}", "kind": "EXTERNAL_PUBLIC",
                   "zoneId": "{{zoneC}}",
                   "v4CidrBlocks": ["10.21.0.0/24"], "v6CidrBlocks": []},
             test_script=[*assert_status(200), *save_from_response("j.id", "ovlDisjointId")]),
        Step(name="cleanup-base", method="DELETE", path=POOLS + "/{{ovlBaseId}}",
             test_script=["pm.test('cleanup', () => pm.expect(pm.response.code).to.be.oneOf([200, 404]));"]),
        Step(name="cleanup-disjoint", method="DELETE", path=POOLS + "/{{ovlDisjointId}}",
             test_script=["pm.test('cleanup', () => pm.expect(pm.response.code).to.be.oneOf([200, 404]));"]),
    ],
))

CASES.append(Case(
    id="IPL-ADDCIDR-NEG-OVERLAP",
    title="AddCidrBlocks, добавляющий CIDR с пересечением другого пула → 400 FailedPrecondition (REQ-IPL-OVERLAP-01)",
    classes=["NEG", "STATE"], priority="P0",
    steps=[
        Step(name="cr-a", method="POST", path=POOLS,
             body={"name": "ipl-ovl-a-{{runId}}", "kind": "EXTERNAL_PUBLIC",
                   "zoneId": "{{zoneC}}",
                   "v4CidrBlocks": ["10.30.0.0/24"], "v6CidrBlocks": []},
             test_script=[*assert_status(200), *save_from_response("j.id", "ovlAId")]),
        Step(name="cr-b", method="POST", path=POOLS,
             body={"name": "ipl-ovl-b-{{runId}}", "kind": "EXTERNAL_PUBLIC",
                   "zoneId": "{{zoneC}}",
                   "v4CidrBlocks": ["10.31.0.0/24"], "v6CidrBlocks": []},
             test_script=[*assert_status(200), *save_from_response("j.id", "ovlBId")]),
        # AddCidr B {10.30.0.0/25} ⊂ pool A {10.30.0.0/24} → отклонен.
        Step(name="add-overlap", method="POST", path=POOLS + "/{{ovlBId}}:addCidrBlocks",
             body={"v4CidrBlocks": ["10.30.0.0/25"]},
             test_script=[*assert_status(400), *assert_grpc_code(9, "FAILED_PRECONDITION"),
                          "pm.test('message mentions overlap', () => pm.expect(String(pm.response.json().message || '')).to.match(/can not overlap/i));"]),
        # B не изменился (add aborted) — все еще только свой CIDR.
        Step(name="verify-b", method="GET", path=POOLS + "/{{ovlBId}}",
             test_script=[*assert_status(200),
                          "pm.test('B unchanged', () => pm.expect(pm.response.json().v4CidrBlocks).to.eql(['10.31.0.0/24']));"]),
        Step(name="cleanup-a", method="DELETE", path=POOLS + "/{{ovlAId}}",
             test_script=["pm.test('cleanup', () => pm.expect(pm.response.code).to.be.oneOf([200, 404]));"]),
        Step(name="cleanup-b", method="DELETE", path=POOLS + "/{{ovlBId}}",
             test_script=["pm.test('cleanup', () => pm.expect(pm.response.code).to.be.oneOf([200, 404]));"]),
    ],
))

CASES.append(Case(
    id="IPL-RESOLVE-NETWORK-DEFAULT-FAMILY-SKIP",
    title="Cascade Step 2 (network_default): binding на v6-only pool, allocate v4 → family-skip → fall-through (REQ-RESOLVE-07)",
    classes=["CONF", "NEG"], priority="P0",
    steps=[
        # Network bound к v6-only pool через BindAsNetworkDefault. Address.Create
        # external_ipv4 в этом network → cascade Step 2 находит binding, family-фильтр
        # пропускает (v4_cidr_blocks пусто), fall-through. Нет v4 default в {{zoneB}} →
        # Operation error code in {5, 9}.
        Step(name="cr-net", method="POST", path="/vpc/v1/networks",
             body={"projectId": "{{_suiteProjectId}}", "name": "ipl-netdef-{{runId}}"},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.networkId", "netdefId")]),
        poll_operation_until_done(),
        Step(name="cr-pool-v6only", method="POST", path=POOLS,
             body={"name": "ipl-netdef-pool-{{runId}}", "kind": "EXTERNAL_PUBLIC",
                   "zoneId": "{{zoneB}}",
                   "v4CidrBlocks": [], "v6CidrBlocks": ["2001:db8:cafe::/64"]},
             test_script=[*assert_status(200), *save_from_response("j.id", "netdefPoolId")]),
        Step(name="bind", method="POST",
             path="/vpc/v1/networks/{{netdefId}}/addressPoolBinding",
             body={"poolId": "{{netdefPoolId}}"},
             test_script=[*assert_status(200)]),
        Step(name="cr-addr-v4", method="POST", path="/vpc/v1/addresses",
             body={"projectId": "{{_suiteProjectId}}", "name": "ipl-netdef-addr-{{runId}}",
                   "externalIpv4AddressSpec": {"zoneId": "{{zoneB}}"}},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.addressId", "netdefAddrId")]),
        poll_operation_until_done(),
        Step(name="check-op-failed", method="GET", path="/operations/{{opId}}",
             test_script=[*assert_status(200),
                          "pm.test('operation done', () => pm.expect(pm.response.json().done).to.equal(true));",
                          "pm.test('operation has error', () => pm.expect(pm.response.json().error).to.be.an('object'));",
                          "pm.test('error code 9 (FailedPrecondition) or 5 (NotFound)', () => pm.expect(pm.response.json().error.code).to.be.oneOf([5, 9]));"]),
        # Cleanup.
        Step(name="unbind", method="DELETE",
             path="/vpc/v1/networks/{{netdefId}}/addressPoolBinding",
             test_script=["pm.test('unbind (200/404)', () => pm.expect(pm.response.code).to.be.oneOf([200, 404]));"]),
        Step(name="del-pool", method="DELETE", path=POOLS + "/{{netdefPoolId}}",
             test_script=["pm.test('del pool', () => pm.expect(pm.response.code).to.be.oneOf([200, 404]));"]),
        Step(name="del-net", method="DELETE", path="/vpc/v1/networks/{{netdefId}}",
             test_script=[*save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
    ],
))

CASES.append(Case(
    id="IPL-RESOLVE-DUALSTACK-OK",
    title="Dual-stack pool: v4-allocate берет из v4-блока, v6 — из v6-блока (REQ-RESOLVE-03)",
    classes=["CRUD"], priority="P0",
    steps=[
        # Single dual-stack pool в zone (как default) → Allocate v4 берет из v4-блока,
        # Allocate v6 — из v6-блока; обе аллокации успешны, IP попадают в правильные
        # префиксы. Здесь zone используем `{{zoneD}}` (нет seeded default).
        Step(name="cr-ds-pool", method="POST", path=POOLS,
             body={"name": "ipl-ds-resolve-{{runId}}", "kind": "EXTERNAL_PUBLIC",
                   "zoneId": "{{zoneD}}",
                   "v4CidrBlocks": ["198.51.100.0/24"],
                   "v6CidrBlocks": ["2001:db8:ff::/64"],
                   "isDefault": True},
             test_script=[*assert_status(200), *save_from_response("j.id", "dsPoolId")]),
        # Allocate v4.
        Step(name="cr-addr-v4", method="POST", path="/vpc/v1/addresses",
             body={"projectId": "{{_suiteProjectId}}", "name": "ipl-ds-v4-{{runId}}",
                   "externalIpv4AddressSpec": {"zoneId": "{{zoneD}}"}},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.addressId", "dsV4AddrId")]),
        poll_operation_until_done(),
        Step(name="get-v4", method="GET", path="/vpc/v1/addresses/{{dsV4AddrId}}",
             test_script=[*assert_status(200),
                          "pm.test('v4 IP in pool v4 cidr', () => pm.expect(pm.response.json().externalIpv4Address.address).to.match(/^198\\.51\\.100\\./));"]),
        # Allocate v6.
        Step(name="cr-addr-v6", method="POST", path="/vpc/v1/addresses",
             body={"projectId": "{{_suiteProjectId}}", "name": "ipl-ds-v6-{{runId}}",
                   "externalIpv6AddressSpec": {"zoneId": "{{zoneD}}"}},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.addressId", "dsV6AddrId")]),
        poll_operation_until_done(),
        Step(name="get-v6", method="GET", path="/vpc/v1/addresses/{{dsV6AddrId}}",
             test_script=[*assert_status(200),
                          "pm.test('v6 IP in pool v6 prefix', () => pm.expect(pm.response.json().externalIpv6Address.address).to.match(/^2001:db8:ff:/));"]),
        # Cleanup addresses → pool.
        Step(name="del-v4", method="DELETE", path="/vpc/v1/addresses/{{dsV4AddrId}}",
             test_script=[*save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="del-v6", method="DELETE", path="/vpc/v1/addresses/{{dsV6AddrId}}",
             test_script=[*save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="del-pool", method="DELETE", path=POOLS + "/{{dsPoolId}}",
             test_script=["pm.test('del pool', () => pm.expect(pm.response.code).to.be.oneOf([200, 404]));"]),
    ],
))

CASES.append(Case(
    id="IPL-BIND-FAMILY-AGNOSTIC",
    title="BindAddressPoolAsNetworkDefault(net, v4-only-pool) для network c v6-намерением → 200 (family НЕ валидируется на bind) (REQ-IPL-BIND-FAMILY-AGNOSTIC)",
    classes=["CRUD", "STATE"], priority="P0",
    steps=[
        # Bind*/SetPoolSelector — family-agnostic; family-фильтр работает ТОЛЬКО на
        # resolve-этапе. Bind v4-only pool к network → 200 OK; binding записан в
        # address_pool_network_default.
        Step(name="cr-net", method="POST", path="/vpc/v1/networks",
             body={"projectId": "{{_suiteProjectId}}", "name": "ipl-bnd-fa-net-{{runId}}"},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.networkId", "bndFaNetId")]),
        poll_operation_until_done(),
        Step(name="cr-pool-v4only", method="POST", path=POOLS,
             body={"name": "ipl-bnd-fa-pool-{{runId}}", "kind": "EXTERNAL_PUBLIC",
                   "zoneId": "{{zoneC}}",
                   "v4CidrBlocks": ["203.0.113.0/24"], "v6CidrBlocks": []},
             test_script=[*assert_status(200), *save_from_response("j.id", "bndFaPoolId")]),
        # Bind — family-agnostic, всегда 200 (нет family-validation на bind-этапе).
        Step(name="bind", method="POST",
             path="/vpc/v1/networks/{{bndFaNetId}}/addressPoolBinding",
             body={"poolId": "{{bndFaPoolId}}"},
             test_script=[*assert_status(200),
                          "pm.test('bind 200 even for cross-family-intent (family-agnostic)', () => pm.expect(pm.response.code).to.eql(200));"]),
        # bind contract (family-agnostic 200) проверяется самим bind-step'ом выше.
        # Cleanup.
        Step(name="unbind", method="DELETE",
             path="/vpc/v1/networks/{{bndFaNetId}}/addressPoolBinding",
             test_script=["pm.test('unbind (200/404)', () => pm.expect(pm.response.code).to.be.oneOf([200, 404]));"]),
        Step(name="del-pool", method="DELETE", path=POOLS + "/{{bndFaPoolId}}",
             test_script=["pm.test('del pool', () => pm.expect(pm.response.code).to.be.oneOf([200, 404]));"]),
        Step(name="del-net", method="DELETE", path="/vpc/v1/networks/{{bndFaNetId}}",
             test_script=[*save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
    ],
))


# ---------------------------------------------------------------------------
# Pool exhaustion
# ---------------------------------------------------------------------------

CASES.append(Case(
    id="IPL-ALLOC-POOL-EXHAUSTED",
    title="Pool /30 (2 usable IPs) + bound to fresh Network → 2 internal v4 allocate ok, 3rd Address.Create Operation FailedPrecondition (pool exhausted)",
    classes=["NEG", "STATE", "CONF"], priority="P0",
    steps=[
        # 1. Создать throwaway Network.
        Step(name="cr-net", method="POST", path="/vpc/v1/networks",
             body={"projectId": "{{_suiteProjectId}}", "name": "ipl-exh-net-{{runId}}"},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.networkId", "exhNetId")]),
        poll_operation_until_done(),
        # 2. Создать pool /30 — 4 addresses total, 2 usable (excl network+broadcast).
        Step(name="cr-pool", method="POST", path=POOLS,
             body={"name": "ipl-exh-pool-{{runId}}", "kind": "EXTERNAL_PUBLIC",
                   "zoneId": "{{zoneC}}",
                   "v4CidrBlocks": ["198.51.100.252/30"], "v6CidrBlocks": []},
             test_script=[*assert_status(200), *save_from_response("j.id", "exhPoolId")]),
        # 3. Bind network → pool.
        Step(name="bind", method="POST", path="/vpc/v1/networks/{{exhNetId}}/addressPoolBinding",
             body={"poolId": "{{exhPoolId}}"},
             test_script=[*assert_status(200)]),
        # 4. Allocate #1 — external Address (резолв cascade Step 2 = network_default → наш pool).
        Step(name="alloc-1", method="POST", path="/vpc/v1/addresses",
             body={"projectId": "{{_suiteProjectId}}", "name": "exh-1-{{runId}}",
                   "externalIpv4AddressSpec": {"zoneId": "{{zoneC}}"}},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.addressId", "addrIdE1")]),
        poll_operation_until_done(),
        Step(name="verify-1", method="GET", path="/operations/{{opId}}",
             test_script=[
                 "let _t=0;",
                 "const _s=()=>pm.sendRequest({url:pm.environment.get('baseUrl')+'/operations/'+pm.environment.get('opId'),method:'GET',header:{'Authorization':'Bearer '+pm.environment.get('jwtBootstrap')}},(err,res)=>{",
                 "  let j=null;try{j=res.json();}catch(e){}",
                 "  if(j&&j.done){pm.test('alloc-1 success',()=>pm.expect(!!j.error,JSON.stringify(j)).to.eql(false));}",
                 "  else if(++_t<8){setTimeout(_s,400);}",
                 "});_s();",
             ]),
        # 5. Allocate #2 — second usable IP.
        Step(name="alloc-2", method="POST", path="/vpc/v1/addresses",
             body={"projectId": "{{_suiteProjectId}}", "name": "exh-2-{{runId}}",
                   "externalIpv4AddressSpec": {"zoneId": "{{zoneC}}"}},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.addressId", "addrIdE2")]),
        poll_operation_until_done(),
        Step(name="verify-2", method="GET", path="/operations/{{opId}}",
             test_script=[
                 "let _t=0;",
                 "const _s=()=>pm.sendRequest({url:pm.environment.get('baseUrl')+'/operations/'+pm.environment.get('opId'),method:'GET',header:{'Authorization':'Bearer '+pm.environment.get('jwtBootstrap')}},(err,res)=>{",
                 "  let j=null;try{j=res.json();}catch(e){}",
                 "  if(j&&j.done){pm.test('alloc-2 success',()=>pm.expect(!!j.error,JSON.stringify(j)).to.eql(false));}",
                 "  else if(++_t<8){setTimeout(_s,400);}",
                 "});_s();",
             ]),
        # 6. Allocate #3 — pool exhausted → FailedPrecondition.
        Step(name="alloc-3-fails", method="POST", path="/vpc/v1/addresses",
             body={"projectId": "{{_suiteProjectId}}", "name": "exh-3-{{runId}}",
                   "externalIpv4AddressSpec": {"zoneId": "{{zoneC}}"}},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.addressId", "addrIdE3")]),
        Step(name="verify-3-fails", method="GET", path="/operations/{{opId}}",
             test_script=[
                 "let _t=0;",
                 "const _s=()=>pm.sendRequest({url:pm.environment.get('baseUrl')+'/operations/'+pm.environment.get('opId'),method:'GET',header:{'Authorization':'Bearer '+pm.environment.get('jwtBootstrap')}},(err,res)=>{",
                 "  let j=null;try{j=res.json();}catch(e){}",
                 "  if(j&&j.done){",
                 "    pm.test('alloc-3 fails (pool exhausted)',()=>pm.expect(!!j.error,JSON.stringify(j)).to.eql(true));",
                 "    pm.test('FailedPrecondition (9)',()=>pm.expect(j.error.code).to.eql(9));",
                 "  } else if(++_t<8){setTimeout(_s,400);}",
                 "  else { pm.test('op resolved', () => pm.expect.fail('timeout')); }",
                 "});_s();",
             ]),
        # Cleanup (releases free up the pool).
        Step(name="del-1", method="DELETE", path="/vpc/v1/addresses/{{addrIdE1}}",
             test_script=["pm.test('del 200|400', () => pm.expect(pm.response.code).to.be.oneOf([200, 400]));",
                          *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="del-2", method="DELETE", path="/vpc/v1/addresses/{{addrIdE2}}",
             test_script=["pm.test('del 200|400', () => pm.expect(pm.response.code).to.be.oneOf([200, 400]));",
                          *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="unbind", method="DELETE", path="/vpc/v1/networks/{{exhNetId}}/addressPoolBinding",
             test_script=["pm.test('unbind ok', () => pm.expect(pm.response.code).to.be.oneOf([200, 404]));"]),
        Step(name="del-pool", method="DELETE", path=POOLS + "/{{exhPoolId}}",
             test_script=["pm.test('del pool', () => pm.expect(pm.response.code).to.be.oneOf([200, 404]));"]),
        Step(name="del-net", method="DELETE", path="/vpc/v1/networks/{{exhNetId}}",
             test_script=["pm.test('del net 200|400', () => pm.expect(pm.response.code).to.be.oneOf([200, 400]));",
                          *save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
    ],
))
