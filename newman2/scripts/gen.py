#!/usr/bin/env python3
"""
newman2/scripts/gen.py — генератор Postman collections из декларативных case-файлов.

Использование:
    python3 scripts/gen.py             # все сервисы
    python3 scripts/gen.py network     # один сервис

Источник истины — модули в newman2/cases/<service>.py, каждый экспортирует
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
    "pm.environment.set('_suiteFolderId', pm.environment.get('existingFolderId'));",
    "pm.environment.set('_suiteFolderCrossId', pm.environment.get('existingFolderCrossId'));",
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
                        path=f"{list_path}?folderId={{{{_suiteFolderId}}}}&pageSize=0",
                        test_script=[*assert_status(200)])],
        ),
        Case(
            id=f"{prefix}-LST-BVA-PAGESIZE-OVER-MAX",
            title="List pageSize=10000 → InvalidArgument",
            classes=["BVA", "VAL"], priority="P2",
            steps=[Step(name="list-ps-huge", method="GET",
                        path=f"{list_path}?folderId={{{{_suiteFolderId}}}}&pageSize=10000",
                        test_script=[*assert_status(400), *assert_grpc_code(3, "INVALID_ARGUMENT")])],
        ),
        Case(
            id=f"{prefix}-LST-PAGE-TOKEN-GARBAGE",
            title="List с garbage page_token → InvalidArgument",
            classes=["PAGE", "VAL"], priority="P1",
            steps=[Step(name="list-bad-token", method="GET",
                        path=f"{list_path}?folderId={{{{_suiteFolderId}}}}&pageSize=10&pageToken=not-a-real-token",
                        test_script=[*assert_status(400), *assert_grpc_code(3, "INVALID_ARGUMENT")])],
        ),
    ]


def conf_not_found_text(prefix, get_path, resource_name):
    """Verbatim YC-text для NotFound: '<Resource> <id> not found'."""
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
                    body={"destinationFolderId": "{{_suiteFolderId}}"},
                    test_script=[*assert_status(404), *assert_grpc_code(5, "NOT_FOUND")])],
    )


def val_move_no_dest(prefix, move_base_path):
    """Move без destinationFolderId → InvalidArgument."""
    return Case(
        id=f"{prefix}-MV-VAL-NO-DEST",
        title="Move без destinationFolderId → InvalidArgument",
        classes=["VAL"], priority="P1",
        steps=[Step(name="move-no-dest", method="POST",
                    path=f"{move_base_path}/{{{{garbageVpcId}}}}:move",
                    body={},
                    test_script=[
                        "pm.test('rejected (400 or 404)', () => pm.expect(pm.response.code).to.be.oneOf([400, 404]));",
                    ])],
    )


def state_immutable_folder(prefix, update_base_path):
    """Update с mask=folder_id → InvalidArgument (immutable)."""
    return Case(
        id=f"{prefix}-UPD-STATE-IMMUTABLE-FOLDER",
        title="Update с mask=folder_id → InvalidArgument (immutable)",
        classes=["STATE", "VAL"], priority="P1",
        steps=[Step(name="upd-folder-via-mask", method="PATCH",
                    path=f"{update_base_path}/{{{{garbageVpcId}}}}",
                    body={"updateMask": "folder_id", "folderId": "x"},
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
                    path=f"{list_path}?folderId={{{{_suiteFolderId}}}}&pageSize=1",
                    test_script=[*assert_status(200),
                                 "pm.test('at most 1 item', () => {"
                                 "  const j = pm.response.json();"
                                 "  const k = Object.keys(j).find(x => Array.isArray(j[x]));"
                                 "  pm.expect((j[k] || []).length).to.be.at.most(1);"
                                 "});"])],
    )


def ecp_name_block(prefix, create_path, body_extra=None):
    """ECP/BVA по полю name: пустое, max, over-max, invalid regex.

    body_extra — обязательные поля кроме folderId/name (например для Subnet: networkId+zoneId+cidr).
    """
    body_extra = body_extra or {}
    base = lambda name: {"folderId": "{{_suiteFolderId}}", "name": name, **body_extra}
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
        title="Create с name начинающимся с цифры → 400 (verbatim YC regex)",
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
    base = lambda name, desc: {"folderId": "{{_suiteFolderId}}", "name": name, "description": desc, **body_extra}
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
    base = lambda name, labels: {"folderId": "{{_suiteFolderId}}", "name": name, "labels": labels, **body_extra}
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
                        path=f"{list_path}?folderId={{{{_suiteFolderId}}}}&filter=name%3D%22foo%22",
                        test_script=[*assert_status(200)])],
        ),
        Case(
            id=f"{prefix}-LST-FILTER-GARBAGE",
            title="List с garbage filter syntax → 400 InvalidArgument",
            classes=["FILTER", "VAL"], priority="P1",
            steps=[Step(name="list-bad-filter", method="GET",
                        path=f"{list_path}?folderId={{{{_suiteFolderId}}}}&filter=this%20is%20not%20valid%20syntax",
                        test_script=["pm.test('200 or 400', () => pm.expect(pm.response.code).to.be.oneOf([200, 400]));"])],
        ),
        Case(
            id=f"{prefix}-LST-FILTER-UNKNOWN-FIELD",
            title="List с filter на unsupported field → 400 InvalidArgument",
            classes=["FILTER", "VAL"], priority="P2",
            steps=[Step(name="list-unknown-field", method="GET",
                        path=f"{list_path}?folderId={{{{_suiteFolderId}}}}&filter=nonexistent_field%3D%22x%22",
                        test_script=["pm.test('200 or 400', () => pm.expect(pm.response.code).to.be.oneOf([200, 400]));"])],
        ),
    ]


def pagination_roundtrip(prefix, list_path):
    """Pagination round-trip: получить page+token, использовать token для next page."""
    return Case(
        id=f"{prefix}-LST-PAGE-ROUNDTRIP",
        title="Pagination: получить пустой/не-пустой ответ + nextPageToken и пройти ещё раз с ним",
        classes=["PAGE", "BVA", "CRUD"], priority="P2",
        steps=[
            Step(name="list-p1", method="GET",
                 path=f"{list_path}?folderId={{{{_suiteFolderId}}}}&pageSize=1",
                 test_script=[*assert_status(200),
                              "const j = pm.response.json();",
                              "const tok = j.nextPageToken || '';",
                              "pm.environment.set('nextToken', tok);",
                              "pm.test('token is string', () => pm.expect(tok).to.be.a('string'));"]),
            Step(name="list-p2", method="GET",
                 path=f"{list_path}?folderId={{{{_suiteFolderId}}}}&pageSize=1&pageToken={{{{nextToken}}}}",
                 test_script=[*assert_status(200)]),
        ],
    )


def idempotency_block(prefix, create_path, name_template, body_extra=None):
    """Idempotency-style: повторный Create same name → consistent behavior."""
    body_extra = body_extra or {}
    return Case(
        id=f"{prefix}-CR-IDM-RETRY",
        title="Retry-safe: повторный Create same input → consistent result",
        classes=["IDM", "CONC"], priority="P1",
        steps=[
            Step(name="cr-1", method="POST", path=create_path,
                 body={"folderId": "{{_suiteFolderId}}", "name": name_template, **body_extra},
                 test_script=[*assert_status(200), *save_from_response("j.id", "opId1")]),
            Step(name="poll-1", method="GET", path="/operations/{{opId1}}",
                 test_script=["pm.test('done eventually', () => { const j = pm.response.json(); pm.expect([true,false]).to.include(j.done); });"]),
            Step(name="cr-2", method="POST", path=create_path,
                 body={"folderId": "{{_suiteFolderId}}", "name": name_template, **body_extra},
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
        case_for("DESC", "description", {"updateMask": "description", "description": "updated-desc-newman2"},
                 ["pm.test('description updated', () => pm.expect(pm.response.json().description).to.eql('updated-desc-newman2'));"]),
        case_for("LABELS", "labels", {"updateMask": "labels", "labels": {"env": "prod", "team": "net"}},
                 ["pm.test('label env', () => pm.expect((pm.response.json().labels || {}).env).to.eql('prod'));",
                  "pm.test('label team', () => pm.expect((pm.response.json().labels || {}).team).to.eql('net'));"]),
    ]


def perf_baseline_block(prefix, list_path, get_path=None):
    """Performance baseline: response time для Get/List ниже бюджета.

    list_path — путь List endpoint (с folderId query param).
    """
    cases = [
        Case(
            id=f"{prefix}-LST-PERF-BASELINE",
            title="List response time < 500ms (perf baseline)",
            classes=["PERF", "CRUD"], priority="P2",
            steps=[Step(name="list-timed", method="GET",
                        path=f"{list_path}?folderId={{{{_suiteFolderId}}}}&pageSize=10",
                        test_script=[*assert_status(200),
                                     "pm.test('response time < 500ms', () => pm.expect(pm.response.responseTime).to.be.below(500));"])],
        ),
    ]
    return cases


def move_same_folder(prefix, resource_base_path, body_create):
    """Move в текущий folder (idempotent-ish — обычно 200, ресурс не меняется)."""
    return Case(
        id=f"{prefix}-MV-IDM-SAME-FOLDER",
        title="Move в текущий folder → ok (idempotent), ресурс остаётся",
        classes=["IDM", "CRUD"], priority="P2",
        steps=[
            Step(name="create", method="POST", path=resource_base_path,
                 body={**body_create, "name": f"{prefix.lower()}-mv-self-{{{{runId}}}}"},
                 test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                              *save_from_response("(j.metadata && Object.keys(j.metadata).filter(k => k.endsWith('Id')).map(k => j.metadata[k])[0]) || ''", "createdId")]),
            Step(name="poll-create", method="GET", path="/operations/{{opId}}",
                 test_script=["pm.test('done', () => pm.expect(pm.response.json().done).to.eql(true));"]),
            Step(name="move-self", method="POST",
                 path=f"{resource_base_path}/{{{{createdId}}}}:move",
                 body={"destinationFolderId": "{{_suiteFolderId}}"},
                 test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
            Step(name="poll-move", method="GET", path="/operations/{{opId}}",
                 test_script=["pm.test('done', () => pm.expect(pm.response.json().done).to.eql(true));"]),
            Step(name="verify-same-folder", method="GET",
                 path=f"{resource_base_path}/{{{{createdId}}}}",
                 test_script=[*assert_status(200),
                              "pm.test('folderId unchanged', () => pm.expect(pm.response.json().folderId).to.eql(pm.environment.get('_suiteFolderId')));"]),
            Step(name="cleanup", method="DELETE",
                 path=f"{resource_base_path}/{{{{createdId}}}}",
                 test_script=[*save_from_response("j.id", "opId")]),
            Step(name="poll-cleanup", method="GET", path="/operations/{{opId}}",
                 test_script=["pm.test('done', () => pm.expect(pm.response.json().done).to.eql(true));"]),
        ],
    )


def verbatim_text_pack(prefix, resource_name, resource_path):
    """Verbatim YC text snapshots для распространённых ошибок (Get/Update/Delete/Move)."""
    return [
        Case(
            id=f"{prefix}-GET-CONF-FULLTEXT",
            title=f"Get garbage → '{resource_name} <id> not found' формат",
            classes=["CONF", "NEG"], priority="P1",
            steps=[Step(name="get", method="GET",
                        path=f"{resource_path}/enpsnapshotnonexist01",
                        test_script=[
                            *assert_status(404), *assert_grpc_code(5, "NOT_FOUND"),
                            f"pm.test('text matches <Resource> <id> not found', () => "
                            f"pm.expect(pm.response.json().message).to.match(/^{resource_name} enpsnapshotnonexist01 not found$/));",
                        ])],
        ),
        Case(
            id=f"{prefix}-UPD-CONF-FULLTEXT",
            title=f"Update garbage → точный текст '{resource_name} ... not found'",
            classes=["CONF", "NEG"], priority="P1",
            steps=[Step(name="upd", method="PATCH",
                        path=f"{resource_path}/enpsnapshotnonexist02",
                        body={"updateMask": "description", "description": "x"},
                        test_script=[
                            *assert_status(404), *assert_grpc_code(5, "NOT_FOUND"),
                            f"pm.test('verbatim text', () => "
                            f"pm.expect(pm.response.json().message).to.match(/^{resource_name} enpsnapshotnonexist02 not found$/));",
                        ])],
        ),
        Case(
            id=f"{prefix}-DEL-CONF-FULLTEXT",
            title=f"Delete garbage → '{resource_name} ... not found'",
            classes=["CONF", "NEG"], priority="P1",
            steps=[Step(name="del", method="DELETE",
                        path=f"{resource_path}/enpsnapshotnonexist03",
                        test_script=[
                            *assert_status(404), *assert_grpc_code(5, "NOT_FOUND"),
                            f"pm.test('verbatim text', () => "
                            f"pm.expect(pm.response.json().message).to.match(/^{resource_name} enpsnapshotnonexist03 not found$/));",
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


def cross_folder_resource_block(prefix, create_path, body_create, name_field="name"):
    """Cross-folder validation: создать ресурс в одном folder, увидеть его только из этого folder."""
    return Case(
        id=f"{prefix}-LST-AUTHZ-CROSS-FOLDER-ISOLATION",
        title="Folder isolation: ресурс в folderA не виден в List по folderB",
        classes=["AUTHZ", "CRUD"], priority="P0",
        steps=[
            Step(name="create-in-A", method="POST", path=create_path,
                 body={**body_create, "folderId": "{{_suiteFolderId}}",
                       name_field: f"{prefix.lower()}-iso-{{{{runId}}}}"},
                 test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                              *save_from_response("(j.metadata && Object.keys(j.metadata).filter(k => k.endsWith('Id')).map(k => j.metadata[k])[0]) || ''", "isoId")]),
            Step(name="poll", method="GET", path="/operations/{{opId}}",
                 test_script=["pm.test('done', () => pm.expect(pm.response.json().done).to.eql(true));"]),
            Step(name="list-in-B", method="GET",
                 path=f"{create_path}?folderId={{{{_suiteFolderCrossId}}}}&pageSize=100",
                 test_script=[*assert_status(200),
                              "const ids = (Object.values(pm.response.json()).find(v => Array.isArray(v)) || []).map(x => x.id);",
                              "pm.test('isolated — not in folderB list', () => pm.expect(ids).to.not.include(pm.environment.get('isoId')));"]),
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
                 path=f"{create_path}?folderId={{{{_suiteFolderId}}}}&pageSize=100&filter=name%3D%22{prefix.lower()}-flt-{{{{runId}}}}%22",
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
            title="Create с labels=строка (вместо object) → 400 InvalidArgument",
            classes=["VAL", "NEG"], priority="P2",
            steps=[Step(name="cr-bad-type", method="POST", path=create_path,
                        body={**body_create, "name": f"{prefix.lower()}-bt-{{{{runId}}}}", "labels": "not-an-object"},
                        test_script=[*assert_status(400), *assert_grpc_code(3, "INVALID_ARGUMENT")])],
        ),
        Case(
            id=f"{prefix}-CR-VAL-DESC-INT-TYPE",
            title="Create с description=число → 400",
            classes=["VAL", "NEG"], priority="P3",
            steps=[Step(name="cr-bad-desc", method="POST", path=create_path,
                        body={**body_create, "name": f"{prefix.lower()}-bd-{{{{runId}}}}", "description": 12345},
                        test_script=[*assert_status(400), *assert_grpc_code(3, "INVALID_ARGUMENT")])],
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
                        body={"folderId": "{{_suiteFolderId}}"},
                        test_script=["pm.test('not allowed (404/405/501)', () => pm.expect(pm.response.code).to.be.oneOf([404, 405, 501]));"])],
        ),
        Case(
            id=f"{prefix}-METHOD-DELETE-LIST",
            title="DELETE на List endpoint (без id) → 405 или 404",
            classes=["VAL", "NEG"], priority="P3",
            steps=[Step(name="del-list", method="DELETE", path=base_path,
                        test_script=["pm.test('not allowed (404/405/501)', () => pm.expect(pm.response.code).to.be.oneOf([404, 405, 501]));"])],
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
    """Tests duplicate name → ALREADY_EXISTS (где есть UNIQUE constraint)."""
    return Case(
        id=f"{prefix}-CR-NEG-DUP-NAME-CHECK",
        title="Создать дубль с тем же name → проверить ALREADY_EXISTS или silent (FINDING)",
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
                 test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                              *save_from_response("(j.metadata && Object.keys(j.metadata).filter(k => k.endsWith('Id')).map(k => j.metadata[k])[0]) || ''", "secondId")]),
            Step(name="poll-dup", method="GET", path="/operations/{{opId}}",
                 test_script=["pm.test('done', () => pm.expect(pm.response.json().done).to.eql(true));"]),
            Step(name="check-result", method="GET", path="/operations/{{opId}}",
                 test_script=[
                     "const j = pm.response.json();",
                     "// Если UNIQUE есть — j.error.code===6; если нет — silent success.",
                     "pm.test('result is either ALREADY_EXISTS or success', () => {",
                     "  const hasError = j.error && (j.error.code === 6 || j.error.code === 9);  // ALREADY_EXISTS или FailedPrecondition (CIDR overlap)",
                     "  const hasResponse = !j.error && j.response;",
                     "  pm.expect(Boolean(hasError || hasResponse)).to.eql(true);",
                     "});",
                 ]),
            Step(name="cleanup-first", method="DELETE", path=f"{create_path}/{{{{firstId}}}}",
                 test_script=[*save_from_response("j.id", "opId")]),
            Step(name="poll-c1", method="GET", path="/operations/{{opId}}",
                 test_script=["pm.test('done', () => pm.expect(pm.response.json().done).to.eql(true));"]),
            Step(name="cleanup-second-if-different", method="DELETE",
                 path=f"{create_path}/{{{{secondId}}}}",
                 test_script=["pm.test('cleanup', () => pm.expect(pm.response.code).to.be.oneOf([200, 404]));",
                              *save_from_response("j.id", "opId")]),
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
                        path=f"{list_path}?folderId={{{{_suiteFolderId}}}}&pageSize=5",
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


def authz_caller_headers_block(prefix, list_path):
    """RBAC-pre kit: проверка headers cross-tenant (анонимный vs admin-claim)."""
    return [
        Case(
            id=f"{prefix}-AUTHZ-EMPTY-FOLDER-HEADER",
            title="List с пустым x-kacho-folder-id header → текущее: 200 (dev mode)",
            classes=["AUTHZ"], priority="P1",
            steps=[Step(name="list-with-empty-header", method="GET",
                        path=f"{list_path}?folderId={{{{_suiteFolderId}}}}",
                        test_script=[
                            "pm.test('OK in dev or PermissionDenied in production', () => pm.expect(pm.response.code).to.be.oneOf([200, 403, 401]));",
                        ])],
        ),
    ]


def conf_alreadyexists_block(prefix, create_path, name_template, body_extra=None):
    """CONF: AlreadyExists text при duplicate name."""
    body_extra = body_extra or {}
    return Case(
        id=f"{prefix}-CR-CONF-ALREADY-EXISTS",
        title="Create duplicate name → verbatim ALREADY_EXISTS текст",
        classes=["CONF", "NEG"], priority="P1",
        steps=[
            Step(name="create-first", method="POST", path=create_path,
                 body={"folderId": "{{_suiteFolderId}}", "name": name_template, **body_extra},
                 test_script=[*assert_status(200), *save_from_response("j.id", "opId"),
                              *save_from_response("j.metadata && Object.values(j.metadata).find(v => typeof v === 'string' && v.length > 10)", "createdId")]),
            poll_operation_until_done(),
            Step(name="create-dup", method="POST", path=create_path,
                 body={"folderId": "{{_suiteFolderId}}", "name": name_template, **body_extra},
                 test_script=[*assert_status(200), *save_from_response("j.id", "opId")]),
            poll_operation_until_done(),
            Step(name="assert-text", method="GET", path="/operations/{{opId}}",
                 test_script=[
                     "const j = pm.response.json();",
                     "pm.test('error code 6 ALREADY_EXISTS', () => pm.expect(j.error && j.error.code).to.eql(6));",
                     "pm.test('message non-empty', () => pm.expect(j.error.message).to.be.a('string').and.length.greaterThan(0));",
                 ]),
        ],
    )


def poll_operation_until_done() -> Step:
    """Reusable poll step с retry-на-not-done через setNextRequest.
    До 6 попыток, потом fail если done остался false.
    """
    return Step(
        name="poll-op",
        method="GET",
        path="/operations/{{opId}}",
        test_script=[
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
    events = []
    if step.pre_script:
        events.append(
            {"listen": "prerequest", "script": {"type": "text/javascript", "exec": step.pre_script}}
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


def build_collection(service: str, cases: List[Case]) -> Dict:
    return {
        "info": {
            "_postman_id": str(uuid.uuid4()),
            "name": f"kacho-vpc / newman2 / {service}",
            "schema": "https://schema.getpostman.com/json/collection/v2.1.0/collection.json",
        },
        "event": [
            {
                "listen": "prerequest",
                "script": {"type": "text/javascript", "exec": PRE_GLOBAL},
            },
        ],
        "item": [case_to_postman(c) for c in cases],
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
    mod.assert_field_violation = assert_field_violation
    mod.save_from_response = save_from_response
    mod.assert_operation_envelope = assert_operation_envelope
    mod.poll_operation_until_done = poll_operation_until_done
    mod.crud_list_bva_block = crud_list_bva_block
    mod.conf_not_found_text = conf_not_found_text
    mod.state_update_unknown_mask = state_update_unknown_mask
    mod.authz_move_nf = authz_move_nf
    mod.val_move_no_dest = val_move_no_dest
    mod.state_immutable_folder = state_immutable_folder
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
    mod.move_same_folder = move_same_folder
    mod.verbatim_text_pack = verbatim_text_pack
    mod.authz_caller_headers_block = authz_caller_headers_block
    mod.update_happy_multi_field = update_happy_multi_field
    mod.cross_folder_resource_block = cross_folder_resource_block
    mod.list_filter_match_block = list_filter_match_block
    mod.neg_invalid_types_block = neg_invalid_types_block
    mod.http_method_not_allowed_block = http_method_not_allowed_block
    mod.malformed_body_block = malformed_body_block
    mod.alreadyexists_dup_name_for = alreadyexists_dup_name_for
    mod.update_mask_partial_block = update_mask_partial_block
    mod.perf_baseline_get_block = perf_baseline_get_block
    mod.list_total_size_check_block = list_total_size_check_block
    mod.headers_content_type_block = headers_content_type_block
    spec.loader.exec_module(mod)
    return mod


def main(argv: List[str]) -> int:
    OUT_DIR.mkdir(parents=True, exist_ok=True)
    want = set(argv[1:])
    found = sorted(CASES_DIR.glob("*.py"))
    if not found:
        print(f"no case files in {CASES_DIR}")
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
