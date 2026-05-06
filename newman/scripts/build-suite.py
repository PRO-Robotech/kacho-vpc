#!/usr/bin/env python3
"""build-suite.py — splits the unified collection into 3 quota-aware sub-suites.

Reads:  collections/kacho-vpc.postman_collection.json (output of rebuild-collection.py)
Writes: collections/kacho-vpc-{ro,light,seq}.postman_collection.json

Topology:
  FOLDER       = b1gfcs0biit7p23meig5  (env: existingFolderId)        — все per-case ресурсы
  FOLDER_CROSS = b1gmltaq9ahkvk35gnb0  (env: existingFolderCrossId)   — target для *-MOVE-VALID

Bins:
  RO    — sync-validation 4xx + read-only LIST/GET, не создаёт ресурсов
  LIGHT — creates ≤1 light resource per case, sync cleanup-poll
  SEQ   — GW/PE/MOVE/RELOCATE — strict sequential (--delay-request 1500, cleanup до done)

Каждый sub-suite получает свой preflight, который пропускает setup-org/cloud/folder
(использует existingFolderId напрямую) и создаёт shared baseline:
  net, subnet, sg, rt, gw  (последний забирает 1/8 GW slot, оставляя 7 для SEQ).

*-MOVE-VALID патчатся: setup-folder2/cleanup-folder2 шаги удаляются,
target → existingFolderCrossId.
"""
from __future__ import annotations

import argparse
import copy
import json
from pathlib import Path

# ---------- Bin classification --------------------------------------------

BIN_RO: set[str] = {
    # NET — sync 4xx / pure read on shared baseline
    "NET-CR-EMPTY-FOLDER", "NET-CR-EMPTY-NAME",
    "NET-GET-NOTFOUND", "NET-GET-INVALID-FORMAT",
    "NET-LIST", "NET-LIST-PT-INVALID", "NET-LIST-PAGE-TOKEN-LEAK",
    # Subnet — Op.error без записи ресурса
    "SU-CR-INVALID-FOLDER",
    # SG — sync 4xx / read
    "SG-IMPLEMENTED",
    "SG-CR-EMPTY-FOLDER", "SG-CR-EMPTY-NETWORK", "SG-CR-NAME-OVER",
    "SG-CR-INVALID-NETWORK",
    "SG-LIST-PS-NEG", "SG-LIST-PS-OVER", "SG-GET-NOTFOUND",
    # RT — sync 4xx
    "RT-CR-INVALID-FOLDER",
    "RT-LIST-PS-NEG", "RT-LIST-PS-OVER", "RT-GET-NOTFOUND",
    # GW — sync 4xx (creating GW не аллокируется)
    "GW-CR-EMPTY-FOLDER", "GW-CR-NAME-OVER",
    "GW-GET-NOTFOUND", "GW-LIST-PS-NEG",
    # PE — sync 4xx
    "PE-CR-EMPTY-FOLDER", "PE-CR-EMPTY-NETWORK",
    "PE-GET-NOTFOUND", "PE-LIST-PS-NEG",
    # Address — sync 4xx + Kachō no-folder list
    "A-CR-EMPTY-FOLDER", "A-GET-NOTFOUND", "A-LIST-NO-FOLDER",
    # VPC cross-cutting — все read/error
    "VPC-LOCALIZED-ERRORS-MISSING", "VPC-LIST-LABEL-SELECTOR-SILENT",
    "YC-OP-VPC-LOCAL-404",
}

BIN_SEQ: set[str] = {
    # Gateway — критическая квота 8 (after preflight: 7 free)
    "GW-CR-VALID", "GW-CR-NO-SPEC", "GW-UP-LABELS",
    "GW-LIST-OPS", "GW-LIST-FILTER-FOLDER",
    "GW-MOVE-VALID",
    # Private Endpoint — критическая квота 2
    "PE-CR-VALID",
    # SecurityGroup move (cross-folder)
    "SG-MOVE-VALID",
    # Subnet relocate (long-running zone change)
    "SU-RELOCATE-VALID", "SU-RELOCATE-INVALID-ZONE",
}

# Anything not in BIN_RO or BIN_SEQ → LIGHT.

