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
    """Reusable poll step. Кейс должен предварительно сохранить opId.
    Note: api-gateway маршрутизирует OperationService на /operations/{id}
    (не /vpc/v1/operations) — 3-char prefix routing по op.id.
    """
    return Step(
        name="poll-op",
        method="GET",
        path="/operations/{{opId}}",
        test_script=[
            "pm.test('poll status 200', () => pm.expect(pm.response.code).to.eql(200));",
            "const j = pm.response.json();",
            "pm.test('operation done', () => pm.expect(j.done, JSON.stringify(j)).to.eql(true));",
            "// сохранить error/response для последующих ассертов",
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
