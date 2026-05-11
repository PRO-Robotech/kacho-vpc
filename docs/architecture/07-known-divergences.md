# Намеренные поведенческие решения (и где они расходятся с verbatim-YC)

Это **не баги** и **не задачи** — осознанные решения, которые могут удивить ревьюера:
либо мы **расходимся** с reference Yandex Cloud VPC API (с обоснованием), либо
**deliberately не делаем** того, что напрашивается. Цель файла — чтобы это не «фиксили»
по второму разу.

**Сюда НЕ пишем** то, что просто корректно реализует verbatim-YC контракт — это не
«решение», а спека (см. `../../CLAUDE.md` §2, `04-api-surface.md`, `05-database.md`).
Например: VPC-ресурсы folder-scoped без `cloud_id`/`organization_id`; `garbage id` →
async `NotFound` а не sync `InvalidArgument`; permissive `NameVPC` — всё это **и есть** YC,
расхождения тут нет, поэтому в файле этого нет.

Баги/задачи (в т.ч. «всё-таки решили выровнять») — GitHub Issues (`PRO-Robotech/kacho-vpc`),
см. `../../CLAUDE.md` §14.4. Несколько пунктов ниже помечены «нужен YC-probe» — поведение
реального YC в этом краю не верифицировано; если probe покажет совпадение — пункт удаляется.

## 1. Мутации несуществующего ресурса (`Update`/`Delete`/`Move`/`AddCidrBlocks`/...) → sync `404`/`403`, не async `Operation` *(вероятно совпадает с YC — нужен probe)*

В отличие от `Create` (всегда async `Operation`), остальные мутации перед созданием
Operation делают `repo.Get(id)` + `AssertFolderOwnership` — без знания `folder_id` ресурса
AuthZ невозможен, поэтому несуществующий ресурс → sync `5 NOT_FOUND`, чужой → `7 PERMISSION_DENIED`,
а не Operation. Скорее всего реальный YC делает так же (не оборачивает «ресурс не найден» в
Operation). **Если** probe покажет, что YC отдаёт асинхронную Operation, которая потом падает —
выравнивание означает «перенести `repo.Get` внутрь worker'а», а это ломает модель AuthZ
(нельзя проверить folder-ownership, не зная folder ресурса) → реальная архитектурная цена.
Задокументировано в proto-комментариях handler'ов + `04-api-surface.md`.

## 2. REST-пути неоднородны — НЕ нормализовать

kebab у custom-методов (`:add-cidr-blocks`, `:move`), snake у child-list (`security_groups`,
`route_tables`), camel у top-level (`routeTables`, `securityGroups`, `addressPools`),
`/operations/{id}` без `/vpc/v1/`-префикса, PrivateEndpoint на `/endpoints`. Всё проистекает
из `google.api.http`-аннотаций в `.proto` (`kacho-proto`) — это калька с YC API surface, не
handwritten-выбор. Если кому-то покажется «надо причесать» — **нельзя**: сломает verbatim-YC.
Если найдётся конкретный путь, не совпадающий с YC — это баг в аннотации в `kacho-proto` (issue туда).
Карта путей — `04-api-surface.md`.

## 3. `OperationService.Get` с id без 3-символьного prefix → `400 INVALID_ARGUMENT "unknown prefix"` *(расходимся с YC; нужен probe для подтверждения текста)*

api-gateway `opsproxy` парсит первые 3 символа id для маршрутизации на нужный backend
(`enp...` → kacho-vpc, `b1g...` → resource-manager). Невалидный prefix отсекается fail-fast
**перед** роутингом → `400`, а не `404 "Operation X not found"`. YC, скорее всего, отдаёт `404`.
Архитектурно `400` обосновано (маршрутизатор не может вернуть `404`, не зная backend), но если
нужен строгий parity — выравнивание: в `opsproxy` при unrouteable id вернуть `404 NOT_FOUND` с
YC-дословным текстом вместо `400`. Маленькое изменение, **в `kacho-api-gateway`**. Низкоприоритетно
(реальные клиенты в это редко упираются). Если решат делать — issue в `kacho-api-gateway`.

## 4. `Address.GetByValue` несуществующего IP → `404 NOT_FOUND` (не `400`/`403`) — намеренно

Info-leak prevention: cross-tenant `Get` существующего чужого Address и `Get` несуществующего
дают **одинаковый** `404` — иначе по коду ответа можно было бы пробить, какие IP вообще выделены
в системе. Реальный YC, скорее всего, ведёт себя так же (это стандартная практика). Документируется,
чтобы никто не «исправил» это на `403` для cross-tenant ради «информативности».

## 5. `InternalCloudService.SetPoolSelector` не проверяет существование `cloud_id` — намеренно

Idempotent upsert; кросс-DB FK между `kacho_vpc` и `kacho_resource_manager` нет. «Висячий»
selector для несуществующего/удалённого cloud безвреден — в IPAM-cascade (Step 3) он просто
не зарезолвится, потому что не будет живых `folder→cloud`-связей, указывающих на этот cloud.
Реальная валидация потребовала бы `CloudService.Exists` RPC на resource-manager — cross-repo
фича, не делаем. (kacho-only RPC, в YC аналога нет — выравнивать не с чем.) Proto-комментарий
(`kacho-proto/.../internal_cloud_service.proto`) это отражает.
