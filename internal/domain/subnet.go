package domain

import "time"

// Subnet — подсеть внутри Network.
type Subnet struct {
	UID               string
	NetworkID         string
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
	CIDRBlock         string
	ZoneID            string
	DisplayName       string
	Description       string
	State             string
}
