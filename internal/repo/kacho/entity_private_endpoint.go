package kacho

import (
	"time"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// PrivateEndpointRecord — repo-entity для PrivateEndpoint. domain.PrivateEndpoint
// + CreatedAt (DB-managed).
//
// Wave 5 replicate (KAC-94, skill evgeniy §4 D.1 / §6 G.2 / §7 H.1): уехал из
// `domain.PrivateEndpointRecord` в repo-leaf — CreatedAt живёт в repo-проекции,
// не в domain.PrivateEndpoint. Parity с `kacho.NetworkRecord`/`kacho.AddressRecord`.
//
// Service-/use-case-слой получает *PrivateEndpointRecord из репозитория (port
// `PrivateEndpointRepo` в `internal/apps/kacho/api/privateendpoint`) и
// пробрасывает в DTO/handler. Через proto клиенту уходит CreatedAt из этой
// структуры (truncate до секунд — verbatim YC, см. `dto/toproto/time.go`).
type PrivateEndpointRecord struct {
	domain.PrivateEndpoint
	CreatedAt time.Time
}
