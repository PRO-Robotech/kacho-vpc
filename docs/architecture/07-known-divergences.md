# Намеренные дизайн-решения Kachō VPC

Это **не баги** и **не задачи** — осознанные решения формы API и поведения, которые
могут удивить ревьюера. Файл существует, чтобы их не «фиксили» по второму разу. Все
решения описаны в собственных терминах Kachō (конвенции API — `06-conventions.md`).

## 1. NetworkInterface — first-class ресурс VPC

NIC — отдельный ресурс домена VPC (`NetworkInterfaceService`
Get/List/Create/Update/Delete/ListOperations), а не вложенная часть Instance.
Принадлежит `Subnet`, ссылается на `Address`-ресурсы по id, привязывается к
Compute-Instance через `nic_id`. Multi-IP на VM собирается из нескольких NIC.
Проекция — lean, control-plane-only (инфра/data-plane полей нет).

## 2. Опциональные поля на Create

- **`Subnet.v4_cidr_blocks` опционально** — CIDR-less подсеть легальна, CIDR
  добавляется позже через `:addCidrBlocks`. Internal-v4-allocate в CIDR-less подсеть →
  `FailedPrecondition "subnet ... has no IPv4 CIDR"`.
- **`SecurityGroup.network_id` опционально** — network-unbound (project-level) SG
  легальна; NIC принимает такие SG, если они того же project. Default-SG-на-сети
  всегда ставит непустой `network_id`.

## 3. IPv6 — симметрично IPv4

`:addCidrBlocks`/`:removeCidrBlocks` принимают и `v6_cidr_blocks`; `UpdateSubnet`
несет `v6_cidr_blocks` как soft-immutable / no-op (зеркало v4). Internal IPv6 —
`Address.internal_ipv6_address` oneof + `InternalAddressService.AllocateInternalIPv6`.

## 4. ListOperations переживает удаление ресурса

Для Network/Subnet/Address/NetworkInterface `ListOperations` отдает историю даже
после удаления самого ресурса: precondition `repo.Get` убран (handler best-effort —
жив → project-ownership; NotFound → пропуск). Строки `operations` не каскадятся.
RouteTable/SecurityGroup/Gateway по-прежнему гейтят на `repo.Get`.

## 5. Geography (Region/Zone) — не в kacho-vpc

Канонический владелец Geography — leaf-сервис `kacho-geo`. В `kacho-vpc` `zone_id`-колонки
(`subnet.zone_id`, `address_pool.zone_id`, `address.external_ipv4.zone_id`) — `TEXT`-id
без FK, валидируются на request-path через `geo.v1.ZoneService.Get`.

## 6. REST-пути неоднородны по форме — НЕ нормализовать

Стиль `google.api.http`-аннотаций в `.proto` (`kacho-proto`) намеренно смешанный:
kebab у custom-методов (`:addCidrBlocks`), child-list под ресурсом, camelCase у
top-level (`routeTables`, `securityGroups`, `addressPools`), `/operations/{id}` без
`/vpc/v1/`-префикса. Это зафиксированная форма поверхности API — «причесывание»
сломает контракт. Карта путей — `04-api-surface.md`.

## 7. Тело ошибки JSON-transcoding — стандартный JSON `{code,message}`

На неверный тип JSON-поля (`description`=число, `labels`=строка, oneof `address_spec`
задан дважды) api-gateway отдает стандартный grpc-gateway error-handler с JSON
(`400` + непустое тело). Кастомный error-handler ради иного формата тела не делаем.
Кейсы `*-CR-VAL-DESC-INT-TYPE` / `*-CR-VAL-LABELS-STRING-TYPE` / `ADR-CR-VAL-BOTH-SPEC` —
defensive (фиксируют `400` + непустое тело).

## 8. Пустое repeated-поле в List-ответе — отдается `[]`

`GET /vpc/v1/networks?projectId=<пустой>` → `{"networks":[]}` (а также
`nextPageToken:""`). Это `EmitUnpopulated`-настройка grpc-gateway marshaller'а; смена
затронула бы все ответы (напр. `done:false` в Operation), blast radius неоправдан.
Кейсы `NET-LST-*` — defensive (`j.networks || []`).

## 9. Subnet.Update с `v4CidrBlocks` в mask — no-op

Запрос принимается (`200`), но `repo.Update` CIDR-колонки не перезаписывает
(defensive depth). Менять CIDR существующей подсети в control-plane-only модели смысла
мало; реальное изменение — через `:addCidrBlocks`/`:removeCidrBlocks`. Кейс
`SUB-UPD-STATE-IMMUTABLE-CIDR` проверяет только `200`.

## 10. OperationService.Get/Cancel с bad id

malformed id → `InvalidArgument "invalid operation id '<X>'"`; well-formed id (известный
prefix, но бэкенд не подключен) → `NotFound "Operation <X> not found"`; id с prefix
домена с подключенным бэкендом → роутится туда. Реализация — `kacho-api-gateway`
`internal/opsproxy/proxy.go`.
