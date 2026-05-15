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
}
