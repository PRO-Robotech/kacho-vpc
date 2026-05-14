package domain

import (
	"time"
)

// DhcpOptions — опции DHCP для подсети.
type DhcpOptions struct {
	DomainNameServers []string `json:"domain_name_servers,omitempty"`
	DomainName        string   `json:"domain_name,omitempty"`
	NtpServers        []string `json:"ntp_servers,omitempty"`
}

// Subnet — подсеть.
type Subnet struct {
	ID           string
	FolderID     string
	CreatedAt    time.Time
	Name         string
	Description  string
	Labels       map[string]string
	NetworkID    string
	ZoneID       string
	V4CidrBlocks []string // repeated string в YC
	V6CidrBlocks []string // OUTPUT_ONLY ipv6
	RouteTableID string
	DhcpOptions  *DhcpOptions
}
