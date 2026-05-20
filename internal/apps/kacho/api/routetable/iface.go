// Package routetable — use-case-структура ресурса RouteTable (skill evgeniy §2 B.1-B.4).
//
// Wave 3b (KAC-94): replicate Wave 3a pilot Network → RouteTable. Бизнес-логика
// CreateRouteTableUseCase / UpdateRouteTableUseCase / DeleteRouteTableUseCase /
// MoveRouteTableUseCase / GetRouteTableUseCase / ListRouteTablesUseCase / ListOperationsUseCase
// плюс тонкий gRPC-handler.
//
// Wave 5 replicate (KAC-94, skill evgeniy §6 G.1-G.7): RouteTable use-case'ы
// перенесены на CQRS-Repository (parity с pilot Network). Каждый use-case
// открывает TX явно (`u.repo.Writer(ctx)` или `Reader(ctx)`), outbox-emit лежит
// в той же tx writer'а — атомарность DML + outbox гарантирована (G.5).
//
// ⚠️ Auto-association (KAC-56, миграция 0019): DB-уровневые PL/pgSQL триггеры
// AFTER INSERT ON route_tables auto-assoc'ят Subnet'ы с `route_table_id IS NULL`
// и эмитят `Subnet.UPDATED` события с маркером `auto_association: true`.
// CQRS-Insert просто делает INSERT — триггер срабатывает в БД, дополнительные
// outbox-события пишутся БД, use-case не управляет ими.
package routetable

import (
	"context"

	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// Pagination, RouteTableFilter — пере-используем единые value-объекты `internal/repo`.
type (
	Pagination       = repo.Pagination
	RouteTableFilter = repo.RouteTableFilter
)

// Re-export CQRS-Repository типов из `internal/repo/kacho` — use-case-код
// работает с ними под коротким именем (`Repo` / `Reader` / `Writer`). Type-alias
// (не type wrap) — тип взаимозаменяем с источником, никаких shim'ов.
//
// Wave 5 replicate (KAC-94): RouteTable теперь работает через CQRS-Repository
// (parity с pilot Network).
type (
	Repo                  = kacho.Repository
	Reader                = kacho.RepositoryReader
	Writer                = kacho.RepositoryWriter
	RouteTableReaderIface = kacho.RouteTableReaderIface
	RouteTableWriterIface = kacho.RouteTableWriterIface
	OutboxEmitter         = kacho.OutboxEmitter
)

// ProjectClient — то, что use-case'ам RouteTable нужно от peer-сервиса
// kacho-iam.
type ProjectClient interface {
	Exists(ctx context.Context, folderID string) (bool, error)
}
