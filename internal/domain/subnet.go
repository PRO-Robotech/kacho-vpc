// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package domain

// DhcpOptions — опции DHCP для подсети.
type DhcpOptions struct {
	DomainNameServers []string `json:"domain_name_servers,omitempty"`
	DomainName        string   `json:"domain_name,omitempty"`
	NtpServers        []string `json:"ntp_servers,omitempty"`
}

// Equal — deep equality. nil-DhcpOptions считается равным nil; order-sensitive
// для DomainNameServers/NtpServers (как и для всех reference-list'ов VPC).
func (d *DhcpOptions) Equal(other *DhcpOptions) bool {
	if d == nil || other == nil {
		return d == other
	}
	return d.DomainName == other.DomainName &&
		stringSlicesEqual(d.DomainNameServers, other.DomainNameServers) &&
		stringSlicesEqual(d.NtpServers, other.NtpServers)
}

// SubnetPlacementType — дискриминатор размещения подсети. Задается при Create,
// immutable. Пустое значение (PlacementUnspecified) на входе невалидно — клиент
// обязан выбрать явно (UNSPECIFIED не дефолтит в ZONAL).
type SubnetPlacementType string

const (
	// PlacementUnspecified — отсутствие выбора (UNSPECIFIED). На Create отвергается.
	PlacementUnspecified SubnetPlacementType = ""
	// PlacementZonal — unicast-адреса, подсеть живет в одной зоне (ZoneID).
	PlacementZonal SubnetPlacementType = "ZONAL"
	// PlacementRegional — anycast-префикс, region-scoped (RegionID), анонсируется
	// active-active из здоровых зон региона.
	PlacementRegional SubnetPlacementType = "REGIONAL"
)

// Subnet — подсеть.
//
// Семантически-нагруженные поля (Name/Description/Labels) — newtypes из
// `domain/types.go` со встроенным Validate(). `CreatedAt` сюда НЕ входит —
// DB-managed, живет в `SubnetRecord` (см. `internal/repo/kacho/entity_subnet.go`).
//
// `ID` / `ProjectID` / `NetworkID` / `ZoneID` / `RegionID` / `RouteTableID` —
// голый `string` (внешние reference-id, валидация — на уровне
// `corevalidate.ResourceID` в service-слое перед запросом к репо).
//
// `PlacementType` дискриминирует пару `ZoneID`/`RegionID`: ровно одно из них
// непусто (ZONAL → ZoneID, REGIONAL → RegionID). Инвариант держит DB-CHECK
// (subnets_placement_payload_chk); service-слой валидирует ту же форму до repo.
type Subnet struct {
	ID            string
	ProjectID     string
	Name          RcNameVPC
	Description   RcDescription
	Labels        RcLabels
	NetworkID     string
	PlacementType SubnetPlacementType
	ZoneID        string
	RegionID      string
	V4CidrBlocks  []string
	V6CidrBlocks  []string // output-only ipv6
	RouteTableID  string
	DhcpOptions   *DhcpOptions
}

// Validate проверяет name/description/labels по domain-контракту. Вызывается
// use-case-слоем ПЕРЕД repo.Insert / repo.Update.
//
// CIDR / zone / network ссылочные поля не валидируются здесь — это zone-id format
// (corevalidate.ZoneId), CIDR host-bits (validateSubnetV4CIDR), и т.п. — они
// живут в service-слое как cross-cutting / cross-resource concerns.
func (s Subnet) Validate() error {
	return combineValidation(
		s.Name.Validate(),
		s.Description.Validate(),
		ValidateLabels(s.Labels),
	)
}

// Equal — deep equality по domain-полям. `CreatedAt` не входит. DhcpOptions
// сравнивается через `DhcpOptions.Equal` (корректно обрабатывает nil/nil).
func (s Subnet) Equal(other Subnet) bool {
	return s.ID == other.ID &&
		s.ProjectID == other.ProjectID &&
		s.Name == other.Name &&
		s.Description == other.Description &&
		LabelsEqual(s.Labels, other.Labels) &&
		s.NetworkID == other.NetworkID &&
		s.PlacementType == other.PlacementType &&
		s.ZoneID == other.ZoneID &&
		s.RegionID == other.RegionID &&
		stringSlicesEqual(s.V4CidrBlocks, other.V4CidrBlocks) &&
		stringSlicesEqual(s.V6CidrBlocks, other.V6CidrBlocks) &&
		s.RouteTableID == other.RouteTableID &&
		s.DhcpOptions.Equal(other.DhcpOptions)
}