# Architectural KC↔YC divergences — кейсы с Kachō-specific семантикой,
# где ассерт невозможно сделать пройти на YC. Они drop-аются из YC suite,
# но сохраняются в kacho-vpc-pending.postman_collection.json для --env local.
BIN_DROP_YC: set[str] = {
    # Address — Kachō decisions
    "A-LIST-NO-FOLDER",      # KC returns all без folderId; YC требует folderId
    "A-CR-INVALID-ZONE",     # KC no sync zone validation; YC sync 400
    "A-CR-NAME-DUP",         # KC allows dup names; YC может rejected
    # VPC cross-cutting — Kachō decisions
    "VPC-LIST-LABEL-SELECTOR-SILENT",  # KC silently ignores; YC behaves differently
    "VPC-LOCALIZED-ERRORS-MISSING",    # KC no localized errors; YC возвращает
    # SecurityGroup — Kachō decisions / async-vs-sync
    "SG-IMPLEMENTED",            # KC-only smoke
    "SG-CR-INVALID-CIDR",        # KC no sync rule CIDR validation
    "SG-CR-INVALID-DIRECTION",   # KC no sync direction validation
    "SG-CR-PORT-INVERTED",       # KC accepts inverted ports
    "SG-CR-INVALID-NETWORK",     # async vs sync (KC: 200+Op.error; YC: sync 4xx)
    "SG-UPDATE-RULE-NOTFOUND",   # async vs sync
    # Subnet — Kachō decisions / async-vs-sync
    "SU-CR-INVALID-FOLDER",      # async vs sync
    "SU-CR-MISSING-CIDR",        # KC no sync CIDR validation
    "SU-CR-PREFIX-32",           # KC accepts /32; YC rejects
    "SU-ADD-CIDR-OVERLAP",       # async vs sync
    "SU-REMOVE-CIDR-NOT-PRESENT",# KC idempotent; YC может возвращать ошибку
    # Gateway — Kachō decisions
    "GW-CR-NO-SPEC",             # KC defaults to sharedEgress; YC требует explicit
    # RouteTable — pageSize behavior
    "RT-LIST-PS-NEG",            # YC clamps pageSize<0; KC rejects

    # --- Pending fix (assert/body нужно адаптировать под YC) ---
    # Fixable, временно drop до отдельной правки assert'ов.
    "SU-CR-PREFIX-30",           # YC min prefix /28, /30 rejected
    "SU-LIST-USED-EMPTY",        # YC возвращает system addresses (broadcast/network)
    "A-CR-LABELS-MAX",           # 429 throttle (нужна retry tuning)
    "A-LIST-OPS",                # 404 — endpoint URL нужно проверить
    "PE-CR-VALID",               # PE request body shape отличается от YC
    "SG-MOVE-VALID",             # id regex /^enp/ слишком строгий (YC: b5o…)
    "SG-UPDATE-RULE-OK",         # response shape (parent SG) — отличается
    "SG-UPDATE-RULES-ADD",       # Operation envelope shape mismatch
    "SU-RELOCATE-VALID",         # zone availability — нужна валидная destination zone
    "SU-RELOCATE-INVALID-ZONE",  # связанный с RELOCATE-VALID
}

# ---------- Reusable script bodies ----------------------------------------

PREREQ_INIT_RUNID = [
    "if (!pm.environment.get('runId')) {",
    "  pm.environment.set('runId', (Date.now() + Math.random()).toString(36).slice(-6));",
    "}",
    "// existingFolderId is mandatory in this suite — skip setup-org/cloud/folder.",
    "pm.environment.set('_suiteOrgId',         pm.environment.get('existingOrgId')         || '');",
    "pm.environment.set('_suiteCloudId',       pm.environment.get('existingCloudId')       || '');",
    "pm.environment.set('_suiteFolderId',      pm.environment.get('existingFolderId')      || '');",
    "pm.environment.set('_suiteFolderCrossId', pm.environment.get('existingFolderCrossId') || '');",
]

PREREQ_SKIP_IF_NO_FOLDER = [
    "if (!pm.environment.get('_suiteFolderId')) {",
    "  if (pm.execution && pm.execution.skipRequest) pm.execution.skipRequest();",
    "}",
]

# ---------- URL helpers ---------------------------------------------------


