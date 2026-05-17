package kacho

import (
	"context"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// PrivateEndpointFilter — фильтр для списка PrivateEndpoints.
//
// Wave 5 replicate (KAC-94, skill evgeniy §6 G.1-G.7): перенесён в leaf-пакет
// `kacho` вместе с Pagination/NetworkFilter/SecurityGroupFilter, чтобы избежать
// import-cycle `repo → repo/kacho → repo`. В `internal/repo/iface.go` остался
// тонкий type-alias `PrivateEndpointFilter = kacho.PrivateEndpointFilter`.
type PrivateEndpointFilter struct {
	ProjectID string
	Name     string
	Filter   string
}

// PrivateEndpointReaderIface — read-операции над PrivateEndpoint в TX-области.
type PrivateEndpointReaderIface interface {
	Get(ctx context.Context, id string) (*PrivateEndpointRecord, error)
	List(ctx context.Context, f PrivateEndpointFilter, p Pagination) ([]*PrivateEndpointRecord, string, error)
}

// PrivateEndpointWriterIface — write-операции + read (G.2 — writer видит свои writes).
//
// DML-методы НЕ открывают свою TX и НЕ emit'ят outbox — это делает caller
// (use-case) через `RepositoryWriter.Outbox().Emit(...)` после успешного DML.
// Atomicity DML + outbox гарантируется тем, что обе операции идут через одну
// pgx.Tx (writer-instance) — parity с NetworkWriterIface / SecurityGroupWriterIface.
//
// Wave 5 replicate (KAC-94, skill evgeniy §6 G.1-G.7): PE переезжает на CQRS
// после Network/SG pilot'а. PE-specific FK (network_id/subnet_id/address_id;
// миграция 0024) — обычные within-service refs на DB-уровне; CQRS-writer
// делает Insert/Update в той же writer-TX, что и outbox-emit и параллельные
// DML по другим ресурсам.
type PrivateEndpointWriterIface interface {
	PrivateEndpointReaderIface
	Insert(ctx context.Context, pe *domain.PrivateEndpoint) (*PrivateEndpointRecord, error)
	Update(ctx context.Context, pe *domain.PrivateEndpoint) (*PrivateEndpointRecord, error)
	Delete(ctx context.Context, id string) error
	// SetProjectID меняет project_id у PrivateEndpoint (parity с другими ресурсами;
	// PE не имеет Move RPC в YC verbatim API, но writer-iface поддерживает метод
	// на будущее / для admin-tooling).
	SetProjectID(ctx context.Context, id, folderID string) (*PrivateEndpointRecord, error)
}
