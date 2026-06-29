// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package domain

// Magic-numbers и enum-константы для domain-слоя (запрет inline-status и
// inline-magic-numbers — выносим в именованные константы).

// ShortIDLen — длина prefix-а ресурс-id, используемого при построении
// derived-имен (например default-sg-<8chars>).
const ShortIDLen = 8

// TruncateID возвращает первые ShortIDLen символов id (или весь id если он
// короче). Используется builder'ами имен вида "default-sg-<short>".
func TruncateID(id string) string {
	if len(id) > ShortIDLen {
		return id[:ShortIDLen]
	}
	return id
}

// SecurityGroupRuleDirection — направление SG-правила (INGRESS/EGRESS).
// Используется builder'ом NewDefaultSecurityGroupRules + sync-валидацией в
// service-слое (validateSGRule).
type SecurityGroupRuleDirection string

const (
	SecurityGroupRuleDirectionIngress SecurityGroupRuleDirection = "INGRESS"
	SecurityGroupRuleDirectionEgress  SecurityGroupRuleDirection = "EGRESS"
)

// ---- GatewayType -------------------------------------------------------------

// GatewayType — sentinel для Gateway.oneof spec; сейчас поддержан один тип
// (shared_egress). Не string-literal, а enum-константа.
type GatewayType string

const (
	// GatewayTypeSharedEgress — единственный поддерживаемый Gateway oneof
	// (SharedEgressGateway: NAT gateway для исходящего трафика).
	GatewayTypeSharedEgress GatewayType = "shared_egress"
)

// ---- NetworkInterfaceStatus --------------------------------------------------

// NetworkInterfaceStatus — грубый статус NIC (зеркалит vpcv1.NetworkInterface_Status).
type NetworkInterfaceStatus int

// Значения NetworkInterfaceStatus. STATUS_UNSPECIFIED — для legacy rows (DB-layer
// возвращает его если status-колонка пустая или содержит неизвестное значение).
const (
	NIStatusUnspecified NetworkInterfaceStatus = iota
	NIStatusProvisioning
	NIStatusActive
	NIStatusAvailable
	NIStatusFailed
	NIStatusDeleting
)

// String-значения NetworkInterfaceStatus для DB-CHECK constraint и DB-маппинга
// (network_interfaces.status TEXT). Используется в маппинге
// internal/repo/helpers/nic.go (NIStatusName / NIStatusFromName), в DTO
// toproto/network_interface.go и в CHECK-constraint
// network_interfaces_status_check (0001_initial.sql).
const (
	NIStatusStrProvisioning = "PROVISIONING"
	NIStatusStrActive       = "ACTIVE"
	NIStatusStrAvailable    = "AVAILABLE"
	NIStatusStrFailed       = "FAILED"
	NIStatusStrDeleting     = "DELETING"
	NIStatusStrUnspecified  = "STATUS_UNSPECIFIED"
)
