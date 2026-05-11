"""Case-set для InternalRegionService + InternalZoneService (kacho-only admin RPC).

Region/Zone — глобальные infrastructure-ресурсы, не привязаны к folder. RPC
проброшены через api-gateway cluster-internal mux на /vpc/v1/regions[/{region_id}]
и /vpc/v1/zones[/{zone_id}] — НЕ verbatim-YC, возвращают ресурсы ПРЯМО (не Operation).

⚠️ DO NOT trogat seeded `ru-central1` region / `ru-central1-{a,b,c,d}` zones.
Тесты создают только runId-суффиксованные throwaway region/zone и убирают за собой.
runId — [a-z0-9]{10}, поэтому id вида `r{{runId}}` / `z{{runId}}a` валидны.
"""

CASES = []

REGIONS = "/vpc/v1/regions"
ZONES = "/vpc/v1/zones"


# ---------------------------------------------------------------------------
# Region CRUD happy path
# ---------------------------------------------------------------------------

CASES.append(Case(
    id="RGN-CR-CRUD-OK",
    title="Region Create → Get → List-includes → Update(name) → Delete → Get-404",
    classes=["CRUD", "CONF"], priority="P0",
    steps=[
        Step(name="create", method="POST", path=REGIONS,
             body={"id": "r{{runId}}", "name": "Region {{runId}}"},
             test_script=[*assert_status(200),
                          "const j = pm.response.json();",
                          "pm.test('id echoed', () => pm.expect(j.id).to.eql('r' + pm.environment.get('runId')));",
                          "pm.test('name echoed', () => pm.expect(j.name).to.eql('Region ' + pm.environment.get('runId')));",
                          "pm.test('createdAt present', () => pm.expect(j.createdAt).to.be.a('string'));"]),
        Step(name="get", method="GET", path=REGIONS + "/r{{runId}}",
             test_script=[*assert_status(200),
                          "pm.test('id matches', () => pm.expect(pm.response.json().id).to.eql('r' + pm.environment.get('runId')));"]),
        Step(name="list-includes", method="GET", path=REGIONS,
             test_script=[*assert_status(200),
                          "const regions = pm.response.json().regions || [];",
                          "pm.test('regions array', () => pm.expect(regions).to.be.an('array'));",
                          "pm.test('list contains created', () => pm.expect(regions.map(r => r.id)).to.include('r' + pm.environment.get('runId')));"]),
        Step(name="update", method="PATCH", path=REGIONS + "/r{{runId}}",
             body={"name": "Renamed {{runId}}"},
             test_script=[*assert_status(200),
                          "pm.test('name updated', () => pm.expect(pm.response.json().name).to.eql('Renamed ' + pm.environment.get('runId')));"]),
        Step(name="delete", method="DELETE", path=REGIONS + "/r{{runId}}",
             test_script=[*assert_status(200),
                          "pm.test('delete returns obj', () => pm.expect(pm.response.json()).to.be.an('object'));"]),
        Step(name="get-404", method="GET", path=REGIONS + "/r{{runId}}",
             test_script=[*assert_status(404), *assert_grpc_code(5, "NOT_FOUND")]),
    ],
))

CASES.append(Case(
    id="RGN-LST-CRUD-OK",
    title="List regions → seeded ru-central1 присутствует",
    classes=["CRUD"], priority="P1",
    steps=[
        Step(name="list", method="GET", path=REGIONS,
             test_script=[*assert_status(200),
                          "const ids = (pm.response.json().regions || []).map(r => r.id);",
                          "pm.test('seeded ru-central1 present', () => pm.expect(ids).to.include('ru-central1'));"]),
        Step(name="list-paged", method="GET", path=REGIONS + "?pageSize=1",
             test_script=[*assert_status(200),
                          "pm.test('at most 1 region', () => pm.expect((pm.response.json().regions || []).length).to.be.at.most(1));"]),
    ],
))

CASES.append(Case(
    id="RGN-CR-VAL-MISSING-ID",
    title="Region Create без id → 400 InvalidArgument",
    classes=["VAL"], priority="P0",
    steps=[
        Step(name="cr-no-id", method="POST", path=REGIONS,
             body={"name": "no id region"},
             test_script=[*assert_status(400), *assert_grpc_code(3, "INVALID_ARGUMENT")]),
    ],
))

CASES.append(Case(
    id="RGN-GET-NEG-NF",
    title="Get несуществующего region → 404 NOT_FOUND",
    classes=["NEG"], priority="P1",
    steps=[
        Step(name="get-garbage", method="GET", path=REGIONS + "/nonexistent-region-xyz",
             test_script=[*assert_status(404), *assert_grpc_code(5, "NOT_FOUND")]),
    ],
))

CASES.append(Case(
    id="RGN-DEL-NEG-NF",
    title="Delete несуществующего region → 404 NOT_FOUND",
    classes=["NEG"], priority="P1",
    steps=[
        Step(name="del-garbage", method="DELETE", path=REGIONS + "/nonexistent-region-xyz",
             test_script=[*assert_status(404), *assert_grpc_code(5, "NOT_FOUND")]),
    ],
))

