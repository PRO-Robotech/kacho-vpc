# Намеренные поведенческие решения (и где они расходятся с verbatim-YC)

Это **не баги** и **не задачи** — осознанные решения, которые могут удивить ревьюера:
либо мы **расходимся** с reference Yandex Cloud VPC API (с обоснованием), либо
**deliberately не делаем** того, что напрашивается. Цель файла — чтобы это не «фиксили»
по второму разу.

**Сюда НЕ пишем** то, что просто корректно реализует verbatim-YC контракт — это не
«решение», а спека (см. `../../CLAUDE.md` §2, `04-api-surface.md`, `05-database.md`).
Например: VPC-ресурсы folder-scoped без `cloud_id`/`organization_id`; permissive `NameVPC` —
всё это **и есть** YC, расхождения тут нет, поэтому в файле этого нет.

Баги/задачи (в т.ч. подтверждённые probe'ами расхождения, которые решили выровнять) —
GitHub Issues (`PRO-Robotech/kacho-vpc` / `kacho-api-gateway`), см. `../../CLAUDE.md` §14.4.

> **Probe YC 2026-05-11** (через `yc` CLI на реальном YC): проверены прежние пункты —
> «мутации несуществующего → sync 404 не async Operation» **подтверждено** (YC: `update`/`delete`
> на `enp...`-id несуществующего ресурса → sync `5 NotFound "Network <id> not found"`, не Operation) →
> это **не расхождение**, пункт удалён; «`Address.GetByValue` несуществующего IP → `NotFound`»
> **подтверждено** (YC: `NotFound "Address <ip> not found"`) → не расхождение, удалён;
> `OperationService.Get` bad-id — **подтверждено расхождение** (см. ниже + issue в `kacho-api-gateway`).
> Заодно всплыло **новое** расхождение (id-syntax validation) — см. ниже, issue в `kacho-vpc`.

## 1. REST-пути неоднородны — НЕ нормализовать

kebab у custom-методов (`:add-cidr-blocks`, `:move`), snake у child-list (`security_groups`,
`route_tables`), camel у top-level (`routeTables`, `securityGroups`, `addressPools`),
`/operations/{id}` без `/vpc/v1/`-префикса, PrivateEndpoint на `/endpoints`. Всё проистекает
из `google.api.http`-аннотаций в `.proto` (`kacho-proto`) — это калька с YC API surface, не
handwritten-выбор. Если кому-то покажется «надо причесать» — **нельзя**: сломает verbatim-YC.
Если найдётся конкретный путь, не совпадающий с YC — это баг в аннотации в `kacho-proto` (issue туда).
Карта путей — `04-api-surface.md`.

## 2. *(выровнено)* `OperationService.Get`/`Cancel` с bad id — `kacho-api-gateway#2`

Было: api-gateway `opsproxy` на любой неотмаршрутизированный id отвечал
`400 "operation_id has unknown prefix"`. Стало (verbatim YC, probe 2026-05-11): malformed id →
`InvalidArgument "invalid operation id '<X>'"`; well-formed id (20 симв., известный prefix, но
бэкенд не подключён) → `NotFound "Operation <X> not found"`; id с prefix домена с подключённым
бэкендом → роутится туда. Закрыто в `PRO-Robotech/kacho-api-gateway#2` (`internal/opsproxy/proxy.go`).

## 3. `InternalCloudService.SetPoolSelector` не проверяет существование `cloud_id` — намеренно

Idempotent upsert; кросс-DB FK между `kacho_vpc` и `kacho_resource_manager` нет. «Висячий»
selector для несуществующего/удалённого cloud безвреден — в IPAM-cascade (Step 3) он просто
не зарезолвится, потому что не будет живых `folder→cloud`-связей, указывающих на этот cloud.
Реальная валидация потребовала бы `CloudService.Exists` RPC на resource-manager — cross-repo
фича, не делаем. (kacho-only RPC, в YC аналога нет — выравнивать не с чем.) Proto-комментарий
(`kacho-proto/.../internal_cloud_service.proto`) это отражает.

## 4. Остаточные расхождения после kacho-vpc#10 (probe YC 2026-05-11)

`kacho-vpc#10` выровнял пачку расхождений из differential-прогона против реального YC (Move
в текущий folder → `400 "Illegal argument Destination folder is the same as the source"`;
Subnet.Update с `v4CidrBlocks` в mask → `200`, не `400`; Subnet IPv4-префикс `>/28` →
`400 "Illegal argument Invalid network prefix /N"`; Subnet.Relocate → всегда `400
FailedPrecondition "Invalid subnet state"`; not-found тексты RouteTable → `"Route table <id>
not found"`, SecurityGroup → `"Security group SecurityGroup.Id(value=<id>) not found"`;
SG.UpdateRule малформированного rule_id → sync `400 "Invalid rule id <ruleId>"`). Осталось
**сознательно не выравнивать** (документировано здесь, не issue):

- **Тело ошибки JSON-transcoding — plain-text у YC, JSON `{code,message}` у нас.** На неверный
  тип JSON-поля (`description`=число, `labels`=строка, oneof `address_spec` задан дважды) YC
  отдаёт `text/plain` (`description: invalid value 12345 for type TYPE_STRING`), наш api-gateway —
  стандартный grpc-gateway error-handler с JSON. Это поведение runtime-библиотеки grpc-gateway;
  кастомный error-handler ради побайтного совпадения тела не делаем. Кейсы `*-CR-VAL-DESC-INT-TYPE`/
  `*-CR-VAL-LABELS-STRING-TYPE`/`ADR-CR-VAL-BOTH-SPEC` — defensive (фиксируют `400` + непустое тело).

- **Пустое repeated-поле в List-ответе — YC опускает, наш api-gateway отдаёт `[]`.** `GET
  /vpc/v1/networks?folderId=<пустой>` → YC `{}`, мы `{"networks":[]}` (а также `nextPageToken:""`).
  Это `EmitUnpopulated`-настройка grpc-gateway marshaller'а; смена затронула бы все ответы (напр.
  `done:false` в Operation), blast radius неоправдан. Кейсы `NET-LST-*` — defensive (`j.networks || []`).

