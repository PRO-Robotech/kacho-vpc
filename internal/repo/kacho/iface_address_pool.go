// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package kacho

import (
	"context"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// AddressPoolFilter — фильтр для списка AddressPool. AddressPool — глобальный
// infrastructure-ресурс (нет project_id), фильтрация по (kind, zone_id).
type AddressPoolFilter struct {
	Kind   domain.AddressPoolKind // 0 = any
	ZoneID string                 // "" = any
}

// AddressPoolReaderIface — read-операции над AddressPool в read-only TX-области.
// AddressPool — admin-only ресурс (cluster-internal listener), read-методы
// используются либо handler'ом
// (InternalAddressPoolService.{Get,List,Check,GetUtilization,...}), либо
// resolver'ом (cascade pool-resolve для Address.Create / Allocate*).
type AddressPoolReaderIface interface {
	Get(ctx context.Context, id string) (*AddressPoolRecord, error)
	List(ctx context.Context, f AddressPoolFilter, p Pagination) ([]*AddressPoolRecord, string, error)

	// GetDefaultForZone — вернуть default-pool для (zone, kind). zoneID == ""
	// = глобальный default (`zone_id IS NULL`). ErrNotFound если default не задан.
	// Используется cascade zone_default / global_default шагами.
	GetDefaultForZone(ctx context.Context, zoneID string, kind domain.AddressPoolKind) (*AddressPoolRecord, error)
	// CountAddressesByPool — admin observability: сколько Address используют pool.
	// Используется `DeleteAddressPoolUseCase` для FailedPrecondition guard и
	// `GetPoolUtilizationUseCase`.
	CountAddressesByPool(ctx context.Context, poolID string) (int64, error)
	// CountAddressesByPoolPerCIDR — для каждого V4CIDR — allocated count. Возвращает
	// V6 CIDR'ы с count=0 placeholder (sparse v6-allocator ведет свою бухгалтерию
	// через ipv6_pool_cursors).
	CountAddressesByPoolPerCIDR(ctx context.Context, poolID string) (map[string]int64, error)
	// ListAddressesByPool — кросс-project список Address с IP из pool.
	// projectFilter == "" → без фильтра. Возвращает AddressRecord (repo-leaf).
	ListAddressesByPool(ctx context.Context, poolID, projectFilter string, p Pagination) ([]*AddressRecord, string, error)
}

// AddressPoolWriterIface — write-операции + read (writer видит свои writes).
//
// DML-методы НЕ открывают свою TX и НЕ emit'ят outbox — это делает caller
// (use-case) через RepositoryWriter.Outbox().Emit(...) после успешного DML.
// Atomicity DML + outbox гарантируется тем, что обе операции идут через одну
// pgx.Tx (writer-instance).
//
// IPAM-операции PopulateFreelistForPool и InitIPv6PoolCursor — backing-data
// setup для pool, поэтому идут в той же writer-TX CreateAddressPool /
// UpdateAddressPool.
type AddressPoolWriterIface interface {
	AddressPoolReaderIface
	Insert(ctx context.Context, p *domain.AddressPool) (*AddressPoolRecord, error)
	Update(ctx context.Context, p *domain.AddressPool) (*AddressPoolRecord, error)
	Delete(ctx context.Context, id string) error
	// LockForUpdate берет row-lock (SELECT ... FOR UPDATE) на pool в writer-TX.
	// AddressPool.Delete вызывает его перед count-проверкой, а external-allocate
	// берет FOR SHARE на тот же pool → Delete сериализуется против in-flight
	// allocate (иначе: count прочитан, потом конкурентный allocate, потом
	// DELETE → dangling-ссылка на удаленный pool). ErrNotFound если pool
	// отсутствует.
	LockForUpdate(ctx context.Context, id string) error
	// PopulateFreelistForPool — materialise per-IP freelist из V4CIDRBlocks.
	// Идемпотентно (ON CONFLICT DO NOTHING). V6-блоки идут через sparse counter
	// (см. AddressWriterIface.InitIPv6PoolCursor).
	PopulateFreelistForPool(ctx context.Context, poolID string) error
	// AddCidrToFreelist — materialise freelist только для дельты v4-CIDR'ов
	// (:addCidrBlocks). Идемпотентно. v6 игнорируются.
	AddCidrToFreelist(ctx context.Context, poolID string, newV4Cidrs []string) error
	// DeleteFreelistForCidrs — удалить free_ips, попадающие в любой из CIDR
	// (:removeCidrBlocks). Берет row-lock — сериализует remove vs alloc.
	DeleteFreelistForCidrs(ctx context.Context, poolID string, cidrs []string) error
	// CountAllocatedInCidrs — кол-во Address с выделенным external IPv4 ∈ CIDR
	// (:removeCidrBlocks use-check). >0 → CIDR нельзя удалить.
	CountAllocatedInCidrs(ctx context.Context, poolID string, cidrs []string) (int64, error)
	// InsertCidrBlocks — нормализует v4/v6 CIDR-блоки пула в child-таблицу
	// address_pool_cidrs (EXCLUDE gist по (kind, block)). DB-level backstop против
	// пересечения CIDR внутри/между пулами. 23P01 (exclusion) →
	// ErrFailedPrecondition "address pool CIDRs can not overlap". Идет в той же
	// writer-TX, что Insert/Update пула.
	InsertCidrBlocks(ctx context.Context, poolID string, kind domain.AddressPoolKind, v4, v6 []string) error
	// DeleteCidrBlocks — удаляет конкретные block'и из address_pool_cidrs
	// (для :removeCidrBlocks). Pool.Delete каскадит (FK ON DELETE CASCADE) — там
	// явный delete не нужен.
	DeleteCidrBlocks(ctx context.Context, poolID string, v4, v6 []string) error
}
