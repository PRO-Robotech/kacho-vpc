package kacho

import (
	"time"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// NetworkInterfaceRecord — repo-entity для NetworkInterface. domain.NetworkInterface
// + CreatedAt (DB-managed).
//
// Wave 5 (KAC-94 D.1): репликация pilot'а NetworkRecord. Live-копия CreatedAt
// убрана из `domain/persistence.go` — теперь живёт в repo-leaf вместе с
// Reader/Writer-iface. См. doc-комментарий на NetworkRecord (entity_network.go).
//
// NIC — самый сложный ресурс эпика: attach-race protection (KAC-52 atomic CAS),
// MAC-allocation с UNIQUE-constraint, v4/v6 cardinality CHECK (миграция 0018),
// ON DELETE RESTRICT каскад, used_by мирроринг. Repo-leaf не «знает» про эти
// инварианты семантически — они на DB-уровне (workspace CLAUDE.md §«Within-service
// refs — DB-уровень обязателен», запрет #10); repo-entity несёт только
// row-snapshot.
type NetworkInterfaceRecord struct {
	domain.NetworkInterface
	CreatedAt time.Time
}