CASES.append(Case(
    id="RGN-DEL-NEG-HAS-ZONES",
    title="Delete region с зависимыми zones → FailedPrecondition (не пустой)",
    classes=["NEG", "STATE", "CONF"], priority="P0",
    steps=[
        Step(name="cr-region", method="POST", path=REGIONS,
             body={"id": "r{{runId}}", "name": "Region with zones"},
             test_script=[*assert_status(200)]),
        Step(name="cr-zone", method="POST", path=ZONES,
             body={"id": "z{{runId}}a", "regionId": "r{{runId}}", "name": "Zone A"},
             test_script=[*assert_status(200)]),
        Step(name="del-region-blocked", method="DELETE", path=REGIONS + "/r{{runId}}",
             test_script=[*assert_status(400), *assert_grpc_code(9, "FAILED_PRECONDITION"),
                          "pm.test('message mentions not empty', () => pm.expect(pm.response.json().message.toLowerCase()).to.include('not empty'));"]),
        # cleanup в правильном порядке: сначала zone, потом region.
        Step(name="del-zone", method="DELETE", path=ZONES + "/z{{runId}}a",
             test_script=[*assert_status(200)]),
        Step(name="del-region", method="DELETE", path=REGIONS + "/r{{runId}}",
             test_script=[*assert_status(200)]),
    ],
))

# Защита от seeded ресурса: проверяем что seeded ru-central1 нельзя удалить (есть зоны).
CASES.append(Case(
    id="RGN-DEL-SEEDED-PROTECTED",
    title="Delete seeded ru-central1 → FailedPrecondition (есть seeded zones) — seeded не повреждается",
    classes=["CONF", "NEG"], priority="P0",
    steps=[
        Step(name="del-seeded", method="DELETE", path=REGIONS + "/ru-central1",
             test_script=[*assert_status(400), *assert_grpc_code(9, "FAILED_PRECONDITION")]),
        Step(name="verify-still-there", method="GET", path=REGIONS + "/ru-central1",
             test_script=[*assert_status(200),
                          "pm.test('ru-central1 intact', () => pm.expect(pm.response.json().id).to.eql('ru-central1'));"]),
    ],
))


# ---------------------------------------------------------------------------
# Zone CRUD happy path
# ---------------------------------------------------------------------------

CASES.append(Case(
    id="ZON-CR-CRUD-OK",
    title="Zone Create → Get → List-includes → Update(name) → Delete → Get-404",
    classes=["CRUD", "CONF"], priority="P0",
    steps=[
        # Своя region для изоляции (чтобы не зависеть от seeded ru-central1).
        Step(name="cr-region", method="POST", path=REGIONS,
             body={"id": "r{{runId}}", "name": "Region for zone test"},
             test_script=[*assert_status(200)]),
        Step(name="create", method="POST", path=ZONES,
             body={"id": "z{{runId}}a", "regionId": "r{{runId}}", "name": "Zone {{runId}} A"},
             test_script=[*assert_status(200),
                          "const j = pm.response.json();",
                          "pm.test('id echoed', () => pm.expect(j.id).to.eql('z' + pm.environment.get('runId') + 'a'));",
                          "pm.test('regionId echoed', () => pm.expect(j.regionId).to.eql('r' + pm.environment.get('runId')));",
                          "pm.test('createdAt present', () => pm.expect(j.createdAt).to.be.a('string'));"]),
        Step(name="get", method="GET", path=ZONES + "/z{{runId}}a",
             test_script=[*assert_status(200),
                          "pm.test('id matches', () => pm.expect(pm.response.json().id).to.eql('z' + pm.environment.get('runId') + 'a'));"]),
        Step(name="list-includes", method="GET", path=ZONES + "?regionId=r{{runId}}",
             test_script=[*assert_status(200),
                          "const zones = pm.response.json().zones || [];",
                          "pm.test('zones array', () => pm.expect(zones).to.be.an('array'));",
                          "pm.test('list contains created', () => pm.expect(zones.map(z => z.id)).to.include('z' + pm.environment.get('runId') + 'a'));"]),
        Step(name="update", method="PATCH", path=ZONES + "/z{{runId}}a",
             body={"name": "Zone Renamed {{runId}}"},
             test_script=[*assert_status(200),
                          "pm.test('name updated', () => pm.expect(pm.response.json().name).to.eql('Zone Renamed ' + pm.environment.get('runId')));"]),
        Step(name="delete", method="DELETE", path=ZONES + "/z{{runId}}a",
             test_script=[*assert_status(200)]),
        Step(name="get-404", method="GET", path=ZONES + "/z{{runId}}a",
             test_script=[*assert_status(404), *assert_grpc_code(5, "NOT_FOUND")]),
        # cleanup region.
        Step(name="del-region", method="DELETE", path=REGIONS + "/r{{runId}}",
             test_script=[*assert_status(200)]),
    ],
))

