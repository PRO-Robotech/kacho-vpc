// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package domain

import "time"

// AddressType — тип IP-адреса.
type AddressType int32

const (
	AddressTypeUnspecified AddressType = 0
	AddressTypeInternal    AddressType = 1
	AddressTypeExternal    AddressType = 2
)

// IpVersion — версия IP.
type IpVersion int32

const (
	IpVersionUnspecified IpVersion = 0
	IpVersionIPv4        IpVersion = 1
	IpVersionIPv6        IpVersion = 2
)

// ExternalIpv4Spec — параметры внешнего IPv4-адреса.
type ExternalIpv4Spec struct {
	Address      string               `json:"address"` // например 203.0.113.X
	ZoneID       string               `json:"zone_id"`
	Requirements *AddressRequirements `json:"requirements,omitempty"`
	// AddressPoolID — internal-only поле, заполняется allocator'ом при
	// AllocateExternalIP. Держит UNIQUE (pool_id, ip) constraint и observability
	// ("из какого пула выделили"). Не сериализуется в публичный VPC API
	// (filter в proto-конвертере).
	AddressPoolID string `json:"address_pool_id,omitempty"`
}

// AddressRequirements — требования к выделенному внешнему IP (DDoS-provider,
// возможность отправки SMTP).
type AddressRequirements struct {
	DdosProtectionProvider string `json:"ddos_protection_provider,omitempty"`
	OutgoingSmtpCapability string `json:"outgoing_smtp_capability,omitempty"`
}

// Equal — deep equality. nil/nil считается равным.
func (r *AddressRequirements) Equal(other *AddressRequirements) bool {
	if r == nil || other == nil {
		return r == other
	}
	return r.DdosProtectionProvider == other.DdosProtectionProvider &&
		r.OutgoingSmtpCapability == other.OutgoingSmtpCapability
}

// Equal — deep equality для ExternalIpv4Spec. nil/nil — равны.
func (s *ExternalIpv4Spec) Equal(other *ExternalIpv4Spec) bool {
	if s == nil || other == nil {
		return s == other
	}
	return s.Address == other.Address &&
		s.ZoneID == other.ZoneID &&
		s.Requirements.Equal(other.Requirements) &&
		s.AddressPoolID == other.AddressPoolID
}

// InternalIpv4Spec — параметры внутреннего IPv4-адреса.
type InternalIpv4Spec struct {
	Address  string `json:"address"` // например 10.0.0.X
	SubnetID string `json:"subnet_id"`
}

// Equal — deep equality. nil/nil — равны.
func (s *InternalIpv4Spec) Equal(other *InternalIpv4Spec) bool {
	if s == nil || other == nil {
		return s == other
	}
	return s.Address == other.Address && s.SubnetID == other.SubnetID
}

// InternalIpv6Spec — параметры внутреннего IPv6-адреса (зеркалит InternalIpv4Spec).
type InternalIpv6Spec struct {
	Address  string `json:"address"` // например 2001:db8::5
	SubnetID string `json:"subnet_id"`
}

// Equal — deep equality. nil/nil — равны.
func (s *InternalIpv6Spec) Equal(other *InternalIpv6Spec) bool {
	if s == nil || other == nil {
		return s == other
	}
	return s.Address == other.Address && s.SubnetID == other.SubnetID
}

// ExternalIpv6Spec — параметры внешнего IPv6-адреса (зеркалит ExternalIpv4Spec).
// Адрес выделяется sparse counter-based аллокатором из глобального AddressPool
// с v6 CIDR.
type ExternalIpv6Spec struct {
	Address      string               `json:"address"` // например 2001:db8::5
	ZoneID       string               `json:"zone_id"`
	Requirements *AddressRequirements `json:"requirements,omitempty"`
	// AddressPoolID — internal-only (как у v4 spec); заполняется allocator'ом
	// при AllocateExternalIPv6, держит UNIQUE (pool_id, ip) constraint и
	// observability.
	AddressPoolID string `json:"address_pool_id,omitempty"`
}

// Equal — deep equality. nil/nil — равны.
func (s *ExternalIpv6Spec) Equal(other *ExternalIpv6Spec) bool {
	if s == nil || other == nil {
		return s == other
	}
	return s.Address == other.Address &&
		s.ZoneID == other.ZoneID &&
		s.Requirements.Equal(other.Requirements) &&
		s.AddressPoolID == other.AddressPoolID
}

// AddressReference — кто использует Address (referrer-tracking). Один referrer
// на адрес. ReferrerType — "compute_instance"/"network_interface"/
// "network_load_balancer" (расширяемо). Owned=true означает, что referrer
// владеет адресом (lifecycle связан): такой адрес освобождается вместе с
// referrer'ом (ClearReference + Delete), а не только отвязывается.
type AddressReference struct {
	AddressID    string
	ReferrerType string
	ReferrerID   string
	ReferrerName string
	Owned        bool
	AttachedAt   time.Time
}

