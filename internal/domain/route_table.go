package domain

import "go.uber.org/multierr"

// StaticRoute — статический маршрут.
type StaticRoute struct {
	DestinationPrefix string            `json:"destination_prefix"`
	NextHopAddress    string            `json:"next_hop_address"`
	Labels            map[string]string `json:"labels,omitempty"`
}

// Equal — deep equality. Labels — map-семантика (order-insensitive).
// skill evgeniy §4 D.10.
func (r StaticRoute) Equal(other StaticRoute) bool {
	return r.DestinationPrefix == other.DestinationPrefix &&
		r.NextHopAddress == other.NextHopAddress &&
		labelsMapEqual(r.Labels, other.Labels)
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

// Equal — deep equality по domain-полям. `CreatedAt` не входит (skill evgeniy
// §4 D.1). StaticRoutes — order-sensitive (порядок маршрутов значим для
// matching priority). skill evgeniy §4 D.10.
func (rt RouteTable) Equal(other RouteTable) bool {
	if rt.ID != other.ID ||
		rt.FolderID != other.FolderID ||
		rt.Name != other.Name ||
		rt.Description != other.Description ||
		rt.NetworkID != other.NetworkID {
		return false
	}
	if !LabelsEqual(rt.Labels, other.Labels) {
		return false
	}
	if len(rt.StaticRoutes) != len(other.StaticRoutes) {
		return false
	}
	for i := range rt.StaticRoutes {
		if !rt.StaticRoutes[i].Equal(other.StaticRoutes[i]) {
			return false
		}
	}
	return true
}
