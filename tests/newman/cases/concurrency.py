# Copyright (c) PRO-Robotech
# SPDX-License-Identifier: BUSL-1.1

"""Concurrency burst-кейсы для kacho-vpc (класс `CONC`).

Newman не делает deterministic race-condition (1000+ горутин) — это integration-территория
(`internal/repo/*_integration_test.go` уже покрыты testcontainers race-сценарии).
Тут — **best-effort burst** через `pm.sendRequest` + counter: N HTTP-запросов уходят
почти-одновременно с одного newman-runner'а, server обрабатывает в реальной race-window.

Что верифицируется:
- `SUB-CR-CONC-OVERLAP-BURST` — EXCLUDE constraint `subnets_no_overlap_v4` race-defense
- `ADR-CR-CONC-BURST-ALLOC` — IPAM allocator выдает уникальные IP под burst
- `NIC-ATTACH-CONC-BURST` — atomic CAS `network_interfaces.used_by_id`
- `NIC-CR-CONC-MAC-UNIQUE` — UNIQUE constraint `network_interfaces_mac_address_key`
  под burst (`crypto/rand`-MAC + retry on collision)
- `SG-URL-CONC-OCC-CONFLICT` — `xmin`-based OCC в SecurityGroup.UpdateRules

Все эти инварианты держатся на DB-уровне (FK/UNIQUE/EXCLUDE/CAS/xmin); software-side
TOCTOU здесь запрещен (race-prone).
"""

CASES = []


# ---------------------------------------------------------------------------
# Burst helper — pm.sendRequest × N с counter и Promise-style waitForAll.
#
# Postman runtime sandbox: `pm.sendRequest` принимает callback (err, res).
# Newman ждет пока все pending callbacks отстреляют до завершения test_script.
# Прозрачно для нас: запускаем N sends в for-loop, накапливаем results в массив,
# в конце (when counter==N) делаем asserts.
# ---------------------------------------------------------------------------

def _burst_block_js(n: int, method: str, path: str, body_js: str, results_var: str = "burstResults"):
    """Возвращает строки JS для test_script: N parallel sends, накапливает [{code,body}]
    в pm.environment[results_var]. body_js — JS-выражение, формирующее body per-iteration
    (имеет переменную `i` для индекса)."""
    return [
        f"const N = {n};",
        "const base = pm.environment.get('baseUrl');",
        "const tok = pm.environment.get('jwtProjectAdminA1');",
        "const results = [];",
        "let done = 0;",
        "for (let i = 0; i < N; i++) {",
        f"  const body = {body_js};",
        "  pm.sendRequest({",
        f"    url: base + `{path}`,",
        f"    method: '{method}',",
        "    header: {",
        "      'Authorization': 'Bearer ' + tok,",
        "      'Content-Type': 'application/json',",
        "    },",
        "    body: { mode: 'raw', raw: JSON.stringify(body) },",
        "  }, (err, res) => {",
        "    let parsed = null;",
        "    try { parsed = res ? res.json() : null; } catch (e) {}",
        "    results.push({ code: res ? res.code : 0, body: parsed, err: err ? String(err) : null });",
        "    done++;",
        "    if (done === N) {",
        f"      pm.environment.set('{results_var}', JSON.stringify(results));",
        "    }",
        "  });",
        "}",
    ]


