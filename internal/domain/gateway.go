// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package domain

// Gateway — NAT Gateway ресурс (shared egress).
//
// Семантически-нагруженные поля (Name/Description/Labels) — newtypes из
// `domain/types.go` со встроенным Validate(). `CreatedAt` сюда НЕ входит —
// DB-managed, живет в `GatewayRecord` (см. `internal/repo/kacho/entity_gateway.go`).
//
// `GatewayType` — sentinel для oneof; сейчас только `GatewayTypeSharedEgress`
// (не голая string-literal). Spec — oneof, но храним единственный поддержанный
// тип через GatewayType.
type Gateway struct {
	ID          string
	ProjectID   string
	Name        RcNameVPC
	Description RcDescription
	Labels      RcLabels
	GatewayType GatewayType
}

// Validate проверяет name/description/labels по domain-контракту. Вызывается
// use-case-слоем ПЕРЕД repo.Insert / repo.Update.
//
// Замечание: Gateway.Name держится здесь как `RcNameVPC` (permissive) — единый
// newtype-набор для всех ресурсов. Strict-name regex (`corevalidate.NameGateway`
// — lowercase, без uppercase/underscore) применяется дополнительно в service-слое
// после `g.Validate()` (см.
// internal/apps/kacho/api/gateway/update.go::validateGatewayUpdate).
func (g Gateway) Validate() error {
	return combineValidation(
		g.Name.Validate(),
		g.Description.Validate(),
		ValidateLabels(g.Labels),
	)
}

// Equal — deep equality по domain-полям. `CreatedAt` не входит.
func (g Gateway) Equal(other Gateway) bool {
	return g.ID == other.ID &&
		g.ProjectID == other.ProjectID &&
		g.Name == other.Name &&
		g.Description == other.Description &&
		LabelsEqual(g.Labels, other.Labels) &&
		g.GatewayType == other.GatewayType
}
