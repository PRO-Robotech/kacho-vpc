// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package kacho

import (
	"context"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// GatewayFilter — фильтр для списка Gateway. Живет в leaf-пакете `kacho` рядом
// с Pagination / NetworkFilter / SecurityGroupFilter; в `internal/repo/iface.go`
// — тонкий type-alias `GatewayFilter = kacho.GatewayFilter`.
type GatewayFilter struct {
	ProjectID string
	Name      string
	// Filter — сырое выражение фильтра (`name="<value>"`); парсится в repo с
	// whitelist allowedFields=["name"].
	Filter string
}

// GatewayReaderIface — read-операции над Gateway в TX-области.
type GatewayReaderIface interface {
	Get(ctx context.Context, id string) (*GatewayRecord, error)
	List(ctx context.Context, f GatewayFilter, p Pagination) ([]*GatewayRecord, string, error)
	// ListByIDs — per-object filtered List (`WHERE id = ANY`), pagination после
	// фильтра. Пустой allowedIDs → (nil, "", nil).
	ListByIDs(ctx context.Context, f GatewayFilter, allowedIDs []string, p Pagination) ([]*GatewayRecord, string, error)
}

// GatewayWriterIface — write-операции плюс read (writer видит свои writes).
//
// DML-методы НЕ открывают свою TX и НЕ emit'ят outbox — это делает caller
// (use-case) через `RepositoryWriter.Outbox().Emit(...)` после успешного DML.
// Атомарность DML + outbox держится на том, что обе операции идут через одну
// pgx.Tx (writer-instance), как у NetworkWriterIface / SecurityGroupWriterIface.
type GatewayWriterIface interface {
	GatewayReaderIface
	Insert(ctx context.Context, g *domain.Gateway) (*GatewayRecord, error)
	Update(ctx context.Context, g *domain.Gateway) (*GatewayRecord, error)
	Delete(ctx context.Context, id string) error
	// GetForUpdate — Get с `SELECT ... FOR UPDATE` (row-lock) внутри writer-TX.
	// Сериализует конкурентный read-modify-write в Update (Get → applyMask →
	// UPDATE всех mutable-колонок): без него две Update с disjoint update_mask
	// читали бы один snapshot и второй commit затирал бы un-masked поле первого
	// (lost-update).
	GetForUpdate(ctx context.Context, id string) (*GatewayRecord, error)
}