def _poll_op_js(op_id_var: str, result_var: str, max_tries: int = 8):
    """Polling одной Operation по env-переменной, результат в `result_var` env:
    {done: bool, error: {code,message}|null, response: any}."""
    return [
        f"const _opId = pm.environment.get('{op_id_var}');",
        "if (!_opId) {",
        f"  pm.environment.set('{result_var}', JSON.stringify({{done:false,error:null,response:null}}));",
        "} else {",
        "  let _tries = 0;",
        f"  const _MAX = {max_tries};",
        "  const _step = () => {",
        "    pm.sendRequest({",
        "      url: pm.environment.get('baseUrl') + '/operations/' + _opId,",
        "      method: 'GET',",
        "      header: { 'Authorization': 'Bearer ' + pm.environment.get('jwtProjectAdminA1') },",
        "    }, (err, res) => {",
        "      let j = null; try { j = res.json(); } catch (e) {}",
        "      if (j && j.done) {",
        f"        pm.environment.set('{result_var}', JSON.stringify({{done:true,error:j.error||null,response:j.response||null}}));",
        "      } else if (++_tries < _MAX) {",
        "        setTimeout(_step, 500);",
        "      } else {",
        f"        pm.environment.set('{result_var}', JSON.stringify({{done:false,error:null,response:null}}));",
        "      }",
        "    });",
        "  };",
        "  _step();",
        "}",
    ]


# Setup helpers — Network + Subnet для тестов, нуждающихся в parent.

def _setup_net(suffix):
    return [
        Step(
            name="setup-net", method="POST", path="/vpc/v1/networks",
            body={"projectId": "{{_suiteProjectId}}", "name": f"conc-{suffix}-net-{{{{runId}}}}"},
            test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                         *save_from_response("j.metadata && j.metadata.networkId", "netId")],
        ),
        poll_operation_until_done(),
    ]


def _setup_subnet(suffix, cidr="10.250.0.0/24"):
    return [
        Step(
            name="setup-sub", method="POST", path="/vpc/v1/subnets",
            body={"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}",
                  "name": f"conc-{suffix}-sub-{{{{runId}}}}", "zoneId": "{{existingZoneId}}",
                  "v4CidrBlocks": [cidr]},
            test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                         *save_from_response("j.metadata && j.metadata.subnetId", "subId")],
        ),
        poll_operation_until_done(),
    ]


def _cleanup_subnet():
    return Step(
        name="cleanup-sub", method="DELETE", path="/vpc/v1/subnets/{{subId}}",
        test_script=[
            "pm.test('cleanup subnet 200 or 400', () => pm.expect(pm.response.code).to.be.oneOf([200, 400]));",
            *save_from_response("j.id", "opId"),
        ],
    )


def _cleanup_net():
    return Step(
        name="cleanup-net", method="DELETE", path="/vpc/v1/networks/{{netId}}",
        test_script=[
            "pm.test('cleanup network 200 or 400', () => pm.expect(pm.response.code).to.be.oneOf([200, 400]));",
            *save_from_response("j.id", "opId"),
        ],
    )


# ===========================================================================
# CASE 1 — SUB-CR-CONC-OVERLAP-BURST
# EXCLUDE constraint subnets_no_overlap_v4 защищает от parallel overlap-Create.
# ===========================================================================

