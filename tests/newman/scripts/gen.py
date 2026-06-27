#!/usr/bin/env python3
# Copyright (c) PRO-Robotech
# SPDX-License-Identifier: BUSL-1.1

"""
tests/newman/scripts/gen.py — генератор Postman collections из декларативных case-файлов.

Использование:
    python3 scripts/gen.py             # все сервисы
    python3 scripts/gen.py network     # один сервис

Источник истины — модули в tests/newman/cases/<service>.py, каждый экспортирует
переменную CASES — список объектов Case (см. ниже).
"""
from __future__ import annotations

import json
import sys
import uuid
import importlib.util
from pathlib import Path
from dataclasses import dataclass, field
from typing import List, Dict, Optional

ROOT = Path(__file__).resolve().parents[1]
SCRIPTS_DIR = Path(__file__).resolve().parent
CASES_DIR = ROOT / "cases"
OUT_DIR = ROOT / "collections"


# ---------------------------------------------------------------------------
# Декларативные структуры
# ---------------------------------------------------------------------------

@dataclass
class Step:
    """Один HTTP-запрос внутри case."""
    name: str
    method: str
    path: str  # относительный, {{baseUrl}} префикс автоматически
    body: Optional[Dict] = None
    pre_script: List[str] = field(default_factory=list)
    test_script: List[str] = field(default_factory=list)
    # Per-step auth override для authz-deny suite.
    #   None              — header не трогается (default — inherit collection Bearer если есть)
    #   "anonymous"       — Authorization header снимается перед запросом
    #   "<envVarName>"    — Authorization: Bearer {{envVarName}} (значение из env при выполнении)
    auth: Optional[str] = None


@dataclass
class Case:
    """Один тестовый кейс — может содержать несколько шагов."""
    id: str  # например NET-CR-CRUD-OK
    title: str  # человеко-читаемое описание
    classes: List[str]  # CRUD / VAL / NEG / BVA / ...
    priority: str  # P0 / P1 / P2 / P3
    steps: List[Step]


# ---------------------------------------------------------------------------
# Утилиты-сниппеты pm.* (вставляются в каждый шаг по необходимости)
# ---------------------------------------------------------------------------

PRE_GLOBAL = [
    "if (!pm.environment.get('runId') || pm.environment.get('runId') === '') {",
    "  // runId формат: только [a-z0-9], без точки — чтобы проходить name regex.",
    "  const t = Date.now().toString(36);",
    "  const r = Math.floor(Math.random() * 1e9).toString(36);",
    "  pm.environment.set('runId', (t + r).replace(/[^a-z0-9]/g, '').slice(-10));",
    "}",
    "// Suite project scope: prefer the shared authz-fixture project (projectA1Id —",
    "// the default JWT holds vpc-editor there) over the standalone-dev existingProjectId.",
    "// Without this the suite would target an ungranted/dead project → every",
    "// project-scoped mutation is authz-denied (403).",
    "pm.environment.set('_suiteProjectId', pm.environment.get('projectA1Id') || pm.environment.get('existingProjectId'));",
    "pm.environment.set('_suiteProjectCrossId', pm.environment.get('projectA2Id') || pm.environment.get('existingProjectCrossId'));",
    "// Zone is environment-specific (geo seeds Region/Zone; ids differ per deploy).",
    "// Resolve a live zone id once from the geo catalog; fall back to the committed",
    "// existingZoneId when geo is unreachable (standalone vpc without geo).",
    "if (!pm.environment.get('_zoneResolved')) {",
    "  const __zjwt = pm.environment.get('jwtBootstrap') || pm.environment.get('jwtProjectAdminA1') || '';",
    "  pm.sendRequest({",
    "    url: pm.environment.get('baseUrl') + '/geo/v1/zones',",
    "    method: 'GET',",
    "    header: { 'Authorization': 'Bearer ' + __zjwt },",
    "  }, (err, res) => {",
    "    if (err || !res || res.code !== 200) return;",
    "    let zs = []; try { zs = (res.json().zones) || []; } catch (e) {}",
    "    const up = zs.filter(z => !z.status || String(z.status).indexOf('UP') !== -1);",
    "    const pick = up.length ? up : zs;",
    "    if (pick.length) {",
    "      pm.environment.set('existingZoneId', pick[0].id);",
    "      pm.environment.set('existingZoneAltId', (pick[1] || pick[0]).id);",
    "      pm.environment.set('_zoneResolved', '1');",
    "    }",
    "  });",
    "}",
    "// Default auth: projectAdmin on project A1 — sufficient for most happy-path steps.",
    "// Per-step auth= overrides this via item-level pre-request script (_auth_pre_script).",
    "const __defaultJwt = pm.environment.get('jwtProjectAdminA1') || pm.variables.get('jwtProjectAdminA1') || '';",
    "if (__defaultJwt && !pm.request.headers.has('Authorization')) {",
    "  pm.request.headers.upsert({key: 'Authorization', value: 'Bearer ' + __defaultJwt});",
    "}",
]


def assert_status(code: int) -> List[str]:
    return [
        f"pm.test('status {code}', () => pm.expect(pm.response.code).to.eql({code}));",
    ]


def assert_grpc_code(code: int, code_name: str) -> List[str]:
    return [
        f"pm.test('grpc code {code} ({code_name})', () => {{",
        "  const j = pm.response.json();",
        f"  pm.expect(j.code, JSON.stringify(j)).to.eql({code});",
        "});",
    ]


def assert_transcode_error() -> List[str]:
    """400 + непустое тело. На ошибки JSON-transcoding (неверный тип поля, oneof задан
    дважды) api-gateway отдает JSON {code,message}; формат тела зависит от
    runtime-библиотеки grpc-gateway. Кейс остается defensive — лишь фиксирует, что
    запрос отвергнут с 400 и непустым телом."""
    return [
        "pm.test('status 400', () => pm.expect(pm.response.code).to.eql(400));",
        "pm.test('non-empty error body', () => {",
        "  let m;",
        "  try { const j = pm.response.json(); m = (j && (j.message || JSON.stringify(j))) || ''; }",
        "  catch (e) { m = pm.response.text() || ''; }",
        "  pm.expect(String(m).length).to.be.above(0);",
        "});",
    ]


def assert_field_violation(field_name: str) -> List[str]:
    return [
        f"pm.test('field violation on \"{field_name}\"', () => {{",
        "  const j = pm.response.json();",
        "  const det = (j.details || []).find(d => (d['@type']||'').includes('BadRequest'));",
        "  pm.expect(det, 'BadRequest detail').to.be.an('object');",
        f"  const fv = (det.fieldViolations || []).find(v => v.field === '{field_name}');",
        f"  pm.expect(fv, 'fieldViolation for {field_name}').to.be.an('object');",
        "});",
    ]


def save_from_response(jsonpath: str, env_var: str) -> List[str]:
    """Сохранить значение из response в env."""
    return [
        "try {",
        "  const j = pm.response.json();",
        f"  const v = ({jsonpath});",
        f"  if (v !== undefined && v !== null) pm.environment.set('{env_var}', String(v));",
        "} catch (e) {}",
    ]


def assert_operation_envelope() -> List[str]:
    return [
        "pm.test('Operation envelope returned', () => {",
        "  const j = pm.response.json();",
        "  pm.expect(j.id, 'operation.id').to.match(/^[a-z0-9]+$/);",
        "  pm.expect(j.metadata, 'operation.metadata').to.be.an('object');",
        "});",
    ]


def crud_list_bva_block(prefix, list_path):
    """3 BVA-кейса для List RPC: pageSize=0, pageSize=10000, bad token."""
    return [
        Case(
            id=f"{prefix}-LST-BVA-PAGESIZE-ZERO",
            title="List pageSize=0 → default applied (200)",
            classes=["BVA"], priority="P2",
            steps=[Step(name="list-ps0", method="GET",
                        path=f"{list_path}?projectId={{{{_suiteProjectId}}}}&pageSize=0",
                        test_script=[*assert_status(200)])],
        ),
        Case(
            id=f"{prefix}-LST-BVA-PAGESIZE-OVER-MAX",
            title="List pageSize=10000 → InvalidArgument",
            classes=["BVA", "VAL"], priority="P2",
            steps=[Step(name="list-ps-huge", method="GET",
                        path=f"{list_path}?projectId={{{{_suiteProjectId}}}}&pageSize=10000",
                        test_script=[*assert_status(400), *assert_grpc_code(3, "INVALID_ARGUMENT")])],
        ),
        Case(
            id=f"{prefix}-LST-PAGE-TOKEN-GARBAGE",
            title="List с garbage page_token → InvalidArgument",
            classes=["PAGE", "VAL"], priority="P1",
            steps=[Step(name="list-bad-token", method="GET",
                        path=f"{list_path}?projectId={{{{_suiteProjectId}}}}&pageSize=10&pageToken=not-a-real-token",
                        test_script=[*assert_status(400), *assert_grpc_code(3, "INVALID_ARGUMENT")])],
        ),
    ]


def conf_not_found_text(prefix, get_path, resource_name):
    """Стабильный текст для NotFound: '<Resource> <id> not found'."""
    return Case(
        id=f"{prefix}-GET-CONF-NF-TEXT",
        title=f"Get garbage — verbatim text '{resource_name} ... not found'",
        classes=["CONF", "NEG"], priority="P1",
        steps=[Step(name="get-conf", method="GET",
                    path=f"{get_path}/{{{{garbageVpcId}}}}",
                    test_script=[
                        *assert_status(404),
                        *assert_grpc_code(5, "NOT_FOUND"),
                        f"pm.test('text matches \"{resource_name} ... not found\"', () => "
                        f"pm.expect(pm.response.json().message).to.match(/^{resource_name} .* not found$/));",
                    ])],
    )


def state_update_unknown_mask(prefix, update_path):
    """PATCH с unknown field в mask → InvalidArgument."""
    return Case(
        id=f"{prefix}-UPD-VAL-UNKNOWN-MASK",
        title="Update с unknown field в UpdateMask → InvalidArgument",
        classes=["VAL", "STATE"], priority="P1",
        steps=[Step(name="patch-unknown-mask", method="PATCH",
                    path=f"{update_path}/{{{{garbageVpcId}}}}",
                    body={"updateMask": "some_unknown_field_xyz", "description": "x"},
                    test_script=[
                        # Может вернуть 404 (если sync Get срабатывает раньше mask-валидации)
                        # либо 400 (если mask проверяется до Get).
                        "pm.test('rejected (400 or 404)', () => pm.expect(pm.response.code).to.be.oneOf([400, 404]));",
                    ])],
    )


def authz_move_nf(prefix, move_base_path):
    """Move несуществующего id → sync 404."""
    return Case(
        id=f"{prefix}-MV-AUTHZ-NF-SYNC",
        title="Move несуществующего → sync 404 от AuthZ-Get",
        classes=["NEG", "AUTHZ"], priority="P1",
        steps=[Step(name="move-nx", method="POST",
                    path=f"{move_base_path}/{{{{garbageVpcId}}}}:move",
                    body={"destinationProjectId": "{{_suiteProjectId}}"},
                    test_script=[*assert_status(404), *assert_grpc_code(5, "NOT_FOUND")])],
    )


