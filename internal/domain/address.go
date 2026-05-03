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
	Address string `json:"address"` // например 203.0.113.X
	ZoneID  string `json:"zone_id"`
}

// InternalIpv4Spec — параметры внутреннего IPv4-адреса.
type InternalIpv4Spec struct {
	Address  string `json:"address"`   // например 10.0.0.X
	SubnetID string `json:"subnet_id"`
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