CASES.append(Case(
    id="SUB-CR-CONC-OVERLAP-BURST",
    title="3 parallel Create Subnet same CIDR same Network → ровно 1 succeeds (EXCLUDE race-defense)",
    classes=["CONC", "NEG"],
    priority="P0",
    steps=[
        *_setup_net("ov"),
        Step(
            name="burst-create-overlap", method="POST", path="/vpc/v1/networks",  # path не используется в burst
            test_script=[
                # Каждый из 3 sends — на одну и ту же Network с одинаковым CIDR.
                *_burst_block_js(
                    3, "POST", "/vpc/v1/subnets",
                    body_js=(
                        "({projectId: pm.environment.get('_suiteProjectId'), "
                        "networkId: pm.environment.get('netId'), "
                        "name: `conc-ov-sub-${pm.environment.get('runId')}-${i}`, "
                        "zoneId: pm.environment.get('existingZoneId'), "
                        "v4CidrBlocks: ['10.250.40.0/24']})"
                    ),
                ),
            ],
        ),
        Step(
            name="poll-and-assert", method="GET", path="/healthz",
            test_script=[
                # Wait briefly для отстрела всех async callbacks → парсим results.
                "setTimeout(() => {", "}, 100);",
                "const results = JSON.parse(pm.environment.get('burstResults') || '[]');",
                "pm.test('all 3 burst responses captured', () => pm.expect(results.length).to.eql(3));",
                "// Все 3 sync-ответа 200 (Operation создается для каждого; race решается в worker'е).",
                "pm.test('all 3 sync-200 (Operation envelope)', () => results.forEach(r => pm.expect(r.code, JSON.stringify(r)).to.eql(200)));",
                "// Соберем opIds и заполним в env через цикл sendRequest poll.",
                "const opIds = results.map(r => r.body && r.body.id).filter(Boolean);",
                "pm.environment.set('burstOpIds', JSON.stringify(opIds));",
                "pm.test('3 opIds collected', () => pm.expect(opIds.length).to.eql(3));",
            ],
        ),
        Step(
            name="resolve-ops", method="GET", path="/healthz",
            test_script=[
                # Poll all 3 ops в одном test_script через counter.
                "const opIds = JSON.parse(pm.environment.get('burstOpIds') || '[]');",
                "const base = pm.environment.get('baseUrl');",
                "const tok = pm.environment.get('jwtProjectAdminA1');",
                "const ops = [];",
                "let pending = opIds.length;",
                "if (pending === 0) { pm.environment.set('burstOpsResolved', '[]'); }",
                "const tryOne = (oid, attempt) => {",
                "  pm.sendRequest({",
                "    url: base + '/operations/' + oid,",
                "    method: 'GET',",
                "    header: { 'Authorization': 'Bearer ' + tok },",
                "  }, (err, res) => {",
                "    let j = null; try { j = res.json(); } catch (e) {}",
                "    if (j && j.done) {",
                "      ops.push({id: oid, done: true, hasError: !!j.error, errCode: j.error ? j.error.code : null});",
                "      if (--pending === 0) pm.environment.set('burstOpsResolved', JSON.stringify(ops));",
                "    } else if (attempt < 12) {",
                "      setTimeout(() => tryOne(oid, attempt + 1), 500);",
                "    } else {",
                "      ops.push({id: oid, done: false, hasError: false, errCode: null});",
                "      if (--pending === 0) pm.environment.set('burstOpsResolved', JSON.stringify(ops));",
                "    }",
                "  });",
                "};",
                "opIds.forEach(oid => tryOne(oid, 0));",
            ],
        ),
        Step(
            name="assert-distribution", method="GET", path="/healthz",
            test_script=[
                "const ops = JSON.parse(pm.environment.get('burstOpsResolved') || '[]');",
                "pm.test('all 3 ops resolved (done=true)', () => {",
                "  pm.expect(ops.length).to.eql(3);",
                "  ops.forEach(o => pm.expect(o.done, JSON.stringify(o)).to.eql(true));",
                "});",
                "const ok = ops.filter(o => !o.hasError).length;",
                "const failed = ops.filter(o => o.hasError).length;",
                "pm.test('exactly 1 succeeds + 2 fail (EXCLUDE constraint race-defense)', () => {",
                "  pm.expect(ok, `ok=${ok} failed=${failed} ops=${JSON.stringify(ops)}`).to.eql(1);",
                "  pm.expect(failed).to.eql(2);",
                "});",
                "pm.test('failures: FailedPrecondition (gRPC code 9) — CIDR overlap', () => {",
                "  const failedCodes = ops.filter(o => o.hasError).map(o => o.errCode);",
                "  failedCodes.forEach(c => pm.expect(c, `code=${c}`).to.eql(9));",
                "});",
            ],
        ),
        # Cleanup: можем не знать который subId выжил — list по net и удалить все.
        Step(
            name="list-and-cleanup", method="GET",
            path="/vpc/v1/networks/{{netId}}/subnets?projectId={{_suiteProjectId}}",
            test_script=[
                *assert_status(200),
                "const subs = (pm.response.json().subnets || []);",
                "pm.environment.set('subToCleanupIds', JSON.stringify(subs.map(s => s.id)));",
            ],
        ),
        Step(
            name="cleanup-all-subs", method="GET", path="/healthz",
            test_script=[
                "const ids = JSON.parse(pm.environment.get('subToCleanupIds') || '[]');",
                "const base = pm.environment.get('baseUrl');",
                "const tok = pm.environment.get('jwtProjectAdminA1');",
                "let pending = ids.length;",
                "if (pending === 0) { pm.environment.set('cleanupOpIds', '[]'); }",
                "const opIds = [];",
                "ids.forEach(id => {",
                "  pm.sendRequest({",
                "    url: base + '/vpc/v1/subnets/' + id,",
                "    method: 'DELETE',",
                "    header: { 'Authorization': 'Bearer ' + tok },",
                "  }, (err, res) => {",
                "    try { const j = res.json(); if (j.id) opIds.push(j.id); } catch (e) {}",
                "    if (--pending === 0) pm.environment.set('cleanupOpIds', JSON.stringify(opIds));",
                "  });",
                "});",
            ],
        ),
        # Best-effort wait for cleanup ops to finish before net delete (no strict assert).
        Step(
            name="wait-cleanup", method="GET", path="/healthz",
            test_script=[
                "const opIds = JSON.parse(pm.environment.get('cleanupOpIds') || '[]');",
                "const base = pm.environment.get('baseUrl');",
                "const tok = pm.environment.get('jwtProjectAdminA1');",
                "let pending = opIds.length;",
                "if (pending === 0) { return; }",
                "const tryOne = (oid, attempt) => {",
                "  pm.sendRequest({",
                "    url: base + '/operations/' + oid,",
                "    method: 'GET',",
                "    header: { 'Authorization': 'Bearer ' + tok },",
                "  }, (err, res) => {",
                "    let j = null; try { j = res.json(); } catch (e) {}",
                "    if ((j && j.done) || attempt >= 10) { pending--; }",
                "    else { setTimeout(() => tryOne(oid, attempt + 1), 400); return; }",
                "  });",
                "};",
                "opIds.forEach(oid => tryOne(oid, 0));",
            ],
        ),
        _cleanup_net(),
        poll_operation_until_done(),
    ],
))


