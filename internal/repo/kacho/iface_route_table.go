// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package kacho

import (
	"context"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// RouteTableFilter — фильтр для списка таблиц маршрутизации. Живет в
// leaf-пакете `kacho`, как NetworkFilter / SecurityGroupFilter; в
// `internal/repo/iface.go` — type-alias `RouteTableFilter = kacho.RouteTableFilter`.
type RouteTableFilter struct {
	ProjectID string
	NetworkID string
	Name      string
	Filter    string
}

// RouteTableReaderIface — read-операции над RouteTable в read-only TX-области.
type RouteTableReaderIface interface {
	Get(ctx context.Context, id string) (*RouteTableRecord, error)
	List(ctx context.Context, f RouteTableFilter, p Pagination) ([]*RouteTableRecord, string, error)
	// ListByNetwork — узкий read для checkNetworkEmpty (Network.Delete) и
	// ListRouteTables(byNetwork). Реализован поверх List с filter NetworkID.
	ListByNetwork(ctx context.Context, networkID string, p Pagination) ([]*RouteTableRecord, string, error)
	// ListByIDs — per-object filtered List (`WHERE id = ANY`), pagination после
	// фильтра. Пустой allowedIDs → (nil, "", nil).
	ListByIDs(ctx context.Context, f RouteTableFilter, allowedIDs []string, p Pagination) ([]*RouteTableRecord, string, error)
}

// RouteTableWriterIface — write-операции плюс read (writer расширяет reader).
//
// DML-методы НЕ открывают свою TX и НЕ emit'ят outbox — это делает caller
// (use-case) через `RepositoryWriter.Outbox().Emit(...)` после успешного DML.
// Атомарность DML + outbox держится на том, что обе операции идут через одну
// pgx.Tx (writer-instance), как у NetworkWriterIface / SecurityGroupWriterIface.
type RouteTableWriterIface interface {
	RouteTableReaderIface
	Insert(ctx context.Context, rt *domain.RouteTable) (*RouteTableRecord, error)
	Update(ctx context.Context, rt *domain.RouteTable) (*RouteTableRecord, error)
	Delete(ctx context.Context, id string) error
	// GetForUpdate — Get с `SELECT ... FOR UPDATE` (row-lock) внутри writer-TX.
	// Сериализует конкурентный read-modify-write в Update (Get → applyMask →
	// UPDATE всех mutable-колонок): без него две Update с disjoint update_mask
	// читали бы один snapshot и второй commit затирал бы un-masked поле первого
	// (lost-update).
	GetForUpdate(ctx context.Context, id string) (*RouteTableRecord, error)
}
