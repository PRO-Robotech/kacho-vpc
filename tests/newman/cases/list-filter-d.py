# Copyright (c) PRO-Robotech
# SPDX-License-Identifier: BUSL-1.1

"""Case-set list-filter-d для kacho-vpc — per-object filtered List.

Black-box проверка: `List<Resource>` отдает ТОЛЬКО доступные объекты (per-object FGA
ListObjects поверх materialized tuples + scope_grant), НЕ all-or-nothing; read==enforce
(List-видимость == Get-allow); no-leak (объект вне гранта отсутствует в List И Get→404,
НЕ 403).

Pre-conditions (готовит `tests/authz-fixtures/setup.sh` на стенде). Setup патчит
env-файл, добавляя:
  - jwtSubnetSubsetViewer : Bearer subject S с per-object (resourceNames) viewer-грантом
                            на ПОДМНОЖЕСТВО subnet'ов проекта (не на весь проект).
  - jwtNoSubnetGrant      : Bearer subject без какого-либо гранта на subnet'ы проекта.
  - listFilterProjectId   : project, в котором живут seed-subnet'ы.
  - subnetVisibleId       : subnet, входящий в грант S (должен быть виден).
  - subnetHiddenId        : subnet того же проекта, НЕ входящий в грант S (no-leak).

Семантика проверок:
  - SUBNET-LF-D-VISIBLE : S → List subnets содержит subnetVisibleId.
  - SUBNET-LF-D-NOLEAK  : S → List subnets НЕ содержит subnetHiddenId (no-leak).
  - SUBNET-LF-D-GET-404 : S → Get(subnetHiddenId) → 404 NOT_FOUND (НЕ 403; не
                          подтверждаем existence чужого объекта — read==enforce no-leak).
  - SUBNET-LF-D-GET-OK  : S → Get(subnetVisibleId) → 200 (read==enforce: видим в List ⇒ Get-allow).
  - SUBNET-LF-D-NONE    : subject без гранта → List subnets пуст (НЕ весь проект).

Helpers Case/Step/assert_status инжектятся через gen.py namespace.

# requires tests/authz-fixtures/setup.sh (rules-model per-object grant fixtures)
"""

CASES = []


def _list_subnets_step(name, auth, test_script):
    return Step(
        name=name,
        method="GET",
        path="/vpc/v1/subnets?projectId={{listFilterProjectId}}&pageSize=1000",
        auth=auth,
        test_script=test_script,
    )


# SUBNET-LF-D-VISIBLE: granted subnet appears in the filtered List.
CASES.append(Case(
    id="SUBNET-LF-D-VISIBLE",
    title="per-object List: subject sees the subnet it was granted (D-40/D-41/D-43)",
    classes=["AUTHZ", "CONF"],
    priority="P0",
    steps=[
        _list_subnets_step(
            "List subnets as subset-viewer",
            "jwtSubnetSubsetViewer",
            [
                "pm.test('[SUBNET-LF-D-VISIBLE] status 200', () => pm.expect(pm.response.code).to.eql(200));",
                "const j = pm.response.json();",
                "const ids = (j.subnets || []).map(s => s.id);",
                "pm.test('[SUBNET-LF-D-VISIBLE] granted subnet present', () => "
                "pm.expect(ids, JSON.stringify(ids)).to.include(pm.environment.get('subnetVisibleId')));",
            ],
        ),
    ],
))

# SUBNET-LF-D-NOLEAK: non-granted subnet of the same project is absent (no-leak).
CASES.append(Case(
    id="SUBNET-LF-D-NOLEAK",
    title="per-object List no-leak: non-granted subnet absent from List (D-44/LST-5)",
    classes=["AUTHZ", "NEG"],
    priority="P0",
    steps=[
        _list_subnets_step(
            "List subnets as subset-viewer (hidden absent)",
            "jwtSubnetSubsetViewer",
            [
                "pm.test('[SUBNET-LF-D-NOLEAK] status 200', () => pm.expect(pm.response.code).to.eql(200));",
                "const j = pm.response.json();",
                "const ids = (j.subnets || []).map(s => s.id);",
                "pm.test('[SUBNET-LF-D-NOLEAK] hidden subnet absent', () => "
                "pm.expect(ids, JSON.stringify(ids)).to.not.include(pm.environment.get('subnetHiddenId')));",
            ],
        ),
    ],
))

# SUBNET-LF-D-GET-404: Get on non-granted subnet → 404 (no-leak, NOT 403).
CASES.append(Case(
    id="SUBNET-LF-D-GET-404",
    title="per-object no-leak: Get(hidden) → 404 NotFound, not 403 (D-44/LST-5)",
    classes=["AUTHZ", "NEG"],
    priority="P0",
    steps=[
        Step(
            name="Get hidden subnet as subset-viewer",
            method="GET",
            path="/vpc/v1/subnets/{{subnetHiddenId}}",
            auth="jwtSubnetSubsetViewer",
            test_script=[
                "pm.test('[SUBNET-LF-D-GET-404] status 404 (no-leak, not 403)', () => "
                "pm.expect(pm.response.code, JSON.stringify(pm.response.text())).to.eql(404));",
                "let j; try { j = pm.response.json(); } catch(e) { j = null; }",
                "pm.test('[SUBNET-LF-D-GET-404] grpc code 5 (NOT_FOUND)', () => "
                "pm.expect(j && j.code, JSON.stringify(j)).to.eql(5));",
            ],
        ),
    ],
))

# SUBNET-LF-D-GET-OK: Get on granted subnet → 200 (read==enforce parity).
CASES.append(Case(
    id="SUBNET-LF-D-GET-OK",
    title="read==enforce: Get(visible) → 200 (parity with List visibility, D-45)",
    classes=["AUTHZ", "CONF"],
    priority="P1",
    steps=[
        Step(
            name="Get visible subnet as subset-viewer",
            method="GET",
            path="/vpc/v1/subnets/{{subnetVisibleId}}",
            auth="jwtSubnetSubsetViewer",
            test_script=[
                "pm.test('[SUBNET-LF-D-GET-OK] status 200', () => "
                "pm.expect(pm.response.code, JSON.stringify(pm.response.text())).to.eql(200));",
            ],
        ),
    ],
))

# SUBNET-LF-D-NONE: subject with no subnet grant → empty List (not the whole project).
CASES.append(Case(
    id="SUBNET-LF-D-NONE",
    title="per-object List: no grant → empty list, not whole project (D-44)",
    classes=["AUTHZ", "NEG"],
    priority="P1",
    steps=[
        _list_subnets_step(
            "List subnets as no-grant subject",
            "jwtNoSubnetGrant",
            [
                "pm.test('[SUBNET-LF-D-NONE] status 200', () => pm.expect(pm.response.code).to.eql(200));",
                "const j = pm.response.json();",
                "const ids = (j.subnets || []).map(s => s.id);",
                "pm.test('[SUBNET-LF-D-NONE] empty (no grant → no rows)', () => "
                "pm.expect(ids.length, JSON.stringify(ids)).to.eql(0));",
            ],
        ),
    ],
))
