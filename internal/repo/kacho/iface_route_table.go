package kacho

import (
	"context"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// RouteTableFilter — фильтр для списка таблиц маршрутизации. Wave 5 replicate
// (KAC-94 D.1): по аналогии с NetworkFilter/SecurityGroupFilter перенесён в
// leaf-пакет `kacho`. В `internal/repo/iface.go` остался type-alias
// `RouteTableFilter = kacho.RouteTableFilter`.
type RouteTableFilter struct {
	ProjectID string
	NetworkID string
	Name      string
	Filter    string
}

// RouteTableReaderIface — read-операции над RouteTable в read-only TX-области.
// Parity с NetworkReaderIface (KAC-94 Wave 5).
type RouteTableReaderIface interface {
	Get(ctx context.Context, id string) (*RouteTableRecord, error)
	List(ctx context.Context, f RouteTableFilter, p Pagination) ([]*RouteTableRecord, string, error)
	// ListByNetwork — узкий read для checkNetworkEmpty (Network.Delete) и
	// ListRouteTables(byNetwork). Реализован поверх List с filter NetworkID.
	ListByNetwork(ctx context.Context, networkID string, p Pagination) ([]*RouteTableRecord, string, error)
}

// RouteTableWriterIface — write-операции + read (writer extends reader, G.2).
//
// DML-методы НЕ открывают свою TX и НЕ emit'ят outbox — это делает caller
// (use-case) через `RepositoryWriter.Outbox().Emit(...)` после успешного DML.
// Atomicity DML + outbox гарантируется тем, что обе операции идут через одну
// pgx.Tx (writer-instance) — parity с NetworkWriterIface / SecurityGroupWriterIface.
type RouteTableWriterIface interface {
	RouteTableReaderIface
	Insert(ctx context.Context, rt *domain.RouteTable) (*RouteTableRecord, error)
	Update(ctx context.Context, rt *domain.RouteTable) (*RouteTableRecord, error)
	Delete(ctx context.Context, id string) error
	// SetProjectID меняет project_id у RouteTable (для :move).
	SetProjectID(ctx context.Context, id, folderID string) (*RouteTableRecord, error)
}
