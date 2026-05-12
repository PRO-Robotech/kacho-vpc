package domain

import "time"

// Network — сетевой ресурс.
type Network struct {
	ID                     string
	FolderID               string
	CreatedAt              time.Time
	Name                   string
	Description            string
	Labels                 map[string]string
	DefaultSecurityGroupID string
	// VPNID — 24-bit data-plane идентификатор сети (1..16777215). Аллоцируется
	// kacho-vpc при создании, стабилен, переиспользуется free-list'ом на Delete.
	// ИНФРА-ЧУВСТВИТЕЛЬНОЕ — отдаётся только через InternalNetworkService.GetNetwork,
	// не на публичном Network-message.
	VPNID uint32
}