CASES.append(Case(
    id="ZON-LST-CRUD-OK",
    title="List zones → seeded ru-central1-a присутствует; фильтр по regionId работает",
    classes=["CRUD"], priority="P1",
    steps=[
        Step(name="list-all", method="GET", path=ZONES,
             test_script=[*assert_status(200),
                          "const ids = (pm.response.json().zones || []).map(z => z.id);",
                          "pm.test('seeded ru-central1-a present', () => pm.expect(ids).to.include('ru-central1-a'));"]),
        Step(name="list-by-region", method="GET", path=ZONES + "?regionId=ru-central1",
             test_script=[*assert_status(200),
                          "const zones = pm.response.json().zones || [];",
                          "pm.test('all zones belong to ru-central1', () => zones.forEach(z => pm.expect(z.regionId).to.eql('ru-central1')));",
                          "pm.test('includes ru-central1-a', () => pm.expect(zones.map(z => z.id)).to.include('ru-central1-a'));"]),
    ],
))

CASES.append(Case(
    id="ZON-CR-NEG-BAD-REGION",
    title="Zone Create с несуществующим regionId → 404 NOT_FOUND ('Region ... not found')",
    classes=["NEG", "CONF"], priority="P0",
    steps=[
        Step(name="cr-bad-region", method="POST", path=ZONES,
             body={"id": "z{{runId}}bad", "regionId": "nonexistent-region-{{runId}}", "name": "x"},
             test_script=[*assert_status(404), *assert_grpc_code(5, "NOT_FOUND"),
                          "pm.test('verbatim Region ... not found', () => pm.expect(pm.response.json().message).to.match(/^Region .* not found$/));"]),
    ],
))

CASES.append(Case(
    id="ZON-CR-VAL-MISSING-ID",
    title="Zone Create без id → 400 InvalidArgument",
    classes=["VAL"], priority="P0",
    steps=[
        Step(name="cr-no-id", method="POST", path=ZONES,
             body={"regionId": "ru-central1", "name": "x"},
             test_script=[*assert_status(400), *assert_grpc_code(3, "INVALID_ARGUMENT")]),
    ],
))

CASES.append(Case(
    id="ZON-CR-VAL-MISSING-REGION",
    title="Zone Create без regionId → 400 InvalidArgument",
    classes=["VAL"], priority="P0",
    steps=[
        Step(name="cr-no-region", method="POST", path=ZONES,
             body={"id": "z{{runId}}nr", "name": "x"},
             test_script=[*assert_status(400), *assert_grpc_code(3, "INVALID_ARGUMENT")]),
    ],
))

CASES.append(Case(
    id="ZON-GET-NEG-NF",
    title="Get несуществующей zone → 404 NOT_FOUND",
    classes=["NEG"], priority="P1",
    steps=[
        Step(name="get-garbage", method="GET", path=ZONES + "/nonexistent-zone-xyz",
             test_script=[*assert_status(404), *assert_grpc_code(5, "NOT_FOUND")]),
    ],
))

CASES.append(Case(
    id="ZON-DEL-NEG-NF",
    title="Delete несуществующей zone → 404 NOT_FOUND",
    classes=["NEG"], priority="P1",
    steps=[
        Step(name="del-garbage", method="DELETE", path=ZONES + "/nonexistent-zone-xyz",
             test_script=[*assert_status(404), *assert_grpc_code(5, "NOT_FOUND")]),
    ],
))

CASES.append(Case(
    id="ZON-DEL-NEG-HAS-POOL",
    title="Delete zone с зависимым AddressPool → FailedPrecondition (не пустая)",
    classes=["NEG", "STATE", "CONF"], priority="P1",
    steps=[
        Step(name="cr-region", method="POST", path=REGIONS,
             body={"id": "r{{runId}}", "name": "Region for pool-zone test"},
             test_script=[*assert_status(200)]),
        Step(name="cr-zone", method="POST", path=ZONES,
             body={"id": "z{{runId}}p", "regionId": "r{{runId}}", "name": "Zone with pool"},
             test_script=[*assert_status(200)]),
        Step(name="cr-pool", method="POST", path="/vpc/v1/addressPools",
             body={"name": "zon-dep-pool-{{runId}}", "kind": "EXTERNAL_TEST",
                   "zoneId": "z{{runId}}p", "cidrBlocks": ["203.0.113.0/24"]},
             test_script=[*assert_status(200), *save_from_response("j.id", "zonDepPoolId")]),
        Step(name="del-zone-blocked", method="DELETE", path=ZONES + "/z{{runId}}p",
             test_script=[*assert_status(400), *assert_grpc_code(9, "FAILED_PRECONDITION"),
                          "pm.test('message mentions not empty', () => pm.expect(pm.response.json().message.toLowerCase()).to.include('not empty'));"]),
        # cleanup: pool → zone → region.
        Step(name="del-pool", method="DELETE", path="/vpc/v1/addressPools/{{zonDepPoolId}}",
             test_script=[*assert_status(200)]),
        Step(name="del-zone", method="DELETE", path=ZONES + "/z{{runId}}p",
             test_script=[*assert_status(200)]),
        Step(name="del-region", method="DELETE", path=REGIONS + "/r{{runId}}",
             test_script=[*assert_status(200)]),
    ],
))
