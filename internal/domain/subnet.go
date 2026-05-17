package domain

import "go.uber.org/multierr"

// DhcpOptions — опции DHCP для подсети.
type DhcpOptions struct {
	DomainNameServers []string `json:"domain_name_servers,omitempty"`
	DomainName        string   `json:"domain_name,omitempty"`
	NtpServers        []string `json:"ntp_servers,omitempty"`
}

// Equal — deep equality. nil-DhcpOptions считается равным nil; order-sensitive
// для DomainNameServers/NtpServers (как и для всех reference-list'ов VPC).
// skill evgeniy §4 D.10.
func (d *DhcpOptions) Equal(other *DhcpOptions) bool {
	if d == nil || other == nil {
		return d == other
	}
	return d.DomainName == other.DomainName &&
		stringSlicesEqual(d.DomainNameServers, other.DomainNameServers) &&
		stringSlicesEqual(d.NtpServers, other.NtpServers)
}

// Subnet — подсеть (Wave 2 batch A, KAC-94).
//
// Семантически-нагруженные поля (Name/Description/Labels) — newtypes из
// `domain/types.go` со встроенным Validate(). `CreatedAt` сюда НЕ входит —
// DB-managed, живёт в `SubnetRecord` (см. `domain/persistence.go`) согласно
// skill evgeniy §4 D.1 / §7 H.1.
//
// `ID` / `ProjectID` / `NetworkID` / `ZoneID` / `RouteTableID` — голый `string`
// (внешние reference-id, валидация — на уровне `corevalidate.ResourceID` в
// service-слое перед запросом к репо).
type Subnet struct {
	ID           string
	ProjectID     string
	Name         RcNameVPC
	Description  RcDescription
	Labels       RcLabels
	NetworkID    string
	ZoneID       string
	V4CidrBlocks []string // repeated string в YC
	V6CidrBlocks []string // OUTPUT_ONLY ipv6
	RouteTableID string
	DhcpOptions  *DhcpOptions
}

// Validate проверяет name/description/labels по domain-контракту. Вызывается
// use-case-слоем ПЕРЕД repo.Insert / repo.Update (skill evgeniy §4 D.4 / D.6).
//
// CIDR / zone / network ссылочные поля не валидируются здесь — это zone-id format
// (corevalidate.ZoneId), CIDR host-bits (validateSubnetV4CIDR), и т.п. — они
// живут в service-слое как cross-cutting / cross-resource concerns.
func (s Subnet) Validate() error {
	return multierr.Combine(
		s.Name.Validate(),
		s.Description.Validate(),
		ValidateLabels(s.Labels),
	)
}

// Equal — deep equality по domain-полям. `CreatedAt` не входит (skill evgeniy
// §4 D.1). DhcpOptions сравнивается через `DhcpOptions.Equal` (handle nil/nil
// корректно). skill evgeniy §4 D.10.
func (s Subnet) Equal(other Subnet) bool {
	return s.ID == other.ID &&
		s.ProjectID == other.ProjectID &&
		s.Name == other.Name &&
		s.Description == other.Description &&
		LabelsEqual(s.Labels, other.Labels) &&
		s.NetworkID == other.NetworkID &&
		s.ZoneID == other.ZoneID &&
		stringSlicesEqual(s.V4CidrBlocks, other.V4CidrBlocks) &&
		stringSlicesEqual(s.V6CidrBlocks, other.V6CidrBlocks) &&
		s.RouteTableID == other.RouteTableID &&
		s.DhcpOptions.Equal(other.DhcpOptions)
}
