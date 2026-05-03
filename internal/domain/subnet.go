package domain

import "time"

// SubnetStatus — статус подсети.
type SubnetStatus int

const (
	SubnetStatusUnspecified  SubnetStatus = 0
	SubnetStatusProvisioning SubnetStatus = 1
	SubnetStatusActive       SubnetStatus = 2
	SubnetStatusDeleting     SubnetStatus = 3
)

// SubnetStatusString — строковые значения статусов подсети.
var SubnetStatusString = map[SubnetStatus]string{
	SubnetStatusUnspecified:  "SUBNET_STATUS_UNSPECIFIED",
	SubnetStatusProvisioning: "SUBNET_STATUS_PROVISIONING",
	SubnetStatusActive:       "SUBNET_STATUS_ACTIVE",
	SubnetStatusDeleting:     "SUBNET_STATUS_DELETING",
}

// ParseSubnetStatus парсит строку статуса.
func ParseSubnetStatus(s string) SubnetStatus {
	for k, v := range SubnetStatusString {
		if v == s {
			return k
		}
	}
	return SubnetStatusUnspecified
}

// Subnet — подсеть внутри Network (sub-phase 1.0).
type Subnet struct {
	ID                     string
	FolderID               string
	NetworkID              string
	ZoneID                 string
	CIDRBlock              string
	Name                   string
	Description            string
	CreatedAt              time.Time
	Labels                 map[string]string
	Status                 SubnetStatus
	Generation             int64
	ResourceVersion        string
	ObservedGeneration     int64
	StatusLastTransitionAt time.Time
	DeletedAt              *time.Time
}
