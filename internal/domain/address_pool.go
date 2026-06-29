// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package domain

// AddressPool — internal-only resource (не выставляется через публичный VPC API).
// Содержит коллекции CIDR-блоков, из которых аллоцируются external IP-адреса.
//
// CIDR-блоки разделены по family — v4_cidr_blocks + v6_cidr_blocks (parity с
// Subnet); это делает family-фильтрацию IPAM cascade явной (без runtime-парсинга
// каждого блока). Pool допустим v4-only, v6-only или dual-stack — хотя бы одно
// поле непусто (service-слой валидирует на Create/Update, на DB-уровне — EXCLUDE).
//
// Семантически-нагруженные поля (Name/Description/Labels/SelectorLabels) —
// self-validating newtypes из `domain/types.go`; их корректность проверяет
// `Validate()`. `CreatedAt`/`ModifiedAt` сюда НЕ входят — DB-managed timestamps
// живут в `AddressPoolRecord` (см. `internal/repo/kacho/entity_address_pool.go`),
// паритет с Subnet/SubnetRecord.
type AddressPool struct {
	ID           string // global infra resource — не привязан к project
	Name         RcNameVPC
	Description  RcDescription
	Labels       RcLabels
	V4CIDRBlocks []string // IPv4-префиксы (host-bits=0); пустой массив = pool не выдает v4
	V6CIDRBlocks []string // IPv6-префиксы (host-bits=0); пустой массив = pool не выдает v6
	Kind         AddressPoolKind
	ZoneID       string // id зоны; empty = global default
	IsDefault    bool
	// SelectorLabels — whitelist labels Network'а, при котором pool участвует
	// в label-cascade-step резолва. Match-семантика: `network.pool_selector ⊆
	// pool.selector_labels`. Empty selector = pool НЕ участвует в label-cascade
	// (только через explicit binding или is_default).
	SelectorLabels   RcLabels
	SelectorPriority int32
}

// Validate проверяет форму пула по domain-контракту: name/description/labels/
// selector_labels — те же newtype-правила, что у прочих VPC-ресурсов;
// selector_priority — неотрицательный; kind — известное значение enum'а.
// Вызывается use-case-слоем ПЕРЕД repo.Insert / repo.Update (DB CHECK — backstop).
//
// selector_labels валидируются тем же `ValidateLabels`-контрактом, что и labels:
// нарушение ключа репортится под field `labels.<key>` (LabelKey.Validate хардкодит
// префикс) — это часть контракта (трассируется в integration/newman-assert).
//
// CIDR/zone/host-bits — cross-cutting concerns service-слоя (family-strict +
// host-bits=0, zone-existence через geo-client), здесь не проверяются.
func (p AddressPool) Validate() error {
	return combineValidation(
		p.Name.Validate(),
		p.Description.Validate(),
		ValidateLabels(p.Labels),
		ValidateLabels(p.SelectorLabels),
		p.validateSelectorPriority(),
		p.Kind.Validate(),
	)
}

// validateSelectorPriority — tie-break weight не может быть отрицательным
// (HIGHER-wins; верхняя граница — естественный int32-ceiling).
func (p AddressPool) validateSelectorPriority() error {
	if p.SelectorPriority < 0 {
		return newValidationError("selector_priority", "selector_priority must be non-negative")
	}
	return nil
}

// AddressPoolKind — категория пула. Зеркалит enum в proto.
type AddressPoolKind int16

const (
	AddressPoolKindUnspecified    AddressPoolKind = 0
	AddressPoolKindExternalPublic AddressPoolKind = 1
)

// Validate отвергает значения вне известного enum-набора. `kind=0`
// (Unspecified) — допустимое значение enum'а (его «обязательность» на Create
// проверяет use-case отдельным ранним возвратом); все, что не входит в
// определенный набор, → "unknown address pool kind".
func (k AddressPoolKind) Validate() error {
	switch k {
	case AddressPoolKindUnspecified, AddressPoolKindExternalPublic:
		return nil
	default:
		return newValidationError("kind", "unknown address pool kind")
	}
}
