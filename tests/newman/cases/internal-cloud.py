"""Case-set для InternalCloudService (kacho-only admin RPC) — Cloud pool-selector.

SetPoolSelector / UnsetPoolSelector / GetPoolSelector — admin-управление
routing-labels на Cloud (используется в IPAM cascade step 3). Хранится в
kacho-vpc БД (cloud_pool_selector), логически принадлежит Cloud (ID из
kacho-resource-manager). RPC проброшены через api-gateway cluster-internal mux
на /vpc/v1/clouds/{cloud_id}/poolSelector — НЕ verbatim-YC, возвращают
структуру ПРЯМО (не Operation).

Использует existingCloudId из env. Тест восстанавливает исходное состояние
selector'а (unset) в конце — selector seeded-стенда по умолчанию present=false.

NB (KAC-50): internal mux api-gateway маршалит ответы с
`EmitUnpopulated=false` — bool false / нулевые значения опускаются из JSON.
Поэтому `present=false` приходит как `undefined`, а не как `false`. Чтобы тест
не зависел от marshaler-режима, проверяем «не true», а не «строго false».
"""

CASES = []

SEL = "/vpc/v1/clouds/{{existingCloudId}}/poolSelector"


CASES.append(Case(
    id="CLD-SEL-CRUD-OK",
    title="PoolSelector: Get(present=false) → Set → Get(present=true, selector echoed) → Unset → Get(present=false)",
    classes=["CRUD", "STATE", "CONF"], priority="P0",
    steps=[
        # Baseline: unset (на случай если предыдущий прогон оставил selector).
        Step(name="reset-baseline", method="DELETE", path=SEL,
             test_script=["pm.test('reset ok', () => pm.expect(pm.response.code).to.be.oneOf([200, 404]));"]),
        Step(name="get-empty", method="GET", path=SEL,
             test_script=[*assert_status(200),
                          "const j = pm.response.json();",
                          "pm.test('present false', () => pm.expect(j.present || false).to.eql(false));",
                          "pm.test('selector empty/absent', () => pm.expect(Object.keys(j.selector || {}).length).to.eql(0));"]),
        Step(name="set", method="POST", path=SEL,
             body={"selector": {"tier": "premium", "region": "ru"}, "setBy": "newman-{{runId}}"},
             test_script=[*assert_status(200),
                          "pm.test('set returns obj', () => pm.expect(pm.response.json()).to.be.an('object'));"]),
        Step(name="get-present", method="GET", path=SEL,
             test_script=[*assert_status(200),
                          "const j = pm.response.json();",
                          "pm.test('present true', () => pm.expect(j.present).to.eql(true));",
                          "pm.test('selector tier', () => pm.expect((j.selector || {}).tier).to.eql('premium'));",
                          "pm.test('selector region', () => pm.expect((j.selector || {}).region).to.eql('ru'));",
                          "pm.test('setBy echoed', () => pm.expect(j.setBy).to.eql('newman-' + pm.environment.get('runId')));",
                          "pm.test('setAtRfc3339 non-empty', () => pm.expect(j.setAtRfc3339).to.be.a('string').and.length.greaterThan(0));"]),
        # Idempotent re-set с другим selector → перезаписывается.
        Step(name="set-again", method="POST", path=SEL,
             body={"selector": {"tier": "standard"}, "setBy": "newman2-{{runId}}"},
             test_script=[*assert_status(200)]),
        Step(name="get-overwritten", method="GET", path=SEL,
             test_script=[*assert_status(200),
                          "const j = pm.response.json();",
                          "pm.test('tier overwritten', () => pm.expect((j.selector || {}).tier).to.eql('standard'));",
                          "pm.test('region removed', () => pm.expect((j.selector || {}).region).to.be.oneOf([undefined, null]));"]),
        Step(name="unset", method="DELETE", path=SEL,
             test_script=[*assert_status(200),
                          "pm.test('unset returns obj', () => pm.expect(pm.response.json()).to.be.an('object'));"]),
        Step(name="get-after-unset", method="GET", path=SEL,
             test_script=[*assert_status(200),
                          "pm.test('present false again', () => pm.expect(pm.response.json().present || false).to.eql(false));"]),
        # Idempotent unset on absent → no-op.
        Step(name="unset-again", method="DELETE", path=SEL,
             test_script=["pm.test('idempotent unset', () => pm.expect(pm.response.code).to.be.oneOf([200, 404]));"]),
    ],
))

