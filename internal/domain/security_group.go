package domain

import "time"

// SecurityGroup — domain-сущность Security Group.
// Поля плоские; Rules embedded (JSONB в БД).
type SecurityGroup struct {
	ID                string
	FolderID          string
	NetworkID         string
	CreatedAt         time.Time
	Name              string
	Description       string
	Labels            map[string]string
	Status            string // CREATING / ACTIVE / UPDATING / DELETING (verbatim YC)
	DefaultForNetwork bool
	Rules             []SecurityGroupRule
}

// SecurityGroupRule — встроенное правило SG.
type SecurityGroupRule struct {
	ID             string
	Description    string
	Labels         map[string]string
	Direction      string // INGRESS / EGRESS
	FromPort       int64  // -1 = any
	ToPort         int64  // -1 = any
	ProtocolName   string // ANY если оба пусто/0
	ProtocolNumber int64
	V4CidrBlocks   []string
	V6CidrBlocks   []string
	// Для упрощения: только cidrBlocks; SG-target / predefined-target — TODO в следующей итерации.
	SecurityGroupID  string
	PredefinedTarget string
}
