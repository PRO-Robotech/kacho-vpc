package kacho

import (
	"context"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// GatewayFilter — фильтр для списка NAT Gateways.
//
// Wave 5 replicate (KAC-94 D.1): перенесён в leaf-пакет `kacho` рядом с
// Pagination / NetworkFilter / SecurityGroupFilter — parity с pilot Network.
// В `internal/repo/iface.go` остаётся тонкий type-alias
// `GatewayFilter = kacho.GatewayFilter`.
type GatewayFilter struct {
	ProjectID string
	Name     string
	// Filter — raw filter expression (YC-syntax: `name="<value>"`). Парсится
	// в repo с whitelist allowedFields=["name"].
	Filter string
}

// GatewayReaderIface — read-операции над Gateway в TX-области.
type GatewayReaderIface interface {
	Get(ctx context.Context, id string) (*GatewayRecord, error)
	List(ctx context.Context, f GatewayFilter, p Pagination) ([]*GatewayRecord, string, error)
}

// GatewayWriterIface — write-операции + read (writer видит свои writes, G.2).
//
// DML-методы НЕ открывают свою TX и НЕ emit'ят outbox — это делает caller
// (use-case) через `RepositoryWriter.Outbox().Emit(...)` после успешного DML.
// Atomicity DML + outbox гарантируется тем, что обе операции идут через одну
// pgx.Tx (writer-instance) — parity с NetworkWriterIface / SecurityGroupWriterIface.
//
// Wave 5 replicate (KAC-94): Gateway переезжает на CQRS вслед за pilot Network
// и batch 33/34 SecurityGroup.
type GatewayWriterIface interface {
	GatewayReaderIface
	Insert(ctx context.Context, g *domain.Gateway) (*GatewayRecord, error)
	Update(ctx context.Context, g *domain.Gateway) (*GatewayRecord, error)
	Delete(ctx context.Context, id string) error
	// SetProjectID меняет project_id у Gateway (для :move).
	SetProjectID(ctx context.Context, id, folderID string) (*GatewayRecord, error)
}
