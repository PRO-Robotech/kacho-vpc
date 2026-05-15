package domain

import (
	"time"

	"go.uber.org/multierr"
)

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
	// AllocateExternalIP. Используется для UNIQUE (pool_id, ip) constraint
	// (миграция 0015) и для observability ("из какого пула выделили").
	// Не сериализуется в публичный VPC API (filter в proto-конвертере).
	AddressPoolID string `json:"address_pool_id,omitempty"`
}

// AddressRequirements — требования к выделенному внешнему IP (DDoS provider,
// возможность отправки SMTP). Поле проброшено verbatim YC.
type AddressRequirements struct {
	DdosProtectionProvider string `json:"ddos_protection_provider,omitempty"`
	OutgoingSmtpCapability string `json:"outgoing_smtp_capability,omitempty"`
}

// InternalIpv4Spec — параметры внутреннего IPv4-адреса.
type InternalIpv4Spec struct {
	Address  string `json:"address"` // например 10.0.0.X
	SubnetID string `json:"subnet_id"`
}

// InternalIpv6Spec — параметры внутреннего IPv6-адреса (зеркалит InternalIpv4Spec).
type InternalIpv6Spec struct {
	Address  string `json:"address"` // например 2001:db8::5
	SubnetID string `json:"subnet_id"`
}

// ExternalIpv6Spec — параметры внешнего IPv6-адреса (KAC-58, зеркалит
// ExternalIpv4Spec). Адрес выделяется sparse counter-based аллокатором
// (см. kacho-vpc/CLAUDE.md §16) из глобального AddressPool с v6 CIDR.
type ExternalIpv6Spec struct {
	Address      string               `json:"address"` // например 2001:db8::5
	ZoneID       string               `json:"zone_id"`
	Requirements *AddressRequirements `json:"requirements,omitempty"`
	// AddressPoolID — internal-only (как у v4 spec); заполняется allocator'ом
	// при AllocateExternalIPv6, используется для UNIQUE (pool_id, ip) constraint
	// (миграция 0020) и для observability.
	AddressPoolID string `json:"address_pool_id,omitempty"`
}

// AddressReference — кто использует Address (YC-like referrer-tracking).
// Один referrer на адрес. ReferrerType — "compute_instance" (расширяемо).
type AddressReference struct {
	AddressID    string
	ReferrerType string
	ReferrerID   string
	ReferrerName string
	AttachedAt   time.Time
}

// Address — IP-адрес (internal или external). Wave 2 batch A (KAC-94).
//
// Семантически-нагруженные поля (Name/Description/Labels) — newtypes из
// `domain/types.go` со встроенным Validate(). `CreatedAt` сюда НЕ входит —
// DB-managed, живёт в `AddressRecord` (см. `domain/persistence.go`) согласно
// skill evgeniy §4 D.1 / §7 H.1.
type Address struct {
	ID                 string
	FolderID           string
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
	// Для external IPv6 (KAC-58):
	ExternalIpv6 *ExternalIpv6Spec
	// UsedBy — кто использует адрес (referrer-tracking, output-only). Заполняется
	// сервис-слоем в Get/List/GetByValue/ListBySubnet из address_references;
	// для compute NIC/NAT-адресов — один элемент с ReferrerType="compute_instance".
	// Не персистится отдельно (это denormalized view на address_references).
	UsedBy []*AddressReference
}

// Validate проверяет name/description/labels по domain-контракту. Вызывается
// use-case-слоем ПЕРЕД repo.Insert / repo.Update (skill evgeniy §4 D.4 / D.6).
//
// Кросс-полевые инварианты (oneof ExternalIpv4/InternalIpv4/InternalIpv6/
// ExternalIpv6 — exactly-one, deletion_protection-семантика, требования
// requirements.ddos_protection_provider whitelist) остаются в service-слое:
// они зависят от proto-контракта и от другого ресурса (Subnet).
func (a Address) Validate() error {
	return multierr.Combine(
		a.Name.Validate(),
		a.Description.Validate(),
		ValidateLabels(a.Labels),
	)
}

// AllocateResult — результат IPAM allocate (используется и use-case-пакетом
// `internal/apps/kacho/api/address.AllocateUseCase`, и
// `internal/handler.AddressAllocator` port'ом). Живёт в domain leaf'е, чтобы
// избежать import-cycle между двумя пакетами (handler ↔ apps/.../address).
//
// PoolID непустой только для external-аллокаций; для internal — "".
// AlreadyAllocated=true означает, что allocate был idempotent no-op.
type AllocateResult struct {
	IP               string
	PoolID           string
	AlreadyAllocated bool
}
