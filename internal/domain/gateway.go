package domain

import "go.uber.org/multierr"

// Gateway — NAT Gateway ресурс (shared egress; Wave 2 batch B, KAC-94).
//
// Семантически-нагруженные поля (Name/Description/Labels) — newtypes из
// `domain/types.go` со встроенным Validate(). `CreatedAt` сюда НЕ входит —
// DB-managed, живёт в `GatewayRecord` (см. `domain/persistence.go`) согласно
// skill evgeniy §4 D.1 / §7 H.1.
//
// `GatewayType` — sentinel для oneof; сейчас только `GatewayTypeSharedEgress`
// (skill §4 D.8: не голая string-literal).
//
// В YC Gateway имеет oneof spec (shared_egress_gateway), но мы храним только
// единственный поддерживаемый тип через GatewayType. См.
// https://yandex.cloud/ru/docs/vpc/api-ref/Gateway/.
type Gateway struct {
	ID          string
	ProjectID   string
	Name        RcNameVPC
	Description RcDescription
	Labels      RcLabels
	GatewayType GatewayType
}

// Validate проверяет name/description/labels по domain-контракту. Вызывается
// use-case-слоем ПЕРЕД repo.Insert / repo.Update (skill evgeniy §4 D.4 / D.6).
//
// Замечание: Gateway.Name в kacho-vpc валидируется через strict-name regex
// (`corevalidate.NameGateway` — lowercase, без uppercase/underscore — verbatim YC).
// Но в Wave 2 батче B мы держим Gateway.Name как `RcNameVPC` (permissive),
// потому что Wave 2 миграция переводит ВСЕ ресурсы на единый newtype-набор; в
// service-слое после `g.Validate()` дополнительно зовётся `corevalidate.NameGateway`
// для верности strict-контракту (см. service/gateway.go::validateGatewayUpdate).
// Это temporary до Wave 3, когда появится `RcNameGateway` с собственным regex'ом.
func (g Gateway) Validate() error {
	return multierr.Combine(
		g.Name.Validate(),
		g.Description.Validate(),
		ValidateLabels(g.Labels),
	)
}

// Equal — deep equality по domain-полям. `CreatedAt` не входит (skill evgeniy
// §4 D.1). skill evgeniy §4 D.10.
func (g Gateway) Equal(other Gateway) bool {
	return g.ID == other.ID &&
		g.ProjectID == other.ProjectID &&
		g.Name == other.Name &&
		g.Description == other.Description &&
		LabelsEqual(g.Labels, other.Labels) &&
		g.GatewayType == other.GatewayType
}