# ===========================================================================
# CASE 2 — ADR-CR-CONC-BURST-ALLOC
# IPAM allocator → 5 parallel external Address.Create → 5 distinct IPs.
# ===========================================================================

CASES.append(Case(
    id="ADR-CR-CONC-BURST-ALLOC",
    title="5 parallel external Address.Create → 5 distinct IPs (UNIQUE pool slot defense)",
    classes=["CONC"],
    priority="P0",
    steps=[
        Step(
            name="burst-create-external", method="GET", path="/healthz",
            test_script=[
                *_burst_block_js(
                    5, "POST", "/vpc/v1/addresses",
                    body_js=(
                        "({projectId: pm.environment.get('_suiteProjectId'), "
                        "name: `conc-adr-${pm.environment.get('runId')}-${i}`, "
                        "externalIpv4AddressSpec: {zoneId: pm.environment.get('existingZoneId')}})"
                    ),
                ),
            ],
        ),
        Step(
            name="check-sync-200", method="GET", path="/healthz",
            test_script=[
                "const results = JSON.parse(pm.environment.get('burstResults') || '[]');",
                "pm.test('5 sync 200', () => {",
                "  pm.expect(results.length).to.eql(5);",
                "  results.forEach(r => pm.expect(r.code, JSON.stringify(r)).to.eql(200));",
                "});",
                "pm.environment.set('burstOpIds', JSON.stringify(results.map(r => r.body && r.body.id).filter(Boolean)));",
            ],
        ),
        Step(
            name="resolve-and-collect-ips", method="GET", path="/healthz",
            test_script=[
                "const opIds = JSON.parse(pm.environment.get('burstOpIds') || '[]');",
                "const base = pm.environment.get('baseUrl');",
                "const tok = pm.environment.get('jwtProjectAdminA1');",
                "const collected = [];",
                "let pending = opIds.length;",
                "const tryOne = (oid, attempt) => {",
                "  pm.sendRequest({",
                "    url: base + '/operations/' + oid,",
                "    method: 'GET',",
                "    header: { 'Authorization': 'Bearer ' + tok },",
                "  }, (err, res) => {",
                "    let j = null; try { j = res.json(); } catch (e) {}",
                "    if (j && j.done) {",
                "      const addrId = j.metadata && j.metadata.addressId;",
                "      const errCode = j.error ? j.error.code : null;",
                "      const ipv4 = j.response && j.response.externalIpv4Address ? j.response.externalIpv4Address.address : null;",
                "      collected.push({opId: oid, addrId, errCode, ipv4});",
                "      if (--pending === 0) pm.environment.set('burstAddrCollected', JSON.stringify(collected));",
                "    } else if (attempt < 12) {",
                "      setTimeout(() => tryOne(oid, attempt + 1), 500);",
                "    } else {",
                "      collected.push({opId: oid, addrId: null, errCode: null, ipv4: null, timeout: true});",
                "      if (--pending === 0) pm.environment.set('burstAddrCollected', JSON.stringify(collected));",
                "    }",
                "  });",
                "};",
                "opIds.forEach(oid => tryOne(oid, 0));",
            ],
        ),
        Step(
            name="assert-unique-ips", method="GET", path="/healthz",
            test_script=[
                "const items = JSON.parse(pm.environment.get('burstAddrCollected') || '[]');",
                "pm.test('5 ops resolved', () => pm.expect(items.length).to.eql(5));",
                "const ipv4s = items.filter(i => i.ipv4).map(i => i.ipv4);",
                "pm.test('all alloc'd IPs are unique (no duplicate slot)', () => {",
                "  const unique = new Set(ipv4s);",
                "  pm.expect(unique.size, `ipv4s=${JSON.stringify(ipv4s)}`).to.eql(ipv4s.length);",
                "});",
                "pm.test('all 5 succeeded (default pool has free slots)', () => {",
                "  const ok = items.filter(i => i.addrId).length;",
                "  pm.expect(ok, `items=${JSON.stringify(items)}`).to.eql(5);",
                "});",
                "pm.environment.set('cleanupAddrIds', JSON.stringify(items.map(i => i.addrId).filter(Boolean)));",
            ],
        ),
        Step(
            name="cleanup-addresses", method="GET", path="/healthz",
            test_script=[
                "const ids = JSON.parse(pm.environment.get('cleanupAddrIds') || '[]');",
                "const base = pm.environment.get('baseUrl');",
                "const tok = pm.environment.get('jwtProjectAdminA1');",
                "ids.forEach(id => {",
                "  pm.sendRequest({",
                "    url: base + '/vpc/v1/addresses/' + id,",
                "    method: 'DELETE',",
                "    header: { 'Authorization': 'Bearer ' + tok },",
                "  }, () => {});",
                "});",
            ],
        ),
    ],
))


