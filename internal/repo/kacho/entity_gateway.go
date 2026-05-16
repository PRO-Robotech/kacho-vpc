// Wave 5 replicate (KAC-94, skill evgeniy §4 D.1 / §7 H.1): GatewayRecord
// уехал из `domain/persistence.go` в repo-leaf — parity с pilot
// NetworkRecord (KAC-94 batch). `CreatedAt` — DB-managed, поэтому физически
// живёт в repo-проекции, не в domain-сущности (которая описывает только
// «намерение» / CRUD-payload).
//
// Импорт-граф: `internal/repo/kacho` импортирует `internal/domain` (parity с
// `entity_network.go`); никаких pgx/grpc/proto в этом пакете — он остаётся
// «leaf-обёрткой» над domain.

package kacho

import (
	"time"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// GatewayRecord — repo-entity для Gateway. domain.Gateway + CreatedAt
// (DB-managed). Service-слой / use-case-слой получает *GatewayRecord из
// репозитория (port `GatewayRepo` в `internal/apps/kacho/api/gateway`) и
// пробрасывает в DTO/handler. Через proto клиенту уходит CreatedAt из этой
// структуры (truncate до секунд — verbatim YC, см. `dto/toproto/time.go`).
//
// Skill evgeniy §4 D.1 / §7 H.1.
type GatewayRecord struct {
	domain.Gateway
	CreatedAt time.Time
}
