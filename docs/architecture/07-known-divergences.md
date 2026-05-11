# Известные расхождения с verbatim-YC и спорные решения (by-design)

Это **не баги** и **не задачи** — осознанные решения, отличающиеся (или выглядящие
отличающимися) от reference Yandex Cloud VPC API, с обоснованием. Баги/задачи —
в GitHub Issues (`PRO-Robotech/kacho-vpc/issues`), см. `../../CLAUDE.md` §14.4.
Если что-то отсюда решат «всё-таки фиксить» — заводится issue, запись переезжает туда.

## 1. Мутации несуществующего ресурса (`Update`/`Delete`/`Move`/`AddCidrBlocks`/...) → sync `404`, не async `Operation`

В отличие от `Create` (всегда async `Operation`), остальные мутации перед созданием
Operation делают `repo.Get(id)` + `AssertFolderOwnership` — без знания `folder_id` ресурса
AuthZ невозможен, поэтому несуществующий/чужой ресурс → sync `5 NOT_FOUND` / `7 PERMISSION_DENIED`,
а не Operation. Задокументировано в proto-комментариях handler'ов + `04-api-surface.md`.

## 2. REST-пути неоднородны

kebab у custom-методов (`:add-cidr-blocks`, `:move`), snake у child-list (`security_groups`,
`route_tables`), `/operations/{id}` без `/vpc/v1/`-префикса, PrivateEndpoint на `/endpoints`.
Всё проистекает из `google.api.http`-аннотаций в `.proto` (proto-decided) — не handwritten-выбор.
Карта путей — `04-api-surface.md`.

## 3. `OperationService.Get` с id без 3-символьного prefix → `400 INVALID_ARGUMENT "unknown prefix"`

api-gateway `opsproxy` парсит первые 3 символа id для маршрутизации на нужный backend
(`enp...` → kacho-vpc, `b1g...` → resource-manager). Невалидный prefix отсекается fail-fast
**перед** роутингом → `400`, а не `404 "Operation X not found"`. Спорно (пользователю `404`
ожидаемее), но архитектурно обосновано: маршрутизатор не может вернуть `404`, не зная backend.
Нормализация к `404` потребовала бы catch-all backend или handling в opsproxy — низкоприоритетно;
если решат делать — issue в `kacho-api-gateway`.

## 4. `Address.GetByValue` несуществующего IP → `404 NOT_FOUND` (не `400`/`403`)

Intentional — info-leak prevention: cross-tenant `Get` существующего чужого Address и `Get`
несуществующего дают **одинаковый** `404`. Иначе по коду ответа можно было бы пробить, какие
IP вообще выделены в системе.

## 5. `InternalAddressPoolService.Create` без `name` → `200`, pool с `name=""`

`AddressPool` — kacho-only admin-ресурс (verbatim-YC аналога нет), резолвится по `id`/labels,
не по `name`. VPC-permissive name policy (пустое имя разрешено) применима и к пулам — `name`
для пула чисто декоративный. Не required.

## 6. `InternalCloudService.SetPoolSelector` не проверяет существование `cloud_id`

Idempotent upsert; кросс-DB FK между `kacho_vpc` и `kacho_resource_manager` нет. «Висячий»
selector для несуществующего/удалённого cloud безвреден — в IPAM-cascade (Step 3) он просто
не зарезолвится, потому что не будет живых `folder→cloud`-связей, указывающих на этот cloud.
Реальная валидация потребовала бы `CloudService.Exists` RPC на resource-manager — cross-repo
фича, не делаем. Proto-комментарий это отражает.

## 7. VPC-ресурсы folder-scoped, без `cloud_id`/`organization_id` на самих ресурсах

Это **verbatim-YC parity**, не gap: в reference YC VPC API `Network`/`Subnet`/`Address`/
`RouteTable`/`SecurityGroup`/`Gateway`/`PrivateEndpoint` несут только `folder_id`, `List*Request`
принимает ровно `folder_id` (required). «Список Network в Cloud» в YC делает клиент:
`resourcemanager.FolderService.List(cloud_id)` → затем `vpc.NetworkService.List(folder_id)`
по каждому folder. `cloud_id` живёт на `resourcemanager.Folder`, `organization_id` — на
`resourcemanager.Cloud`. Единственное место в kacho-vpc, где `cloud_id` вообще нужен —
IPAM cloud-level pool-selector (`FolderClient.GetCloudID` в cascade Step 3), и это kacho-only
расширение, не часть VPC API. Добавление `cloud_id`/`organization_id` на VPC-ресурсы было бы
**отклонением** от YC, а не его реализацией. Также `../../CLAUDE.md` §2.