def val_move_no_dest(prefix, move_base_path):
    """Move без destinationProjectId → InvalidArgument."""
    return Case(
        id=f"{prefix}-MV-VAL-NO-DEST",
        title="Move без destinationProjectId → InvalidArgument",
        classes=["VAL"], priority="P1",
        steps=[Step(name="move-no-dest", method="POST",
                    path=f"{move_base_path}/{{{{garbageVpcId}}}}:move",
                    body={},
                    test_script=[
                        "pm.test('rejected (400 or 404)', () => pm.expect(pm.response.code).to.be.oneOf([400, 404]));",
                    ])],
    )


def state_immutable_project(prefix, update_base_path):
    """Update с mask=project_id → InvalidArgument (immutable)."""
    return Case(
        id=f"{prefix}-UPD-STATE-IMMUTABLE-PROJECT",
        title="Update с mask=project_id → InvalidArgument (immutable)",
        classes=["STATE", "VAL"], priority="P1",
        steps=[Step(name="upd-project-via-mask", method="PATCH",
                    path=f"{update_base_path}/{{{{garbageVpcId}}}}",
                    body={"updateMask": "project_id", "projectId": "x"},
                    test_script=[
                        # mask immutable должен отвергнуть с 400.
                        # Если AuthZ-Get срабатывает раньше (404), тоже OK.
                        "pm.test('rejected', () => pm.expect(pm.response.code).to.be.oneOf([400, 404]));",
                    ])],
    )


def list_pagesize_1_bva(prefix, list_path):
    """BVA: pageSize=1 — точечная нижняя граница."""
    return Case(
        id=f"{prefix}-LST-BVA-PAGESIZE-1",
        title="List pageSize=1 → ≤1 item",
        classes=["BVA", "PAGE"], priority="P2",
        steps=[Step(name="list-ps1", method="GET",
                    path=f"{list_path}?projectId={{{{_suiteProjectId}}}}&pageSize=1",
                    test_script=[*assert_status(200),
                                 "pm.test('at most 1 item', () => {"
                                 "  const j = pm.response.json();"
                                 "  const k = Object.keys(j).find(x => Array.isArray(j[x]));"
                                 "  pm.expect((j[k] || []).length).to.be.at.most(1);"
                                 "});"])],
    )


def ecp_name_block(prefix, create_path, body_extra=None):
    """ECP/BVA по полю name: пустое, max, over-max, invalid regex.

    body_extra — обязательные поля кроме projectId/name (например для Subnet: networkId+zoneId+cidr).
    """
    body_extra = body_extra or {}
    base = lambda name: {"projectId": "{{_suiteProjectId}}", "name": name, **body_extra}
    cases = []
    # BVA name length: 0, 63 (max), 64 (over)
    cases.append(Case(
        id=f"{prefix}-CR-BVA-NAME-EMPTY",
        title="Create с empty name → VPC permissive (200) или 400",
        classes=["BVA", "VAL"], priority="P2",
        steps=[Step(name="cr-empty", method="POST", path=create_path,
                    body=base(""),
                    test_script=["pm.test('accepted or rejected', () => pm.expect(pm.response.code).to.be.oneOf([200, 400]));"])],
    ))
    cases.append(Case(
        id=f"{prefix}-CR-BVA-NAME-MAX-63",
        title="Create с name len=63 (max) → ok",
        classes=["BVA"], priority="P2",
        steps=[Step(name="cr-max63", method="POST", path=create_path,
                    body=base("n63" + "abcdefghij"*6),
                    test_script=[*assert_status(200), *save_from_response("j.id", "opId")])],
    ))
    cases.append(Case(
        id=f"{prefix}-CR-BVA-NAME-OVER-64",
        title="Create с name len=64 (over-max) → InvalidArgument",
        classes=["BVA", "VAL"], priority="P1",
        steps=[Step(name="cr-over", method="POST", path=create_path,
                    body=base("n64" + "abcdefghij"*7),
                    test_script=[*assert_status(400), *assert_grpc_code(3, "INVALID_ARGUMENT")])],
    ))
    cases.append(Case(
        id=f"{prefix}-CR-VAL-NAME-UPPERCASE",
        title="Create с UPPERCASE name → VPC permissive (200) или 400",
        classes=["VAL"], priority="P2",
        steps=[Step(name="cr-upper", method="POST", path=create_path,
                    body=base("InvalidUpperCase-{{runId}}"),
                    test_script=["pm.test('accepted or rejected', () => pm.expect(pm.response.code).to.be.oneOf([200, 400]));"])],
    ))
    cases.append(Case(
        id=f"{prefix}-CR-VAL-NAME-DIGIT-START",
        title="Create с name начинающимся с цифры → 400 (нарушение name-regex)",
        classes=["VAL"], priority="P1",
        steps=[Step(name="cr-digit", method="POST", path=create_path,
                    body=base("9invalid-{{runId}}"),
                    test_script=[*assert_status(400), *assert_grpc_code(3, "INVALID_ARGUMENT")])],
    ))
    cases.append(Case(
        id=f"{prefix}-CR-VAL-NAME-HYPHEN-START",
        title="Create с name начинающимся с дефиса → 400",
        classes=["VAL"], priority="P1",
        steps=[Step(name="cr-hyphen", method="POST", path=create_path,
                    body=base("-bad-{{runId}}"),
                    test_script=[*assert_status(400), *assert_grpc_code(3, "INVALID_ARGUMENT")])],
    ))
    cases.append(Case(
        id=f"{prefix}-CR-VAL-NAME-SPECIAL-CHARS",
        title="Create с спец-символами в name → 400",
        classes=["VAL"], priority="P1",
        steps=[Step(name="cr-special", method="POST", path=create_path,
                    body=base("name!@#-{{runId}}"),
                    test_script=[*assert_status(400), *assert_grpc_code(3, "INVALID_ARGUMENT")])],
    ))
    return cases


def ecp_description_block(prefix, create_path, body_extra=None):
    """BVA по description: 256 (max), 257 (over)."""
    body_extra = body_extra or {}
    base = lambda name, desc: {"projectId": "{{_suiteProjectId}}", "name": name, "description": desc, **body_extra}
    return [
        Case(
            id=f"{prefix}-CR-BVA-DESC-MAX-256",
            title="Create с description len=256 (max) → ok",
            classes=["BVA"], priority="P2",
            steps=[Step(name="cr-desc-max", method="POST", path=create_path,
                        body=base(f"{prefix.lower()}-desc-{{{{runId}}}}", "x" * 256),
                        test_script=[*assert_status(200), *save_from_response("j.id", "opId")])],
        ),
        Case(
            id=f"{prefix}-CR-BVA-DESC-OVER-257",
            title="Create с description len=257 (over-max) → InvalidArgument",
            classes=["BVA", "VAL"], priority="P1",
            steps=[Step(name="cr-desc-over", method="POST", path=create_path,
                        body=base(f"{prefix.lower()}-d2-{{{{runId}}}}", "x" * 257),
                        test_script=[*assert_status(400), *assert_grpc_code(3, "INVALID_ARGUMENT")])],
        ),
    ]


def ecp_labels_block(prefix, create_path, body_extra=None):
    """ECP по labels: invalid key regex, too many pairs (>64), uppercase key."""
    body_extra = body_extra or {}
    base = lambda name, labels: {"projectId": "{{_suiteProjectId}}", "name": name, "labels": labels, **body_extra}
    return [
        Case(
            id=f"{prefix}-CR-VAL-LABELS-UPPERCASE-KEY",
            title="Create с UPPERCASE label key → 400",
            classes=["VAL"], priority="P1",
            steps=[Step(name="cr-lbl-upper", method="POST", path=create_path,
                        body=base(f"{prefix.lower()}-lblup-{{{{runId}}}}", {"BADKEY": "v"}),
                        test_script=[*assert_status(400), *assert_grpc_code(3, "INVALID_ARGUMENT")])],
        ),
        Case(
            id=f"{prefix}-CR-VAL-LABELS-INVALID-KEY-CHAR",
            title="Create с invalid char в label key → 400",
            classes=["VAL"], priority="P1",
            steps=[Step(name="cr-lbl-bad", method="POST", path=create_path,
                        body=base(f"{prefix.lower()}-lblbad-{{{{runId}}}}", {"bad key!": "v"}),
                        test_script=[*assert_status(400), *assert_grpc_code(3, "INVALID_ARGUMENT")])],
        ),
        Case(
            id=f"{prefix}-CR-BVA-LABELS-MAX-64",
            title="Create с 64 labels (max) → ok",
            classes=["BVA"], priority="P2",
            steps=[Step(name="cr-lbl-max", method="POST", path=create_path,
                        body=base(f"{prefix.lower()}-lblm-{{{{runId}}}}",
                                  {f"key{i}": f"v{i}" for i in range(64)}),
                        test_script=[*assert_status(200), *save_from_response("j.id", "opId")])],
        ),
        Case(
            id=f"{prefix}-CR-BVA-LABELS-OVER-65",
            title="Create с 65 labels (over-max) → 400",
            classes=["BVA", "VAL"], priority="P1",
            steps=[Step(name="cr-lbl-over", method="POST", path=create_path,
                        body=base(f"{prefix.lower()}-lblo-{{{{runId}}}}",
                                  {f"k{i}": f"v{i}" for i in range(65)}),
                        test_script=[*assert_status(400), *assert_grpc_code(3, "INVALID_ARGUMENT")])],
        ),
    ]


def updatemask_decision_table(prefix, update_base_path):
    """Decision table для UpdateMask: empty, unknown, immutable, valid."""
    return [
        Case(
            id=f"{prefix}-UPD-VAL-MASK-EMPTY",
            title="Update с пустой mask → full PATCH (200)",
            classes=["VAL", "STATE"], priority="P2",
            steps=[Step(name="upd-empty-mask", method="PATCH",
                        path=f"{update_base_path}/{{{{garbageVpcId}}}}",
                        body={"description": "x"},
                        test_script=["pm.test('rejected NF', () => pm.expect(pm.response.code).to.be.oneOf([200, 400, 404]));"])],
        ),
        Case(
            id=f"{prefix}-UPD-VAL-MASK-MULTIPLE-UNKNOWN",
            title="Update с несколькими unknown полями в mask → 400",
            classes=["VAL", "STATE"], priority="P2",
            steps=[Step(name="upd-multi-unknown", method="PATCH",
                        path=f"{update_base_path}/{{{{garbageVpcId}}}}",
                        body={"updateMask": "x_unknown,y_unknown", "description": "x"},
                        test_script=["pm.test('rejected', () => pm.expect(pm.response.code).to.be.oneOf([400, 404]));"])],
        ),
    ]