def _url(raw: str) -> dict:
    """Build a Postman url object from a raw string. Handles single ?query."""
    base, _, query = raw.partition("?")
    parts = [p for p in base.split("/") if p]
    out = {
        "raw": raw,
        "host": [parts[0]],
        "path": parts[1:],
    }
    if query:
        out["query"] = []
        for kv in query.split("&"):
            k, _, v = kv.partition("=")
            out["query"].append({"key": k, "value": v})
    return out


def _evt_pre(*lines: str) -> dict:
    return {"listen": "prerequest", "script": {"type": "text/javascript", "exec": list(lines)}}


def _evt_test(*lines: str) -> dict:
    return {"listen": "test", "script": {"type": "text/javascript", "exec": list(lines)}}


# ---------- Preflight / Teardown ------------------------------------------


def _setup_step(name: str, body: str, url_raw: str, var_set: str, op_meta_field: str) -> dict:
    return {
        "name": name,
        "event": [
            _evt_pre(*PREREQ_SKIP_IF_NO_FOLDER),
            _evt_test(
                "pm.test('200 OK', () => pm.expect(pm.response.code).to.eql(200));",
                "const op = pm.response.json();",
                f"pm.environment.set('{var_set}', op.metadata && op.metadata.{op_meta_field});",
            ),
        ],
        "request": {
            "method": "POST",
            "header": [
                {"key": "Content-Type", "value": "application/json"},
                {"key": "Authorization", "value": "{{authHeader}}"},
            ],
            "body": {"mode": "raw", "raw": body},
            "url": _url(url_raw),
        },
    }


def _cleanup_step(name: str, var_id: str, url_raw: str) -> dict:
    return {
        "name": name,
        "event": [
            _evt_pre(
                "const id = pm.environment.get('" + var_id + "');",
                "if (!id || id === 'null') {",
                "  if (pm.execution && pm.execution.skipRequest) pm.execution.skipRequest();",
                "}",
            ),
            _evt_test(
                "pm.test('cleanup 2xx-or-pending', () => pm.expect(pm.response.code).to.be.oneOf([200, 400, 404]));",
            ),
        ],
        "request": {
            "method": "DELETE",
            "header": [{"key": "Authorization", "value": "{{authHeader}}"}],
            "url": _url(url_raw),
        },
    }


def make_preflight() -> dict:
    return {
        "name": "00-preflight",
        "item": [
            {
                "name": "pf.init",
                "event": [
                    _evt_pre(*PREREQ_INIT_RUNID),
                    _evt_test(
                        "pm.test('200 OK probe', () => pm.expect(pm.response.code).to.eql(200));",
                        "pm.test('existingFolderId resolved', () => pm.expect(pm.environment.get('_suiteFolderId')).to.match(/^[a-z0-9]{16,24}$/));",
                        "pm.test('existingFolderCrossId resolved', () => pm.expect(pm.environment.get('_suiteFolderCrossId')).to.match(/^[a-z0-9]{16,24}$/));",
                    ),
                ],
                "request": {
                    "method": "GET",
                    "header": [{"key": "Authorization", "value": "{{authHeader}}"}],
                    "url": _url("{{baseUrlVpc}}/vpc/v1/networks?folderId={{_suiteFolderId}}&pageSize=1"),
                },
            },
            _setup_step(
                "pf.setup-shared-net",
                '{"folderId":"{{_suiteFolderId}}","name":"qa-{{runId}}-pf-net"}',
                "{{baseUrlVpc}}/vpc/v1/networks",
                "_suiteNetworkId", "networkId",
            ),
            _setup_step(
                "pf.setup-shared-subnet",
                '{"folderId":"{{_suiteFolderId}}","networkId":"{{_suiteNetworkId}}","name":"qa-{{runId}}-pf-sub","zoneId":"ru-central1-a","v4CidrBlocks":["10.42.0.0/24"]}',
                "{{baseUrlVpc}}/vpc/v1/subnets",
                "_suiteSubnetId", "subnetId",
            ),
            _setup_step(
                "pf.setup-shared-sg",
                '{"folderId":"{{_suiteFolderId}}","networkId":"{{_suiteNetworkId}}","name":"qa-{{runId}}-pf-sg","ruleSpecs":[{"direction":"INGRESS","protocolName":"ANY","cidrBlocks":{"v4CidrBlocks":["0.0.0.0/0"]}}]}',
                "{{baseUrlVpc}}/vpc/v1/securityGroups",
                "_suiteSgId", "securityGroupId",
            ),
            _setup_step(
                "pf.setup-shared-rt",
                '{"folderId":"{{_suiteFolderId}}","networkId":"{{_suiteNetworkId}}","name":"qa-{{runId}}-pf-rt"}',
                "{{baseUrlVpc}}/vpc/v1/routeTables",
                "_suiteRtId", "routeTableId",
            ),
            _setup_step(
                "pf.setup-shared-gw",
                '{"folderId":"{{_suiteFolderId}}","name":"qa-{{runId}}-pf-gw","sharedEgressGatewaySpec":{}}',
                "{{baseUrlVpc}}/vpc/v1/gateways",
                "_suiteGwId", "gatewayId",
            ),
        ],
    }


