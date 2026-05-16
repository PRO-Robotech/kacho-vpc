package kacho

import (
	"context"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// Pagination — постраничная навигация. Wave 5 (KAC-94 D.1) перенесён из
// `internal/repo/iface.go` в leaf-пакет `kacho`, чтобы избежать import-cycle
// `repo → repo/kacho → repo` (поскольку `repo.NetworkRepoIface` теперь
// возвращает `*kacho.NetworkRecord`, и `repo.Network` — alias на него).
// В `internal/repo/iface.go` остался тонкий type-alias `Pagination = kacho.Pagination`.
type Pagination struct {
	PageToken string
	PageSize  int64
}

// NetworkFilter — фильтр для списка сетей.
//
// Wave 5 (KAC-94 D.1): перенесён в leaf-пакет `kacho` вместе с Pagination —
// см. doc-комментарий на Pagination выше.
type NetworkFilter struct {
	FolderID string
	Name     string
	// Filter — raw filter expression (YC-syntax: `name="<value>"`).
	// Парсится в repo с whitelist allowedFields=["name"].
	Filter string
}

// NetworkReaderIface — read-операции над Network в read-only TX-области.
type NetworkReaderIface interface {
	Get(ctx context.Context, id string) (*NetworkRecord, error)
	List(ctx context.Context, f NetworkFilter, p Pagination) ([]*NetworkRecord, string, error)
}

// NetworkWriterIface — write-операции + read (writer видит свои writes, G.2).
//
// DML-методы НЕ открывают свою TX и НЕ emit'ят outbox — это делает caller
// (use-case) через `RepositoryWriter.Outbox().Emit(...)` после успешного DML.
// Atomicity DML + outbox гарантируется тем, что обе операции идут через одну
// pgx.Tx (writer-instance).
type NetworkWriterIface interface {
	NetworkReaderIface
	Insert(ctx context.Context, n *domain.Network) (*NetworkRecord, error)
	Update(ctx context.Context, n *domain.Network) (*NetworkRecord, error)
	SetFolderID(ctx context.Context, id, folderID string) (*NetworkRecord, error)
	Delete(ctx context.Context, id string) error
	// SetDefaultSGID атомарно проставляет networks.default_security_group_id для
	// конкретной сети. Wave 5 batch 33/34 (KAC-94, skill evgeniy I.9/I.10):
	// узкий update-помощник для atomic default-SG-creation в Network.Create
	// (Insert(Network) → Insert(SG) → SetDefaultSGID — всё в одной writer-TX,
	// без дополнительного `UPDATE networks SET name=…, description=…, …`
	// верхнеуровневого Update, который перезаписывал бы immutable-поля).
	SetDefaultSGID(ctx context.Context, id, sgID string) (*NetworkRecord, error)
}
