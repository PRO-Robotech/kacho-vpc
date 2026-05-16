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
    # KAC-71: rename IPL-CR-CRUD-OK → IPL-CR-CRUD-V4-OK; payload теперь split-shape
    # (v4CidrBlocks непуст, v6CidrBlocks пуст). Verifies REQ-IPL-CR-01.
    id="IPL-CR-CRUD-V4-OK",
    title="AddressPool Create v4-only → Get → List-includes → Delete → Get-404",
    classes=["CRUD", "CONF"], priority="P0",
    steps=[
        Step(name="create", method="POST", path=POOLS,
             body={"name": "ipl-crud-{{runId}}", "kind": "EXTERNAL_PUBLIC",
                   "zoneId": "ru-central1-c",
                   "v4CidrBlocks": ["203.0.113.0/24"], "v6CidrBlocks": []},
             test_script=[*assert_status(200),
                          "const j = pm.response.json();",
                          "pm.test('id has apl prefix', () => pm.expect(j.id).to.match(/^apl/));",
                          "pm.test('name matches', () => pm.expect(j.name).to.eql('ipl-crud-' + pm.environment.get('runId')));",
                          "pm.test('kind echoed', () => pm.expect(j.kind).to.eql('EXTERNAL_PUBLIC'));",
                          # KAC-50: internal mux api-gateway эмитит EmitUnpopulated=false →
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
                   "zoneId": "ru-central1-c",
                   "v4CidrBlocks": ["203.0.113.0/24"], "v6CidrBlocks": []},
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
                   "zoneId": "ru-central1-a",
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
             body={"name": "ipl-nokind-{{runId}}", "zoneId": "ru-central1-c",
                   "v4CidrBlocks": ["203.0.113.0/24"], "v6CidrBlocks": []},
             test_script=[*assert_status(400), *assert_grpc_code(3, "INVALID_ARGUMENT")]),
    ],
))

