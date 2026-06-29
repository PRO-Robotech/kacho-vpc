# Copyright (c) PRO-Robotech
# SPDX-License-Identifier: BUSL-1.1

"""Case-set authz-deny для kacho-vpc.

Проверяет default-deny matrix для 6 классов субъектов на каждом публичном CRUD
каждого VPC-ресурса — против ЖИВОГО object-scoped authz (api-gateway → FGA), не
dev-mode anonymous→full-access.

Pre-conditions: `tests/authz-fixtures/setup.sh` создает фикстуры (accounts,
projects, users, bindings, seed networks) и патчит env-файл:
  - jwt*               : Bearer-токены (no-bindings / proj-admin-a1 / account-admin-a/b / invitee)
  - accountAId / Bid
  - projectA1Id / A2Id / B1Id
  - seedNetworkA1Id / seedNetworkB1Id

Реальные гранты фикстуры (источник истины — setup.sh):
  - PA1 → editor   @ project:A1
  - AAA → admin    @ account:A   (каскад на project:A1)
  - AAB → admin    @ account:B   (каскад на project:B1)
  - INV → admin    @ account:B (каскад на project:B1) + editor @ project:A1
          (KAC-125 invite-flow: AAA приглашает INV в account-A editor'ом на
          project-A1) → INV имеет доступ к ОБОИМ project-A1 и project-B1.
  - NOB → грантов нет (НО см. kacho-iam#276: iam-suite IAM-ACB-CR-CRUD-OK грантит
    userNOB глобальную *.* view-роль на account-A/-B — cross-suite fixture collision.
    Поэтому AUTHZ-*-LS-{OWN,CROSS}-NOB сейчас known-RED, issue-backed, до owner-decided
    semantics/test-hygiene фикса. NOB фактически authorized; кейсы остаются красными
    честно (ban#13), но whitelisted в assert-suites-green.sh. verifies kacho-iam#276)

Контракт ответов (api-gateway authz middleware, см. kacho-api-gateway):
  - Анонимный запрос (нет токена) → 401 UNAUTHENTICATED (grpc 16) ВЕЗДЕ.
  - Object-scoped READ (`/Get`) на запрещенный/несуществующий ресурс → существование
    скрывается: 404 NOT_FOUND (grpc 5). `/Get`-deny одинаков для «нет такого» и
    «есть-но-не-твой» (anti-enumeration).
  - Object-scoped MUTATION (`/Update`, `/Delete`) на запрещенный/несуществующий
    ресурс → 403 PERMISSION_DENIED (grpc 7). Existence не скрывается для мутаций
    (deny одинаков для существующего и нет → утечки тоже нет).
  - `/List` — scope-filtered: НЕ gated «все или ничего», а сужается до видимого
    subject'у набора. Нет доступа → 403 (gated List RPC) ЛИБО 200 + ПУСТОЙ список
    (scope-filtered List RPC). 200 + чужие ресурсы = LEAK (валит тест).
  - Create-child / админ-only RPC без нужного гранта → 403.

Helpers Case/Step инжектятся через gen.py namespace.
"""

CASES = []

SUBJECTS = [
    # code, label, auth (None→anonymous, иначе env-var-name)
    ("ANON", "anon",       "anonymous"),
    ("NOB",  "no-bind",    "jwtNoBindings"),
    ("PA1",  "proj-adm",   "jwtProjectAdminA1"),
    ("AAA",  "acct-adm-a", "jwtAccountAdminA"),
    ("AAB",  "acct-adm-b", "jwtAccountAdminB"),
    ("INV",  "invitee",    "jwtInvitee"),
]

# scope-class → subject-code → expected ('ALLOW'/'DENY'). Отражает РЕАЛЬНЫЕ гранты
# фикстуры (см. docstring), а не «кому хотелось бы».
EXPECT = {
    # project-A1: editor у PA1; account-A admin (AAA) каскадит на A1; INV — editor
    # @ project-A1 через KAC-125 invite-flow.
    "project-A1":          {"ANON":"DENY","NOB":"DENY","PA1":"ALLOW","AAA":"ALLOW","AAB":"DENY", "INV":"ALLOW"},
    # project-B1: account-B admin (AAB, INV) каскадит на B1.
    "project-B1":          {"ANON":"DENY","NOB":"DENY","PA1":"DENY", "AAA":"DENY", "AAB":"ALLOW","INV":"ALLOW"},
    # AddressPool — admin-only (cluster system_admin): ни один из 6 субъектов его не
    # несет → DENY у всех аутентифицированных; ANON → 401.
    "addresspool-admin-only": {"ANON":"DENY","NOB":"DENY","PA1":"DENY","AAA":"DENY","AAB":"DENY","INV":"DENY"},
}


