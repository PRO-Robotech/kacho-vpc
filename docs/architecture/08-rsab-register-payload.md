# Resource-register payload: VPC per-tuple labels — осознанное решение

Контекст: resource-scoped AccessBinding. При мутации ресурса VPC эмитит owner-hierarchy
FGA-tuple(ы) + mirror-feed (labels + parent_project_id + source_version) в
`fga_register_outbox` (writer-TX), откуда дренер форвардит их в
`kacho-iam InternalIAMService.RegisterResource`. Та же кросс-сервисная форма у compute и nlb.

## Расхождение формы payload (осознанное)

`internal/apps/kacho/fgaregister`:

- **VPC — labels PER-TUPLE.** `Item` = один tuple + его собственные `Labels` /
  `ParentProjectID`; `Intent.Items[]` — слайс таких Item, и emitter раскрывает Intent в
  **одну `fga_register_outbox`-строку (один `Payload`) на каждый Item**. То есть labels
  привязаны к КОНКРЕТНОМУ tuple, а не к интенту целиком.
- **compute / nlb — labels PER-INTENT.** Intent несет `Tuples[]` + ОДИН общий набор
  labels на весь интент (один ресурс → один набор tuples с общими labels).

## Почему VPC иначе — и почему это правильно

`Network.Create` — единственный VPC-lifecycle event, который в ОДНОМ writer-TX эмитит
**два разных объекта-tuple**:

1. tuple самой Network — с labels Network'а (mirror-feed для селектора по `vpc.network`);
2. отдельный tuple inline default-SG (`default-sg-{net8}`) — это ДРУГОЙ объект
   (`vpc.security_group`) со своими (пустыми / собственными) labels.

Если бы labels были per-intent (общий набор на весь Intent), оба объекта получили бы
ОДИН набор labels — что неверно: default-SG ≠ Network, у него своя identity и свой
mirror-feed в `resource_mirror` на стороне IAM. Per-tuple labels позволяют каждому из
двух объектов нести корректный собственный feed в пределах одной атомарной writer-TX.

compute/nlb такого «два объекта за один lifecycle event» не имеют (один ресурс → один
объект), поэтому per-intent у них достаточно и проще.

Wire-форма (`Payload` JSONB в `fga_register_outbox`) у всех трех сервисов **идентична**
(embed `Tuple` + `labels` + `parent_project_id` + `source_version`) и одинаково
декодируется приемником `kacho-iam`. Расхождение — только во ВНУТРЕННЕМ emitter-API
(где живут labels: на Item или на Intent), не в контракте на проводе.

## Замечание для maintainer'а

**НЕ «выравнивать» VPC `fgaregister` под per-intent форму compute/nlb.** Это выглядит как
«несогласованность», но это требование Network.Create (multi-object emit за одну TX). Свод
к per-intent сломает mirror-feed default-SG (оба объекта получат labels Network'а).
Если когда-нибудь в compute/nlb появится multi-object lifecycle event — выравнивать в
ДРУГУЮ сторону (их → к per-tuple VPC-форме), не наоборот.

См. `internal/apps/kacho/fgaregister/fgaregister.go` (`Item` / `Intent` / `Payload`
docstrings) и `internal/apps/kacho/api/network/create.go` (двойной emit Network + default-SG).

## Consumer обязан re-emit mirror на label-Update (не только Create)

ARM_LABELS-грант на cross-service ресурс ревокается IAM-стороной (reconciler)
**только** когда `resource_mirror[<type>:<id>].labels` обновляется. IAM ре-материализует
membership на каждый `mirror.upsert` reconcile-event (включая revoke fell-out members).
Значит **consumer обязан эмитить `RegisterResource` (mirror.upsert с актуальными labels)
не только на Create, но и на Update, когда labels изменились** — иначе зеркало протухает
и стейл-членство держится бессрочно.

Дизайн-решения:

- **Emit-точка + gate.** `Network.Update` и `SecurityGroup.Update` эмитят
  register-intent **в той же writer-TX**, что и UPDATE ресурса, **gated по
  `labelsInMask`**: пустая маска = full-object PATCH ⇒ эмит обязателен; явная маска ⇒ эмит
  iff `"labels"` ∈ mask; не-label Update (rename/desc/rule_specs) ⇒ **no-op** (меньше
  reconcile-шума; external-наблюдаемое поведение идентично always-emit за счет
  `source_version`-monotonic). Эталон — `subnet/update.go`.
- **Upsert, НЕ Unregister, при полном снятии меток.** `labels → {}` эмитит
  `RegisterResource` (mirror.upsert) с **пустым** labels-map, НЕ `UnregisterResource`.
  Ресурс жив → mirror-строка должна остаться (с `labels={}`), иначе снесся бы
  owner-tuple/containment живого ресурса. `UnregisterResource` остается **только** на
  Delete ресурса.
- **Atomicity.** mirror-intent — в той же writer-TX, что UPDATE (один
  `Commit`); rollback ⇒ intent не записан; нет dual-write. Дрейн в IAM — отдельный
  at-least-once drainer (mTLS); недоступность IAM **не** блокирует Update (async outbox,
  не sync-предусловие — поэтому Update НЕ падает в `UNAVAILABLE`).

**SecurityGroup — двойной случай:** ранее `Create` эмитил bare-tuple без labels
(`ProjectHierarchy` 3-арг) → mirror SG вообще без labels, селектор не матчил даже
свежесозданный SG; `Update` не эмитил вовсе. Исправлены **обе** точки: Create перешел на
`ProjectHierarchyItem(... domain.LabelsToMap(created.Labels))`, Update получил
`labelsInMask`-gated emit.

`labelsInMask` намеренно **продублирован** в `subnet`/`network`/`securitygroup`
update-use-case'ах (а не вынесен в shared-пакет): one-liner-trivial, со-локирован с
`applyXxxMask` каждого ресурса, чтобы full-PATCH-набор полей и emit-gate не разъехались;
shared-helper связал бы несвязанные use-case-пакеты без реальной выгоды (locality over
premature sharing).

Корректные эмиттеры (vpc.subnet) не трогались. Остаточный gap
(vpc.routeTable/address/gateway/networkInterface: selectable, но labels не feed-ятся) —
осознанно отложен.

См. `internal/apps/kacho/api/network/update.go`,
`internal/apps/kacho/api/securitygroup/{create,update}.go` и integration-тесты
`internal/repo/{network,securitygroup}_fga_register_integration_test.go`.