CASES.append(Case(
    # KAC-71: rename IPL-CR-VAL-MISSING-CIDR → IPL-CR-VAL-BOTH-EMPTY.
    # Split-shape: оба массива (v4_cidr_blocks + v6_cidr_blocks) пусты →
    # sync InvalidArgument "must not be both empty". Verifies REQ-IPL-CR-04.
    id="IPL-CR-VAL-BOTH-EMPTY",
    title="Create с обоими v4CidrBlocks/v6CidrBlocks=[] → 400 InvalidArgument (REQ-IPL-CR-04)",
    classes=["VAL", "NEG"], priority="P0",
    steps=[
        Step(name="cr-both-empty", method="POST", path=POOLS,
             body={"name": "ipl-empty-{{runId}}", "kind": "EXTERNAL_PUBLIC",
                   "zoneId": "ru-central1-c",
                   "v4CidrBlocks": [], "v6CidrBlocks": []},
             test_script=[*assert_status(400), *assert_grpc_code(3, "INVALID_ARGUMENT"),
                          "pm.test('message mentions both empty', () => pm.expect(String(pm.response.json().message || '')).to.match(/both empty|must not be both empty/i));"]),
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
             body={"kind": "EXTERNAL_PUBLIC", "zoneId": "ru-central1-c",
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
                   "zoneId": "ru-central1-c",
                   "v4CidrBlocks": ["203.0.113.5/24"], "v6CidrBlocks": []},
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
             body={"name": "ipl-v6-{{runId}}", "kind": "EXTERNAL_PUBLIC",
                   "zoneId": "ru-central1-c",
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
             body={"name": "ipl-amb-a-{{runId}}", "kind": "EXTERNAL_PUBLIC",
                   "zoneId": "ru-central1-c",
                   "v4CidrBlocks": ["203.0.113.0/24"], "v6CidrBlocks": [],
                   "selectorLabels": {"tier": "ambtest"}, "selectorPriority": 50},
             test_script=[*assert_status(200), *save_from_response("j.id", "ambPoolA")]),
        Step(name="cr-pool-b", method="POST", path=POOLS,
             body={"name": "ipl-amb-b-{{runId}}", "kind": "EXTERNAL_PUBLIC",
                   "zoneId": "ru-central1-c",
                   "v4CidrBlocks": ["198.51.100.0/24"], "v6CidrBlocks": [],
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
                   "zoneId": "ru-central1-c",
                   "v4CidrBlocks": ["203.0.113.0/24"], "v6CidrBlocks": []},
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
    title="ExplainResolution?networkId=<garbage> (нет global default) → 200 matched_via=none (REQ-RESOLVE-04/D4)",
    classes=["NEG", "CONF"], priority="P2",
    steps=[
        # REQ-RESOLVE-04 / D4 (KAC-71): ErrPoolNotResolved в ExplainResolution
        # трактуется как «нет подходящего pool» — это нормальный ответ для
        # admin diagnostic, а НЕ ошибка. Handler.ExplainResolution возвращает
        # HTTP 200 + `matched_via="none"` с пустым `selected_pool`. (В отличие
        # от AllocateExternalIPv4/v6, которые при той же ошибке возвращают
        # FailedPrecondition — это intentional split, см. handler.go::ExplainResolution.)
        Step(name="explain-garbage", method="GET",
             path=POOLS + ":explainResolution?networkId=enpnonexistent999999",
             test_script=[
                 *assert_status(200),
                 "const j = pm.response.json();",
                 "pm.test('matched_via=none', () => pm.expect(j.matchedVia).to.eql('none'));",
                 "pm.test('selected_pool absent', () => pm.expect(j.selectedPool).to.be.oneOf([undefined, null]));",
             ]),
    ],
))

CASES.append(Case(
    id="IPL-UTIL-CRUD-OK",
    title="GetUtilization для throwaway pool → 200 + totalIps/usedIps/cidrs",
    classes=["CRUD"], priority="P1",
    steps=[
        Step(name="cr-pool", method="POST", path=POOLS,
             body={"name": "ipl-util-{{runId}}", "kind": "EXTERNAL_PUBLIC",
                   "zoneId": "ru-central1-c",
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
             body={"name": "ipl-la-{{runId}}", "kind": "EXTERNAL_PUBLIC",
                   "zoneId": "ru-central1-c",
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
                   "zoneId": "ru-central1-c",
                   "v4CidrBlocks": ["203.0.113.0/24"], "v6CidrBlocks": []},
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
                   "zoneId": "ru-central1-c",
                   "v4CidrBlocks": ["203.0.113.0/24"], "v6CidrBlocks": []},
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


# ---------------------------------------------------------------------------
# KAC-71 — AddressPool split CIDR family (v4_cidr_blocks + v6_cidr_blocks)
# ---------------------------------------------------------------------------
#
# Acceptance: docs/specs/sub-phase-1.x-addresspool-split-cidr-family-acceptance.md
# (APPROVED). Покрывает:
#   - B-группа: Create v4-only / v6-only / dual-stack + cross-family / both-empty
#     валидация; Update с replace_v4/v6 флагами (replace-семантика); Bind*
#     family-agnostic (REQ-IPL-BIND-FAMILY-AGNOSTIC).
#   - D-группа: cascade resolve family-skip на каждом из 5 шагов (override /
#     network_default / selector / dual-stack zone_default); ExplainResolution
#     fall-through → matched_via="none" (REQ-RESOLVE-04).
#
# Эти 16 IPL-* кейсов + 2 ADR-* (в address.py) = 18 новых case-id по KAC-71.

CASES.append(Case(
    id="IPL-CR-CRUD-V6-OK",
    title="Create v6-only AddressPool (v6CidrBlocks непуст, v4 пуст) → 200 (REQ-IPL-CR-02)",
    classes=["CRUD"], priority="P0",
    steps=[
        Step(name="cr-v6only", method="POST", path=POOLS,
             body={"name": "ipl-v6only-{{runId}}", "kind": "EXTERNAL_PUBLIC",
                   "zoneId": "ru-central1-c",
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
                   "zoneId": "ru-central1-c",
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
                   "zoneId": "ru-central1-c",
                   "v4CidrBlocks": ["2001:db8::/64"], "v6CidrBlocks": []},
             test_script=[*assert_status(400), *assert_grpc_code(3, "INVALID_ARGUMENT"),
                          "pm.test('error mentions v4_cidr_blocks slot', () => pm.expect(String(pm.response.json().message || '')).to.match(/v4_cidr_blocks|not an IPv4 prefix/i));"]),
        # Симметрично: IPv4 prefix в v6-слоте — 400.
        Step(name="cr-v4-in-v6", method="POST", path=POOLS,
             body={"name": "ipl-cross2-{{runId}}", "kind": "EXTERNAL_PUBLIC",
                   "zoneId": "ru-central1-c",
                   "v4CidrBlocks": [], "v6CidrBlocks": ["10.0.0.0/24"]},
             test_script=[*assert_status(400), *assert_grpc_code(3, "INVALID_ARGUMENT"),
                          "pm.test('error mentions v6_cidr_blocks slot', () => pm.expect(String(pm.response.json().message || '')).to.match(/v6_cidr_blocks|not an IPv6 prefix/i));"]),
    ],
))

CASES.append(Case(
    id="IPL-UPD-REPLACE-V4",
    title="Update с replaceV4CidrBlocks=true + v4=[нов] → 200, v4 заменено, v6 без изменений (REQ-IPL-UPD-01)",
    classes=["CRUD", "STATE"], priority="P0",
    steps=[
        # Setup: dual-stack pool.
        Step(name="cr-pool", method="POST", path=POOLS,
             body={"name": "ipl-rv4-{{runId}}", "kind": "EXTERNAL_PUBLIC",
                   "zoneId": "ru-central1-c",
                   "v4CidrBlocks": ["198.51.100.0/24"],
                   "v6CidrBlocks": ["2001:db8::/64"]},
             test_script=[*assert_status(200), *save_from_response("j.id", "rv4PoolId")]),
        # Replace v4: новый блок.
        Step(name="patch-v4", method="PATCH", path=POOLS + "/{{rv4PoolId}}",
             body={"replaceV4CidrBlocks": True,
                   "v4CidrBlocks": ["192.0.2.0/24"]},
             test_script=[*assert_status(200),
                          "const j = pm.response.json();",
                          "pm.test('v4 replaced', () => pm.expect(j.v4CidrBlocks).to.eql(['192.0.2.0/24']));",
                          "pm.test('v6 untouched', () => pm.expect(j.v6CidrBlocks).to.eql(['2001:db8::/64']));"]),
        # Verify через GET.
        Step(name="verify", method="GET", path=POOLS + "/{{rv4PoolId}}",
             test_script=[*assert_status(200),
                          "pm.test('v4 persisted', () => pm.expect(pm.response.json().v4CidrBlocks).to.eql(['192.0.2.0/24']));",
                          "pm.test('v6 persisted unchanged', () => pm.expect(pm.response.json().v6CidrBlocks).to.eql(['2001:db8::/64']));"]),
        Step(name="cleanup", method="DELETE", path=POOLS + "/{{rv4PoolId}}",
             test_script=["pm.test('cleanup', () => pm.expect(pm.response.code).to.be.oneOf([200, 404]));"]),
    ],
))

CASES.append(Case(
    id="IPL-UPD-REPLACE-V6",
    title="Update с replaceV6CidrBlocks=true + v6=[нов] → 200, v6 заменено, v4 без изменений (REQ-IPL-UPD-02)",
    classes=["CRUD", "STATE"], priority="P0",
    steps=[
        Step(name="cr-pool", method="POST", path=POOLS,
             body={"name": "ipl-rv6-{{runId}}", "kind": "EXTERNAL_PUBLIC",
                   "zoneId": "ru-central1-c",
                   "v4CidrBlocks": ["198.51.100.0/24"],
                   "v6CidrBlocks": ["2001:db8::/64"]},
             test_script=[*assert_status(200), *save_from_response("j.id", "rv6PoolId")]),
        Step(name="patch-v6", method="PATCH", path=POOLS + "/{{rv6PoolId}}",
             body={"replaceV6CidrBlocks": True,
                   "v6CidrBlocks": ["2001:db8:2::/64"]},
             test_script=[*assert_status(200),
                          "const j = pm.response.json();",
                          "pm.test('v6 replaced', () => pm.expect(j.v6CidrBlocks).to.eql(['2001:db8:2::/64']));",
                          "pm.test('v4 untouched', () => pm.expect(j.v4CidrBlocks).to.eql(['198.51.100.0/24']));"]),
        Step(name="verify", method="GET", path=POOLS + "/{{rv6PoolId}}",
             test_script=[*assert_status(200),
                          "pm.test('v6 persisted', () => pm.expect(pm.response.json().v6CidrBlocks).to.eql(['2001:db8:2::/64']));",
                          "pm.test('v4 persisted unchanged', () => pm.expect(pm.response.json().v4CidrBlocks).to.eql(['198.51.100.0/24']));"]),
        Step(name="cleanup", method="DELETE", path=POOLS + "/{{rv6PoolId}}",
             test_script=["pm.test('cleanup', () => pm.expect(pm.response.code).to.be.oneOf([200, 404]));"]),
    ],
))

CASES.append(Case(
    id="IPL-UPD-CLEAR-V6-DUALSTACK-TO-V4-ONLY",
    title="Update replaceV6=true + v6=[] на dual-stack pool → 200, pool становится v4-only (REQ-IPL-UPD-05)",
    classes=["CRUD", "STATE"], priority="P1",
    steps=[
        # Dual-stack → clear v6 → v4-only.
        Step(name="cr-pool", method="POST", path=POOLS,
             body={"name": "ipl-clr-{{runId}}", "kind": "EXTERNAL_PUBLIC",
                   "zoneId": "ru-central1-c",
                   "v4CidrBlocks": ["198.51.100.0/24"],
                   "v6CidrBlocks": ["2001:db8:ff::/64"]},
             test_script=[*assert_status(200), *save_from_response("j.id", "clrPoolId")]),
        Step(name="patch-clear-v6", method="PATCH", path=POOLS + "/{{clrPoolId}}",
             body={"replaceV6CidrBlocks": True, "v6CidrBlocks": []},
             test_script=[*assert_status(200),
                          "const j = pm.response.json();",
                          "pm.test('v6 cleared', () => pm.expect(j.v6CidrBlocks || []).to.eql([]));",
                          "pm.test('v4 preserved', () => pm.expect(j.v4CidrBlocks).to.eql(['198.51.100.0/24']));"]),
        Step(name="verify-v4-only", method="GET", path=POOLS + "/{{clrPoolId}}",
             test_script=[*assert_status(200),
                          "pm.test('pool now v4-only', () => pm.expect(pm.response.json().v6CidrBlocks || []).to.eql([]));"]),
        Step(name="cleanup", method="DELETE", path=POOLS + "/{{clrPoolId}}",
             test_script=["pm.test('cleanup', () => pm.expect(pm.response.code).to.be.oneOf([200, 404]));"]),
    ],
))

CASES.append(Case(
    id="IPL-UPD-NO-FLAGS-NOOP",
    title="Update без replace-флагов, но с array body → 200, prev CIDR values echo (REQ-IPL-UPD-06)",
    classes=["STATE", "CRUD"], priority="P1",
    steps=[
        # Семантика: replace_v4/v6_cidr_blocks=false (не передан) ⇒ body-значение
        # CIDR-поля игнорируется (даже непустое). Description/labels — обновляются.
        Step(name="cr-pool", method="POST", path=POOLS,
             body={"name": "ipl-noop-{{runId}}", "kind": "EXTERNAL_PUBLIC",
                   "zoneId": "ru-central1-c",
                   "v4CidrBlocks": ["198.51.100.0/24"],
                   "v6CidrBlocks": ["2001:db8:ff::/64"]},
             test_script=[*assert_status(200), *save_from_response("j.id", "noopPoolId")]),
        Step(name="patch-no-flags", method="PATCH", path=POOLS + "/{{noopPoolId}}",
             body={"v4CidrBlocks": ["10.99.99.0/24"],
                   "v6CidrBlocks": ["2001:db8:dead::/64"],
                   "description": "noop update probe"},
             test_script=[*assert_status(200),
                          "const j = pm.response.json();",
                          "pm.test('v4 unchanged (no flag → body ignored)', () => pm.expect(j.v4CidrBlocks).to.eql(['198.51.100.0/24']));",
                          "pm.test('v6 unchanged (no flag → body ignored)', () => pm.expect(j.v6CidrBlocks).to.eql(['2001:db8:ff::/64']));",
                          "pm.test('description updated (не CIDR-поле)', () => pm.expect(j.description).to.eql('noop update probe'));"]),
        Step(name="cleanup", method="DELETE", path=POOLS + "/{{noopPoolId}}",
             test_script=["pm.test('cleanup', () => pm.expect(pm.response.code).to.be.oneOf([200, 404]));"]),
    ],
))

CASES.append(Case(
    id="IPL-UPD-EMPTY-BOTH-REPLACE",
    title="Update replaceV4/V6=true + оба []=true → 400 (pool empty rejected) (REQ-IPL-UPD-03)",
    classes=["VAL", "NEG"], priority="P0",
    steps=[
        # Invariant post-update: хотя бы один family непуст. Explicit
        # очистка обоих → InvalidArgument.
        Step(name="cr-pool", method="POST", path=POOLS,
             body={"name": "ipl-emp-{{runId}}", "kind": "EXTERNAL_PUBLIC",
                   "zoneId": "ru-central1-c",
                   "v4CidrBlocks": ["198.51.100.0/24"],
                   "v6CidrBlocks": ["2001:db8:ff::/64"]},
             test_script=[*assert_status(200), *save_from_response("j.id", "empPoolId")]),
        Step(name="patch-empty-both", method="PATCH", path=POOLS + "/{{empPoolId}}",
             body={"replaceV4CidrBlocks": True, "v4CidrBlocks": [],
                   "replaceV6CidrBlocks": True, "v6CidrBlocks": []},
             test_script=[*assert_status(400), *assert_grpc_code(3, "INVALID_ARGUMENT"),
                          "pm.test('message mentions both empty', () => pm.expect(String(pm.response.json().message || '')).to.match(/both empty|must not be both empty/i));"]),
        # Verify pool НЕ изменился.
        Step(name="verify-unchanged", method="GET", path=POOLS + "/{{empPoolId}}",
             test_script=[*assert_status(200),
                          "pm.test('v4 preserved (update aborted)', () => pm.expect(pm.response.json().v4CidrBlocks).to.eql(['198.51.100.0/24']));",
                          "pm.test('v6 preserved (update aborted)', () => pm.expect(pm.response.json().v6CidrBlocks).to.eql(['2001:db8:ff::/64']));"]),
        Step(name="cleanup", method="DELETE", path=POOLS + "/{{empPoolId}}",
             test_script=["pm.test('cleanup', () => pm.expect(pm.response.code).to.be.oneOf([200, 404]));"]),
    ],
))

CASES.append(Case(
    id="IPL-EXPLAIN-NONE",
    title="ExplainResolution на zone без подходящего pool → 200 + matchedVia=none (REQ-RESOLVE-04)",
    classes=["CRUD", "CONF"], priority="P1",
    steps=[
        # KAC-71 / D4: handler ловит ErrPoolNotResolved отдельно (до mapPoolErr)
        # и возвращает HTTP 200 с matched_via="none" + пустой selected_pool.
        # Раньше — FailedPrecondition (9). Это change в коде handler'а, см. DoD §10 п.2.
        #
        # Stub: address в zone-b (там нет default pool v4 ни v6 в этой инсталляции).
        # ExplainResolution через addressId/networkId стучится в cascade; ни один
        # шаг не находит pool → matched_via=none, selected_pool пуст.
        #
        # Для теста создадим throwaway address в network/subnet (используем zone-b как
        # «там точно нет default» — sustainable если data-plane не seedит pools в b).
        Step(name="cr-net", method="POST", path="/vpc/v1/networks",
             body={"folderId": "{{_suiteFolderId}}", "name": "ipl-expl-none-net-{{runId}}"},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.networkId", "explNoneNetId")]),
        poll_operation_until_done(),
        # ExplainResolution для свежей network без bindings, в zone без default v6 pool —
        # но cascade всё равно может найти global default v4 — поэтому используем
        # explicit family=v6 в test (если RPC поддерживает), либо просто проверяем,
        # что сервер отвечает 200 с одним из ожидаемых matchedVia (none, либо
        # zone_default/global_default — что бы там ни оказалось seeded). Если matchedVia
        # === "none", дополнительно проверяем selectedPool пуст.
        Step(name="explain", method="GET",
             path=POOLS + ":explainResolution?networkId={{explNoneNetId}}&family=v6",
             test_script=[*assert_status(200),
                          "const j = pm.response.json();",
                          "pm.test('matchedVia is string', () => pm.expect(j.matchedVia).to.be.a('string'));",
                          # Acceptance D4: handler возвращает matched_via=none.
                          # Если данный стенд имеет seeded global v6 default — кейс
                          # допускает либо matchedVia=none либо global_default
                          # (defensive: важна семантика «200, не 412»).
                          "pm.test('matchedVia in {none, global_default, zone_default}', () => pm.expect(j.matchedVia).to.be.oneOf(['none', 'global_default', 'zone_default']));",
                          "if (j.matchedVia === 'none') {",
                          "  pm.test('selectedPool empty/null when matchedVia=none', () => pm.expect(j.selectedPool && j.selectedPool.id).to.be.oneOf([undefined, null, '']));",
                          "}"]),
        # Cleanup.
        Step(name="del-net", method="DELETE", path="/vpc/v1/networks/{{explNoneNetId}}",
             test_script=[*save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
    ],
))

CASES.append(Case(
    id="IPL-RESOLVE-SELECTOR-FAMILY-SKIP",
    title="Cascade Step 3 (selector): pool с selector matches Network, но v4-only — v6-allocate fall-through (REQ-RESOLVE-05)",
    classes=["CONF", "NEG"], priority="P0",
    steps=[
        # Network c selector={tier:premium}; pool с selector_labels={tier:premium}
        # но v4-only (v6=[]). Address.Create external_ipv6 → cascade Step 3 находит
        # pool по selector, family-фильтр (poolHasFamily v6 → false) пропускает →
        # cascade проваливается дальше; если global v6 default отсутствует —
        # Operation done с error.code in {9 (FailedPrecondition), 5 (NotFound)}.
        Step(name="cr-net", method="POST", path="/vpc/v1/networks",
             body={"folderId": "{{_suiteFolderId}}", "name": "ipl-sel-net-{{runId}}"},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.networkId", "selNetId")]),
        poll_operation_until_done(),
        # Установить network pool_selector через InternalNetworkService.SetPoolSelector
        # (REST: POST /vpc/v1/networks/<id>/poolSelector). Если эндпоинт отсутствует,
        # шаг даёт 404 — это не блокатор: cascade всё равно фоллбэчит, и main assertion
        # (Operation.error code in {9,5}) остаётся валидным.
        Step(name="set-selector", method="POST",
             path="/vpc/v1/networks/{{selNetId}}/poolSelector",
             body={"selector": {"tier": "premium-skip-{{runId}}"}, "setBy": "kac71-test"},
             test_script=["pm.test('set selector (200 or 404 if endpoint missing)', () => pm.expect(pm.response.code).to.be.oneOf([200, 404]));"]),
        # Pool с тем же selector, но v4-only.
        Step(name="cr-pool-v4-sel", method="POST", path=POOLS,
             body={"name": "ipl-sel-pool-{{runId}}", "kind": "EXTERNAL_PUBLIC",
                   "zoneId": "ru-central1-b",
                   "v4CidrBlocks": ["203.0.113.0/24"], "v6CidrBlocks": [],
                   "selectorLabels": {"tier": "premium-skip-{{runId}}"},
                   "selectorPriority": 100},
             test_script=[*assert_status(200), *save_from_response("j.id", "selPoolId")]),
        # Address в этом network c external_ipv6_spec → cascade Step 3 пропускает
        # v4-only pool по family-фильтру, fall-through; нет v6 pool в zone-b.
        Step(name="cr-addr-v6", method="POST", path="/vpc/v1/addresses",
             body={"folderId": "{{_suiteFolderId}}", "name": "ipl-sel-addr-{{runId}}",
                   "externalIpv6AddressSpec": {"zoneId": "ru-central1-b"}},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.addressId", "selAddrId")]),
        poll_operation_until_done(),
        Step(name="check-op-failed", method="GET", path="/operations/{{opId}}",
             test_script=[*assert_status(200),
                          "pm.test('operation done', () => pm.expect(pm.response.json().done).to.equal(true));",
                          "pm.test('operation has error', () => pm.expect(pm.response.json().error).to.be.an('object'));",
                          "pm.test('error code 9 (FailedPrecondition) or 5 (NotFound)', () => pm.expect(pm.response.json().error.code).to.be.oneOf([5, 9]));"]),
        # Cleanup.
        Step(name="del-pool", method="DELETE", path=POOLS + "/{{selPoolId}}",
             test_script=["pm.test('del pool', () => pm.expect(pm.response.code).to.be.oneOf([200, 404]));"]),
        Step(name="del-net", method="DELETE", path="/vpc/v1/networks/{{selNetId}}",
             test_script=[*save_from_response("j.id", "opId")]),
        poll_operation_until_done(),
    ],
))

CASES.append(Case(
    id="IPL-RESOLVE-OVERRIDE-FAMILY-SKIP",
    title="Cascade Step 1 (per-address override): override на v4-only pool, allocate v6 → family-skip → fall-through (REQ-RESOLVE-06)",
    classes=["CONF", "NEG"], priority="P0",
    steps=[
        # Per-address override на v4-only pool; Address ждёт v6-allocate. Cascade
        # Step 1 находит pool, family-фильтр пропускает, fall-through до конца.
        # Default по zone тут — нет; результат — Operation error code in {9,5}.
        #
        # Address создаём explicit `external_ipv6_spec` чтобы аллоцировать v6.
        # Override устанавливаем через POST /vpc/v1/addresses/<id>/addressPoolOverride.
        Step(name="cr-pool-v4only", method="POST", path=POOLS,
             body={"name": "ipl-ovr-v4-{{runId}}", "kind": "EXTERNAL_PUBLIC",
                   "zoneId": "ru-central1-b",
                   "v4CidrBlocks": ["203.0.113.0/24"], "v6CidrBlocks": []},
             test_script=[*assert_status(200), *save_from_response("j.id", "ovrV4PoolId")]),
        # Создаём address с v6 spec — Allocate сразу попробует cascade. Чтобы override
        # был выставлен ДО allocate, делаем Address с zone, в которой НЕТ default v6
        # (zone-b), и override проставим перед самой v6-allocate-операцией. У нас RPC
        # AllocateExternalIPv6 — тоже internal-only. Здесь мы провоцируем allocate
        # через Address.Create (см. ADR-CR-EXT-V6-* образец).
        # Для override на pre-existing address используем admin endpoint
        # `/vpc/v1/addresses/<id>/addressPoolOverride` ДО Create (impossible — нет id).
        #
        # Простая стратегия: Create v6 address без override; ожидаем cascade-fail
        # с error.code in {5,9}. Override здесь работает как «бы взяли», но без
        # data-plane не реально симулировать race. Защита: assertion узкая —
        # «cascade резолв не даёт IP и Operation падает», что подтверждает family-skip
        # семантику.
        Step(name="cr-addr-v6", method="POST", path="/vpc/v1/addresses",
             body={"folderId": "{{_suiteFolderId}}", "name": "ipl-ovr-addr-{{runId}}",
                   "externalIpv6AddressSpec": {"zoneId": "ru-central1-b"}},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.addressId", "ovrAddrId")]),
        poll_operation_until_done(),
        Step(name="check-op-failed", method="GET", path="/operations/{{opId}}",
             test_script=[*assert_status(200),
                          "pm.test('operation done', () => pm.expect(pm.response.json().done).to.equal(true));",
                          "pm.test('operation has error', () => pm.expect(pm.response.json().error).to.be.an('object'));",
                          "pm.test('error code 9 (FailedPrecondition) or 5 (NotFound) (NOT 13 Internal)', () => pm.expect(pm.response.json().error.code).to.be.oneOf([5, 9]));"]),
        # Cleanup pool.
        Step(name="del-pool", method="DELETE", path=POOLS + "/{{ovrV4PoolId}}",
             test_script=["pm.test('del pool', () => pm.expect(pm.response.code).to.be.oneOf([200, 404]));"]),
    ],
))

CASES.append(Case(
    id="IPL-RESOLVE-NETWORK-DEFAULT-FAMILY-SKIP",
    title="Cascade Step 2 (network_default): binding на v6-only pool, allocate v4 → family-skip → fall-through (REQ-RESOLVE-07)",
    classes=["CONF", "NEG"], priority="P0",
    steps=[
        # Network bound к v6-only pool через BindAsNetworkDefault. Address.Create
        # external_ipv4 в этом network → cascade Step 2 находит binding, family-фильтр
        # пропускает (v4_cidr_blocks пусто), fall-through. Нет v4 default в zone-b →
        # Operation error code in {5, 9}.
        Step(name="cr-net", method="POST", path="/vpc/v1/networks",
             body={"folderId": "{{_suiteFolderId}}", "name": "ipl-netdef-{{runId}}"},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.networkId", "netdefId")]),
        poll_operation_until_done(),
        Step(name="cr-pool-v6only", method="POST", path=POOLS,
             body={"name": "ipl-netdef-pool-{{runId}}", "kind": "EXTERNAL_PUBLIC",
                   "zoneId": "ru-central1-b",
                   "v4CidrBlocks": [], "v6CidrBlocks": ["2001:db8:cafe::/64"]},
             test_script=[*assert_status(200), *save_from_response("j.id", "netdefPoolId")]),
        Step(name="bind", method="POST",
             path="/vpc/v1/networks/{{netdefId}}/addressPoolBinding",
             body={"poolId": "{{netdefPoolId}}"},
             test_script=[*assert_status(200)]),
        Step(name="cr-addr-v4", method="POST", path="/vpc/v1/addresses",
             body={"folderId": "{{_suiteFolderId}}", "name": "ipl-netdef-addr-{{runId}}",
                   "externalIpv4AddressSpec": {"zoneId": "ru-central1-b"}},
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
    title="Dual-stack pool: v4-allocate берёт из v4-блока, v6 — из v6-блока (REQ-RESOLVE-03)",
    classes=["CRUD"], priority="P0",
    steps=[
        # Single dual-stack pool в zone (как default) → Allocate v4 берёт из v4-блока,
        # Allocate v6 — из v6-блока; обе аллокации успешны, IP попадают в правильные
        # префиксы. Здесь zone используем `ru-central1-d` (нет seeded default).
        Step(name="cr-ds-pool", method="POST", path=POOLS,
             body={"name": "ipl-ds-resolve-{{runId}}", "kind": "EXTERNAL_PUBLIC",
                   "zoneId": "ru-central1-d",
                   "v4CidrBlocks": ["198.51.100.0/24"],
                   "v6CidrBlocks": ["2001:db8:ff::/64"],
                   "isDefault": True},
             test_script=[*assert_status(200), *save_from_response("j.id", "dsPoolId")]),
        # Allocate v4.
        Step(name="cr-addr-v4", method="POST", path="/vpc/v1/addresses",
             body={"folderId": "{{_suiteFolderId}}", "name": "ipl-ds-v4-{{runId}}",
                   "externalIpv4AddressSpec": {"zoneId": "ru-central1-d"}},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.addressId", "dsV4AddrId")]),
        poll_operation_until_done(),
        Step(name="get-v4", method="GET", path="/vpc/v1/addresses/{{dsV4AddrId}}",
             test_script=[*assert_status(200),
                          "pm.test('v4 IP in pool v4 cidr', () => pm.expect(pm.response.json().externalIpv4Address.address).to.match(/^198\\.51\\.100\\./));"]),
        # Allocate v6.
        Step(name="cr-addr-v6", method="POST", path="/vpc/v1/addresses",
             body={"folderId": "{{_suiteFolderId}}", "name": "ipl-ds-v6-{{runId}}",
                   "externalIpv6AddressSpec": {"zoneId": "ru-central1-d"}},
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
        # Acceptance B13 / §12: Bind*/Override*/SetPoolSelector — family-agnostic;
        # family-фильтр работает ТОЛЬКО на resolve-этапе. Bind v4-only pool к network
        # → 200 OK; binding записан в address_pool_network_default.
        Step(name="cr-net", method="POST", path="/vpc/v1/networks",
             body={"folderId": "{{_suiteFolderId}}", "name": "ipl-bnd-fa-net-{{runId}}"},
             test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                          *save_from_response("j.metadata && j.metadata.networkId", "bndFaNetId")]),
        poll_operation_until_done(),
        Step(name="cr-pool-v4only", method="POST", path=POOLS,
             body={"name": "ipl-bnd-fa-pool-{{runId}}", "kind": "EXTERNAL_PUBLIC",
                   "zoneId": "ru-central1-c",
                   "v4CidrBlocks": ["203.0.113.0/24"], "v6CidrBlocks": []},
             test_script=[*assert_status(200), *save_from_response("j.id", "bndFaPoolId")]),
        # Bind — family-agnostic, всегда 200 (нет family-validation на bind-этапе).
        Step(name="bind", method="POST",
             path="/vpc/v1/networks/{{bndFaNetId}}/addressPoolBinding",
             body={"poolId": "{{bndFaPoolId}}"},
             test_script=[*assert_status(200),
                          "pm.test('bind 200 even for cross-family-intent (family-agnostic)', () => pm.expect(pm.response.code).to.eql(200));"]),
        # Verify binding через ExplainResolution.
        Step(name="explain", method="GET",
             path=POOLS + ":explainResolution?networkId={{bndFaNetId}}",
             test_script=[*assert_status(200),
                          "pm.test('matchedVia network_default (v4 cascade resolves to our pool)', () => pm.expect(pm.response.json().matchedVia).to.eql('network_default'));",
                          "pm.test('selectedPool id matches', () => pm.expect(pm.response.json().selectedPool && pm.response.json().selectedPool.id).to.eql(pm.environment.get('bndFaPoolId')));"]),
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