def deny_asserts(case_id):
    """Аутентифицированный субъект без доступа → 403 PERMISSION_DENIED (grpc 7)."""
    return [
        f"pm.test('[{case_id}] DENY: status 403', () => pm.expect(pm.response.code, JSON.stringify(pm.response.text())).to.equal(403));",
        "let j; try { j = pm.response.json(); } catch(e) { j = null; }",
        f"pm.test('[{case_id}] DENY: grpc code 7 (PERMISSION_DENIED)', () => pm.expect(j && j.code, JSON.stringify(j)).to.equal(7));",
        f"pm.test('[{case_id}] DENY: message contains permission denied', () => pm.expect((j && j.message || '').toLowerCase()).to.contain('permission denied'));",
    ]


def unauth_asserts(case_id):
    """Anonymous (нет токена) → 401 UNAUTHENTICATED (grpc 16)."""
    return [
        f"pm.test('[{case_id}] UNAUTH: status 401', () => pm.expect(pm.response.code, JSON.stringify(pm.response.text())).to.equal(401));",
        "let j; try { j = pm.response.json(); } catch(e) { j = null; }",
        f"pm.test('[{case_id}] UNAUTH: grpc code 16 (UNAUTHENTICATED)', () => pm.expect(j && j.code, JSON.stringify(j)).to.equal(16));",
    ]


def notfound_asserts(case_id):
    """Object-scoped `/Get`-deny → existence-hiding: 404 NOT_FOUND (grpc 5).
    Одинаков для «нет такого» и «есть-но-не-твой» (anti-enumeration)."""
    return [
        f"pm.test('[{case_id}] NF: status 404 (read-deny existence-hiding)', () => pm.expect(pm.response.code, JSON.stringify(pm.response.text())).to.equal(404));",
        "let j; try { j = pm.response.json(); } catch(e) { j = null; }",
        f"pm.test('[{case_id}] NF: grpc code 5 (NOT_FOUND)', () => pm.expect(j && j.code, JSON.stringify(j)).to.equal(5));",
    ]


def allow_asserts(case_id):
    """ALLOW — авторизованный субъект НЕ получает 403/401 (200/400/404/409 — на
    усмотрение downstream-валидации; важно лишь отсутствие authz-отказа)."""
    return [
        f"pm.test('[{case_id}] ALLOW: not 403 PermissionDenied', () => pm.expect(pm.response.code, 'unexpected 403 with body: ' + pm.response.text()).to.not.equal(403));",
        f"pm.test('[{case_id}] ALLOW: not 401 Unauthenticated', () => pm.expect(pm.response.code, 'unexpected 401 with body: ' + pm.response.text()).to.not.equal(401));",
    ]


def list_allow_asserts(case_id):
    """List субъектом, имеющим ГРАНТ на project (tier viewer/editor/admin или v_list).

    `/List` дочерних ресурсов гейтится verb-relation'ом `v_list`, который РАЗВЯЗАН
    от tier (anti-#241: editor/viewer/admin НЕ имплицируют v_list). Поэтому субъект
    с tier-грантом, но без явного v_list, корректно получает 403 «lacks relation
    v_list» — это by-design read-gating, а не отказ доступа к project'у. Субъект же с
    v_list (или у кого list-filter резолвит viewer на сами ресурсы) получает
    200 + отфильтрованный по своему гранту список.

    Обе ветки безопасны (свой project → утечки чужих ресурсов нет), поэтому
    допускаем 200 ИЛИ 403; 401 (потерянная аутентификация) — fail."""
    return [
        f"pm.test('[{case_id}] LIST grant: 200 (v_list/filtered) OR 403 (lacks v_list)', () => "
        f"pm.expect(pm.response.code, 'expected 200 or 403, body: ' + pm.response.text()).to.be.oneOf([200, 403]));",
    ]


def list_deny_asserts(case_id, list_key):
    """List без доступа → 403 (gated List RPC) ИЛИ 200 + ПУСТОЙ список (scope-filtered).
    200 + непустой список чужого project'а = LEAK (валит тест)."""
    return [
        "let j; try { j = pm.response.json(); } catch(e) { j = null; }",
        (
            f"pm.test('[{case_id}] LIST no-access: 403 OR 200+empty (no leak)', () => {{\n"
            "  const code = pm.response.code;\n"
            "  if (code === 403) return;\n"
            "  pm.expect(code, 'expected 403 or 200, body: ' + pm.response.text()).to.equal(200);\n"
            f"  const arr = (j && j['{list_key}']) || [];\n"
            "  pm.expect(arr.length, 'no-access List must be scope-filtered to EMPTY (LEAK!): ' + pm.response.text()).to.equal(0);\n"
            "});"
        ),
    ]