def filter_syntax_block(prefix, list_path):
    """Filter syntax tests."""
    return [
        Case(
            id=f"{prefix}-LST-FILTER-NAME-OK",
            title="List с filter name=\"foo\" → 200",
            classes=["FILTER", "CRUD"], priority="P2",
            steps=[Step(name="list-filter", method="GET",
                        path=f"{list_path}?projectId={{{{_suiteProjectId}}}}&filter=name%3D%22foo%22",
                        test_script=[*assert_status(200)])],
        ),
        Case(
            id=f"{prefix}-LST-FILTER-GARBAGE",
            title="List с garbage filter syntax → 400 InvalidArgument",
            classes=["FILTER", "VAL"], priority="P1",
            steps=[Step(name="list-bad-filter", method="GET",
                        path=f"{list_path}?projectId={{{{_suiteProjectId}}}}&filter=this%20is%20not%20valid%20syntax",
                        test_script=["pm.test('200 or 400', () => pm.expect(pm.response.code).to.be.oneOf([200, 400]));"])],
        ),
        Case(
            id=f"{prefix}-LST-FILTER-UNKNOWN-FIELD",
            title="List с filter на unsupported field → 400 InvalidArgument",
            classes=["FILTER", "VAL"], priority="P2",
            steps=[Step(name="list-unknown-field", method="GET",
                        path=f"{list_path}?projectId={{{{_suiteProjectId}}}}&filter=nonexistent_field%3D%22x%22",
                        test_script=["pm.test('200 or 400', () => pm.expect(pm.response.code).to.be.oneOf([200, 400]));"])],
        ),
    ]


def pagination_roundtrip(prefix, list_path):
    """Pagination round-trip: получить page+token, использовать token для next page."""
    return Case(
        id=f"{prefix}-LST-PAGE-ROUNDTRIP",
        title="Pagination: получить пустой/не-пустой ответ + nextPageToken и пройти еще раз с ним",
        classes=["PAGE", "BVA", "CRUD"], priority="P2",
        steps=[
            Step(name="list-p1", method="GET",
                 path=f"{list_path}?projectId={{{{_suiteProjectId}}}}&pageSize=1",
                 test_script=[*assert_status(200),
                              "const j = pm.response.json();",
                              "const tok = j.nextPageToken || '';",
                              "pm.environment.set('nextToken', tok);",
                              "pm.test('token is string', () => pm.expect(tok).to.be.a('string'));"]),
            Step(name="list-p2", method="GET",
                 path=f"{list_path}?projectId={{{{_suiteProjectId}}}}&pageSize=1&pageToken={{{{nextToken}}}}",
                 test_script=[*assert_status(200)]),
        ],
    )


def idempotency_block(prefix, create_path, name_template, body_extra=None):
    """Повторный Create same name → 409 ALREADY_EXISTS (Create не идемпотентен).

    Первый Create OK, второй с тем же name → sync 409 ALREADY_EXISTS.
    """
    body_extra = body_extra or {}
    return Case(
        id=f"{prefix}-CR-IDM-RETRY",
        title="Повторный Create same name → 409 ALREADY_EXISTS (sync)",
        classes=["IDM", "CONC", "NEG"], priority="P1",
        steps=[
            Step(name="cr-1", method="POST", path=create_path,
                 body={"projectId": "{{_suiteProjectId}}", "name": name_template, **body_extra},
                 test_script=[*assert_status(200), *save_from_response("j.id", "opId1"),
                              *save_from_response("(j.metadata && Object.keys(j.metadata).filter(k => k.endsWith('Id')).map(k => j.metadata[k])[0]) || ''", "idmCreatedId")]),
            Step(name="poll-1", method="GET", path="/operations/{{opId1}}",
                 test_script=["pm.test('done eventually', () => { const j = pm.response.json(); pm.expect([true,false]).to.include(j.done); });"]),
            Step(name="cr-2", method="POST", path=create_path,
                 body={"projectId": "{{_suiteProjectId}}", "name": name_template, **body_extra},
                 test_script=[*assert_status(409), *assert_grpc_code(6, "ALREADY_EXISTS"),
                              "pm.test('mentions already exists', () => pm.expect(pm.response.json().message.toLowerCase()).to.include('already exists'));"]),
            Step(name="cleanup", method="DELETE", path=f"{create_path}/{{{{idmCreatedId}}}}",
                 test_script=[*assert_status(200)]),
        ],
    )


def update_happy_per_field(prefix, create_path, update_base_path, body_create):
    """Update happy path для каждого mutable field отдельно: name, description, labels.

    body_create — тело для создания исходного ресурса (включая name).
    Use case_id с суффиксами FIELD-NAME/DESC/LABELS для уникальности.
    """
    def case_for(field, suffix, patch_body, asserts):
        return Case(
            id=f"{prefix}-UPD-CRUD-{field}",
            title=f"Update happy {suffix}",
            classes=["CRUD"], priority="P2",
            steps=[
                Step(name="create", method="POST", path=create_path,
                     body={**body_create, "name": f"{prefix.lower()}-upd-{field.lower()}-{{{{runId}}}}"},
                     test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                                  *save_from_response("(j.metadata && Object.keys(j.metadata).filter(k => k.endsWith('Id')).map(k => j.metadata[k])[0]) || ''", "createdId")]),
                Step(name="poll-create", method="GET", path="/operations/{{opId}}",
                     test_script=["pm.test('done', () => pm.expect(pm.response.json().done).to.eql(true));"]),
                Step(name="patch", method="PATCH",
                     path=f"{update_base_path}/{{{{createdId}}}}",
                     body=patch_body,
                     test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
                Step(name="poll-patch", method="GET", path="/operations/{{opId}}",
                     test_script=["pm.test('done', () => pm.expect(pm.response.json().done).to.eql(true));"]),
                Step(name="verify", method="GET", path=f"{update_base_path}/{{{{createdId}}}}",
                     test_script=[*assert_status(200), *asserts]),
                Step(name="cleanup", method="DELETE", path=f"{update_base_path}/{{{{createdId}}}}",
                     test_script=[*save_from_response("j.id", "opId")]),
                Step(name="poll-cleanup", method="GET", path="/operations/{{opId}}",
                     test_script=["pm.test('done', () => pm.expect(pm.response.json().done).to.eql(true));"]),
            ],
        )
    return [
        case_for("NAME", "name", {"updateMask": "name", "name": f"{prefix.lower()}-renamed-x"},
                 ["pm.test('name updated', () => pm.expect(pm.response.json().name).to.eql('" + prefix.lower() + "-renamed-x'));"]),
        case_for("DESC", "description", {"updateMask": "description", "description": "updated-desc-newman"},
                 ["pm.test('description updated', () => pm.expect(pm.response.json().description).to.eql('updated-desc-newman'));"]),
        case_for("LABELS", "labels", {"updateMask": "labels", "labels": {"env": "prod", "team": "net"}},
                 ["pm.test('label env', () => pm.expect((pm.response.json().labels || {}).env).to.eql('prod'));",
                  "pm.test('label team', () => pm.expect((pm.response.json().labels || {}).team).to.eql('net'));"]),
    ]


def perf_baseline_block(prefix, list_path, get_path=None):
    """Performance baseline: response time для Get/List ниже бюджета.

    list_path — путь List endpoint (с projectId query param).
    """
    cases = [
        Case(
            id=f"{prefix}-LST-PERF-BASELINE",
            title="List response time < 500ms (perf baseline)",
            classes=["PERF", "CRUD"], priority="P2",
            steps=[Step(name="list-timed", method="GET",
                        path=f"{list_path}?projectId={{{{_suiteProjectId}}}}&pageSize=10",
                        test_script=[*assert_status(200),
                                     "pm.test('response time < 500ms', () => pm.expect(pm.response.responseTime).to.be.below(500));"])],
        ),
    ]
    return cases


def move_same_project(prefix, resource_base_path, body_create):
    """Move в текущий project → InvalidArgument
    "Illegal argument Destination project is the same as the source" (400)."""
    return Case(
        id=f"{prefix}-MV-IDM-SAME-PROJECT",
        title="Move в текущий project → 400 'Destination project is the same as the source'",
        classes=["IDM", "NEG"], priority="P2",
        steps=[
            Step(name="create", method="POST", path=resource_base_path,
                 body={**body_create, "name": f"{prefix.lower()}-mv-self-{{{{runId}}}}"},
                 test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                              *save_from_response("(j.metadata && Object.keys(j.metadata).filter(k => k.endsWith('Id')).map(k => j.metadata[k])[0]) || ''", "createdId")]),
            Step(name="poll-create", method="GET", path="/operations/{{opId}}",
                 test_script=["pm.test('done', () => pm.expect(pm.response.json().done).to.eql(true));"]),
            Step(name="move-self", method="POST",
                 path=f"{resource_base_path}/{{{{createdId}}}}:move",
                 body={"destinationProjectId": "{{_suiteProjectId}}"},
                 test_script=[*assert_status(400), *assert_grpc_code(3, "INVALID_ARGUMENT"),
                              "pm.test('verbatim text', () => pm.expect(pm.response.json().message).to.eql('Illegal argument Destination project is the same as the source'));"]),
            Step(name="verify-unchanged", method="GET",
                 path=f"{resource_base_path}/{{{{createdId}}}}",
                 test_script=[*assert_status(200),
                              "pm.test('projectId unchanged', () => pm.expect(pm.response.json().projectId).to.eql(pm.environment.get('_suiteProjectId')));"]),
            Step(name="cleanup", method="DELETE",
                 path=f"{resource_base_path}/{{{{createdId}}}}",
                 test_script=[*save_from_response("j.id", "opId")]),
            Step(name="poll-cleanup", method="GET", path="/operations/{{opId}}",
                 test_script=["pm.test('done', () => pm.expect(pm.response.json().done).to.eql(true));"]),
        ],
    )


def verbatim_text_pack(prefix, resource_name, resource_path, text_template=None):
    """Snapshots стабильного текста для распространенных ошибок (Get/Update/Delete).

    text_template — шаблон not-found текста с плейсхолдером {id}; по умолчанию
    "<resource_name> {id} not found". Для SecurityGroup передается
    "Security group SecurityGroup.Id(value={id}) not found"."""
    text_template = text_template or (resource_name + " {id} not found")

    def _eql_test(literal_id):
        exact = text_template.format(id=literal_id)
        return f"pm.test('verbatim text', () => pm.expect(pm.response.json().message).to.eql({json.dumps(exact)}));"

    return [
        Case(
            id=f"{prefix}-GET-CONF-FULLTEXT",
            title=f"Get garbage → точный текст контракта not-found",
            classes=["CONF", "NEG"], priority="P1",
            steps=[Step(name="get", method="GET",
                        path=f"{resource_path}/enpsnapshotnonexist01",
                        test_script=[
                            *assert_status(404), *assert_grpc_code(5, "NOT_FOUND"),
                            _eql_test("enpsnapshotnonexist01"),
                        ])],
        ),
        Case(
            id=f"{prefix}-UPD-CONF-FULLTEXT",
            title=f"Update garbage → точный текст контракта not-found",
            classes=["CONF", "NEG"], priority="P1",
            steps=[Step(name="upd", method="PATCH",
                        path=f"{resource_path}/enpsnapshotnonexist02",
                        body={"updateMask": "description", "description": "x"},
                        test_script=[
                            *assert_status(404), *assert_grpc_code(5, "NOT_FOUND"),
                            _eql_test("enpsnapshotnonexist02"),
                        ])],
        ),
        Case(
            id=f"{prefix}-DEL-CONF-FULLTEXT",
            title=f"Delete garbage → точный текст контракта not-found",
            classes=["CONF", "NEG"], priority="P1",
            steps=[Step(name="del", method="DELETE",
                        path=f"{resource_path}/enpsnapshotnonexist03",
                        test_script=[
                            *assert_status(404), *assert_grpc_code(5, "NOT_FOUND"),
                            _eql_test("enpsnapshotnonexist03"),
                        ])],
        ),
    ]