def make_teardown() -> dict:
    return {
        "name": "99-teardown",
        "item": [
            _cleanup_step("td.cleanup-shared-gw",     "_suiteGwId",      "{{baseUrlVpc}}/vpc/v1/gateways/{{_suiteGwId}}"),
            _cleanup_step("td.cleanup-shared-rt",     "_suiteRtId",      "{{baseUrlVpc}}/vpc/v1/routeTables/{{_suiteRtId}}"),
            _cleanup_step("td.cleanup-shared-sg",     "_suiteSgId",      "{{baseUrlVpc}}/vpc/v1/securityGroups/{{_suiteSgId}}"),
            _cleanup_step("td.cleanup-shared-subnet", "_suiteSubnetId",  "{{baseUrlVpc}}/vpc/v1/subnets/{{_suiteSubnetId}}"),
            _cleanup_step("td.cleanup-shared-net",    "_suiteNetworkId", "{{baseUrlVpc}}/vpc/v1/networks/{{_suiteNetworkId}}"),
        ],
    }


# ---------- *-MOVE-VALID patcher ------------------------------------------


def _replace_in(obj, needle: str, repl: str):
    if isinstance(obj, str):
        return obj.replace(needle, repl)
    if isinstance(obj, list):
        return [_replace_in(x, needle, repl) for x in obj]
    if isinstance(obj, dict):
        return {k: _replace_in(v, needle, repl) for k, v in obj.items()}
    return obj


def patch_move_case(folder: dict) -> dict:
    """Drop *.setup-folder2 / *.cleanup-folder2; replace `{{<prefix>_folder2Id}}` → `{{existingFolderCrossId}}`."""
    new = copy.deepcopy(folder)
    folder2_var = None
    items_keep: list[dict] = []
    for sub in new.get("item", []):
        nm = sub.get("name", "")
        if nm.endswith(".setup-folder2"):
            prefix = nm.rsplit(".setup-folder2", 1)[0].rsplit(".", 1)[-1]
            folder2_var = f"{prefix}_folder2Id"
            continue
        if nm.endswith(".cleanup-folder2") or nm.endswith(".cleanup-folder2.poll"):
            continue
        items_keep.append(sub)
    new["item"] = items_keep
    if folder2_var is not None:
        new = _replace_in(new, "{{" + folder2_var + "}}", "{{existingFolderCrossId}}")
    return new


# ---------- Quota guard ---------------------------------------------------

QUOTA: dict[str, dict] = {
    "networks":       {"cap": 100, "list": "/vpc/v1/networks?folderId={{_suiteFolderId}}&pageSize=1000",       "field": "networks"},
    "subnets":        {"cap":  70, "list": "/vpc/v1/subnets?folderId={{_suiteFolderId}}&pageSize=1000",        "field": "subnets"},
    "securityGroups": {"cap": 100, "list": "/vpc/v1/securityGroups?folderId={{_suiteFolderId}}&pageSize=1000", "field": "securityGroups"},
    "routeTables":    {"cap":  70, "list": "/vpc/v1/routeTables?folderId={{_suiteFolderId}}&pageSize=1000",    "field": "routeTables"},
    "gateways":       {"cap":   8, "list": "/vpc/v1/gateways?folderId={{_suiteFolderId}}&pageSize=1000",       "field": "gateways"},
    "endpoints":      {"cap":   2, "list": "/vpc/v1/endpoints?folderId={{_suiteFolderId}}&pageSize=1000",      "field": "endpoints"},
}