CASES.append(Case(
    id="CLD-SEL-GET-UNKNOWN-CLOUD",
    title="GetPoolSelector для несуществующего cloud → present=false (selector хранится lazily, существование cloud не проверяется на Get)",
    classes=["CONF", "NEG"], priority="P2",
    steps=[
        # Reset на случай если предыдущий прогон оставил selector на этом id.
        Step(name="reset", method="DELETE", path="/vpc/v1/clouds/b1gnonexistent999999/poolSelector",
             test_script=["pm.test('reset ok', () => pm.expect(pm.response.code).to.be.oneOf([200, 404]));"]),
        # Get не делает folder/cloud-existence check — просто читает row (или его нет).
        Step(name="get-unknown", method="GET", path="/vpc/v1/clouds/b1gnonexistent999999/poolSelector",
             test_script=[*assert_status(200),
                          "pm.test('present false for unknown cloud', () => pm.expect(pm.response.json().present || false).to.eql(false));"]),
    ],
))

CASES.append(Case(
    id="CLD-SEL-SET-UNKNOWN-CLOUD",
    title="SetPoolSelector для несуществующего cloud → текущее: 200 (cloud-existence не валидируется) — см. FINDING-009",
    classes=["CONF", "NEG"], priority="P2",
    steps=[
        # FINDING-009: SetPoolSelector делает upsert в cloud_pool_selector без
        # проверки существования cloud_id в kacho-resource-manager. Proto-комментарий
        # обещает existence-check, но реализация (cloudSel.Set) его не делает.
        # Кейс ассертит фактическое поведение + чистит за собой.
        Step(name="set-unknown", method="POST", path="/vpc/v1/clouds/b1gnonexistent999998/poolSelector",
             body={"selector": {"x": "y"}, "setBy": "newman-{{runId}}"},
             test_script=["pm.test('accepted (200) or rejected (404/400)', () => pm.expect(pm.response.code).to.be.oneOf([200, 400, 404]));"]),
        # cleanup: unset (no-op if rejected).
        Step(name="cleanup-unset", method="DELETE", path="/vpc/v1/clouds/b1gnonexistent999998/poolSelector",
             test_script=["pm.test('cleanup', () => pm.expect(pm.response.code).to.be.oneOf([200, 404]));"]),
    ],
))

CASES.append(Case(
    id="CLD-SEL-SET-EMPTY-SELECTOR",
    title="SetPoolSelector с пустым selector map → 200; Get показывает present (или present=false если empty трактуется как unset)",
    classes=["VAL", "CONF"], priority="P3",
    steps=[
        Step(name="reset-baseline", method="DELETE", path=SEL,
             test_script=["pm.test('reset ok', () => pm.expect(pm.response.code).to.be.oneOf([200, 404]));"]),
        Step(name="set-empty", method="POST", path=SEL,
             body={"selector": {}, "setBy": "newman-empty-{{runId}}"},
             test_script=["pm.test('accepted or rejected', () => pm.expect(pm.response.code).to.be.oneOf([200, 400]));"]),
        Step(name="get-after", method="GET", path=SEL,
             test_script=[*assert_status(200),
                          "pm.test('present is boolean', () => pm.expect(pm.response.json().present).to.be.a('boolean'));"]),
        # cleanup.
        Step(name="cleanup-unset", method="DELETE", path=SEL,
             test_script=["pm.test('cleanup', () => pm.expect(pm.response.code).to.be.oneOf([200, 404]));"]),
    ],
))
