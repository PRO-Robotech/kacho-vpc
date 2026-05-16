package kacho

import (
	"context"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// SecurityGroupFilter — фильтр для списка SG. Wave 5 (KAC-94 D.1) перенесён
// в leaf-пакет `kacho` (вместе с Pagination/NetworkFilter), чтобы избежать
// import-cycle `repo → repo/kacho → repo`. В `internal/repo/iface.go` остался
// тонкий type-alias `SecurityGroupFilter = kacho.SecurityGroupFilter`.
type SecurityGroupFilter struct {
	FolderID  string
	NetworkID string
	Name      string
	Filter    string
}

// SecurityGroupReaderIface — read-операции над SecurityGroup в TX-области.
type SecurityGroupReaderIface interface {
	Get(ctx context.Context, id string) (*domain.SecurityGroupRecord, error)
	List(ctx context.Context, f SecurityGroupFilter, p Pagination) ([]*domain.SecurityGroupRecord, string, error)
}

// SecurityGroupWriterIface — write-операции + read (G.2 — writer видит свои writes).
//
// DML-методы НЕ открывают свою TX и НЕ emit'ят outbox — это делает caller
// (use-case) через `RepositoryWriter.Outbox().Emit(...)` после успешного DML.
// Atomicity DML + outbox гарантируется тем, что обе операции идут через одну
// pgx.Tx (writer-instance) — parity с NetworkWriterIface.
//
// Wave 5 batch 33/34 (KAC-94, skill evgeniy I.9/I.10): SG переезжает на CQRS,
// чтобы Network.Create мог inline создать default-SG в одной writer-TX вместо
// трёх отдельных TX (orphan-SG window закрыт).
type SecurityGroupWriterIface interface {
	SecurityGroupReaderIface
	Insert(ctx context.Context, sg *domain.SecurityGroup) (*domain.SecurityGroupRecord, error)
	Update(ctx context.Context, sg *domain.SecurityGroup) (*domain.SecurityGroupRecord, error)
	Delete(ctx context.Context, id string) error
	// SetFolderID меняет folder_id у SG (для :move).
	SetFolderID(ctx context.Context, id, folderID string) (*domain.SecurityGroupRecord, error)
	// UpdateRules атомарно заменяет набор правил SG (xmin-OCC).
	// Concurrent-modification → ErrFailedPrecondition.
	UpdateRules(ctx context.Context, sgID string, deleteIDs []string, add []domain.SecurityGroupRule) (*domain.SecurityGroupRecord, error)
	// UpdateRule обновляет description/labels единичного правила в SG (xmin-OCC).
	UpdateRule(ctx context.Context, sgID, ruleID, description string, labels map[string]string, mask []string) (*domain.SecurityGroupRecord, error)
}