- **Subnet.Update с `v4CidrBlocks` в mask: YC меняет CIDR, мы — no-op.** Принимаем запрос (`200`),
  но `repo.Update` CIDR-колонки не перезаписывает (defensive depth — см. `12.*` в CLAUDE.md). Менять
  CIDR существующей подсети в control-plane без data-plane смысла мало; реальное изменение CIDR — через
  `:add-cidr-blocks`/`:remove-cidr-blocks`. Кейс `SUB-UPD-STATE-IMMUTABLE-CIDR` проверяет только `200`.

- **`zones` на стенде содержит лишнюю `ru-central1-c`.** `kacho-deploy/ci/seed.sh` сидит зоны
  `ru-central1-{a,b,c,d}` (для покрытия internal-pool admin-кейсов, которым нужна 3-я зона);
  у реального YC зоны `{a,b,d}` (Subnet.Create на `ru-central1-c` → `400 "Illegal argument
  zone_id"`). Поскольку internal-* RPC в YC нет, parity-кейсы публичного API не используют
  `ru-central1-c` (`pairwise_subnet_pack` переведён на `ru-central1-d`). Расхождение — только
  seed-данные стенда, не код. Доп. нюанс: для «похожего-на-zone-id, но несуществующего» значения
  (`ru-central1-c`) YC говорит `"Illegal argument zone_id"`, а наш `validateZoneID` — `"unknown
  zone id '<x>'"` (одинаково для всех неизвестных zone_id); ни один кейс этот путь не дёргает.

---

## Подтверждённые расхождения, вынесенные в issues (здесь — только указатель)

(пока пусто. Закрытые: `kacho-api-gateway#2` — `OperationService.Get` bad-id → `InvalidArgument
"invalid operation id"` / well-formed-unroutable → `NotFound`; `kacho-vpc#7` — malformed /
нераспознанный resource-id → sync `InvalidArgument "invalid <res> id '<X>'"` через
`corevalidate.ResourceID`, см. `06-conventions.md` gotcha #1; `kacho-vpc#8` — синхронная валидация
parent-existence / name-uniqueness / CIDR-overlap / FK / zone в мутирующих RPC; `kacho-vpc#9` —
`GatewayService.Create` форма (`sharedEgressGatewaySpec`, обязательный gateway-type oneof, без
name-uniqueness); `kacho-vpc#10` — см. §2/§4 выше.)
