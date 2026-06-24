// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package kacho

import (
	"context"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// Pagination — постраничная навигация. Живет в leaf-пакете `kacho`, чтобы
// избежать import-cycle `repo → repo/kacho → repo`. Его type-alias
// `Pagination = kacho.Pagination` в `internal/repo/iface.go` используют узкие
// port'ы use-case-пакетов и repomock.
type Pagination struct {
	PageToken string
	PageSize  int64
}

// NetworkFilter — фильтр для списка сетей. Живет в leaf-пакете `kacho` вместе
// с Pagination (см. doc-комментарий на Pagination выше).
type NetworkFilter struct {
	ProjectID string
	Name      string
	// Filter — сырое выражение фильтра (`name="<value>"`); парсится в repo с
	// whitelist allowedFields=["name"].
	Filter string
}

// NetworkReaderIface — read-операции над Network в read-only TX-области.
type NetworkReaderIface interface {
	Get(ctx context.Context, id string) (*NetworkRecord, error)
	List(ctx context.Context, f NetworkFilter, p Pagination) ([]*NetworkRecord, string, error)
	// ListByIDs возвращает только networks с id из allowedIDs
	// (`WHERE id = ANY($allowed_ids)`); project_id-фильтр уже применен выше
	// (handler проверил ownership). Пустой allowedIDs → (nil, "", nil)
	// (short-circuit вместо SQL с пустым массивом). Используется в
	// FGA-filtered List handlers.
	ListByIDs(ctx context.Context, f NetworkFilter, allowedIDs []string, p Pagination) ([]*NetworkRecord, string, error)
}

// NetworkWriterIface — write-операции плюс read (writer видит свои writes).
//
// DML-методы НЕ открывают свою TX и НЕ emit'ят outbox — это делает caller
// (use-case) через `RepositoryWriter.Outbox().Emit(...)` после успешного DML.
// Атомарность DML + outbox держится на том, что обе операции идут через одну
// pgx.Tx (writer-instance).
type NetworkWriterIface interface {
	NetworkReaderIface
	Insert(ctx context.Context, n *domain.Network) (*NetworkRecord, error)
	Update(ctx context.Context, n *domain.Network) (*NetworkRecord, error)
	Delete(ctx context.Context, id string) error
	// SetDefaultSGID атомарно проставляет networks.default_security_group_id для
	// конкретной сети — узкий update-помощник для inline-создания default-SG в
	// Network.Create (Insert(Network) → Insert(SG) → SetDefaultSGID, все в одной
	// writer-TX), без верхнеуровневого `UPDATE networks SET name=…, …`, который
	// перезаписывал бы immutable-поля.
	SetDefaultSGID(ctx context.Context, id, sgID string) (*NetworkRecord, error)
	// GetForUpdate — Get с `SELECT ... FOR UPDATE` (row-lock) внутри writer-TX.
	// Сериализует конкурентный read-modify-write в Update (Get → applyMask →
	// UPDATE всех mutable-колонок): без него две Update с disjoint update_mask
	// читали бы один snapshot и второй commit затирал бы un-masked поле первого
	// (lost-update).
	GetForUpdate(ctx context.Context, id string) (*NetworkRecord, error)
}
