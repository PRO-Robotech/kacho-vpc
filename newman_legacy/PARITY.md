# kacho-vpc parity registry

Кейсы, которые **временно** вне unified VPC suite. Каждая запись — `<unified-id>` + причина + ожидаемый разблокирующий шаг.

## pending-parity (Network domain)

Кейсы вне unified suite потому что **архитектурное расхождение** Kachō ↔ YC: Kachō по
`CLAUDE.md`-контракту возвращает `Operation` для всех мутаций (даже если ошибка
обнаруживается до вставки), YC отдаёт sync-ошибку для типичных pre-mutation проверок.
Чтобы вернуть кейс в unified, нужен Kachō-refactor с sync-validation в handler-слое
(cross-service call к kacho-resource-manager или Read-query перед Operation).

| Unified id | YC behavior | Kachō behavior | Blocker (atomic PR) |
|---|---|---|---|
| NET-CR-DUP-NAME       | sync 409 Conflict при duplicate name в folder | 200 + Operation success (allows dup) | `repo.Network.ExistsByName(folderId,name)` перед Create |
| NET-CR-MISSING-FOLDER | sync 404 NotFound (folder не существует) | 200 + Operation NOT_FOUND | RM-clients call `Folder.Exists(folderId)` в handler |
| NET-CR-INVALID-FOLDER | sync 404 (parses uuid, ищет folder, не находит) | 200 + Operation NOT_FOUND | то же — sync existence check |
| NET-DEL-WITH-SUBNETS  | sync 400 FailedPrecondition при non-empty network | 200 + Operation FAILED_PRECONDITION | `repo.Subnet.HasNetworkRefs(networkId)` перед Delete |
| NET-PATCH-NOOP        | sync 400 InvalidArgument на empty PATCH body | 200 (treats as no-op) | handler-level reject если update_mask + body оба empty |
| NET-LIST-PS-NEG       | 200 (clamp pageSize<0 to default) | 400 InvalidArgument | `corevalidate.PageSize` clamp instead of reject (см. validate.go:153) |
| NET-MOVE-VALID        | move semantics нужно сравнить probe'ом | Operation создается, но семантика relocate неясна | сравнительный прогон `yc vpc network move` |
| NET-LIST-FILTER       | rate-limit на 2-й POST подряд (квота networks per cloud) | то же | разнести 2 setup-POST по разным runs или поднять folder-level quota; добавить poll-op в cleanup чтобы освобождать квоту |
| NET-LIST-PAGE-TOKEN-FORMAT | то же — rate-limit на 2-й POST | то же | то же |

## kacho-only (не в unified VPC suite)

Тесты Kachō-specific фич, которых в YC нет. Запускаются отдельно:
`./scripts/run.sh --collection collections/kacho-vpc-internal.postman_collection.json` (только `--env local`).

| Suite case | Reason |
|---|---|
| NET-DEFAULT-SG-AUTO | Kachō reconciler auto-создаёт `defaultSecurityGroupId` для каждой Network (eventually). YC не делает auto-populate — `defaultSecurityGroupId` остаётся unset до явного :setDefaultSecurityGroup. |
| NETBOX-NETWORK-UPDATE-DESC-SYNC | NetBox VRF sync — Kachō-specific integration, в YC отсутствует. |

> **Resolved test-bugs** (изначально были в pending, оказались тестовыми багами оригинальной
> коллекции; зафиксированы в `scripts/rebuild-collection.py` и сейчас в unified):
> NAME-ACCEPTS (CAPS permissive), NAME-OVER (padding), LABELS-MAX (brace balance),
> LIST-PAGE-TOKEN-FORMAT (явное создание networks).

## kacho-only (не в unified VPC suite)

Кейсы тестируют функциональность, которой нет в YC API. Живут отдельно (или удаляются — см. ниже).

| Suite case | Reason |
|---|---|
| NETBOX-NETWORK-UPDATE-DESC-SYNC | NetBox VRF sync — Kachō-specific integration, в YC отсутствует. |

Эти кейсы выкинуты из `kacho-vpc.postman_collection.json` rebuild-скриптом. Если понадобятся — заведём `collections/kacho-vpc-internal.postman_collection.json` для KC-only smoke (отдельный entrypoint, например `npm run test:internal`).

## Следующие домены

После Network в pending перейдут аналоги для Subnet/Address/RT/SG/GW/PE. Шаблон унификации зафиксирован в `kacho-workspace/docs/superpowers/specs/2026-05-05-vpc-newman-unification-design.md`.