def emit(case_id_prefix, title, scope, method, path, body, subject, mode="gate", list_key=None):
    """mode:
        gate — стандартный ALLOW/DENY по EXPECT[scope][code] (Create / admin-only).
        list — scope-filtered List: ANON→401; иначе has-access→200, no-access→403|200+empty.
        nf   — object-scoped `/Get` на garbage id → 404 (existence-hiding); ANON→401.
        deny — object-scoped `/Update|/Delete` на garbage id → 403; ANON→401.
    """
    code, label, auth = subject
    cid = f"AUTHZ-{case_id_prefix}-{code}"
    if mode == "list":
        if code == "ANON":
            decision, asserts = "UNAUTH", unauth_asserts(cid)
        else:
            access = EXPECT[scope][code]
            if access == "ALLOW":
                decision, asserts = "LIST-ALLOW", list_allow_asserts(cid)
            else:
                decision, asserts = "LIST-DENY", list_deny_asserts(cid, list_key)
    elif mode == "nf":
        decision = "UNAUTH" if code == "ANON" else "NF"
        asserts = unauth_asserts(cid) if code == "ANON" else notfound_asserts(cid)
    elif mode == "deny":
        decision = "UNAUTH" if code == "ANON" else "DENY"
        asserts = unauth_asserts(cid) if code == "ANON" else deny_asserts(cid)
    else:  # gate
        decision = EXPECT[scope][code]
        if decision == "DENY":
            asserts = unauth_asserts(cid) if code == "ANON" else deny_asserts(cid)
        else:
            asserts = allow_asserts(cid)

    is_pos = decision in ("ALLOW", "LIST-ALLOW", "NF", "LIST-DENY")
    CASES.append(Case(
        id=cid,
        title=f"[{decision}] {title} as {label} ({scope})",
        classes=["AUTHZ", "POS" if is_pos else "NEG"],
        priority="P1",
        steps=[Step(name=method.lower(), method=method, path=path, body=body, auth=auth, test_script=asserts)],
    ))


# ---------------------------------------------------------------------------
# RESOURCES — определение CRUD-эндпоинтов VPC
# ---------------------------------------------------------------------------

# Для Get/Update/Delete используем well-formed-но-несуществующий id: prefix `enp`
# распознается как валидный (api-gateway не отдает 400 InvalidArgument), длина 20 →
# id проходит формат-валидацию gateway'я и доходит до FGA Check → deny.
#   `/Get`            → existence-hiding 404 (NF).
#   `/Update|/Delete` → 403 (мутация-deny, existence НЕ скрывается).
GARBAGE_ID = "enpnonexistent000001"


def define_resource_cases(resource_name, plural, create_body_extra=None, supports_update=True):
    """Генерирует authz-проверки для одного project-scoped VPC ресурса."""
    create_body_extra = create_body_extra or {}
    plural_path = f"/vpc/v1/{plural}"

    for subj in SUBJECTS:
        # === Create в own project A1 (editor-scope) ===
        body_own = {"projectId": "{{projectA1Id}}", "name": f"authz-{resource_name}-{subj[0].lower()}-own-{{{{runId}}}}", **create_body_extra}
        emit(f"{resource_name.upper()}-CR-OWN", f"Create {resource_name} в project-A1", "project-A1",
             "POST", plural_path, body_own, subj, mode="gate")

        # === Create в cross-account project B1 ===
        body_cross = {"projectId": "{{projectB1Id}}", "name": f"authz-{resource_name}-{subj[0].lower()}-cross-{{{{runId}}}}", **create_body_extra}
        emit(f"{resource_name.upper()}-CR-CROSS", f"Create {resource_name} в project-B1 (cross-account)", "project-B1",
             "POST", plural_path, body_cross, subj, mode="gate")

        # === List в own project (scope-filtered) ===
        emit(f"{resource_name.upper()}-LS-OWN", f"List {plural} в project-A1", "project-A1",
             "GET", f"{plural_path}?projectId={{{{projectA1Id}}}}", None, subj, mode="list", list_key=plural)

        # === List в cross-account project (scope-filtered) ===
        emit(f"{resource_name.upper()}-LS-CROSS", f"List {plural} в project-B1 (cross-account)", "project-B1",
             "GET", f"{plural_path}?projectId={{{{projectB1Id}}}}", None, subj, mode="list", list_key=plural)

        # === Get garbage-id → existence-hiding 404 ===
        emit(f"{resource_name.upper()}-GT-OWN", f"Get {resource_name} (well-formed nonexistent id)", "project-A1",
             "GET", f"{plural_path}/{GARBAGE_ID}", None, subj, mode="nf")

        if supports_update:
            # === Update garbage-id → mutation-deny 403 ===
            emit(f"{resource_name.upper()}-UP-OWN", f"Update {resource_name} (well-formed nonexistent id)", "project-A1",
                 "PATCH", f"{plural_path}/{GARBAGE_ID}", {"name": "x"}, subj, mode="deny")

        # === Delete garbage-id → mutation-deny 403 ===
        emit(f"{resource_name.upper()}-DL-OWN", f"Delete {resource_name} (well-formed nonexistent id)", "project-A1",
             "DELETE", f"{plural_path}/{GARBAGE_ID}", None, subj, mode="deny")


