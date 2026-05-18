package domain

import (
	"reflect"

	"go.uber.org/multierr"
)

// PrivateEndpoint — PrivateLink endpoint к internal services (Object Storage etc;
// Wave 2 batch B, KAC-94).
//
// Семантически-нагруженные поля (Name/Description/Labels) — newtypes из
// `domain/types.go` со встроенным Validate(). `CreatedAt` сюда НЕ входит —
// DB-managed, живёт в `PrivateEndpointRecord` (см. `domain/persistence.go`)
// согласно skill evgeniy §4 D.1 / §7 H.1.
//
// `ServiceType` — sentinel oneof через enum `PrivateEndpointServiceType`
// (сейчас только object_storage). `Status` — enum `PrivateEndpointStatus`
// (PENDING/AVAILABLE/DELETING). skill §4 D.8.
//
// См. https://yandex.cloud/ru/docs/vpc/api-ref/PrivateEndpoint/.
type PrivateEndpoint struct {
	ID          string
	ProjectID   string
	Name        RcNameVPC
	Description RcDescription
	Labels      RcLabels

	NetworkID string
	SubnetID  string
	AddressID string
	IPAddress string

	ServiceType PrivateEndpointServiceType
	// DnsOptions — JSON-blob с опциями DNS (private_dns_records_enabled).
	DnsOptions map[string]any
	Status     PrivateEndpointStatus
}

// Validate проверяет name/description/labels по domain-контракту. Вызывается
// use-case-слоем ПЕРЕД repo.Insert / repo.Update (skill evgeniy §4 D.4 / D.6).
//
// SubnetID/AddressID/IPAddress + service-type / dns_options — cross-resource
// references / oneof-семантика; валидация — в service-слое.
func (p PrivateEndpoint) Validate() error {
	return multierr.Combine(
		p.Name.Validate(),
		p.Description.Validate(),
		ValidateLabels(p.Labels),
	)
}

// Equal — deep equality по domain-полям. `CreatedAt` не входит (skill evgeniy
// §4 D.1). `DnsOptions` (`map[string]any`) сравнивается через `reflect.DeepEqual`
// — это единственный безопасный способ для опционального JSON-blob'а с
// произвольной структурой. skill evgeniy §4 D.10.
func (p PrivateEndpoint) Equal(other PrivateEndpoint) bool {
	return p.ID == other.ID &&
		p.ProjectID == other.ProjectID &&
		p.Name == other.Name &&
		p.Description == other.Description &&
		LabelsEqual(p.Labels, other.Labels) &&
		p.NetworkID == other.NetworkID &&
		p.SubnetID == other.SubnetID &&
		p.AddressID == other.AddressID &&
		p.IPAddress == other.IPAddress &&
		p.ServiceType == other.ServiceType &&
		p.Status == other.Status &&
		reflect.DeepEqual(p.DnsOptions, other.DnsOptions)
}
