package kacho

import (
	"time"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// AddressRecord — repo-entity для Address. domain.Address + CreatedAt (DB-managed).
//
// Wave 5 replicate (KAC-94, skill evgeniy §4 D.1 / §6 G.2 / §7 H.1): уехал из
// `domain.AddressRecord` в repo-leaf — CreatedAt живёт в repo-проекции, не в
// domain.Address. Parity с `kacho.NetworkRecord` (entity_network.go).
//
// Service-/use-case-слой получает *AddressRecord из репозитория (port
// `AddressRepo` в `internal/apps/kacho/api/address`) и пробрасывает в DTO/
// handler. Через proto клиенту уходит CreatedAt из этой структуры (truncate
// до секунд — verbatim YC, см. `dto/toproto/time.go`).
type AddressRecord struct {
	domain.Address
	CreatedAt time.Time
}
