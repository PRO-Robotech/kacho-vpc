package kacho

import (
	"time"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// SecurityGroupRecord — repo-entity для SecurityGroup. domain.SecurityGroup +
// CreatedAt (DB-managed).
//
// Wave 5 (KAC-94, skill evgeniy §4 D.1): переехал из `internal/domain/persistence.go`
// в repo-leaf — parity с NetworkRecord/SubnetRecord/AddressRecord/RouteTableRecord/
// GatewayRecord/PrivateEndpointRecord/NetworkInterfaceRecord. CreatedAt живёт в
// repo-проекции, не в domain (skill §4 D.1 — domain-сущность не несёт DB-managed
// полей, чтобы оставаться чистой domain-логикой).
//
// Service-/use-case-слой получает *SecurityGroupRecord из репозитория (CQRS-iface
// `SecurityGroupReaderIface` / `SecurityGroupWriterIface` в `iface_security_group.go`),
// пишет в репо `*domain.SecurityGroup` (без CreatedAt — заполняется БД).
type SecurityGroupRecord struct {
	domain.SecurityGroup
	CreatedAt time.Time
	// UsedBy — KAC-239 S2: потребители SG (output-only, derived-on-read; не
	// персистится в security_groups). Заполняется reader'ом через UsedBy(ctx,id)
	// в Get; в List опускается (per-row scan дорог; UI читает used_by на detail).
	UsedBy []domain.SecurityGroupReference
}