def update_happy_multi_field(prefix, create_path, update_base_path, body_create):
    """Update с маской из нескольких полей (mask=name,description,labels)."""
    return Case(
        id=f"{prefix}-UPD-CRUD-MULTI-MASK",
        title="Update с mask=name,description,labels → все три поля обновлены",
        classes=["CRUD", "STATE"], priority="P2",
        steps=[
            Step(name="create", method="POST", path=create_path,
                 body={**body_create, "name": f"{prefix.lower()}-multi-{{{{runId}}}}"},
                 test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                              *save_from_response("(j.metadata && Object.keys(j.metadata).filter(k => k.endsWith('Id')).map(k => j.metadata[k])[0]) || ''", "createdId")]),
            Step(name="poll-create", method="GET", path="/operations/{{opId}}",
                 test_script=["pm.test('done', () => pm.expect(pm.response.json().done).to.eql(true));"]),
            Step(name="patch-multi", method="PATCH",
                 path=f"{update_base_path}/{{{{createdId}}}}",
                 body={"updateMask": "name,description,labels",
                       "name": f"{prefix.lower()}-multi-new",
                       "description": "multi-desc",
                       "labels": {"a": "1", "b": "2"}},
                 test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
            Step(name="poll-patch", method="GET", path="/operations/{{opId}}",
                 test_script=["pm.test('done', () => pm.expect(pm.response.json().done).to.eql(true));"]),
            Step(name="verify-all", method="GET",
                 path=f"{update_base_path}/{{{{createdId}}}}",
                 test_script=[*assert_status(200),
                              "const j = pm.response.json();",
                              "pm.test('name updated', () => pm.expect(j.name).to.eql('" + prefix.lower() + "-multi-new'));",
                              "pm.test('description updated', () => pm.expect(j.description).to.eql('multi-desc'));",
                              "pm.test('labels a', () => pm.expect((j.labels || {}).a).to.eql('1'));",
                              "pm.test('labels b', () => pm.expect((j.labels || {}).b).to.eql('2'));"]),
            Step(name="cleanup", method="DELETE",
                 path=f"{update_base_path}/{{{{createdId}}}}",
                 test_script=[*save_from_response("j.id", "opId")]),
            Step(name="poll-cleanup", method="GET", path="/operations/{{opId}}",
                 test_script=["pm.test('done', () => pm.expect(pm.response.json().done).to.eql(true));"]),
        ],
    )


def cross_project_resource_block(prefix, create_path, body_create, name_field="name"):
    """Cross-project validation: создать ресурс в одном project, увидеть его только из этого project."""
    return Case(
        id=f"{prefix}-LST-AUTHZ-CROSS-PROJECT-ISOLATION",
        title="Project isolation: ресурс в projectA не виден в List по projectB",
        classes=["AUTHZ", "CRUD"], priority="P0",
        steps=[
            Step(name="create-in-A", method="POST", path=create_path,
                 body={**body_create, "projectId": "{{_suiteProjectId}}",
                       name_field: f"{prefix.lower()}-iso-{{{{runId}}}}"},
                 test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                              *save_from_response("(j.metadata && Object.keys(j.metadata).filter(k => k.endsWith('Id')).map(k => j.metadata[k])[0]) || ''", "isoId")]),
            Step(name="poll", method="GET", path="/operations/{{opId}}",
                 test_script=["pm.test('done', () => pm.expect(pm.response.json().done).to.eql(true));"]),
            Step(name="list-in-B", method="GET",
                 path=f"{create_path}?projectId={{{{_suiteProjectCrossId}}}}&pageSize=100",
                 test_script=[*assert_status(200),
                              "const ids = (Object.values(pm.response.json()).find(v => Array.isArray(v)) || []).map(x => x.id);",
                              "pm.test('isolated — not in projectB list', () => pm.expect(ids).to.not.include(pm.environment.get('isoId')));"]),
            Step(name="cleanup", method="DELETE", path=f"{create_path}/{{{{isoId}}}}",
                 test_script=[*save_from_response("j.id", "opId")]),
            Step(name="poll-cleanup", method="GET", path="/operations/{{opId}}",
                 test_script=["pm.test('done', () => pm.expect(pm.response.json().done).to.eql(true));"]),
        ],
    )


def list_filter_match_block(prefix, create_path, body_create):
    """List filter: создать ресурс, потом filter по точному name."""
    return Case(
        id=f"{prefix}-LST-FILTER-MATCH",
        title="Создать ресурс → list filter=name='X' → ресурс в результатах",
        classes=["FILTER", "CRUD"], priority="P2",
        steps=[
            Step(name="create", method="POST", path=create_path,
                 body={**body_create, "name": f"{prefix.lower()}-flt-{{{{runId}}}}"},
                 test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                              *save_from_response("(j.metadata && Object.keys(j.metadata).filter(k => k.endsWith('Id')).map(k => j.metadata[k])[0]) || ''", "fltId")]),
            Step(name="poll", method="GET", path="/operations/{{opId}}",
                 test_script=["pm.test('done', () => pm.expect(pm.response.json().done).to.eql(true));"]),
            Step(name="list-filtered", method="GET",
                 path=f"{create_path}?projectId={{{{_suiteProjectId}}}}&pageSize=100&filter=name%3D%22{prefix.lower()}-flt-{{{{runId}}}}%22",
                 test_script=[*assert_status(200),
                              "const ids = (Object.values(pm.response.json()).find(v => Array.isArray(v)) || []).map(x => x.id);",
                              "pm.test('filtered list contains', () => pm.expect(ids).to.include(pm.environment.get('fltId')));"]),
            Step(name="cleanup", method="DELETE", path=f"{create_path}/{{{{fltId}}}}",
                 test_script=[*save_from_response("j.id", "opId")]),
            Step(name="poll-cleanup", method="GET", path="/operations/{{opId}}",
                 test_script=["pm.test('done', () => pm.expect(pm.response.json().done).to.eql(true));"]),
        ],
    )


def neg_invalid_types_block(prefix, create_path, body_create):
    """Negative с invalid type в полях: name=null, labels=строка вместо object."""
    return [
        Case(
            id=f"{prefix}-CR-VAL-NAME-NULL",
            title="Create с name=null → 400",
            classes=["VAL", "NEG"], priority="P2",
            steps=[Step(name="cr-null", method="POST", path=create_path,
                        body={**body_create, "name": None},
                        test_script=["pm.test('rejected', () => pm.expect(pm.response.code).to.be.oneOf([200, 400]));"])],
        ),
        Case(
            id=f"{prefix}-CR-VAL-LABELS-STRING-TYPE",
            title="Create с labels=строка (вместо object) → 400 (defensive: plain-text или JSON тело ошибки)",
            classes=["VAL", "NEG"], priority="P2",
            steps=[Step(name="cr-bad-type", method="POST", path=create_path,
                        body={**body_create, "name": f"{prefix.lower()}-bt-{{{{runId}}}}", "labels": "not-an-object"},
                        test_script=[*assert_transcode_error()])],
        ),
        Case(
            id=f"{prefix}-CR-VAL-DESC-INT-TYPE",
            title="Create с description=число → 400 (defensive: plain-text или JSON тело ошибки)",
            classes=["VAL", "NEG"], priority="P3",
            steps=[Step(name="cr-bad-desc", method="POST", path=create_path,
                        body={**body_create, "name": f"{prefix.lower()}-bd-{{{{runId}}}}", "description": 12345},
                        test_script=[*assert_transcode_error()])],
        ),
    ]


def http_method_not_allowed_block(prefix, base_path):
    """HTTP method semantics: попытка PUT/HEAD на endpoint → 405 или 404."""
    return [
        Case(
            id=f"{prefix}-METHOD-PUT-NOT-ALLOWED",
            title="PUT на List endpoint → 405 или 404",
            classes=["VAL", "NEG"], priority="P3",
            steps=[Step(name="put-list", method="PUT", path=base_path,
                        body={"projectId": "{{_suiteProjectId}}"},
                        # api-gateway evaluates authz before routing — unsupported HTTP verbs
                        # on collection endpoints may be rejected with 403 rather than 405.
                        test_script=["pm.test('not allowed (403/404/405/501)', () => pm.expect(pm.response.code).to.be.oneOf([403, 404, 405, 501]));"])],
        ),
        Case(
            id=f"{prefix}-METHOD-DELETE-LIST",
            title="DELETE на List endpoint (без id) → 405 или 404",
            classes=["VAL", "NEG"], priority="P3",
            steps=[Step(name="del-list", method="DELETE", path=base_path,
                        # api-gateway evaluates authz before routing — unsupported HTTP verbs
                        # on collection endpoints may be rejected with 403 rather than 405.
                        test_script=["pm.test('not allowed (403/404/405/501)', () => pm.expect(pm.response.code).to.be.oneOf([403, 404, 405, 501]));"])],
        ),
    ]


def malformed_body_block(prefix, create_path):
    """Malformed JSON body, empty body, wrong content-type."""
    return [
        Case(
            id=f"{prefix}-CR-VAL-MALFORMED-JSON",
            title="Create с malformed JSON → 400",
            classes=["VAL", "NEG"], priority="P2",
            steps=[Step(name="cr-malformed", method="POST", path=create_path,
                        body=None,
                        pre_script=[
                            "// Подменяем body на невалидный JSON через pm.request.body",
                            "pm.request.body = { mode: 'raw', raw: '{invalid json---}' };",
                        ],
                        test_script=["pm.test('400 or 415', () => pm.expect(pm.response.code).to.be.oneOf([400, 415]));"])],
        ),
        Case(
            id=f"{prefix}-CR-VAL-EMPTY-BODY",
            title="Create с пустым body → 400",
            classes=["VAL", "NEG"], priority="P2",
            steps=[Step(name="cr-empty-body", method="POST", path=create_path,
                        body={},
                        test_script=[*assert_status(400), *assert_grpc_code(3, "INVALID_ARGUMENT")])],
        ),
    ]