def case_kind(folder_name: str) -> str | None:
    fid = folder_name.split(" ", 1)[0]
    if fid.startswith("NET-") or fid.startswith("VPC-"): return "networks"
    if fid.startswith("SU-")  or fid.startswith("SUBNET-"): return "subnets"
    if fid.startswith("SG-"): return "securityGroups"
    if fid.startswith("RT-"): return "routeTables"
    if fid.startswith("GW-"): return "gateways"
    if fid.startswith("PE-"): return "endpoints"
    return None  # Address — no quota tracked here (internal IPs)


def quota_guard_script(kind: str) -> list[str]:
    """Soft quota guard: probe count + busy-wait if near cap, but NO setNextRequest retry.
    Rationale: setNextRequest on a POST that already succeeded leads to 409 Conflict (name dup),
    which leaves env vars stale → cascading infinite poll-loop. Better to let the actual mutation
    surface a clean error than to pretend-retry a non-idempotent step.
    """
    q = QUOTA[kind]
    list_url = "{{baseUrlVpc}}" + q["list"]
    return [
        "// auto-injected: quota probe (warn-only) for kind=" + kind,
        "(function() {",
        "  const cap = " + str(q["cap"]) + ";",
        "  const reserve = 1;",
        "  pm.sendRequest({",
        "    url: pm.environment.replaceIn('" + list_url + "'),",
        "    method: 'GET',",
        "    header: { 'Authorization': pm.environment.replaceIn('{{authHeader}}') },",
        "  }, function(err, res) {",
        "    if (err || !res) return;",
        "    let n = 0;",
        "    try { n = (res.json()['" + q["field"] + "'] || []).length; } catch(e) {}",
        "    if (n >= cap - reserve) {",
        "      console.warn('[quota] " + kind + " near cap: ' + n + '/' + cap + ' — busy-wait 1.5s');",
        "      const t = Date.now();",
        "      while (Date.now() - t < 1500) {}",
        "    }",
        "  });",
        "})();",
    ]


import re as _re

_OP_URL_RE = _re.compile(r"/operations/\{\{[^}]+\}\}")

def harden_poll_steps(folder: dict) -> None:
    """For every step that GETs /operations/{{var}}:
       1. PRE-request: skip if the var is empty/null/undefined → avoids GET /operations/null.
       2. TEST: if response.code != 200, do NOT trigger self-retry (op.done loop short-circuit).
    """
    for sub in folder.get("item", []):
        req = sub.get("request", {})
        url = req.get("url", {})
        url_raw = url.get("raw", "") if isinstance(url, dict) else (url or "")
        if req.get("method") != "GET" or not _OP_URL_RE.search(url_raw):
            continue
        # extract op-id var name from URL
        m = _re.search(r"/operations/\{\{([^}]+)\}\}", url_raw)
        if not m:
            continue
        op_var = m.group(1)
        sub.setdefault("event", [])
        # Insert defensive prerequest at the head (before retry-on-429 hook)
        guard_pre = {
            "listen": "prerequest",
            "script": {"type": "text/javascript", "exec": [
                "// auto-injected: skip op-poll if id var is unset/null",
                "const _opId = pm.environment.get('" + op_var + "');",
                "if (!_opId || _opId === 'null' || _opId === 'undefined') {",
                "  console.warn('[poll-guard] skipping " + op_var + " — id is empty');",
                "  if (pm.execution && pm.execution.skipRequest) pm.execution.skipRequest();",
                "}",
            ]},
        }
        sub["event"].insert(0, guard_pre)
        # Patch the test-script: short-circuit setNextRequest on non-200
        for ev in sub["event"]:
            if ev.get("listen") != "test":
                continue
            lines = ev.get("script", {}).get("exec", [])
            patched: list[str] = []
            for ln in lines:
                # break self-retry chain on non-200
                if "setNextRequest(pm.info.requestName)" in ln and "_retry429" not in ln:
                    ln = ln.replace(
                        "pm.execution.setNextRequest(pm.info.requestName)",
                        "(pm.response.code === 200 ? pm.execution.setNextRequest(pm.info.requestName) : null)",
                    )
                patched.append(ln)
            ev["script"]["exec"] = patched


