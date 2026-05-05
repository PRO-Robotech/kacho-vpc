package domain

import "time"

// PrivateEndpoint — PrivateLink endpoint к internal services (Object Storage etc).
//
// См. https://yandex.cloud/ru/docs/vpc/api-ref/PrivateEndpoint/.
type PrivateEndpoint struct {
	ID          string
	FolderID    string
	CreatedAt   time.Time
	Name        string
	Description string
	Labels      map[string]string

	NetworkID string
	SubnetID  string
	AddressID string
	IPAddress string

	// ServiceType — sentinel oneof. Сейчас только "object_storage".
	ServiceType string
	// DnsOptions — JSON-blob с опциями DNS (private_dns_records_enabled).
	DnsOptions map[string]any
	// Status — PENDING / AVAILABLE / DELETING.
	Status string
}
