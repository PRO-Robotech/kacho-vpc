// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package kacho

import (
	"context"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// SecurityGroupFilter — фильтр для списка SG. Живет в leaf-пакете `kacho`
// (вместе с Pagination/NetworkFilter), чтобы избежать import-cycle
// `repo → repo/kacho → repo`; в `internal/repo/iface.go` — тонкий type-alias
// `SecurityGroupFilter = kacho.SecurityGroupFilter`.
type SecurityGroupFilter struct {
	ProjectID string
	NetworkID string
	Name      string
	Filter    string
}

// SecurityGroupReaderIface — read-операции над SecurityGroup в TX-области.
type SecurityGroupReaderIface interface {
	Get(ctx context.Context, id string) (*SecurityGroupRecord, error)
	List(ctx context.Context, f SecurityGroupFilter, p Pagination) ([]*SecurityGroupRecord, string, error)
	// ListByIDs — per-object filtered List (`WHERE id = ANY`), pagination после
	// фильтра. Пустой allowedIDs → (nil, "", nil).
	ListByIDs(ctx context.Context, f SecurityGroupFilter, allowedIDs []string, p Pagination) ([]*SecurityGroupRecord, string, error)
}

// SecurityGroupWriterIface — write-операции плюс read (writer видит свои writes).
//
// DML-методы НЕ открывают свою TX и НЕ emit'ят outbox — это делает caller
// (use-case) через `RepositoryWriter.Outbox().Emit(...)` после успешного DML.
// Атомарность DML + outbox держится на том, что обе операции идут через одну
// pgx.Tx (writer-instance), как у NetworkWriterIface.
//
// SG разнесен на CQRS, чтобы Network.Create мог inline создать default-SG в
// одной writer-TX вместо трех отдельных (окно orphan-SG закрыто).
type SecurityGroupWriterIface interface {
	SecurityGroupReaderIface
	Insert(ctx context.Context, sg *domain.SecurityGroup) (*SecurityGroupRecord, error)
	Update(ctx context.Context, sg *domain.SecurityGroup) (*SecurityGroupRecord, error)
	Delete(ctx context.Context, id string) error
	// UpdateRules атомарно заменяет набор правил SG (xmin-OCC).
	// Concurrent-modification → ErrFailedPrecondition.
	UpdateRules(ctx context.Context, sgID string, deleteIDs []string, add []domain.SecurityGroupRule) (*SecurityGroupRecord, error)
	// UpdateRule обновляет description/labels единичного правила в SG (xmin-OCC).
	UpdateRule(ctx context.Context, sgID, ruleID, description string, labels map[string]string, mask []string) (*SecurityGroupRecord, error)
	// GetForUpdate — Get с `SELECT ... FOR UPDATE` (row-lock) внутри writer-TX.
	// Сериализует конкурентный read-modify-write в Update (Get → applyMask →
	// UPDATE всех mutable-колонок, включая rule_specs): без него две Update с
	// disjoint update_mask читали бы один snapshot и второй commit затирал бы
	// un-masked поле первого (lost-update).
	GetForUpdate(ctx context.Context, id string) (*SecurityGroupRecord, error)
}
