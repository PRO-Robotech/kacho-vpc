package domain

import "time"

// Gateway — NAT Gateway ресурс (shared egress).
//
// В YC Gateway имеет oneof spec (shared_egress_gateway), но мы храним только
// единственный поддерживаемый тип через GatewayType. См.
// https://yandex.cloud/ru/docs/vpc/api-ref/Gateway/.
type Gateway struct {
	ID          string
	FolderID    string
	CreatedAt   time.Time
	Name        string
	Description string
	Labels      map[string]string
	// GatewayType — sentinel для oneof. Сейчас только "shared_egress".
	GatewayType string
}
