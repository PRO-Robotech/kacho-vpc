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
