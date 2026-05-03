package domain

import "time"

// SecurityGroupRule — правило группы безопасности.
type SecurityGroupRule struct {
	ID           string
	Direction    string // INGRESS | EGRESS
	Protocol     string // TCP | UDP | ICMP | ANY
	PortRangeMin int32
	PortRangeMax int32
	CIDRBlocks   []string
	Description  string
}

// SecurityGroup — группа безопасности, привязана к Network.
type SecurityGroup struct {
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
	DisplayName       string
	Description       string
	Rules             []SecurityGroupRule
	State             string
}
