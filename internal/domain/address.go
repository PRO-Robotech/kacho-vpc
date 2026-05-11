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

// AddressReference — кто использует Address (YC-like referrer-tracking).
// Один referrer на адрес. ReferrerType — "compute_instance" (расширяемо).
type AddressReference struct {
	AddressID    string
	ReferrerType string
	ReferrerID   string
	ReferrerName string
	AttachedAt   time.Time
}

// Address — IP-адрес (internal или external).
type Address struct {
	ID                 string
	FolderID           string
	CreatedAt          time.Time
	Name               string
	Description        string
	Labels             map[string]string
	Type               AddressType
	IpVersion          IpVersion
	Reserved           bool
	Used               bool
	DeletionProtection bool
	// Для external:
	ExternalIpv4 *ExternalIpv4Spec
	// Для internal:
	InternalIpv4 *InternalIpv4Spec
}
