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

// NetworkInterfaceStatus / NI* константы определены рядом с domain.NetworkInterface
// (см. network_interface.go) — pilot KAC-99 для NIC ещё не делается, оставляем
// их там. На Wave 2 iteration для NIC они переедут сюда.
