"""Case-set authz-deny для kacho-vpc (KAC-122).

Проверяет default-deny matrix для 6 классов субъектов на каждом публичном CRUD
каждого VPC-ресурса. Источник истины матрицы —
`docs/superpowers/specs/2026-05-19-authz-default-deny-matrix-newman-design.md`.

Pre-conditions: `tests/authz-fixtures/setup.sh` должен заранее создать фикстуры
(accounts, projects, users, bindings, seed networks). Setup патчит env-файл, добавляя:
  - jwt*               : 5 Bearer-токенов (no-bindings / proj-admin-a1 / account-admin-a/b / invitee)
  - accountAId / Bid
  - projectA1Id / A2Id / B1Id
  - seedNetworkA1Id / seedNetworkB1Id

Decision per (resource, op, subject):
  - DENY  → HTTP 403 + grpc-code 7 (PERMISSION_DENIED) + body содержит "permission denied"
  - ALLOW → HTTP code != 403 (200/400/404 — приемлемо, важно лишь отсутствие 403)

Helpers Case/Step/assert_status инжектятся через gen.py namespace.
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

# scope-class → subject-code → expected ('DENY'/'ALLOW'). Источник истины — design §6.
EXPECT = {
    "project-A1":          {"ANON":"DENY","NOB":"DENY","PA1":"ALLOW","AAA":"ALLOW","AAB":"DENY", "INV":"ALLOW"},
    "project-B1":          {"ANON":"DENY","NOB":"DENY","PA1":"DENY", "AAA":"DENY", "AAB":"ALLOW","INV":"ALLOW"},
    "addresspool-mutate":  {"ANON":"DENY","NOB":"DENY","PA1":"DENY", "AAA":"DENY", "AAB":"DENY", "INV":"DENY"},
}


def deny_asserts(case_id):
    return [
        f"pm.test('[{case_id}] DENY: status 403', () => pm.expect(pm.response.code, JSON.stringify(pm.response.text())).to.equal(403));",
        "let j; try { j = pm.response.json(); } catch(e) { j = null; }",
        f"pm.test('[{case_id}] DENY: grpc code 7 (PERMISSION_DENIED)', () => pm.expect(j && j.code, JSON.stringify(j)).to.equal(7));",
        f"pm.test('[{case_id}] DENY: message contains permission denied', () => pm.expect((j && j.message || '').toLowerCase()).to.contain('permission denied'));",
    ]


def allow_asserts(case_id):
    return [
        f"pm.test('[{case_id}] ALLOW: not 403 PermissionDenied', () => pm.expect(pm.response.code, 'unexpected 403 with body: ' + pm.response.text()).to.not.equal(403));",
        # дополнительно — не должно быть Unauthenticated (16). Если так — стенд не настроен (нет user) → fail с понятным сообщением.
        "let _j; try { _j = pm.response.json(); } catch(e) { _j = null; }",
        f"pm.test('[{case_id}] ALLOW: not Unauthenticated (16)', () => pm.expect(_j && _j.code, JSON.stringify(_j)).to.not.equal(16));",
    ]


def emit(case_id_prefix, title, scope, method, path, body, subject):
    code, label, auth = subject
    decision = EXPECT[scope][code]
    case_id = f"AUTHZ-{case_id_prefix}-{code}"
    asserts = deny_asserts(case_id) if decision == "DENY" else allow_asserts(case_id)
    CASES.append(Case(
        id=case_id,
        title=f"[{decision}] {title} as {label} ({scope})",
        classes=["AUTHZ", "NEG" if decision == "DENY" else "POS"],
        priority="P1",
        steps=[Step(name=method.lower(), method=method, path=path, body=body, auth=auth, test_script=asserts)],
    ))


# ---------------------------------------------------------------------------
# RESOURCES — определение CRUD-эндпоинтов VPC
# ---------------------------------------------------------------------------

# Per-resource: ((prefix, "name", scope-own, scope-cross, seedOwn, seedCross), create_body_extra)
# Формат паттернов CRUD — для VPC project-scoped ресурсов:
#   Create (POST /vpc/v1/<resource>, body has projectId)
#   List   (GET  /vpc/v1/<resource>?projectId=<X>)
#   Get    (GET  /vpc/v1/<resource>/<id>)
#   Update (PATCH /vpc/v1/<resource>/<id>)
#   Delete (DELETE /vpc/v1/<resource>/<id>)
# Для Get/Update/Delete — используем `garbageVpcId` как id (existence не нужно
# проверять; DENY возвращает 403 ДО repo, ALLOW возвращает NotFound (404) что нас устраивает).

GARBAGE_ID = "enpnonexistent000001"

# (resource, create_path, create_body_template, list_path_template, get_update_delete_path_template, name_field)
# create_body_template: f-string с {{projectId}} placeholder для project; "{{name}}-{{runId}}" suffix.
def define_resource_cases(resource_name, plural, create_body_extra=None, supports_update=True):
    """Генерирует CRUD-проверки для одного project-scoped VPC ресурса."""
    create_body_extra = create_body_extra or {}
    plural_path = f"/vpc/v1/{plural}"

    for subj in SUBJECTS:
        # === Create в own project A1 ===
        body_own = {"projectId": "{{projectA1Id}}", "name": f"authz-{resource_name}-{subj[0].lower()}-own-{{{{runId}}}}", **create_body_extra}
        emit(f"{resource_name.upper()}-CR-OWN", f"Create {resource_name} в project-A1", "project-A1",
             "POST", plural_path, body_own, subj)

        # === Create в cross-account project B1 ===
        body_cross = {"projectId": "{{projectB1Id}}", "name": f"authz-{resource_name}-{subj[0].lower()}-cross-{{{{runId}}}}", **create_body_extra}
        emit(f"{resource_name.upper()}-CR-CROSS", f"Create {resource_name} в project-B1 (cross-account)", "project-B1",
             "POST", plural_path, body_cross, subj)

        # === List в own project ===
        emit(f"{resource_name.upper()}-LS-OWN", f"List {plural} в project-A1", "project-A1",
             "GET", f"{plural_path}?projectId={{{{projectA1Id}}}}", None, subj)

        # === List в cross-account project ===
        emit(f"{resource_name.upper()}-LS-CROSS", f"List {plural} в project-B1 (cross-account)", "project-B1",
             "GET", f"{plural_path}?projectId={{{{projectB1Id}}}}", None, subj)

        # === Get garbage-id (DENY проверяется ДО repo) ===
        emit(f"{resource_name.upper()}-GT-OWN", f"Get {resource_name} (garbage id)", "project-A1",
             "GET", f"{plural_path}/{GARBAGE_ID}", None, subj)

        if supports_update:
            # === Update garbage-id ===
            emit(f"{resource_name.upper()}-UP-OWN", f"Update {resource_name} (garbage id)", "project-A1",
                 "PATCH", f"{plural_path}/{GARBAGE_ID}", {"name": "x"}, subj)

        # === Delete garbage-id ===
        emit(f"{resource_name.upper()}-DL-OWN", f"Delete {resource_name} (garbage id)", "project-A1",
             "DELETE", f"{plural_path}/{GARBAGE_ID}", None, subj)


# Network
define_resource_cases("network", "networks")
# Subnet — body requires networkId + zoneId
define_resource_cases("subnet", "subnets", create_body_extra={
    "networkId": "{{seedNetworkA1Id}}", "zoneId": "ru-central1-a", "v4CidrBlocks": ["10.99.0.0/16"]
})
# Address — folder-level w/ external IPv4 spec
define_resource_cases("address", "addresses", create_body_extra={
    "externalIpv4AddressSpec": {"zoneId": "ru-central1-a"}
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
# PrivateEndpoint
define_resource_cases("private-endpoint", "privateEndpoints", create_body_extra={
    "networkId": "{{seedNetworkA1Id}}",
    "addressSpec": {"subnetId": "{{seedNetworkA1Id}}"},
    "objectStorage": {},
})
# NetworkInterface (KAC-2)
define_resource_cases("nic", "networkInterfaces", create_body_extra={
    "subnetId": "{{seedNetworkA1Id}}"
})


# ---------------------------------------------------------------------------
# AddressPool — admin/internal, все 6 субъектов DENY на mutate
# ---------------------------------------------------------------------------

for subj in SUBJECTS:
    emit("APL-CR", "Create AddressPool", "addresspool-mutate",
         "POST", "/vpc/v1/addressPools",
         {"name": f"authz-apl-{subj[0].lower()}-{{{{runId}}}}",
          "kind": "EXTERNAL_PUBLIC",
          "zoneId": "ru-central1-a",
          "v4CidrBlocks": ["198.51.100.0/24"]}, subj)
    emit("APL-UP", "Update AddressPool (garbage id)", "addresspool-mutate",
         "PATCH", "/vpc/v1/addressPools/aplnonexistent00000", {"name": "x"}, subj)
    emit("APL-DL", "Delete AddressPool (garbage id)", "addresspool-mutate",
         "DELETE", "/vpc/v1/addressPools/aplnonexistent00000", None, subj)
