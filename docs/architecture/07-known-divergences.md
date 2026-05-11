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

## 2. `OperationService.Get`/`Cancel` с bad id — частично расходимся с YC *(issue в `kacho-api-gateway`)*

api-gateway `opsproxy` парсит первые 3 символа id для маршрутизации на нужный backend
(`enp...`/`e9b...` → kacho-vpc, `b1g...`/`bpf...` → resource-manager) и на **любой** id, который
не смаршрутизировал, возвращает `400 INVALID_ARGUMENT "operation_id has unknown prefix"`.

Поведение реального YC (probe 2026-05-11):
- malformed id (не похож на id — `not-a-real-operation-id`, `xyz`) → `InvalidArgument "invalid operation id '<X>'"`.
- well-formed id (20 симв., известный prefix, но операции нет) → `NotFound "Operation <X> not found"`.
- well-formed id с prefix чужого домена (`fhm...` — compute) → YC роутит туда и получает `NotFound "Operation <X> not found"`.

То есть: для malformed id мы совпадаем по коду (`InvalidArgument`), но текст другой; для
well-formed id с prefix без backend мы расходимся **по коду** (`400` vs YC `404`). Архитектурно
`400` обосновано (маршрутизатор не может вернуть `404`, не зная backend), но для parity лучше:
malformed → `InvalidArgument "invalid operation id '<X>'"`, well-formed-but-unroutable →
`NotFound "Operation <X> not found"`. Низкоприоритетно (реальные клиенты в это редко упираются).
→ **GitHub Issue `PRO-Robotech/kacho-api-gateway#2`**.

## 3. `InternalCloudService.SetPoolSelector` не проверяет существование `cloud_id` — намеренно

Idempotent upsert; кросс-DB FK между `kacho_vpc` и `kacho_resource_manager` нет. «Висячий»
selector для несуществующего/удалённого cloud безвреден — в IPAM-cascade (Step 3) он просто
не зарезолвится, потому что не будет живых `folder→cloud`-связей, указывающих на этот cloud.
Реальная валидация потребовала бы `CloudService.Exists` RPC на resource-manager — cross-repo
фича, не делаем. (kacho-only RPC, в YC аналога нет — выравнивать не с чем.) Proto-комментарий
(`kacho-proto/.../internal_cloud_service.proto`) это отражает.

---

## Подтверждённые расхождения, вынесенные в issues (здесь — только указатель)

- **Malformed / wrong-prefix resource id → мы `NotFound`, YC `InvalidArgument`.** Probe 2026-05-11:
  `yc vpc network update --id not-a-real-network-id` → `InvalidArgument "invalid network id 'X'"`;
  `--id xyz00000000000000000` (20 симв., prefix не реальный) → то же. Наш код (gotcha #1 в
  `06-conventions.md`, commit `ac61127`) **не** валидирует синтаксис id sync — идёт в `repo.Get` →
  `NotFound`. Это расхождение (исторически gotcha #1 утверждал обратное — вероятно YC изменил
  поведение после перехода с UUID на `enp...`-формат). Выравнивание затрагивает ~все RPC, берущие
  resource-id, + newman-кейсы, ассертящие «garbage id → 404». → **GitHub Issue `PRO-Robotech/kacho-vpc#7`**.
