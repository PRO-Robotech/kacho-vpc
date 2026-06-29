// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package domain

// AnycastAddressPool — tenant-facing flat-ресурс vpc: project-scoped пул
// anycast-адресов. Из пула выдаются network-scoped глобально-уникальные адреса
// для active-active single-VIP. Фаза 1 — только INTERNAL (IPv4 из reserved-среза
// 100.64.0.0/10, IPv6 — provider-ULA /48).
//
// Адресное пространство пула — массив CIDRBlocks; внутри пула блоки не
// пересекаются (DB-инвариант на child-таблице). Scope/IPVersion/CIDRBlocks —
// immutable после Create; name/description/labels — mutable через update_mask.
// Семантически-нагруженные поля — self-validating newtypes из `domain/types.go`.
type AnycastAddressPool struct {
	ID          string
	ProjectID   string // cross-service ref на iam, без FK
	Name        RcNameVPC
	Description RcDescription
	Labels      RcLabels
	Scope       AnycastScope
	IPVersion   IpVersion
	CIDRBlocks  []string // блоки из reserved-среза; within-pool не пересекаются
	IsDefault   bool     // платформенный admin-owned пул (output-only для тенанта)
	Status      AnycastPoolStatus
}

// AnycastScope — сфера достижимости пула. Зеркалит enum в proto.
type AnycastScope int32

const (
	AnycastScopeUnspecified AnycastScope = 0
	AnycastScopeInternal    AnycastScope = 1
	// AnycastScopeExternal зарезервирован под фазу 2 (EXTERNAL anycast);
	// в фазе 1 service-слой отвергает его на Create.
	AnycastScopeExternal AnycastScope = 2
)

// AnycastPoolStatus — стадия жизненного цикла пула. Зеркалит enum в proto.
type AnycastPoolStatus int32

const (
	AnycastPoolStatusUnspecified AnycastPoolStatus = 0
	AnycastPoolStatusCreating    AnycastPoolStatus = 1
	AnycastPoolStatusActive      AnycastPoolStatus = 2
	AnycastPoolStatusDeleting    AnycastPoolStatus = 3
)

// Validate проверяет форму пула по domain-контракту: name/description/labels —
// те же newtype-правила, что у прочих VPC-ресурсов. Вызывается use-case-слоем
// ПЕРЕД repo.Insert / repo.Update (DB CHECK — backstop).
//
// Scope/IPVersion/CIDRBlocks — cross-cutting concerns service-слоя (reserved-
// диапазон, family-strict, within-pool non-overlap), здесь не проверяются.
func (p AnycastAddressPool) Validate() error {
	return combineValidation(
		p.Name.Validate(),
		p.Description.Validate(),
		ValidateLabels(p.Labels),
	)
}
