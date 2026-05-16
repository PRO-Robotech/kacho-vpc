package kacho

import (
	"time"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// RouteTableRecord — repo-entity для RouteTable. domain.RouteTable + CreatedAt
// (DB-managed). Wave 5 replicate (KAC-94, skill evgeniy §4 D.1 / §6 G.2 /
// §7 H.1): переезжает из `internal/domain/persistence.go` в repo-leaf —
// parity с NetworkRecord (KAC-94 Wave 5).
//
// Dependency rule — см. doc-комментарий пакета в `entity_network.go`.
type RouteTableRecord struct {
	domain.RouteTable
	CreatedAt time.Time
}