# Network
define_resource_cases("network", "networks")
# Subnet — body requires networkId + zoneId
define_resource_cases("subnet", "subnets", create_body_extra={
    "networkId": "{{seedNetworkA1Id}}", "zoneId": "{{zoneA}}", "v4CidrBlocks": ["10.99.0.0/16"]
})
# Address — project-level w/ external IPv4 spec
define_resource_cases("address", "addresses", create_body_extra={
    "externalIpv4AddressSpec": {"zoneId": "{{zoneA}}"}
})
# RouteTable
define_resource_cases("route-table", "routeTables", create_body_extra={
    "networkId": "{{seedNetworkA1Id}}"
})
# SecurityGroup
define_resource_cases("security-group", "securityGroups", create_body_extra={
    "networkId": "{{seedNetworkA1Id}}"
})
# Gateway
define_resource_cases("gateway", "gateways", create_body_extra={
    "sharedEgressGateway": {}
})
# NetworkInterface
define_resource_cases("nic", "networkInterfaces", create_body_extra={
    "subnetId": "{{seedNetworkA1Id}}"
})


# ---------------------------------------------------------------------------
# AddressPool — admin-only (cluster system_admin); все 6 субъектов DENY
# ---------------------------------------------------------------------------

APL_GARBAGE_ID = "aplnonexistent000001"

for subj in SUBJECTS:
    # Create AddressPool — admin-only: каждый аутентифицированный без system_admin → 403.
    emit("APL-CR", "Create AddressPool (admin-only)", "addresspool-admin-only",
         "POST", "/vpc/v1/addressPools",
         {"name": f"authz-apl-{subj[0].lower()}-{{{{runId}}}}",
          "kind": "EXTERNAL_PUBLIC",
          "zoneId": "{{zoneA}}",
          "v4CidrBlocks": ["198.51.100.0/24"]}, subj, mode="gate")
    # Update/Delete nonexistent AddressPool — admin-only мутация: non-admin → 403.
    emit("APL-UP", "Update AddressPool (admin-only, nonexistent id)", "addresspool-admin-only",
         "PATCH", f"/vpc/v1/addressPools/{APL_GARBAGE_ID}", {"name": "x"}, subj, mode="gate")
    emit("APL-DL", "Delete AddressPool (admin-only, nonexistent id)", "addresspool-admin-only",
         "DELETE", f"/vpc/v1/addressPools/{APL_GARBAGE_ID}", None, subj, mode="gate")


# ---------------------------------------------------------------------------
# Cross-domain / data-leak cases
# ---------------------------------------------------------------------------

# AddressPool.List на public endpoint — admin-only (cluster system_admin): non-admin
# субъект НЕ может перечислить инфраструктурные пулы (anti data-leak). 403 у всех.
for subj in SUBJECTS:
    emit("DATA-LEAK-APL-LS", "AddressPool.List на public listener (infra data-leak guard)",
         "addresspool-admin-only", "GET", "/vpc/v1/addressPools", None, subj, mode="gate")

# Create Subnet в project-A1 со ссылкой на network из cross-account project-B1.
# Authz-граница здесь — право создавать subnet в project-A1 (editor-scope): субъекты
# без A1-доступа → 403; с доступом → authz пропускает (cross-account network-ref
# отбивается peer-validation downstream, не в этой суите). Проверяем именно
# authz-границу project-A1 editor-scope.
for subj in SUBJECTS:
    emit("CD-SUBNET-XACCT", "Create Subnet ссылающийся на network из cross-account project",
         "project-A1", "POST", "/vpc/v1/subnets",
         {"projectId":"{{projectA1Id}}","name": f"cd-{subj[0].lower()}-{{{{runId}}}}",
          "networkId":"{{seedNetworkB1Id}}","zoneId":"{{zoneA}}","v4CidrBlocks":["10.88.0.0/16"]}, subj, mode="gate")