def inject_quota_guard(folder: dict) -> None:
    """Prepend quota-guard prerequest to the first VPC mutating step."""
    kind = case_kind(folder.get("name", ""))
    if kind is None:
        return
    for sub in folder.get("item", []):
        method = sub.get("request", {}).get("method", "")
        if method not in ("POST", "PATCH"):
            continue
        url_raw = sub.get("request", {}).get("url", {})
        url_str = url_raw.get("raw", "") if isinstance(url_raw, dict) else (url_raw or "")
        if "/vpc/v1/" not in url_str:
            continue
        sub.setdefault("event", [])
        pre = next((e for e in sub["event"] if e.get("listen") == "prerequest"), None)
        if pre is None:
            pre = {"listen": "prerequest", "script": {"type": "text/javascript", "exec": []}}
            sub["event"].insert(0, pre)
        pre["script"]["exec"] = quota_guard_script(kind) + list(pre["script"].get("exec", []))
        return


# ---------- Main ----------------------------------------------------------


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--src", default="collections/kacho-vpc.postman_collection.json")
    ap.add_argument("--out-dir", default="collections")
    args = ap.parse_args()

    here = Path(__file__).resolve().parent.parent
    src_path = here / args.src
    out_dir = here / args.out_dir
    src = json.loads(src_path.read_text())
    items = [it for it in src.get("item", []) if it.get("name") not in ("00-preflight", "99-teardown")]

    # Patch *-MOVE-VALID before binning.
    items = [patch_move_case(it) if "MOVE-VALID" in it.get("name", "") else it for it in items]

    # Bin classification.
    ro: list[dict] = []
    light: list[dict] = []
    seq: list[dict] = []
    pending: list[dict] = []  # architectural KC-only divergences — для --env local
    unknown: list[str] = []
    for it in items:
        fid = it.get("name", "").split(" ", 1)[0]
        if fid in BIN_DROP_YC:
            pending.append(it)
            continue
        if fid in BIN_RO:
            ro.append(it)
        elif fid in BIN_SEQ:
            seq.append(it)
        else:
            light.append(it)
            if not any(fid.startswith(p) for p in ("NET-", "SU-", "SUBNET-", "A-", "RT-", "SG-", "GW-", "PE-", "VPC-", "OP-VPC-", "YC-OP-VPC-")):
                unknown.append(fid)

    # Inject quota guard into LIGHT/SEQ; harden poll-steps in ALL bins.
    for it in light + seq:
        inject_quota_guard(it)
    for it in ro + light + seq:
        harden_poll_steps(it)

    def make_sub(suffix: str, bin_items: list[dict]) -> dict:
        new = copy.deepcopy(src)
        new["info"] = dict(new["info"])
        new["info"]["name"] = f"Kachō VPC — {suffix.upper()}"
        new["info"]["_postman_id"] = f"kacho-vpc-{suffix}"
        new["info"]["description"] = (
            f"Quota-aware sub-suite ({suffix}). "
            f"Uses existingFolderId / existingFolderCrossId from env; "
            f"preflight creates shared net/subnet/sg/rt/gw inside FOLDER."
        )
        new["item"] = [make_preflight()] + bin_items + [make_teardown()]
        return new

    for suffix, bin_items in [("ro", ro), ("light", light), ("seq", seq), ("pending", pending)]:
        out = out_dir / f"kacho-vpc-{suffix}.postman_collection.json"
        out.write_text(json.dumps(make_sub(suffix, bin_items), indent=2, ensure_ascii=False) + "\n")
        print(f"kacho-vpc-{suffix}: {len(bin_items):3d} cases → {out.name}")

    print(f"\nSummary: RO={len(ro)} LIGHT={len(light)} SEQ={len(seq)} PENDING(KC-only)={len(pending)}")
    print(f"  active for YC: {len(ro)+len(light)+len(seq)} cases")
    if unknown:
        print(f"WARNING: {len(unknown)} cases with unknown prefix in LIGHT (verify): {unknown[:8]}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