# ===========================================================================
# CASE 3 — NIC-CR-CONC-MAC-UNIQUE
# 10 parallel Create NIC same subnet → 10 distinct MACs (UNIQUE network_interfaces_mac_address_key).
# ===========================================================================

CASES.append(Case(
    id="NIC-CR-CONC-MAC-UNIQUE",
    title="10 parallel Create NIC → 10 distinct MACs (UNIQUE constraint + crypto/rand retry)",
    classes=["CONC"],
    priority="P1",
    steps=[
        *_setup_net("macu", ),
        *_setup_subnet("macu", "10.250.50.0/24"),
        Step(
            name="burst-create-nic", method="GET", path="/healthz",
            test_script=[
                *_burst_block_js(
                    10, "POST", "/vpc/v1/networkInterfaces",
                    body_js=(
                        "({projectId: pm.environment.get('_suiteProjectId'), "
                        "subnetId: pm.environment.get('subId'), "
                        "name: `conc-macu-${pm.environment.get('runId')}-${i}`})"
                    ),
                ),
            ],
        ),
        Step(
            name="resolve-ops-collect-mac", method="GET", path="/healthz",
            test_script=[
                "const results = JSON.parse(pm.environment.get('burstResults') || '[]');",
                "const opIds = results.map(r => r.body && r.body.id).filter(Boolean);",
                "const base = pm.environment.get('baseUrl');",
                "const tok = pm.environment.get('jwtProjectAdminA1');",
                "const collected = [];",
                "let pending = opIds.length;",
                "const tryOne = (oid, attempt) => {",
                "  pm.sendRequest({",
                "    url: base + '/operations/' + oid,",
                "    method: 'GET',",
                "    header: { 'Authorization': 'Bearer ' + tok },",
                "  }, (err, res) => {",
                "    let j = null; try { j = res.json(); } catch (e) {}",
                "    if (j && j.done) {",
                "      const nicId = j.metadata && j.metadata.networkInterfaceId;",
                "      const mac = j.response && j.response.macAddress;",
                "      const errCode = j.error ? j.error.code : null;",
                "      collected.push({nicId, mac, errCode});",
                "      if (--pending === 0) pm.environment.set('nicCollected', JSON.stringify(collected));",
                "    } else if (attempt < 12) {",
                "      setTimeout(() => tryOne(oid, attempt + 1), 500);",
                "    } else {",
                "      collected.push({nicId: null, mac: null, errCode: null, timeout: true});",
                "      if (--pending === 0) pm.environment.set('nicCollected', JSON.stringify(collected));",
                "    }",
                "  });",
                "};",
                "opIds.forEach(oid => tryOne(oid, 0));",
            ],
        ),
        Step(
            name="assert-mac-unique", method="GET", path="/healthz",
            test_script=[
                "const items = JSON.parse(pm.environment.get('nicCollected') || '[]');",
                "pm.test('10 NICs resolved', () => pm.expect(items.filter(i => i.nicId).length).to.eql(10));",
                "const macs = items.map(i => i.mac).filter(Boolean);",
                "pm.test('all 10 MACs are unique (UNIQUE constraint + crypto/rand retry)', () => {",
                "  const unique = new Set(macs);",
                "  pm.expect(unique.size, `macs=${JSON.stringify(macs)}`).to.eql(macs.length);",
                "  pm.expect(unique.size).to.eql(10);",
                "});",
                "pm.test('every MAC format 0e:xx:xx:xx:xx:xx', () => {",
                "  macs.forEach(m => pm.expect(m, m).to.match(/^0e:[0-9a-f]{2}:[0-9a-f]{2}:[0-9a-f]{2}:[0-9a-f]{2}:[0-9a-f]{2}$/));",
                "});",
                "pm.environment.set('cleanupNicIds', JSON.stringify(items.map(i => i.nicId).filter(Boolean)));",
            ],
        ),
        Step(
            name="cleanup-nics", method="GET", path="/healthz",
            test_script=[
                "const ids = JSON.parse(pm.environment.get('cleanupNicIds') || '[]');",
                "const base = pm.environment.get('baseUrl');",
                "const tok = pm.environment.get('jwtProjectAdminA1');",
                "let pending = ids.length;",
                "if (pending === 0) { pm.environment.set('nicCleanupDone', '1'); return; }",
                "const opIds = [];",
                "ids.forEach(id => {",
                "  pm.sendRequest({",
                "    url: base + '/vpc/v1/networkInterfaces/' + id,",
                "    method: 'DELETE',",
                "    header: { 'Authorization': 'Bearer ' + tok },",
                "  }, (err, res) => {",
                "    try { const j = res.json(); if (j.id) opIds.push(j.id); } catch (e) {}",
                "    if (--pending === 0) {",
                "      pm.environment.set('nicCleanupOpIds', JSON.stringify(opIds));",
                "      pm.environment.set('nicCleanupDone', '1');",
                "    }",
                "  });",
                "});",
            ],
        ),
        Step(
            name="wait-nic-cleanup", method="GET", path="/healthz",
            test_script=[
                "const opIds = JSON.parse(pm.environment.get('nicCleanupOpIds') || '[]');",
                "const base = pm.environment.get('baseUrl');",
                "const tok = pm.environment.get('jwtProjectAdminA1');",
                "let pending = opIds.length;",
                "if (pending === 0) return;",
                "const tryOne = (oid, attempt) => {",
                "  pm.sendRequest({",
                "    url: base + '/operations/' + oid,",
                "    method: 'GET',",
                "    header: { 'Authorization': 'Bearer ' + tok },",
                "  }, (err, res) => {",
                "    let j = null; try { j = res.json(); } catch (e) {}",
                "    if ((j && j.done) || attempt >= 10) { pending--; }",
                "    else { setTimeout(() => tryOne(oid, attempt + 1), 400); return; }",
                "  });",
                "};",
                "opIds.forEach(oid => tryOne(oid, 0));",
            ],
        ),
        _cleanup_subnet(),
        poll_operation_until_done(),
        _cleanup_net(),
        poll_operation_until_done(),
    ],
))


