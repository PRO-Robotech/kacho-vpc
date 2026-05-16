// Package kacho — repo-leaf entities (per-resource DTO между domain и SQL-схемой
// kacho_vpc). Здесь живут структуры типа `<X>Record`, представляющие
// «row из таблицы + DB-managed поля» (`CreatedAt`, в будущем — `UpdatedAt`,
// `Generation`).
//
// Зачем отдельный пакет:
//   - skill evgeniy §4 D.1 / §6 G.2 / §7 H.1: `CreatedAt` — DB-managed, не часть
//     domain-сущности. `<X>Record = domain.X + CreatedAt` физически живёт рядом
//     с repo-имплементацией (этим пакетом), а не в domain (domain — чистый, без
//     знания про SQL/DB).
//   - правило D.1 уточняет: repo-entity должна жить в repo-leaf
//     (`internal/repo/<resource>/entity.go` или общий `internal/repo/kacho/`).
//     Выбран общий `internal/repo/kacho/` — все 8 Record-структур в одном
//     leaf-пакете, без под-папок per-resource (минимизирует количество
//     packages под Clean Architecture). Имя `kacho` — отсылает на target-структуру
//     `internal/repo/kacho/pg/` из skill §1 A.3.
//
// Dependency rule:
//
//	dto/type2pb → repo/kacho → domain
//	apps/kacho/api/<res>/{ports,handler,helpers,...} → repo/kacho → domain
//	repo (pgxpool implementations) → repo/kacho → domain
//	cmd/vpc/main.go → repo/kacho (через repo-implementations)
//
// Импорт: только stdlib `time` + `internal/domain`. Никаких pgx/grpc/proto.
package kacho

import (
	"time"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// NetworkRecord — repo-entity для Network. domain.Network + CreatedAt
// (DB-managed). Service-слой получает *NetworkRecord из репозитория (port
// `NetworkRepo` в `internal/service` / `internal/apps/kacho/api/network`) и
// пробрасывает в DTO/handler. Через proto клиенту уходит CreatedAt из этой
// структуры (truncate до секунд — verbatim YC, см. `dto/type2pb/time.go`).
//
// Skill evgeniy §4 D.1 / §7 H.1.
type NetworkRecord struct {
	domain.Network
	CreatedAt time.Time
}