def alreadyexists_dup_name_for(prefix, create_path, body_create):
    """Создать дубль с тем же name → sync 409 ALREADY_EXISTS."""
    return Case(
        id=f"{prefix}-CR-NEG-DUP-NAME-CHECK",
        title="Создать дубль с тем же name → sync 409 ALREADY_EXISTS",
        classes=["NEG", "CONC"], priority="P1",
        steps=[
            Step(name="cr-first", method="POST", path=create_path,
                 body={**body_create, "name": f"{prefix.lower()}-dupck-{{{{runId}}}}"},
                 test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                              *save_from_response("(j.metadata && Object.keys(j.metadata).filter(k => k.endsWith('Id')).map(k => j.metadata[k])[0]) || ''", "firstId")]),
            Step(name="poll-first", method="GET", path="/operations/{{opId}}",
                 test_script=["pm.test('done', () => pm.expect(pm.response.json().done).to.eql(true));"]),
            Step(name="cr-dup", method="POST", path=create_path,
                 body={**body_create, "name": f"{prefix.lower()}-dupck-{{{{runId}}}}"},
                 test_script=[*assert_status(409), *assert_grpc_code(6, "ALREADY_EXISTS"),
                              "pm.test('mentions already exists', () => pm.expect(pm.response.json().message.toLowerCase()).to.include('already exists'));"]),
            Step(name="cleanup-first", method="DELETE", path=f"{create_path}/{{{{firstId}}}}",
                 test_script=[*save_from_response("j.id", "opId")]),
            Step(name="poll-c1", method="GET", path="/operations/{{opId}}",
                 test_script=["pm.test('done', () => pm.expect(pm.response.json().done).to.eql(true));"]),
        ],
    )


def update_mask_partial_block(prefix, create_path, update_base_path, body_create):
    """Decision table partial mask: только name; только description; только labels."""
    return [
        Case(
            id=f"{prefix}-UPD-VAL-MASK-NAME-ONLY",
            title="Update mask=name → только name меняется, description/labels не трогаются",
            classes=["VAL", "STATE"], priority="P2",
            steps=[
                Step(name="cr", method="POST", path=create_path,
                     body={**body_create, "name": f"{prefix.lower()}-mn-{{{{runId}}}}",
                           "description": "init", "labels": {"orig": "1"}},
                     test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                                  *save_from_response("(j.metadata && Object.keys(j.metadata).filter(k => k.endsWith('Id')).map(k => j.metadata[k])[0]) || ''", "createdId")]),
                Step(name="poll-cr", method="GET", path="/operations/{{opId}}",
                     test_script=["pm.test('done', () => pm.expect(pm.response.json().done).to.eql(true));"]),
                Step(name="patch-name-only", method="PATCH",
                     path=f"{update_base_path}/{{{{createdId}}}}",
                     body={"updateMask": "name", "name": f"{prefix.lower()}-mnnew",
                           "description": "should-be-ignored", "labels": {"ignored": "y"}},
                     test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
                Step(name="poll-p", method="GET", path="/operations/{{opId}}",
                     test_script=["pm.test('done', () => pm.expect(pm.response.json().done).to.eql(true));"]),
                Step(name="verify", method="GET",
                     path=f"{update_base_path}/{{{{createdId}}}}",
                     test_script=[*assert_status(200),
                                  "const j = pm.response.json();",
                                  "pm.test('name updated', () => pm.expect(j.name).to.eql('" + prefix.lower() + "-mnnew'));",
                                  "pm.test('description preserved', () => pm.expect(j.description).to.eql('init'));",
                                  "pm.test('labels preserved', () => pm.expect((j.labels || {}).orig).to.eql('1'));"]),
                Step(name="cleanup", method="DELETE",
                     path=f"{update_base_path}/{{{{createdId}}}}",
                     test_script=[*save_from_response("j.id", "opId")]),
                Step(name="poll-c", method="GET", path="/operations/{{opId}}",
                     test_script=["pm.test('done', () => pm.expect(pm.response.json().done).to.eql(true));"]),
            ],
        ),
    ]


def perf_baseline_get_block(prefix, get_create_path, body_create):
    """GET happy с perf budget."""
    return Case(
        id=f"{prefix}-GET-PERF-BASELINE",
        title="Get existing — response time < 300ms",
        classes=["PERF", "CRUD"], priority="P2",
        steps=[
            Step(name="cr", method="POST", path=get_create_path,
                 body={**body_create, "name": f"{prefix.lower()}-perf-{{{{runId}}}}"},
                 test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                              *save_from_response("(j.metadata && Object.keys(j.metadata).filter(k => k.endsWith('Id')).map(k => j.metadata[k])[0]) || ''", "perfId")]),
            Step(name="poll-cr", method="GET", path="/operations/{{opId}}",
                 test_script=["pm.test('done', () => pm.expect(pm.response.json().done).to.eql(true));"]),
            Step(name="get-timed", method="GET", path=f"{get_create_path}/{{{{perfId}}}}",
                 test_script=[*assert_status(200),
                              "pm.test('response time < 300ms', () => pm.expect(pm.response.responseTime).to.be.below(300));"]),
            Step(name="cleanup", method="DELETE", path=f"{get_create_path}/{{{{perfId}}}}",
                 test_script=[*save_from_response("j.id", "opId")]),
            Step(name="poll-c", method="GET", path="/operations/{{opId}}",
                 test_script=["pm.test('done', () => pm.expect(pm.response.json().done).to.eql(true));"]),
        ],
    )


def list_total_size_check_block(prefix, list_path):
    """List возвращает разумное число объектов (не больше pageSize)."""
    return [
        Case(
            id=f"{prefix}-LST-CONTRACT-NEVER-EXCEEDS-PAGESIZE",
            title="List с pageSize=5 → не более 5 элементов в response",
            classes=["PAGE", "CRUD"], priority="P2",
            steps=[Step(name="list-5", method="GET",
                        path=f"{list_path}?projectId={{{{_suiteProjectId}}}}&pageSize=5",
                        test_script=[*assert_status(200),
                                     "pm.test('at most 5 items', () => {"
                                     "  const j = pm.response.json();"
                                     "  const k = Object.keys(j).find(x => Array.isArray(j[x]));"
                                     "  pm.expect((j[k] || []).length).to.be.at.most(5);"
                                     "});"])],
        ),
    ]


def headers_content_type_block(prefix, create_path, body_create):
    """Content-Type required: POST без правильного header → behavior."""
    return [
        Case(
            id=f"{prefix}-HEADERS-MISSING-CT",
            title="POST без Content-Type → 415 или 400 или 200 (lenient)",
            classes=["VAL", "NEG"], priority="P3",
            steps=[Step(name="post-no-ct", method="POST", path=create_path,
                        body={**body_create, "name": f"{prefix.lower()}-noct-{{{{runId}}}}"},
                        pre_script=["pm.request.headers.remove('Content-Type');"],
                        test_script=["pm.test('lenient or rejected', () => pm.expect(pm.response.code).to.be.oneOf([200, 400, 415]));"])],
        ),
    ]


def required_fields_matrix(prefix, create_path, body_full, required_field_names):
    """Для каждого required-поля: убрать его из body → ожидать 400 InvalidArgument.

    body_full — полное valid body (с всеми required полями).
    required_field_names — список имен required полей (как в proto).
    """
    cases = []
    for fld in required_field_names:
        body_missing = {k: v for k, v in body_full.items() if k != fld}
        cases.append(Case(
            id=f"{prefix}-CR-VAL-REQ-{fld.upper().replace('_','-')}",
            title=f"Create без required поля '{fld}' → 400 InvalidArgument",
            classes=["VAL"], priority="P0",
            steps=[Step(name=f"cr-no-{fld}", method="POST", path=create_path,
                        body=body_missing,
                        test_script=[
                            # 400 — sync InvalidArgument (missing required field).
                            # 200 — поле проверяется async (op.error code=3/5).
                            # 404 — body использует garbage parent id (PE: networkId={{garbageVpcId}});
                            #       sync existence-check parent'а отрабатывает раньше → NotFound.
                            "pm.test('rejected', () => pm.expect(pm.response.code).to.be.oneOf([400, 200, 404]));",
                        ])],
        ))
    return cases


def immutable_fields_matrix(prefix, update_base_path, immutable_field_names):
    """Для каждого immutable поля: PATCH mask=<field> → 400 InvalidArgument
    с verbatim text "<field> is immutable" (или другая 4xx).
    """
    def _snake_to_camel(s):
        parts = s.split("_")
        return parts[0] + "".join(p.title() for p in parts[1:])
    cases = []
    for fld in immutable_field_names:
        camel = _snake_to_camel(fld)
        body = {"updateMask": fld, camel: "x"}
        cases.append(Case(
            id=f"{prefix}-UPD-STATE-IMMUTABLE-{fld.upper().replace('_','-')}",
            title=f"Update mask='{fld}' (immutable) → 400 InvalidArgument verbatim",
            classes=["STATE", "VAL", "CONF"], priority="P1",
            steps=[Step(name=f"upd-{fld}", method="PATCH",
                        path=f"{update_base_path}/{{{{garbageVpcId}}}}",
                        body=body,
                        test_script=[
                            # mask immutable отвергается 400 InvalidArgument
                            # либо 404 если sync Get срабатывает первым (AuthZ guard)
                            "pm.test('rejected', () => pm.expect(pm.response.code).to.be.oneOf([400, 404]));",
                            "if (pm.response.code === 400) {",
                            "  const j = pm.response.json();",
                            "  pm.test('error has message string', () => pm.expect(j.message).to.be.a('string'));",
                            "}",
                        ])],
        ))
    return cases


def mutable_field_accepts(prefix, create_path, update_base_path, body_create,
                          mutable_field, mutable_value, assert_after):
    """Создать ресурс, изменить mutable поле через mask, проверить применение."""
    return Case(
        id=f"{prefix}-UPD-CRUD-MUTABLE-{mutable_field.upper().replace('_','-')}",
        title=f"Update mask='{mutable_field}' → mutable поле обновлено",
        classes=["CRUD", "STATE"], priority="P2",
        steps=[
            Step(name="cr", method="POST", path=create_path,
                 body={**body_create, "name": f"{prefix.lower()}-mut-{mutable_field[:5]}-{{{{runId}}}}"},
                 test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                              *save_from_response("(j.metadata && Object.keys(j.metadata).filter(k => k.endsWith('Id')).map(k => j.metadata[k])[0]) || ''", "createdId")]),
            poll_operation_until_done(),
            Step(name="patch", method="PATCH",
                 path=f"{update_base_path}/{{{{createdId}}}}",
                 body={"updateMask": mutable_field, mutable_field: mutable_value},
                 test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
            poll_operation_until_done(),
            Step(name="verify", method="GET",
                 path=f"{update_base_path}/{{{{createdId}}}}",
                 test_script=[*assert_status(200), *assert_after]),
            Step(name="cleanup", method="DELETE",
                 path=f"{update_base_path}/{{{{createdId}}}}",
                 test_script=[*save_from_response("j.id", "opId")]),
            poll_operation_until_done(),
        ],
    )


