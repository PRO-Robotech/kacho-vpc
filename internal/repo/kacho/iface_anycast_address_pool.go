// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package kacho

import (
	"context"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// AnycastAddressPoolFilter — фильтр списка AnycastAddressPool. Пул project-scoped
// (tenant-facing), поэтому первичный фильтр — ProjectID (закрывает cross-project
// enumeration). is_default-пул живёт в платформенном project; он сурфейсится в
// результат ТОЛЬКО когда задан NetworkID (selector-контекст), т.к. is_default
// авто-доступен каждой сети без pivot-строки.
type AnycastAddressPoolFilter struct {
	ProjectID string              // обязателен на List
	Name      string              // exact-match по имени (для uniqueness-precheck)
	NetworkID string              // опц.: пулы, приаттаченные к этой сети, ∪ платформенный is_default
	Scope     domain.AnycastScope // опц.: фильтр по scope (Unspecified → не фильтруем)
	IPVersion domain.IpVersion    // опц.: фильтр по семейству (Unspecified → не фильтруем)
}

// AnycastAddressPoolReaderIface — read-операции над AnycastAddressPool в
// read-only TX-области.
type AnycastAddressPoolReaderIface interface {
	Get(ctx context.Context, id string) (*AnycastAddressPoolRecord, error)
	List(ctx context.Context, f AnycastAddressPoolFilter, p Pagination) ([]*AnycastAddressPoolRecord, string, error)
	// ListByIDs — List, суженный per-object FGA-фильтром (explicit allowed-set);
	// project_id-фильтр применяется поверх (own-only + grant-set).
	ListByIDs(ctx context.Context, f AnycastAddressPoolFilter, ids []string, p Pagination) ([]*AnycastAddressPoolRecord, string, error)
	// AllocatedBlocks — все блоки из anycast_address_pool_cidrs (глобально) для
	// детерминированного free-scan при platform-assign пустого cidr_blocks.
	AllocatedBlocks(ctx context.Context) ([]string, error)
	// CountAttachments — число pivot-строк (приаттаченных сетей) пула. >0 → пул не
	// пуст (Delete отвергается).
	CountAttachments(ctx context.Context, poolID string) (int64, error)
	// IsAttached — приаттачен ли пул к сети (для idempotent-detach no-op).
	IsAttached(ctx context.Context, poolID, networkID string) (bool, error)
	// CountAllocationsInNetwork — число живых anycast-аллокаций пула в сети
	// (anycast-Address'ы, у которых anycast.pool_id = poolID и
	// anycast_network_id = networkID). Detach отвергается при >0.
	CountAllocationsInNetwork(ctx context.Context, poolID, networkID string) (int64, error)
	// DefaultForFamily — платформенный is_default INTERNAL-пул для семейства
	// (IPV4/IPV6). Fallback при auto-аллокации без явного anycast_pool_id.
	// ErrNotFound, если default-пула для семейства нет.
	DefaultForFamily(ctx context.Context, ipVersion domain.IpVersion) (*AnycastAddressPoolRecord, error)
}

// AnycastAddressPoolWriterIface — write-операции + read (writer видит свои
// writes). DML-методы НЕ открывают свою TX и НЕ emit'ят outbox — это делает
// use-case через RepositoryWriter в той же pgx.Tx.
type AnycastAddressPoolWriterIface interface {
	AnycastAddressPoolReaderIface
	// Insert — INSERT пула + материализация блоков в child-таблицу
	// anycast_address_pool_cidrs в той же writer-TX. 23P01 на child EXCLUDE
	// (within-pool overlap) → ErrFailedPrecondition.
	Insert(ctx context.Context, p *domain.AnycastAddressPool) (*AnycastAddressPoolRecord, error)
	// Update — UPDATE name/description/labels (остальное immutable).
	Update(ctx context.Context, p *domain.AnycastAddressPool) (*AnycastAddressPoolRecord, error)
	// Delete — снять anycast-claim'ы пула (network_cidr_claims, у них нет FK на
	// пул) + DELETE пула (FK CASCADE снимает child-блоки и pivot).
	Delete(ctx context.Context, id string) error
	// AttachNetwork — INSERT pivot ON CONFLICT DO NOTHING + INSERT claim на КАЖДЫЙ
	// блок пула ON CONFLICT DO NOTHING (идемпотентно). 23P01 (overlap с subnet/
	// другим пулом) → ErrFailedPrecondition "network CIDR claims can not overlap".
	AttachNetwork(ctx context.Context, poolID, networkID string, blocks []string) error
	// DetachNetwork — DELETE anycast-claim'ы пула в сети + DELETE pivot. Detach
	// непривязанного — no-op.
	DetachNetwork(ctx context.Context, poolID, networkID string) error
}
