package kacho

import (
	"context"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
)

// Type-aliases на value-объекты `internal/repo`. Каждый use-case-пакет уже
// делает то же (`internal/apps/kacho/api/network/repo.go`) — мы повторяем
// чтобы pilot-CQRS-interfaces не тянули зависимость от use-case-пакетов
// (cycles). Иначе пришлось бы переносить Pagination / *Filter сюда.
type (
	Pagination    = repo.Pagination
	NetworkFilter = repo.NetworkFilter
)

// NetworkReaderIface — read-операции над Network в read-only TX-области.
type NetworkReaderIface interface {
	Get(ctx context.Context, id string) (*domain.NetworkRecord, error)
	List(ctx context.Context, f NetworkFilter, p Pagination) ([]*domain.NetworkRecord, string, error)
}

// NetworkWriterIface — write-операции + read (writer видит свои writes, G.2).
//
// DML-методы НЕ открывают свою TX и НЕ emit'ят outbox — это делает caller
// (use-case) через `RepositoryWriter.Outbox().Emit(...)` после успешного DML.
// Atomicity DML + outbox гарантируется тем, что обе операции идут через одну
// pgx.Tx (writer-instance).
type NetworkWriterIface interface {
	NetworkReaderIface
	Insert(ctx context.Context, n *domain.Network) (*domain.NetworkRecord, error)
	Update(ctx context.Context, n *domain.Network) (*domain.NetworkRecord, error)
	SetFolderID(ctx context.Context, id, folderID string) (*domain.NetworkRecord, error)
	Delete(ctx context.Context, id string) error
}
