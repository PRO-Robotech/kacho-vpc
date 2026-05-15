package domain

import "go.uber.org/multierr"

// StaticRoute — статический маршрут.
type StaticRoute struct {
	DestinationPrefix string            `json:"destination_prefix"`
	NextHopAddress    string            `json:"next_hop_address"`
	Labels            map[string]string `json:"labels,omitempty"`
}

// RouteTable — таблица маршрутизации (Wave 2 batch A, KAC-94).
//
// Семантически-нагруженные поля (Name/Description/Labels) — newtypes из
// `domain/types.go` со встроенным Validate(). `CreatedAt` сюда НЕ входит —
// DB-managed, живёт в `RouteTableRecord` (см. `domain/persistence.go`) согласно
// skill evgeniy §4 D.1 / §7 H.1.
type RouteTable struct {
	ID           string
	FolderID     string
	Name         RcNameVPC
	Description  RcDescription
	Labels       RcLabels
	NetworkID    string
	StaticRoutes []StaticRoute
}

// Validate проверяет name/description/labels по domain-контракту. Вызывается
// use-case-слоем ПЕРЕД repo.Insert / repo.Update (skill evgeniy §4 D.4 / D.6).
//
// StaticRoutes-валидация (CIDR без host-bits + next-hop IP) — в service-слое
// (validateStaticRoutes), это netip-проверка, не newtype-уровень.
func (rt RouteTable) Validate() error {
	return multierr.Combine(
		rt.Name.Validate(),
		rt.Description.Validate(),
		ValidateLabels(rt.Labels),
	)
}
