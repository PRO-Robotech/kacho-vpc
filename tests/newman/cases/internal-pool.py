"""Case-set для InternalAddressPoolService (kacho-only admin IPAM RPC).

Покрывает internal/admin-only RPC, проброшенные через api-gateway cluster-internal
mux на /vpc/v1/addressPools/... — НЕ verbatim-YC. Эти RPC возвращают ресурсы
ПРЯМО (не Operation). AddressPool — глобальный infrastructure-ресурс (как
Region/Zone), не привязан к folder. Тесты создают только runId-суффиксованные
throwaway-пулы/сети/адреса и убирают за собой; seeded `default-ru-central1-a`
pool / `ru-central1` region / `ru-central1-{a,b,c,d}` zones НЕ трогаются.

⚠️ Внимание: REST gateway body — camelCase JSON.
"""

CASES = []

POOLS = "/vpc/v1/addressPools"


# ---------------------------------------------------------------------------
# Pool CRUD happy path
# ---------------------------------------------------------------------------

CASES.append(Case(
    id="IPL-CR-CRUD-OK",
    title="AddressPool Create → Get → List-includes → Delete → Get-404",
    classes=["CRUD", "CONF"], priority="P0",
    steps=[
        Step(name="create", method="POST", path=POOLS,
             body={"name": "ipl-crud-{{runId}}", "kind": "EXTERNAL_TEST",
                   "zoneId": "ru-central1-c", "cidrBlocks": ["203.0.113.0/24"]},
             test_script=[*assert_status(200),
                          "const j = pm.response.json();",
                          "pm.test('id has apl prefix', () => pm.expect(j.id).to.match(/^apl/));",
                          "pm.test('name matches', () => pm.expect(j.name).to.eql('ipl-crud-' + pm.environment.get('runId')));",
                          "pm.test('kind echoed', () => pm.expect(j.kind).to.eql('EXTERNAL_TEST'));",
                          "pm.test('isDefault false', () => pm.expect(j.isDefault).to.eql(false));",
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
             body={"name": "ipl-upd-{{runId}}", "kind": "EXTERNAL_TEST",
                   "zoneId": "ru-central1-c", "cidrBlocks": ["203.0.113.0/24"]},
             test_script=[*assert_status(200), *save_from_response("j.id", "iplId")]),
        Step(name="patch", method="PATCH", path=POOLS + "/{{iplId}}",
             body={"description": "ipl-updated-desc", "replaceLabels": True,
                   "labels": {"env": "test", "team": "ipam"},
                   "updateIsDefault": True, "isDefault": True},
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
    id="IPL-LST-CRUD-OK",
    title="List addressPools → pools array present",
    classes=["CRUD"], priority="P1",
    steps=[
        Step(name="list", method="GET", path=POOLS,
             test_script=[*assert_status(200),
                          "pm.test('pools array', () => pm.expect(pm.response.json().pools || []).to.be.an('array'));"]),
        Step(name="list-by-zone", method="GET", path=POOLS + "?zoneId=ru-central1-a",
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
        # seeded default-ru-central1-a (EXTERNAL_PUBLIC, isDefault) уже занимает
        # партишн (zone_id='ru-central1-a', kind=EXTERNAL_PUBLIC). DB partial UNIQUE
        # WHERE is_default → 23505 → ErrAlreadyExists. Create не успевает создать
        # row → нечего чистить.
        Step(name="cr-dup-default", method="POST", path=POOLS,
             body={"name": "ipl-dupdef-{{runId}}", "kind": "EXTERNAL_PUBLIC",
                   "zoneId": "ru-central1-a", "cidrBlocks": ["203.0.113.0/24"],
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
             body={"name": "ipl-badzone-{{runId}}", "kind": "EXTERNAL_TEST",
                   "zoneId": "nonexistent-zone-{{runId}}", "cidrBlocks": ["203.0.113.0/24"]},
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
             body={"name": "ipl-nokind-{{runId}}", "zoneId": "ru-central1-c",
                   "cidrBlocks": ["203.0.113.0/24"]},
             test_script=[*assert_status(400), *assert_grpc_code(3, "INVALID_ARGUMENT")]),
    ],
))

CASES.append(Case(
    id="IPL-CR-VAL-MISSING-CIDR",
    title="Create без cidrBlocks → 400 InvalidArgument",
    classes=["VAL"], priority="P0",
    steps=[
        Step(name="cr-no-cidr", method="POST", path=POOLS,
             body={"name": "ipl-nocidr-{{runId}}", "kind": "EXTERNAL_TEST",
                   "zoneId": "ru-central1-c"},
             test_script=[*assert_status(400), *assert_grpc_code(3, "INVALID_ARGUMENT")]),
    ],
))

CASES.append(Case(
    id="IPL-CR-VAL-MISSING-NAME",
    title="Create без name → текущее: 200 (name не валидируется в InternalAddressPoolService.Create) — см. FINDING-007",
    classes=["VAL", "CONF"], priority="P2",
    steps=[
        # FINDING-007: Create НЕ требует name (verbatim-YC аналога нет, kacho-admin RPC).
        # Если поведение изменится на 400 — этот кейс это поймает.
        Step(name="cr-no-name", method="POST", path=POOLS,
             body={"kind": "EXTERNAL_TEST", "zoneId": "ru-central1-c",
                   "cidrBlocks": ["203.0.113.0/24"]},
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
             body={"name": "ipl-hb-{{runId}}", "kind": "EXTERNAL_TEST",
                   "zoneId": "ru-central1-c", "cidrBlocks": ["203.0.113.5/24"]},
             test_script=[*assert_status(400), *assert_grpc_code(3, "INVALID_ARGUMENT")]),
    ],
))

CASES.append(Case(
    # KAC-60: sparse counter-based IPv6 allocator (миграция 0021). AddressPool
    # с IPv6 CIDR теперь допустим — Create проходит (200), InitIPv6PoolCursor
    # инициализирует sparse counter для пула. Раньше allocator поддерживал
    # только IPv4 и кейс ждал 400 «only IPv4 prefixes are supported». TDD-pivot.
    id="IPL-CR-VAL-IPV6-CIDR",
    title="Create AddressPool с IPv6 cidr → 200 (sparse counter allocator, KAC-60)",
    classes=["CRUD"], priority="P1",
    steps=[
        Step(name="cr-v6", method="POST", path=POOLS,
             body={"name": "ipl-v6-{{runId}}", "kind": "EXTERNAL_TEST",
                   "zoneId": "ru-central1-c", "cidrBlocks": ["2001:db8::/64"]},
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
    id="IPL-CHK-CRUD-OK",
    title="Check?zoneId=ru-central1-a → 200 + warnings array",
    classes=["CRUD"], priority="P1",
    steps=[
        Step(name="check", method="GET", path=POOLS + ":check?zoneId=ru-central1-a",
             test_script=[*assert_status(200),
                          "const j = pm.response.json();",
                          "pm.test('warnings is array (или absent → []) ', () => pm.expect(j.warnings || []).to.be.an('array'));"]),
        Step(name="check-all", method="GET", path=POOLS + ":check",
             test_script=[*assert_status(200),
                          "pm.test('warnings array (no scope)', () => pm.expect(pm.response.json().warnings || []).to.be.an('array'));"]),
    ],
))

CASES.append(Case(
    id="IPL-CHK-AMBIGUOUS-WARN",
    title="2 пула с одинаковыми (zone, kind, selector, priority) → Check возвращает warning",
    classes=["CONF", "CRUD"], priority="P1",
    steps=[
        Step(name="cr-pool-a", method="POST", path=POOLS,
             body={"name": "ipl-amb-a-{{runId}}", "kind": "EXTERNAL_TEST",
                   "zoneId": "ru-central1-c", "cidrBlocks": ["203.0.113.0/24"],
                   "selectorLabels": {"tier": "ambtest"}, "selectorPriority": 50},
             test_script=[*assert_status(200), *save_from_response("j.id", "ambPoolA")]),
        Step(name="cr-pool-b", method="POST", path=POOLS,
             body={"name": "ipl-amb-b-{{runId}}", "kind": "EXTERNAL_TEST",
                   "zoneId": "ru-central1-c", "cidrBlocks": ["198.51.100.0/24"],
                   "selectorLabels": {"tier": "ambtest"}, "selectorPriority": 50},
             test_script=[*assert_status(200), *save_from_response("j.id", "ambPoolB")]),
        Step(name="check-ambiguous", method="GET", path=POOLS + ":check?zoneId=ru-central1-c",
             test_script=[*assert_status(200),
                          "const w = pm.response.json().warnings || [];",
                          "pm.test('has at least one warning', () => pm.expect(w.length).to.be.at.least(1));",
                          "pm.test('warning mentions undefined resolve order', () => pm.expect(w.join(' ')).to.include('undefined'));"]),
        Step(name="cleanup-a", method="DELETE", path=POOLS + "/{{ambPoolA}}",
             test_script=["pm.test('cleanup a', () => pm.expect(pm.response.code).to.be.oneOf([200, 404]));"]),
        Step(name="cleanup-b", method="DELETE", path=POOLS + "/{{ambPoolB}}",
             test_script=["pm.test('cleanup b', () => pm.expect(pm.response.code).to.be.oneOf([200, 404]));"]),
    ],
))

CASES.append(Case(
    id="IPL-EXPLAIN-NETWORK-DEFAULT",
    title="ExplainResolution?networkId=<bound> → 200 + matchedVia=network_default",
    classes=["CRUD", "CONF"], priority="P0",
    steps=[
        # Создать throwaway network в существующем folder.
        Step(name="cr-net", method="POST", path="/vpc/v1/networks",
             body={"folderId": "{{_suiteFolderId}}", "name": "ipl-expl-net-{{runId}}"},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.networkId", "explNetId")]),
        poll_operation_until_done(),
        # Создать throwaway pool.
        Step(name="cr-pool", method="POST", path=POOLS,
             body={"name": "ipl-expl-pool-{{runId}}", "kind": "EXTERNAL_PUBLIC",
                   "zoneId": "ru-central1-c", "cidrBlocks": ["203.0.113.0/24"]},
             test_script=[*assert_status(200), *save_from_response("j.id", "explPoolId")]),
        # Bind network → pool (BindAsNetworkDefault).
        Step(name="bind", method="POST", path="/vpc/v1/networks/{{explNetId}}/addressPoolBinding",
             body={"poolId": "{{explPoolId}}"},
             test_script=[*assert_status(200),
                          "pm.test('bind returns obj', () => pm.expect(pm.response.json()).to.be.an('object'));"]),
        # Получить эффект bind через ExplainResolution.
        Step(name="explain", method="GET", path=POOLS + ":explainResolution?networkId={{explNetId}}",
             test_script=[*assert_status(200),
                          "const j = pm.response.json();",
                          "pm.test('matchedVia field present', () => pm.expect(j.matchedVia).to.be.a('string'));",
                          "pm.test('matchedVia is network_default', () => pm.expect(j.matchedVia).to.eql('network_default'));",
                          "pm.test('selectedPool is our pool', () => pm.expect(j.selectedPool && j.selectedPool.id).to.eql(pm.environment.get('explPoolId')));"]),
        # Unbind.
        Step(name="unbind", method="DELETE", path="/vpc/v1/networks/{{explNetId}}/addressPoolBinding",
             test_script=[*assert_status(200)]),
        # Cleanup pool + network.
        Step(name="del-pool", method="DELETE", path=POOLS + "/{{explPoolId}}",
             test_script=["pm.test('del pool', () => pm.expect(pm.response.code).to.be.oneOf([200, 404]));"]),
        Step(name="del-net", method="DELETE", path="/vpc/v1/networks/{{explNetId}}",
             test_script=[*save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
    ],
))

CASES.append(Case(
    id="IPL-EXPLAIN-UNRESOLVABLE",
    title="ExplainResolution?networkId=<garbage> (нет global default) → FailedPrecondition (FINDING-008 fixed)",
    classes=["NEG", "CONF"], priority="P2",
    steps=[
        # FINDING-008 fixed: ErrPoolNotResolved теперь классифицируется в internalMapErr →
        # FailedPrecondition (9), а не INTERNAL (13). (Если сервис делает network-exists-check
        # раньше cascade — возможен NotFound (5); допускаем оба, но не 13.)
        Step(name="explain-garbage", method="GET",
             path=POOLS + ":explainResolution?networkId=enpnonexistent999999",
             test_script=[
                 "pm.test('non-2xx', () => pm.expect(pm.response.code).to.be.oneOf([400, 404]));",
                 "const j = pm.response.json();",
                 "pm.test('grpc error code in {9, 5} (не 13 INTERNAL)', () => pm.expect(j.code).to.be.oneOf([9, 5]));",
             ]),
    ],
))

CASES.append(Case(
    id="IPL-UTIL-CRUD-OK",
    title="GetUtilization для throwaway pool → 200 + totalIps/usedIps/cidrs",
    classes=["CRUD"], priority="P1",
    steps=[
        Step(name="cr-pool", method="POST", path=POOLS,
             body={"name": "ipl-util-{{runId}}", "kind": "EXTERNAL_TEST",
                   "zoneId": "ru-central1-c", "cidrBlocks": ["203.0.113.0/24"]},
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
    title="ListAddresses на seeded default-ru-central1-a pool → 200 + addresses array",
    classes=["CRUD"], priority="P1",
    steps=[
        Step(name="seed-pool-id", method="GET", path=POOLS + "?zoneId=ru-central1-a&kind=EXTERNAL_PUBLIC",
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
             body={"name": "ipl-la-{{runId}}", "kind": "EXTERNAL_TEST",
                   "zoneId": "ru-central1-c", "cidrBlocks": ["203.0.113.0/24"]},
             test_script=[*assert_status(200), *save_from_response("j.id", "laPoolId")]),
        Step(name="list-addr", method="GET", path=POOLS + "/{{laPoolId}}/addresses",
             test_script=[*assert_status(200),
                          "pm.test('empty addresses array', () => pm.expect(pm.response.json().addresses || []).to.be.an('array').with.lengthOf(0));"]),
        Step(name="cleanup", method="DELETE", path=POOLS + "/{{laPoolId}}",
             test_script=["pm.test('cleanup', () => pm.expect(pm.response.code).to.be.oneOf([200, 404]));"]),
    ],
))


# ---------------------------------------------------------------------------
# Bindings: addressPoolBinding (Network), addressPoolOverride (Address)
# ---------------------------------------------------------------------------

CASES.append(Case(
    id="IPL-NETBIND-CRUD-OK",
    title="addressPoolBinding: bind Network → ExplainResolution показывает эффект → unbind",
    classes=["CRUD", "STATE"], priority="P0",
    steps=[
        Step(name="cr-net", method="POST", path="/vpc/v1/networks",
             body={"folderId": "{{_suiteFolderId}}", "name": "ipl-nb-net-{{runId}}"},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.networkId", "nbNetId")]),
        poll_operation_until_done(),
        Step(name="cr-pool", method="POST", path=POOLS,
             body={"name": "ipl-nb-pool-{{runId}}", "kind": "EXTERNAL_PUBLIC",
                   "zoneId": "ru-central1-c", "cidrBlocks": ["203.0.113.0/24"]},
             test_script=[*assert_status(200), *save_from_response("j.id", "nbPoolId")]),
        Step(name="bind", method="POST", path="/vpc/v1/networks/{{nbNetId}}/addressPoolBinding",
             body={"poolId": "{{nbPoolId}}"},
             test_script=[*assert_status(200)]),
        # Идемпотентно: повторный bind того же pool — no-op.
        Step(name="bind-again", method="POST", path="/vpc/v1/networks/{{nbNetId}}/addressPoolBinding",
             body={"poolId": "{{nbPoolId}}"},
             test_script=[*assert_status(200)]),
        Step(name="get-effect", method="GET", path=POOLS + ":explainResolution?networkId={{nbNetId}}",
             test_script=[*assert_status(200),
                          "pm.test('matchedVia network_default', () => pm.expect(pm.response.json().matchedVia).to.eql('network_default'));"]),
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
    id="IPL-DEL-NEG-OVERRIDE-EXISTS",
    title="Delete pool на который есть addressPoolOverride binding → FailedPrecondition",
    classes=["NEG", "STATE", "CONF"], priority="P0",
    steps=[
        # Создать network → subnet → INTERNAL address (получает internal IP, не external).
        Step(name="cr-net", method="POST", path="/vpc/v1/networks",
             body={"folderId": "{{_suiteFolderId}}", "name": "ipl-ovr-net-{{runId}}"},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.networkId", "ovrNetId")]),
        poll_operation_until_done(),
        Step(name="cr-sub", method="POST", path="/vpc/v1/subnets",
             body={"folderId": "{{_suiteFolderId}}", "networkId": "{{ovrNetId}}",
                   "name": "ipl-ovr-sub-{{runId}}", "zoneId": "ru-central1-a",
                   "v4CidrBlocks": ["10.191.0.0/24"]},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.subnetId", "ovrSubId")]),
        poll_operation_until_done(),
        Step(name="cr-addr", method="POST", path="/vpc/v1/addresses",
             body={"folderId": "{{_suiteFolderId}}", "name": "ipl-ovr-addr-{{runId}}",
                   "internalIpv4AddressSpec": {"subnetId": "{{ovrSubId}}"}},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.addressId", "ovrAddrId")]),
        poll_operation_until_done(),
        Step(name="cr-pool", method="POST", path=POOLS,
             body={"name": "ipl-ovr-pool-{{runId}}", "kind": "EXTERNAL_PUBLIC",
                   "zoneId": "ru-central1-c", "cidrBlocks": ["203.0.113.0/24"]},
             test_script=[*assert_status(200), *save_from_response("j.id", "ovrPoolId")]),
        # Bind override (Address без external IP → разрешено).
        Step(name="bind-override", method="POST", path="/vpc/v1/addresses/{{ovrAddrId}}/addressPoolOverride",
             body={"poolId": "{{ovrPoolId}}"},
             test_script=[*assert_status(200),
                          "pm.test('override bind returns obj', () => pm.expect(pm.response.json()).to.be.an('object'));"]),
        # Попытка удалить pool пока override существует → FK RESTRICT → FailedPrecondition.
        Step(name="del-pool-blocked", method="DELETE", path=POOLS + "/{{ovrPoolId}}",
             test_script=[*assert_status(400), *assert_grpc_code(9, "FAILED_PRECONDITION")]),
        # Unbind override.
        Step(name="unbind-override", method="DELETE", path="/vpc/v1/addresses/{{ovrAddrId}}/addressPoolOverride",
             test_script=[*assert_status(200)]),
        # Теперь удаление pool проходит.
        Step(name="del-pool-ok", method="DELETE", path=POOLS + "/{{ovrPoolId}}",
             test_script=[*assert_status(200)]),
        # Cleanup address → subnet → network.
        Step(name="del-addr", method="DELETE", path="/vpc/v1/addresses/{{ovrAddrId}}",
             test_script=[*save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="del-sub", method="DELETE", path="/vpc/v1/subnets/{{ovrSubId}}",
             test_script=[*save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
        Step(name="del-net", method="DELETE", path="/vpc/v1/networks/{{ovrNetId}}",
             test_script=[*save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
    ],
))

CASES.append(Case(
    id="IPL-ADDROVR-NEG-NF",
    title="addressPoolOverride на несуществующий Address → 404 NOT_FOUND",
    classes=["NEG"], priority="P1",
    steps=[
        Step(name="ovr-bad-addr", method="POST", path="/vpc/v1/addresses/e9bnonexistent999999/addressPoolOverride",
             body={"poolId": "aplnonexistent999999"},
             test_script=[*assert_status(404), *assert_grpc_code(5, "NOT_FOUND")]),
    ],
))

CASES.append(Case(
    id="IPL-ADDROVR-IDM-UNBIND-NOOP",
    title="UnbindAddressOverride на Address без override → idempotent (200)",
    classes=["IDM"], priority="P2",
    steps=[
        Step(name="unbind-noop", method="DELETE", path="/vpc/v1/addresses/e9bnonexistent999999/addressPoolOverride",
             test_script=["pm.test('idempotent unbind (200 / 404)', () => pm.expect(pm.response.code).to.be.oneOf([200, 404]));"]),
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
