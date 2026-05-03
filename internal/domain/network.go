package domain

import "time"

// NetworkStatus — статус сети.
type NetworkStatus int

const (
	NetworkStatusUnspecified  NetworkStatus = 0
	NetworkStatusProvisioning NetworkStatus = 1
	NetworkStatusActive       NetworkStatus = 2
	NetworkStatusDeleting     NetworkStatus = 3
)

// NetworkStatusString — строковые значения статусов сети.
var NetworkStatusString = map[NetworkStatus]string{
	NetworkStatusUnspecified:  "NETWORK_STATUS_UNSPECIFIED",
	NetworkStatusProvisioning: "NETWORK_STATUS_PROVISIONING",
	NetworkStatusActive:       "NETWORK_STATUS_ACTIVE",
	NetworkStatusDeleting:     "NETWORK_STATUS_DELETING",
}

// ParseNetworkStatus парсит строку статуса.
func ParseNetworkStatus(s string) NetworkStatus {
	for k, v := range NetworkStatusString {
		if v == s {
			return k
		}
	}
	return NetworkStatusUnspecified
}

// Network — виртуальная сеть Kachō VPC (sub-phase 1.0).
// Плоская структура: нет envelope metadata/spec/status.
type Network struct {
	ID                      string
	FolderID                string
	Name                    string
	Description             string
	CreatedAt               time.Time
	Labels                  map[string]string
	Status                  NetworkStatus
	Generation              int64
	ResourceVersion         string
	ObservedGeneration      int64
	StatusLastTransitionAt  time.Time
	DeletedAt               *time.Time
}