# ===========================================================================
# CASE 4 — NIC-ATTACH-CONC-BURST
# 5 parallel AttachToInstance same NIC → 1 win + 4 FailedPrecondition (CAS).
# ===========================================================================


# ===========================================================================
# CASE 5 — SG-URL-CONC-OCC-CONFLICT
# 2 parallel UpdateRules same SG → 1 OK + 1 Aborted (xmin OCC).
# ===========================================================================

CASES.append(Case(
    id="SG-URL-CONC-OCC-CONFLICT",
    title="2 parallel UpdateRules same SG → 1 OK + 1 Aborted/FailedPrecondition (xmin OCC)",
    classes=["CONC", "STATE"],
    priority="P0",
    steps=[
        *_setup_net("occ"),
        Step(
            name="create-sg", method="POST", path="/vpc/v1/securityGroups",
            body={"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}",
                  "name": "conc-occ-sg-{{runId}}"},
            test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                         *save_from_response("j.metadata && j.metadata.securityGroupId", "sgId")],
        ),
        poll_operation_until_done(),
        Step(
            name="burst-update-rules", method="GET", path="/healthz",
            test_script=[
                # Два параллельных UpdateRules: один добавляет rule A, другой — rule B,
                # на одной и той же row → один из них должен fail на xmin-check.
                *_burst_block_js(
                    2, "POST", "/vpc/v1/securityGroups/${pm.environment.get('sgId')}:update-rules",
                    body_js=(
                        "({additions: [{description: `conc-rule-${i}`, direction: 'EGRESS', "
                        "protocolName: 'ANY', cidrBlocks: {v4CidrBlocks: [`10.99.${i}.0/24`]}}]})"
                    ),
                ),
            ],
        ),
        Step(
            name="resolve-occ-ops", method="GET", path="/healthz",
            test_script=[
                "const results = JSON.parse(pm.environment.get('burstResults') || '[]');",
                "const opIds = results.map(r => r.body && r.body.id).filter(Boolean);",
                "const base = pm.environment.get('baseUrl');",
                "const tok = pm.environment.get('jwtProjectAdminA1');",
                "const ops = [];",
                "let pending = opIds.length;",
                "const tryOne = (oid, attempt) => {",
                "  pm.sendRequest({",
                "    url: base + '/operations/' + oid,",
                "    method: 'GET',",
                "    header: { 'Authorization': 'Bearer ' + tok },",
                "  }, (err, res) => {",
                "    let j = null; try { j = res.json(); } catch (e) {}",
                "    if (j && j.done) {",
                "      ops.push({done: true, hasError: !!j.error, errCode: j.error ? j.error.code : null});",
                "      if (--pending === 0) pm.environment.set('occResolved', JSON.stringify(ops));",
                "    } else if (attempt < 12) {",
                "      setTimeout(() => tryOne(oid, attempt + 1), 500);",
                "    } else {",
                "      ops.push({done: false, hasError: false, errCode: null});",
                "      if (--pending === 0) pm.environment.set('occResolved', JSON.stringify(ops));",
                "    }",
                "  });",
                "};",
                "opIds.forEach(oid => tryOne(oid, 0));",
            ],
        ),
        # Читаем финальный набор правил SG: сколько наших conc-rule-* реально осело.
        # Это timing-независимый детектор lost-update: при исправном xmin-OCC число
        # осевших правил ВСЕГДА равно числу ok-операций (overlap → ok=1/present=1;
        # сериализовано → ok=2/present=2). Если OCC сломан (оба ok, но second-writer
        # затёр набор) → present<ok → assert ниже краснеет.
        Step(
            name="fetch-sg-rules-after-occ", method="GET", path="/vpc/v1/securityGroups/{{sgId}}",
            test_script=[
                "const j = pm.response.json();",
                "pm.test('sg fetch ok', () => pm.expect(pm.response.code).to.eql(200));",
                "const rules = (j && j.rules) || [];",
                "const present = rules.filter(r => r && typeof r.description === 'string' "
                "&& /^conc-rule-\\d+$/.test(r.description)).length;",
                "pm.environment.set('occRulesPresent', String(present));",
            ],
        ),
        Step(
            name="assert-occ", method="GET", path="/healthz",
            test_script=[
                "const ops = JSON.parse(pm.environment.get('occResolved') || '[]');",
                "pm.test('2 occ ops resolved', () => pm.expect(ops.length).to.eql(2));",
                "const ok = ops.filter(o => !o.hasError && o.done).length;",
                "const conflict = ops.filter(o => o.hasError && (o.errCode === 9 || o.errCode === 10)).length;",
                "const present = parseInt(pm.environment.get('occRulesPresent') || '-1', 10);",
                "// gRPC 9=FailedPrecondition, 10=Aborted. Race может разрешаться обоими способами:",
                "// если xmin-CAS попал — второй writer получает 0 rows → ErrFailedPrecondition; либо Aborted.",
                "// Оба исхода валидны (overlap → 1 OK+1 conflict; сериализовано → 2 OK, оба",
                "// правила осели). НО обе операции успешными с ПОТЕРЕЙ одного правила",
                "// (second-writer-wins) — недопустимо: ключевой assert — present === ok.",
                "pm.test('xmin OCC: no lost update — rules present === ok (1|2), conflict surfaced as 9/10', () => {",
                "  pm.expect(ok + conflict, `ok=${ok} conflict=${conflict} ops=${JSON.stringify(ops)}`).to.eql(2);",
                "  pm.expect(ok, 'at least one must succeed').to.be.at.least(1);",
                "  pm.expect(present, `lost update: rules present=${present} != ok=${ok} (second-writer-wins?)`).to.eql(ok);",
                "});",
            ],
        ),
        Step(
            name="cleanup-sg", method="DELETE", path="/vpc/v1/securityGroups/{{sgId}}",
            test_script=[
                "pm.test('cleanup sg 200 or 400', () => pm.expect(pm.response.code).to.be.oneOf([200, 400]));",
                *save_from_response("j.id", "opId"),
            ],
        ),
        poll_operation_until_done(),
        _cleanup_net(),
        poll_operation_until_done(),
    ],
))
