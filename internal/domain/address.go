package domain

import "time"

// AddressStatus — статус публичного IP-адреса.
type AddressStatus int

const (
	AddressStatusUnspecified AddressStatus = 0
	AddressStatusReserved    AddressStatus = 1
	AddressStatusInUse       AddressStatus = 2
	AddressStatusReleased    AddressStatus = 3
)

// AddressStatusString — строковые значения статусов адреса.
var AddressStatusString = map[AddressStatus]string{
	AddressStatusUnspecified: "ADDRESS_STATUS_UNSPECIFIED",
	AddressStatusReserved:    "ADDRESS_STATUS_RESERVED",
	AddressStatusInUse:       "ADDRESS_STATUS_IN_USE",
	AddressStatusReleased:    "ADDRESS_STATUS_RELEASED",
}

// ParseAddressStatus парсит строку статуса.
func ParseAddressStatus(s string) AddressStatus {
	for k, v := range AddressStatusString {
		if v == s {
			return k
		}
	}
	return AddressStatusUnspecified
}

// Address — публичный IP-адрес (sub-phase 1.0, только EXTERNAL).
type Address struct {
	ID          string
	FolderID    string
	Name        string
	Description string
	CreatedAt   time.Time
	Labels      map[string]string
	AddressType string // ADDRESS_TYPE_EXTERNAL
	ZoneID      string
	// AllocatedIPv4 — server-allocated из 203.0.113.0/24 (TEST-NET-3).
	AllocatedIPv4 string
	Status        AddressStatus
	DeletedAt     *time.Time
}