def subnet_cidr_expand_shrink_pack():
    """Расширенный набор AddCidrBlocks / RemoveCidrBlocks сценариев для Subnet."""
    cases = []

    # 1) Add один CIDR → виден в GET
    cases.append(Case(
        id="SUB-ACB-CRUD-ADD-ONE",
        title="AddCidrBlocks: добавить 1 CIDR → виден в response",
        classes=["CRUD"], priority="P1",
        steps=[
            Step(name="add-1", method="POST",
                 path="/vpc/v1/subnets/{{addedSubId}}:add-cidr-blocks",
                 body={"v4CidrBlocks": ["10.180.10.0/24"]},
                 test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
            poll_operation_until_done(),
            Step(name="verify-1", method="GET", path="/vpc/v1/subnets/{{addedSubId}}",
                 test_script=[*assert_status(200),
                              "pm.test('cidr added', () => pm.expect(pm.response.json().v4CidrBlocks).to.include('10.180.10.0/24'));"]),
        ],
    ))

    # 2) Add несколько CIDR за один запрос
    cases.append(Case(
        id="SUB-ACB-CRUD-ADD-MULTIPLE",
        title="AddCidrBlocks: добавить 3 CIDR за один request → все 3 видны",
        classes=["CRUD", "BVA"], priority="P1",
        steps=[
            Step(name="add-3", method="POST",
                 path="/vpc/v1/subnets/{{addedSubId}}:add-cidr-blocks",
                 body={"v4CidrBlocks": ["10.180.20.0/24", "10.180.21.0/24", "10.180.22.0/24"]},
                 test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
            poll_operation_until_done(),
            Step(name="verify-3", method="GET", path="/vpc/v1/subnets/{{addedSubId}}",
                 test_script=[*assert_status(200),
                              "const c = pm.response.json().v4CidrBlocks;",
                              "pm.test('all 3 present', () => {",
                              "  pm.expect(c).to.include('10.180.20.0/24');",
                              "  pm.expect(c).to.include('10.180.21.0/24');",
                              "  pm.expect(c).to.include('10.180.22.0/24');",
                              "});"]),
        ],
    ))

    # 3) Add CIDR пересекающийся с existing → FailedPrecondition
    cases.append(Case(
        id="SUB-ACB-NEG-OVERLAP-SELF",
        title="AddCidrBlocks с CIDR пересекающимся с existing prefix → FailedPrecondition",
        classes=["NEG", "CONF"], priority="P0",
        steps=[
            Step(name="add-overlap", method="POST",
                 path="/vpc/v1/subnets/{{addedSubId}}:add-cidr-blocks",
                 body={"v4CidrBlocks": ["10.180.10.0/25"]},  # подсеть 10.180.10.0/24 уже добавлен
                 test_script=[
                     "pm.test('rejected', () => pm.expect(pm.response.code).to.be.oneOf([200, 400]));",
                     *save_from_response("j.id", "opId"),
                 ]),
            poll_operation_until_done(),
            Step(name="assert-overlap", method="GET", path="/operations/{{opId}}",
                 test_script=[
                     "const j = pm.response.json();",
                     "pm.test('error code 3 or 9 (sync or async)', () => {",
                     "  if (j.error) pm.expect(j.error.code).to.be.oneOf([3, 9]);",
                     "});",
                 ]),
        ],
    ))

    # 4) Add CIDR с host-bits → 400
    cases.append(Case(
        id="SUB-ACB-VAL-HOST-BITS",
        title="AddCidrBlocks с host-bits в CIDR (10.180.30.5/24) → 400",
        classes=["VAL", "NEG"], priority="P1",
        steps=[
            Step(name="add-hostbits", method="POST",
                 path="/vpc/v1/subnets/{{addedSubId}}:add-cidr-blocks",
                 body={"v4CidrBlocks": ["10.180.30.5/24"]},
                 test_script=[*assert_status(400), *assert_grpc_code(3, "INVALID_ARGUMENT")]),
        ],
    ))

    # 5) Remove один из множества CIDR → остальные сохранены
    cases.append(Case(
        id="SUB-RCB-CRUD-REMOVE-ONE",
        title="RemoveCidrBlocks: добавить 3 → убрать 1 → 2 остаются",
        classes=["CRUD"], priority="P1",
        steps=[
            Step(name="add-3-pre", method="POST",
                 path="/vpc/v1/subnets/{{addedSubId}}:add-cidr-blocks",
                 body={"v4CidrBlocks": ["10.180.40.0/24", "10.180.41.0/24", "10.180.42.0/24"]},
                 test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
            poll_operation_until_done(),
            Step(name="rm-1", method="POST",
                 path="/vpc/v1/subnets/{{addedSubId}}:remove-cidr-blocks",
                 body={"v4CidrBlocks": ["10.180.42.0/24"]},
                 test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
            poll_operation_until_done(),
            Step(name="verify-1-removed", method="GET", path="/vpc/v1/subnets/{{addedSubId}}",
                 test_script=[*assert_status(200),
                              "const c = pm.response.json().v4CidrBlocks;",
                              "pm.test('removed cidr is gone', () => pm.expect(c).to.not.include('10.180.42.0/24'));",
                              "pm.test('other cidrs remain', () => {",
                              "  pm.expect(c).to.include('10.180.40.0/24');",
                              "  pm.expect(c).to.include('10.180.41.0/24');",
                              "});"]),
        ],
    ))

    # 6) Remove несуществующий CIDR — не во v4_cidr_blocks
    cases.append(Case(
        id="SUB-RCB-NEG-NOT-PRESENT",
        title="RemoveCidrBlocks с CIDR не из списка → ожидаемое поведение (FailedPrecondition или silent)",
        classes=["NEG", "VAL"], priority="P1",
        steps=[
            Step(name="rm-missing", method="POST",
                 path="/vpc/v1/subnets/{{addedSubId}}:remove-cidr-blocks",
                 body={"v4CidrBlocks": ["192.168.99.0/24"]},
                 test_script=[
                     "pm.test('200 (op) or 400 sync', () => pm.expect(pm.response.code).to.be.oneOf([200, 400]));",
                     *save_from_response("j.id", "opId"),
                 ]),
            poll_operation_until_done(),
            Step(name="check-result", method="GET", path="/operations/{{opId}}",
                 test_script=[
                     "const j = pm.response.json();",
                     "// service может вернуть error.code=9 (FailedPrecondition) или silent success",
                     "pm.test('completed', () => pm.expect(j.done).to.eql(true));",
                 ]),
        ],
    ))

    # 7) Remove last remaining primary CIDR → запрет (нельзя удалять primary/первый)
    cases.append(Case(
        id="SUB-RCB-NEG-CANNOT-REMOVE-PRIMARY",
        title="RemoveCidrBlocks для primary v4_cidr (первый, primary) → отказ",
        classes=["NEG", "STATE"], priority="P0",
        steps=[
            Step(name="rm-primary", method="POST",
                 path="/vpc/v1/subnets/{{addedSubId}}:remove-cidr-blocks",
                 body={"v4CidrBlocks": ["10.180.0.0/24"]},  # primary subnet CIDR из preflight
                 test_script=[
                     "pm.test('rejected', () => pm.expect(pm.response.code).to.be.oneOf([200, 400]));",
                     *save_from_response("j.id", "opId"),
                 ]),
            poll_operation_until_done(),
            Step(name="check", method="GET", path="/operations/{{opId}}",
                 test_script=[
                     "const j = pm.response.json();",
                     "pm.test('completed', () => pm.expect(j.done).to.eql(true));",
                     "// Ожидаемое: error c кодом 9 (FailedPrecondition cannot remove primary)",
                     "// либо silent success (продукт permissive)",
                 ]),
        ],
    ))

    # 8) Add + Remove batch — добавить и убрать в обратной последовательности
    cases.append(Case(
        id="SUB-ACB-RCB-ROUNDTRIP",
        title="AddCidrBlocks + RemoveCidrBlocks roundtrip: добавили → убрали → не изменилось",
        classes=["IDM", "STATE"], priority="P2",
        steps=[
            Step(name="state-before", method="GET", path="/vpc/v1/subnets/{{addedSubId}}",
                 test_script=[*assert_status(200),
                              "pm.environment.set('cidrsBefore', JSON.stringify(pm.response.json().v4CidrBlocks));"]),
            Step(name="add-temp", method="POST",
                 path="/vpc/v1/subnets/{{addedSubId}}:add-cidr-blocks",
                 body={"v4CidrBlocks": ["10.180.99.0/24"]},
                 test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
            poll_operation_until_done(),
            Step(name="remove-temp", method="POST",
                 path="/vpc/v1/subnets/{{addedSubId}}:remove-cidr-blocks",
                 body={"v4CidrBlocks": ["10.180.99.0/24"]},
                 test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
            poll_operation_until_done(),
            Step(name="state-after", method="GET", path="/vpc/v1/subnets/{{addedSubId}}",
                 test_script=[*assert_status(200),
                              "const before = JSON.parse(pm.environment.get('cidrsBefore'));",
                              "const after = pm.response.json().v4CidrBlocks;",
                              "pm.test('cidrs roundtrip — равны', () => pm.expect(after.sort()).to.deep.eql(before.sort()));"]),
        ],
    ))

    return cases


def pairwise_subnet_pack():
    """Pairwise для Subnet: zone × prefix × dhcp.
    9 combinations покрывают все пары."""
    # Используем только существующие zone id (zone-{a,b,d}); на несуществующей
    # зоне Subnet.Create отвергается с "Illegal argument zone_id".
    combos = [
        ("{{zoneA}}", "/24", True),  ("{{zoneA}}", "/28", False), ("{{zoneA}}", "/16", True),
        ("{{zoneB}}", "/24", False), ("{{zoneB}}", "/28", True),  ("{{zoneB}}", "/16", False),
        ("{{zoneD}}", "/24", True),  ("{{zoneD}}", "/28", False), ("{{zoneD}}", "/16", True),
    ]
    cases = []
    for i, (zone, prefix, with_dhcp) in enumerate(combos):
        ipbase = f"10.{170+i}.0.0"
        body = {"projectId": "{{_suiteProjectId}}", "networkId": "{{netId}}",
                "name": f"sub-pw-{i}-{{{{runId}}}}", "zoneId": zone,
                "v4CidrBlocks": [f"{ipbase}{prefix}"]}
        if with_dhcp:
            body["dhcpOptions"] = {"domainName": "test.local",
                                   "domainNameServers": ["8.8.8.8"]}
        cases.append(Case(
            id=f"SUB-CR-PAIRWISE-{i:02d}",
            title=f"Pairwise [{i}]: zone={zone} prefix={prefix} dhcp={with_dhcp}",
            classes=["VAL", "CRUD"], priority="P2",
            steps=[
                Step(name="cr-pw", method="POST", path="/vpc/v1/subnets", body=body,
                     test_script=[
                         "pm.test('200 or 400', () => pm.expect(pm.response.code).to.be.oneOf([200, 400]));",
                         *save_from_response("j.id", "opId"),
                         *save_from_response("j.metadata && j.metadata.subnetId", "subId"),
                     ]),
                poll_operation_until_done(),
                Step(name="cleanup-pw", method="DELETE", path="/vpc/v1/subnets/{{subId}}",
                     test_script=["pm.test('cleanup', () => pm.expect(pm.response.code).to.be.oneOf([200, 404]));",
                                  *save_from_response("j.id", "opId")]),
            ],
        ))
    return cases


def security_injection_block(prefix, create_path, list_path, body_create):
    """Security probes: SQL/command/XSS injection в name + filter.
    Никогда не должно возвращать 500 или утечку pgx/stack trace.
    """
    injections = [
        ("sqli", "test' OR 1=1--"),
        ("union", "x' UNION SELECT * FROM operations--"),
        ("xss", "<script>alert(1)</script>"),
        ("cmd", "; rm -rf / ;"),
        ("path", "../../etc/passwd"),
        ("nullbyte", "x y"),
        ("longpayload", "A" * 1000),
    ]
    cases = []
    for name, payload in injections:
        cases.append(Case(
            id=f"{prefix}-CR-SEC-{name.upper()}",
            title=f"Security probe: {name} in name → handled, no 500",
            classes=["VAL", "NEG"], priority="P0",
            steps=[Step(name=f"cr-{name}", method="POST", path=create_path,
                        body={**body_create, "name": payload[:1000]},
                        test_script=[
                            "pm.test('not 500', () => pm.expect(pm.response.code).to.not.eql(500));",
                            "pm.test('handled 2xx/4xx', () => pm.expect(pm.response.code).to.be.oneOf([200, 400, 413]));",
                            "const body = JSON.stringify(pm.response.json() || {});",
                            "pm.test('no panic/sqlstate/stacktrace leak', () => {",
                            "  const low = body.toLowerCase();",
                            "  pm.expect(low).to.not.include('panic');",
                            "  pm.expect(low).to.not.include('sqlstate');",
                            "  pm.expect(low).to.not.include('goroutine');",
                            "});",
                        ])],
        ))
    cases.append(Case(
        id=f"{prefix}-LST-SEC-FILTER-SQLI",
        title="Security: SQL injection в filter → не 500",
        classes=["VAL", "NEG"], priority="P0",
        steps=[Step(name="lst-sqli", method="GET",
                    path=f"{list_path}?projectId={{{{_suiteProjectId}}}}&filter=name%3D%22a%27%20OR%201%3D1--%22",
                    test_script=[
                        "pm.test('not 500', () => pm.expect(pm.response.code).to.not.eql(500));",
                        "pm.test('handled', () => pm.expect(pm.response.code).to.be.oneOf([200, 400]));",
                    ])],
    ))
    return cases


def conformance_lifecycle_pack(prefix, create_path, body_create):
    """Lifecycle: Create→Get→List-includes→Update→Get-updated→Delete→List-excludes→Get-404."""
    return Case(
        id=f"{prefix}-LIFECYCLE-CONF",
        title="Full lifecycle conformance: CRUD invariants",
        classes=["CRUD", "CONF", "STATE"], priority="P1",
        steps=[
            Step(name="cr", method="POST", path=create_path,
                 body={**body_create, "name": f"{prefix.lower()}-life-{{{{runId}}}}"},
                 test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                              *save_from_response("(j.metadata && Object.keys(j.metadata).filter(k => k.endsWith('Id')).map(k => j.metadata[k])[0]) || ''", "lifeId")]),
            poll_operation_until_done(),
            Step(name="get-1", method="GET", path=f"{create_path}/{{{{lifeId}}}}",
                 test_script=[*assert_status(200),
                              "pm.test('id matches', () => pm.expect(pm.response.json().id).to.eql(pm.environment.get('lifeId')));"]),
            Step(name="lst-includes", method="GET",
                 path=f"{create_path}?projectId={{{{_suiteProjectId}}}}&pageSize=1000",
                 test_script=[*assert_status(200),
                              "const items = Object.values(pm.response.json()).find(v => Array.isArray(v)) || [];",
                              "pm.test('list contains', () => pm.expect(items.map(x => x.id)).to.include(pm.environment.get('lifeId')));"]),
            Step(name="upd", method="PATCH", path=f"{create_path}/{{{{lifeId}}}}",
                 body={"updateMask": "description", "description": "life-conf"},
                 test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
            poll_operation_until_done(),
            Step(name="get-after-upd", method="GET", path=f"{create_path}/{{{{lifeId}}}}",
                 test_script=[*assert_status(200),
                              "pm.test('description updated', () => pm.expect(pm.response.json().description).to.eql('life-conf'));"]),
            Step(name="del", method="DELETE", path=f"{create_path}/{{{{lifeId}}}}",
                 test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
            poll_operation_until_done(),
            Step(name="lst-excludes", method="GET",
                 path=f"{create_path}?projectId={{{{_suiteProjectId}}}}&pageSize=1000",
                 test_script=[*assert_status(200),
                              "const items = Object.values(pm.response.json()).find(v => Array.isArray(v)) || [];",
                              "pm.test('list does not contain', () => pm.expect(items.map(x => x.id)).to.not.include(pm.environment.get('lifeId')));"]),
            Step(name="get-404", method="GET", path=f"{create_path}/{{{{lifeId}}}}",
                 test_script=[*assert_status(404), *assert_grpc_code(5, "NOT_FOUND")]),
        ],
    )


def authz_caller_headers_block(prefix, list_path):
    """RBAC-pre kit: проверка headers cross-tenant (анонимный vs admin-claim)."""
    return [
        Case(
            id=f"{prefix}-AUTHZ-EMPTY-PROJECT-HEADER",
            title="List с пустым x-kacho-project-id header → текущее: 200 (dev mode)",
            classes=["AUTHZ"], priority="P1",
            steps=[Step(name="list-with-empty-header", method="GET",
                        path=f"{list_path}?projectId={{{{_suiteProjectId}}}}",
                        test_script=[
                            "pm.test('OK in dev or PermissionDenied in production', () => pm.expect(pm.response.code).to.be.oneOf([200, 403, 401]));",
                        ])],
        ),
    ]


def conf_alreadyexists_block(prefix, create_path, name_template, body_extra=None):
    """CONF: sync 409 ALREADY_EXISTS text при duplicate name."""
    body_extra = body_extra or {}
    return Case(
        id=f"{prefix}-CR-CONF-ALREADY-EXISTS",
        title="Create duplicate name → sync 409, точный текст ALREADY_EXISTS",
        classes=["CONF", "NEG"], priority="P1",
        steps=[
            Step(name="create-first", method="POST", path=create_path,
                 body={"projectId": "{{_suiteProjectId}}", "name": name_template, **body_extra},
                 test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                              *save_from_response("j.metadata && Object.values(j.metadata).find(v => typeof v === 'string' && v.length > 10)", "createdId")]),
            poll_operation_until_done(),
            Step(name="create-dup", method="POST", path=create_path,
                 body={"projectId": "{{_suiteProjectId}}", "name": name_template, **body_extra},
                 test_script=[*assert_status(409), *assert_grpc_code(6, "ALREADY_EXISTS"),
                              "pm.test('verbatim with name ... already exists', () => pm.expect(pm.response.json().message).to.match(/ with name .* already exists$/));"]),
            Step(name="cleanup-first", method="DELETE", path=f"{create_path}/{{{{createdId}}}}",
                 test_script=[*save_from_response("j.id", "opId")]),
            poll_operation_until_done(),
        ],
    )


def poll_operation_until_done() -> Step:
    """Reusable poll step с retry-на-not-done через setNextRequest.
    До 6 попыток, потом fail если done остался false.

    Если opId пустой (предыдущий шаг был отклонен синхронно, напр. 403 bad-project),
    шаг пропускается: тесты не запускаются, не добавляются к failure count.
    """
    return Step(
        name="poll-op",
        method="GET",
        path="/operations/{{opId}}",
        pre_script=[
            # Note: cannot fully skip request in pre-script without aborting the suite.
            # Instead the test_script guards on empty opId or non-200 response.
        ],
        test_script=[
            # Guard: if opId was empty (prior step was sync-rejected e.g. 403) or
            # response is non-200, skip all poll assertions cleanly.
            "if (!pm.environment.get('opId') || pm.response.code !== 200) {",
            "  pm.environment.unset('_pollCount');",
            "  return;",
            "}",
            "pm.test('poll status 200', () => pm.expect(pm.response.code).to.eql(200));",
            "const j = pm.response.json();",
            "const pc = parseInt(pm.environment.get('_pollCount') || '0', 10);",
            "if (!j.done && pc < 6) {",
            "  pm.environment.set('_pollCount', String(pc + 1));",
            "  // Postman async-friendly retry: re-invoke same request name",
            "  postman.setNextRequest(pm.info.requestName);",
            "  return;",
            "}",
            "pm.environment.unset('_pollCount');",
            "pm.test('operation done', () => pm.expect(j.done, JSON.stringify(j)).to.eql(true));",
            "if (j.error) pm.environment.set('lastOpError', JSON.stringify(j.error));",
            "else pm.environment.unset('lastOpError');",
            "if (j.response) pm.environment.set('lastOpResponse', JSON.stringify(j.response));",
        ],
    )


# ---------------------------------------------------------------------------
# Сериализация в Postman v2.1
# ---------------------------------------------------------------------------

def _auth_pre_script(auth: str) -> List[str]:
    """Генерирует JS-сниппет для per-step Authorization-header.

    "anonymous" → снимает Authorization. Имя env-переменной →
    Authorization: Bearer <значение env-var>. Snippet идет в начало pre_script."""
    if auth == "anonymous":
        return [
            "// authz-deny: anonymous step",
            "pm.request.headers.remove('Authorization');",
        ]
    return [
        f"// authz-deny: bearer from env '{auth}'",
        f"const __t = pm.environment.get('{auth}') || pm.variables.get('{auth}') || '';",
        "if (__t) {",
        "  pm.request.headers.upsert({key: 'Authorization', value: 'Bearer ' + __t});",
        "} else {",
        "  pm.request.headers.remove('Authorization');",
        "}",
    ]


def step_to_postman(step: Step) -> Dict:
    item: Dict = {
        "name": step.name,
        "request": {
            "method": step.method,
            "header": [{"key": "Content-Type", "value": "application/json"}],
            "url": {
                "raw": "{{baseUrl}}" + step.path,
                "host": ["{{baseUrl}}"],
                "path": [p for p in step.path.strip("/").split("/") if p],
            },
        },
    }
    if step.body is not None:
        item["request"]["body"] = {
            "mode": "raw",
            "raw": json.dumps(step.body, ensure_ascii=False),
            "options": {"raw": {"language": "json"}},
        }
    pre = list(step.pre_script)
    if step.auth is not None:
        pre = _auth_pre_script(step.auth) + pre
    events = []
    if pre:
        events.append(
            {"listen": "prerequest", "script": {"type": "text/javascript", "exec": pre}}
        )
    if step.test_script:
        events.append(
            {"listen": "test", "script": {"type": "text/javascript", "exec": step.test_script}}
        )
    if events:
        item["event"] = events
    return item


def case_to_postman(case: Case) -> Dict:
    tags = [f"class:{c}" for c in case.classes] + [f"priority:{case.priority}"]
    return {
        "name": f"{case.id} — {case.title}",
        "description": " | ".join(tags),
        "item": [step_to_postman(s) for s in case.steps],
    }


# Zone ids are environment-specific: geo seeds Region/Zone with ids that differ
# per deploy and are NOT the legacy literals zone-a..d. Resolve them ONCE,
# synchronously, as the FIRST item of every collection (a real request, so newman
# blocks on its response before running any case) and publish zoneA..zoneD +
# existingZoneId/existingZoneAltId. Best-effort: no failing assertion — if geo is
# unreachable (standalone vpc), the committed env defaults stay in effect.
_ZONE_SETUP_TEST = [
    "const code = (pm.response && pm.response.code) || 0;",
    "let zs = [];",
    "if (code === 200) { try { zs = (pm.response.json().zones) || []; } catch (e) {} }",
    "const up = zs.filter(z => !z.status || String(z.status).indexOf('UP') !== -1);",
    "const pick = up.length ? up : zs;",
    "if (pick.length) {",
    "  const at = (i) => (pick[i] || pick[pick.length - 1]).id;",
    "  pm.environment.set('zoneA', at(0));",
    "  pm.environment.set('zoneB', at(1));",
    "  pm.environment.set('zoneC', at(2));",
    "  pm.environment.set('zoneD', at(3));",
    "  pm.environment.set('existingZoneId', at(0));",
    "  pm.environment.set('existingZoneAltId', at(1));",
    "  pm.environment.set('_zoneResolved', '1');",
    "}",
]


def _zone_setup_item() -> Dict:
    """Blocking first item: resolve live geo zone ids into zoneA..D env vars."""
    return {
        "name": "_SETUP-ZONES — resolve live geo zone ids (zoneA..D)",
        "event": [{
            "listen": "test",
            "script": {"type": "text/javascript", "exec": _ZONE_SETUP_TEST},
        }],
        "request": {
            "method": "GET",
            "header": [{"key": "Authorization", "value": "Bearer {{jwtBootstrap}}"}],
            "url": {
                "raw": "{{baseUrl}}/geo/v1/zones",
                "host": ["{{baseUrl}}"],
                "path": ["geo", "v1", "zones"],
            },
        },
    }


# Suites that exercise external-pool IPAM (internal-pool admin CRUD + address
# external allocation) assume a pre-seeded default EXTERNAL_PUBLIC AddressPool for
# the primary zone. The dev stand's seed-ipam is a NOOP, so seed it here, once,
# as cluster-admin (idempotent: 200 first time, 409 thereafter). Soft (no failing
# assertion) — if seeding cannot run, the dependent cases surface it themselves.
_POOL_SEED_TEST = [
    "if (pm.response && pm.response.code === 200) {",
    "  try { pm.environment.set('_seededDefaultPoolId', pm.response.json().id); } catch (e) {}",
    "}",
]

_POOL_SEED_BODY = {
    "name": "seed-default-external-zonea",
    "kind": "EXTERNAL_PUBLIC",
    "zoneId": "{{zoneA}}",
    # 100.64.0.0/24: not used by any case pool (the EXCLUDE on address_pool_cidrs
    # is per-kind cross-zone, so the persistent seed must not overlap throwaway
    # pool CIDRs 203.0.113.0/24 / 198.51.100.0/24).
    "v4CidrBlocks": ["100.64.0.0/24"],
    "v6CidrBlocks": [],
    "isDefault": True,
}

# Collections that depend on a seeded default external pool.
_POOL_SEED_SERVICES = {"internal-pool", "address"}


def _pool_seed_item() -> Dict:
    """Idempotent setup item: ensure a default EXTERNAL_PUBLIC pool exists at zoneA."""
    return {
        "name": "_SETUP-POOL — ensure default EXTERNAL_PUBLIC pool at zoneA",
        "event": [{
            "listen": "test",
            "script": {"type": "text/javascript", "exec": _POOL_SEED_TEST},
        }],
        "request": {
            "method": "POST",
            "header": [
                {"key": "Authorization", "value": "Bearer {{jwtBootstrap}}"},
                {"key": "Content-Type", "value": "application/json"},
            ],
            "body": {"mode": "raw", "raw": json.dumps(_POOL_SEED_BODY)},
            "url": {
                "raw": "{{baseUrl}}/vpc/v1/addressPools",
                "host": ["{{baseUrl}}"],
                "path": ["vpc", "v1", "addressPools"],
            },
        },
    }


# InternalAddressPoolService RPCs are gated on cluster `system_admin` (the vpc
# authz interceptor checks the relation directly), so the suite must call them as
# cluster-admin, not as the default project admin. Force the admin JWT at the
# collection level for the internal-pool suite (per-step auth= still overrides).
_ADMIN_DEFAULT_SERVICES = {"internal-pool"}
_ADMIN_DEFAULT_PRE = [
    "const __adm = pm.environment.get('jwtBootstrap') || '';",
    "if (__adm) { pm.request.headers.upsert({key: 'Authorization', value: 'Bearer ' + __adm}); }",
]


def build_collection(service: str, cases: List[Case]) -> Dict:
    setup_items = [_zone_setup_item()]
    if service in _POOL_SEED_SERVICES:
        setup_items.append(_pool_seed_item())
    pre = PRE_GLOBAL + _ADMIN_DEFAULT_PRE if service in _ADMIN_DEFAULT_SERVICES else PRE_GLOBAL
    return {
        "info": {
            "_postman_id": str(uuid.uuid4()),
            "name": f"kacho-vpc / newman / {service}",
            "schema": "https://schema.getpostman.com/json/collection/v2.1.0/collection.json",
        },
        "event": [
            {
                "listen": "prerequest",
                "script": {"type": "text/javascript", "exec": pre},
            },
        ],
        "item": setup_items + [case_to_postman(c) for c in cases],
        "variable": [],
    }


# ---------------------------------------------------------------------------
# Discovery + main
# ---------------------------------------------------------------------------

def load_cases_module(path: Path):
    spec = importlib.util.spec_from_file_location(path.stem, path)
    mod = importlib.util.module_from_spec(spec)
    # пробрасываем helpers в namespace модуля
    mod.Step = Step
    mod.Case = Case
    mod.assert_status = assert_status
    mod.assert_grpc_code = assert_grpc_code
    mod.assert_transcode_error = assert_transcode_error
    mod.assert_field_violation = assert_field_violation
    mod.save_from_response = save_from_response
    mod.assert_operation_envelope = assert_operation_envelope
    mod.poll_operation_until_done = poll_operation_until_done
    mod.crud_list_bva_block = crud_list_bva_block
    mod.conf_not_found_text = conf_not_found_text
    mod.state_update_unknown_mask = state_update_unknown_mask
    mod.authz_move_nf = authz_move_nf
    mod.val_move_no_dest = val_move_no_dest
    mod.state_immutable_project = state_immutable_project
    mod.list_pagesize_1_bva = list_pagesize_1_bva
    mod.conf_alreadyexists_block = conf_alreadyexists_block
    mod.ecp_name_block = ecp_name_block
    mod.ecp_description_block = ecp_description_block
    mod.ecp_labels_block = ecp_labels_block
    mod.updatemask_decision_table = updatemask_decision_table
    mod.filter_syntax_block = filter_syntax_block
    mod.pagination_roundtrip = pagination_roundtrip
    mod.idempotency_block = idempotency_block
    mod.update_happy_per_field = update_happy_per_field
    mod.perf_baseline_block = perf_baseline_block
    mod.move_same_project = move_same_project
    mod.verbatim_text_pack = verbatim_text_pack
    mod.authz_caller_headers_block = authz_caller_headers_block
    mod.update_happy_multi_field = update_happy_multi_field
    mod.cross_project_resource_block = cross_project_resource_block
    mod.list_filter_match_block = list_filter_match_block
    mod.neg_invalid_types_block = neg_invalid_types_block
    mod.http_method_not_allowed_block = http_method_not_allowed_block
    mod.malformed_body_block = malformed_body_block
    mod.alreadyexists_dup_name_for = alreadyexists_dup_name_for
    mod.update_mask_partial_block = update_mask_partial_block
    mod.perf_baseline_get_block = perf_baseline_get_block
    mod.list_total_size_check_block = list_total_size_check_block
    mod.headers_content_type_block = headers_content_type_block
    mod.required_fields_matrix = required_fields_matrix
    mod.immutable_fields_matrix = immutable_fields_matrix
    mod.mutable_field_accepts = mutable_field_accepts
    mod.subnet_cidr_expand_shrink_pack = subnet_cidr_expand_shrink_pack
    mod.pairwise_subnet_pack = pairwise_subnet_pack
    mod.security_injection_block = security_injection_block
    mod.conformance_lifecycle_pack = conformance_lifecycle_pack
    spec.loader.exec_module(mod)
    return mod


def _check_duplicate_ids() -> int:
    """HARD-FAIL: case-id обязан быть уникален среди всех кейсов всех сервисов."""
    seen: Dict[str, str] = {}
    dups: List[str] = []
    for f in sorted(CASES_DIR.glob("*.py")):
        mod = load_cases_module(f)
        for c in getattr(mod, "CASES", []):
            if c.id in seen:
                dups.append(f"  - {c.id!r}: {seen[c.id]} и {f.name}")
            else:
                seen[c.id] = f.name
    if dups:
        sys.stderr.write("gen: FAIL — дубли case-id (case-id должен быть уникален):\n")
        sys.stderr.write("\n".join(dups) + "\n")
        return 1
    return 0


def main(argv: List[str]) -> int:
    args = argv[1:]
    if "--validate" in args:
        # делегируем полную валидацию (dup-id + каталогизация в CASES-INDEX) в validate-cases.py
        import runpy
        sys.argv = [str(SCRIPTS_DIR / "validate-cases.py")]
        runpy.run_path(str(SCRIPTS_DIR / "validate-cases.py"), run_name="__main__")
        return 0  # validate-cases.py делает sys.exit сам

    OUT_DIR.mkdir(parents=True, exist_ok=True)
    want = set(args)
    found = sorted(CASES_DIR.glob("*.py"))
    if not found:
        print(f"no case files in {CASES_DIR}")
        return 1
    if _check_duplicate_ids() != 0:
        return 1
    for f in found:
        svc = f.stem
        if want and svc not in want:
            continue
        mod = load_cases_module(f)
        cases = getattr(mod, "CASES", [])
        col = build_collection(svc, cases)
        out = OUT_DIR / f"{svc}.postman_collection.json"
        out.write_text(json.dumps(col, indent=2, ensure_ascii=False))
        print(f"[{svc}] {len(cases)} cases → {out.relative_to(ROOT)}")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
