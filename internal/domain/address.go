package domain

import "time"

// Address — публичный IP-адрес (EXTERNAL only в sub-phase 0.3).
type Address struct {
	UID               string
	FolderID          string
	CloudID           string
	OrganizationID    string
	Name              string
	Labels            map[string]string
	Annotations       map[string]string
	CreationTimestamp time.Time
	ResourceVersion   int64
	Generation        int64
	DeletionTimestamp *time.Time
	Finalizers        []string
	AddressType       string // EXTERNAL
	ZoneID            string
	DisplayName       string
	Description       string
	State             string // RESERVED | IN_USE | RELEASED
	AllocatedIPv4     string
}
