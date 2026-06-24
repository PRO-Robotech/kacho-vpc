#!/usr/bin/env python3
# Copyright (c) PRO-Robotech
# SPDX-License-Identifier: BUSL-1.1

"""
tests/newman/scripts/validate-cases.py — MANDATORY case-uniqueness validation.

Гоняется в CI **до** тяжелого newman-шага (pure-Python, без сети). Hard-fail
(exit 1), если:

  1. Один и тот же case-id встречается более одного раза среди всех кейсов,
     которые генерирует `gen.py` (внутри файла, между файлами, в helper-блоках) —
     дубль case-id запрещен.
  2. Новый кейс не зафиксирован в каталоге паттернов `docs/CASES-INDEX.md`:
     каждый case-id обязан либо
       (a) быть покрытым `CASES-INDEX.md` — суффикс-паттерн `*-<SUFFIX>` ИЛИ
           литеральный case-id присутствует в тексте `CASES-INDEX.md`, ЛИБО
       (b) быть помеченным в case-файле тегом-комментарием  `# index: <ref>`
           (на строке с `id="..."`, либо на 1-2 строках выше) — это значит
           «инстанс известного паттерна <ref>, отдельная запись в индексе не нужна».

  Исключение: case-файлы `internal-*.py` (admin/IPAM RPC, prefix `IPL-*`/
  `CLD-*`) — CASES-INDEX каталогизирует их отдельной заметкой,
  а не таблицей паттернов (см. шапку `CASES-INDEX.md`), поэтому от них
  индекс-покрытие не требуется (но dup-id-проверка работает и для них).

Замечание про helper-генерируемые кейсы: их case-id не встречаются литерально в
case-файлах (их собирают функции в `gen.py` — `crud_list_bva_block`, ...), поэтому
пометить их `# index:` нельзя — они проходят (2) только через `CASES-INDEX.md`
(их паттерны там и каталогизированы — это и есть смысл каталога).

Если кейс не проходит (2) — он либо новый уникальный паттерн (→ добавь запись
в `CASES-INDEX.md`, при необходимости — `REQ-*` в `PRODUCT-REQUIREMENTS.md`,
апдейт `TEST-PLAN.md`/`RESULTS.md`), либо инстанс существующего (→ пометь
`# index: <ref>` рядом с `id=`).

Использование:
    python3 tests/newman/scripts/validate-cases.py
    # или (то же самое):
    python3 tests/newman/scripts/gen.py --validate
"""
from __future__ import annotations

import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
SCRIPTS_DIR = ROOT / "scripts"
CASES_DIR = ROOT / "cases"
INDEX_FILE = ROOT / "docs" / "CASES-INDEX.md"

# case-файлы, для которых индекс-покрытие не требуется (каталогизированы заметкой).
INTERNAL_FILE_RE = re.compile(r"^internal-")

_ID_RE = re.compile(r"""id\s*=\s*["']([A-Z0-9][A-Z0-9_-]+)["']""")
_INDEX_TAG_RE = re.compile(r"#\s*index:\s*(\S+)")

sys.path.insert(0, str(SCRIPTS_DIR))


def _suffix(case_id: str) -> str:
    """`NIC-CR-CRUD-OK` -> `CR-CRUD-OK` (отрезаем domain-prefix перед первым '-')."""
    parts = case_id.split("-")
    return "-".join(parts[1:]) if len(parts) > 1 else case_id


def _text_tags() -> dict[str, set[str]]:
    """Скан case-файлов: {case_id: {filenames where id= встречается with `# index:` tag}}."""
    tagged: dict[str, set[str]] = {}
    for f in sorted(CASES_DIR.glob("*.py")):
        lines = f.read_text().splitlines()
        for i, line in enumerate(lines):
            m = _ID_RE.search(line)
            if not m:
                continue
            case_id = m.group(1)
            window = "\n".join(lines[max(0, i - 2): i + 1])
            if _INDEX_TAG_RE.search(window):
                tagged.setdefault(case_id, set()).add(f.name)
    return tagged


def _all_cases() -> list[tuple[str, str]]:
    """Импортирует case-модули как это делает gen.py → [(case_id, service_filename), ...]
    в порядке генерации (включая helper-блоки)."""
    import gen  # noqa: E402  (lazy — sys.path подправлен выше)

    out: list[tuple[str, str]] = []
    for f in sorted(CASES_DIR.glob("*.py")):
        mod = gen.load_cases_module(f)
        for c in getattr(mod, "CASES", []):
            out.append((c.id, f.name))
    return out


def main() -> int:
    errors: list[str] = []

    try:
        cases = _all_cases()
    except Exception as exc:  # noqa: BLE001 — surface как ошибка валидации
        sys.stderr.write(f"validate-cases: FAIL — не удалось загрузить case-модули: {exc}\n")
        return 1
    if not cases:
        sys.stderr.write("validate-cases: FAIL — нет кейсов\n")
        return 1

    # ---- (1) duplicate case-id (по всему набору, что генерит gen.py) ----
    seen: dict[str, str] = {}
    for case_id, fname in cases:
        if case_id in seen:
            errors.append(
                f"duplicate case-id {case_id!r}: встречается в {seen[case_id]} и {fname} "
                f"(case-id должен быть уникален среди всех кейсов)"
            )
        else:
            seen[case_id] = fname

    # ---- (2) каждый case-id зафиксирован в CASES-INDEX.md или помечен `# index:` ----
    index_text = INDEX_FILE.read_text() if INDEX_FILE.exists() else ""
    if not index_text:
        errors.append(f"missing {INDEX_FILE}")
    tagged = _text_tags()
    # дедуп для отчета: каждый case-id проверяем один раз
    checked: set[str] = set()
    for case_id, fname in cases:
        if case_id in checked:
            continue
        checked.add(case_id)
        if INTERNAL_FILE_RE.match(fname):
            continue  # admin/IPAM — каталогизированы заметкой
        if case_id in tagged:
            continue
        suf = _suffix(case_id)
        if f"*-{suf}" in index_text or case_id in index_text:
            continue
        errors.append(
            f"case {case_id!r} (из {fname}) не зафиксирован в docs/CASES-INDEX.md.\n"
            f"    → НОВЫЙ уникальный паттерн: добавь запись `*-{suf}` (или `{case_id}`) "
            f"в docs/CASES-INDEX.md (+ при необходимости REQ-* в PRODUCT-REQUIREMENTS.md, "
            f"апдейт TEST-PLAN.md/RESULTS.md);\n"
            f"    → ИНСТАНС существующего паттерна: пометь строку с id= тегом "
            f"`# index: <pattern-ref>`."
        )

    if errors:
        sys.stderr.write("validate-cases: FAIL\n")
        for e in errors:
            sys.stderr.write("  - " + e + "\n")
        return 1
    print(
        f"validate-cases: OK — {len(seen)} уникальных case-id, нет дублей, "
        f"все каталогизированы (CASES-INDEX / # index:)"
    )
    return 0


if __name__ == "__main__":
    sys.exit(main())
