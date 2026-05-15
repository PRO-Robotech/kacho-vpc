package domain

// Magic-numbers и enum-константы для domain-слоя. Skill evgeniy §4 D.8 / D.9,
// AP-2 / AP-4 (запрет inline-status / inline-magic-numbers).

// ShortIDLen — длина prefix-а ресурс-id, используемого при построении
// derived-имён (например default-sg-<8chars>). Раньше был magic `8`
// inline'ом в `service/network.go::doCreate`.
const ShortIDLen = 8

// TruncateID возвращает первые ShortIDLen символов id (или весь id если он
// короче). Используется builder'ами имён вида "default-sg-<short>".
func TruncateID(id string) string {
	if len(id) > ShortIDLen {
		return id[:ShortIDLen]
	}
	return id
}

// ---- SecurityGroupStatus -----------------------------------------------------

// SecurityGroupStatus — статус SG (verbatim YC: CREATING/ACTIVE/UPDATING/DELETING).
type SecurityGroupStatus string

const (
	SecurityGroupStatusActive   SecurityGroupStatus = "ACTIVE"
	SecurityGroupStatusCreating SecurityGroupStatus = "CREATING"
	SecurityGroupStatusUpdating SecurityGroupStatus = "UPDATING"
	SecurityGroupStatusDeleting SecurityGroupStatus = "DELETING"
)

// SecurityGroupRuleDirection — направление SG-правила (verbatim YC: INGRESS/EGRESS).
// Используется builder'ом NewDefaultSecurityGroupRules + sync-валидацией в
// service-слое (validateSGRule). Wave 2 batch B (KAC-94).
type SecurityGroupRuleDirection string

const (
	SecurityGroupRuleDirectionIngress SecurityGroupRuleDirection = "INGRESS"
	SecurityGroupRuleDirectionEgress  SecurityGroupRuleDirection = "EGRESS"
)

// ---- GatewayType -------------------------------------------------------------

// GatewayType — sentinel для Gateway.oneof spec. В YC verbatim — только один тип
// (shared_egress). Не string-literal, а enum-константа — skill evgeniy §4 D.8.
type GatewayType string

const (
	// GatewayTypeSharedEgress — единственный поддерживаемый Gateway oneof в YC
	// и kacho-vpc (SharedEgressGateway: NAT gateway для исходящего трафика).
	GatewayTypeSharedEgress GatewayType = "shared_egress"
)

// ---- PrivateEndpointServiceType ---------------------------------------------

// PrivateEndpointServiceType — sentinel для PrivateEndpoint.oneof service.
// Сейчас в YC verbatim — только object_storage. Wave 2 batch B (KAC-94).
type PrivateEndpointServiceType string

const (
	// PrivateEndpointServiceTypeObjectStorage — PrivateLink endpoint к Object Storage.
	PrivateEndpointServiceTypeObjectStorage PrivateEndpointServiceType = "object_storage"
)

// ---- PrivateEndpointStatus ---------------------------------------------------

// PrivateEndpointStatus — статус PrivateEndpoint (verbatim YC: PENDING/AVAILABLE/DELETING).
type PrivateEndpointStatus string

const (
	PrivateEndpointStatusPending   PrivateEndpointStatus = "PENDING"
	PrivateEndpointStatusAvailable PrivateEndpointStatus = "AVAILABLE"
	PrivateEndpointStatusDeleting  PrivateEndpointStatus = "DELETING"
)

// ---- NetworkInterfaceStatus --------------------------------------------------

// NetworkInterfaceStatus — грубый статус NIC (зеркалит vpcv1.NetworkInterface_Status).
// Wave 2 batch C (KAC-94, skill evgeniy §4 D.8): переехал из network_interface.go
// сюда вместе с остальными enum-константами. iota → string-typed enum для parity
// с другими VPC-ресурсами (SecurityGroupStatus / PrivateEndpointStatus).
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
// (network_interfaces.status TEXT). Используется в repo.niStatusName /
// niStatusFromName, в DTO type2pb/network_interface.go и в миграции CHECK
// constraint (0032_network_interface_check_constraints.sql).
const (
	NIStatusStrProvisioning = "PROVISIONING"
	NIStatusStrActive       = "ACTIVE"
	NIStatusStrAvailable    = "AVAILABLE"
	NIStatusStrFailed       = "FAILED"
	NIStatusStrDeleting     = "DELETING"
	NIStatusStrUnspecified  = "STATUS_UNSPECIFIED"
)