// Equal — deep equality. nil/nil — равны. AttachedAt сравнивается через
// `time.Time.Equal` (учитывает monotonic clock / location-agnostic).
func (r *AddressReference) Equal(other *AddressReference) bool {
	if r == nil || other == nil {
		return r == other
	}
	return r.AddressID == other.AddressID &&
		r.ReferrerType == other.ReferrerType &&
		r.ReferrerID == other.ReferrerID &&
		r.ReferrerName == other.ReferrerName &&
		r.Owned == other.Owned &&
		r.AttachedAt.Equal(other.AttachedAt)
}

// Address — IP-адрес (internal или external).
//
// Семантически-нагруженные поля (Name/Description/Labels) — newtypes из
// `domain/types.go` со встроенным Validate(). `CreatedAt` сюда НЕ входит —
// DB-managed, живет в `AddressRecord` (см. `internal/repo/kacho/entity_address.go`).
type Address struct {
	ID                 string
	ProjectID          string
	Name               RcNameVPC
	Description        RcDescription
	Labels             RcLabels
	Type               AddressType
	IpVersion          IpVersion
	Reserved           bool
	Used               bool
	DeletionProtection bool
	// Для external:
	ExternalIpv4 *ExternalIpv4Spec
	// Для internal:
	InternalIpv4 *InternalIpv4Spec
	// Для internal IPv6 (IpVersion == IpVersionIPv6):
	InternalIpv6 *InternalIpv6Spec
	// Для external IPv6:
	ExternalIpv6 *ExternalIpv6Spec
	// UsedBy — кто использует адрес (referrer-tracking, output-only). Заполняется
	// сервис-слоем в Get/List/GetByValue/ListBySubnet из address_references;
	// для compute NIC/NAT-адресов — один элемент с ReferrerType="compute_instance".
	// Не персистится отдельно (это denormalized view на address_references).
	UsedBy []*AddressReference
}

// Validate проверяет name/description/labels по domain-контракту. Вызывается
// use-case-слоем ПЕРЕД repo.Insert / repo.Update.
//
// Кросс-полевые инварианты (oneof ExternalIpv4/InternalIpv4/InternalIpv6/
// ExternalIpv6 — exactly-one, deletion_protection-семантика, требования
// requirements.ddos_protection_provider whitelist) остаются в service-слое:
// они зависят от proto-контракта и от другого ресурса (Subnet).
func (a Address) Validate() error {
	return combineValidation(
		a.Name.Validate(),
		a.Description.Validate(),
		ValidateLabels(a.Labels),
	)
}

// Equal — deep equality по domain-полям. `CreatedAt` не входит. Nested specs
// (ExternalIpv4/InternalIpv4/InternalIpv6/ExternalIpv6) — через их `Equal`-методы
// (handle nil/nil). UsedBy — order-sensitive slice of *AddressReference.
func (a Address) Equal(other Address) bool {
	if a.ID != other.ID ||
		a.ProjectID != other.ProjectID ||
		a.Name != other.Name ||
		a.Description != other.Description ||
		a.Type != other.Type ||
		a.IpVersion != other.IpVersion ||
		a.Reserved != other.Reserved ||
		a.Used != other.Used ||
		a.DeletionProtection != other.DeletionProtection {
		return false
	}
	if !LabelsEqual(a.Labels, other.Labels) {
		return false
	}
	if !a.ExternalIpv4.Equal(other.ExternalIpv4) ||
		!a.InternalIpv4.Equal(other.InternalIpv4) ||
		!a.InternalIpv6.Equal(other.InternalIpv6) ||
		!a.ExternalIpv6.Equal(other.ExternalIpv6) {
		return false
	}
	if len(a.UsedBy) != len(other.UsedBy) {
		return false
	}
	for i := range a.UsedBy {
		if !a.UsedBy[i].Equal(other.UsedBy[i]) {
			return false
		}
	}
	return true
}

// AllocateResult — результат IPAM allocate (используется и use-case-пакетом
// `internal/apps/kacho/api/address.AllocateUseCase`, и
// `internal/handler.AddressAllocator` port'ом). Живет в domain leaf'е, чтобы
// избежать import-cycle между двумя пакетами (handler ↔ apps/.../address).
//
// PoolID непустой только для external-аллокаций; для internal — "".
// AlreadyAllocated=true означает, что allocate был idempotent no-op.
type AllocateResult struct {
	IP               string
	PoolID           string
	AlreadyAllocated bool
}
