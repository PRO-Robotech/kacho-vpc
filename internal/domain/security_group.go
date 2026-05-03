package domain

import "time"

// SecurityGroupStatus — статус группы безопасности.
type SecurityGroupStatus int

const (
	SecurityGroupStatusUnspecified  SecurityGroupStatus = 0
	SecurityGroupStatusProvisioning SecurityGroupStatus = 1
	SecurityGroupStatusActive       SecurityGroupStatus = 2
	SecurityGroupStatusDeleting     SecurityGroupStatus = 3
)

// SecurityGroupStatusString — строковые значения статусов группы безопасности.
var SecurityGroupStatusString = map[SecurityGroupStatus]string{
	SecurityGroupStatusUnspecified:  "SECURITY_GROUP_STATUS_UNSPECIFIED",
	SecurityGroupStatusProvisioning: "SECURITY_GROUP_STATUS_PROVISIONING",
	SecurityGroupStatusActive:       "SECURITY_GROUP_STATUS_ACTIVE",
	SecurityGroupStatusDeleting:     "SECURITY_GROUP_STATUS_DELETING",
}

// ParseSecurityGroupStatus парсит строку статуса.
func ParseSecurityGroupStatus(s string) SecurityGroupStatus {
	for k, v := range SecurityGroupStatusString {
		if v == s {
			return k
		}
	}
	return SecurityGroupStatusUnspecified
}

// SecurityGroupRule — одно правило фильтрации трафика.
type SecurityGroupRule struct {
	// ID server-assigned UUID правила.
	ID           string
	Direction    string // INGRESS / EGRESS
	Protocol     string // TCP / UDP / ICMP / ANY
	PortRangeMin int32
	PortRangeMax int32
	CIDRBlocks   []string
	Description  string
}

// SecurityGroup — группа правил фильтрации трафика (sub-phase 1.0).
// rules[] хранятся как JSONB внутри строки; Update — full-replace.
type SecurityGroup struct {
	ID                     string
	FolderID               string
	NetworkID              string
	Name                   string
	Description            string
	CreatedAt              time.Time
	Labels                 map[string]string
	Status                 SecurityGroupStatus
	Generation             int64
	ResourceVersion        string
	Rules                  []SecurityGroupRule
	DeletedAt              *time.Time
}
