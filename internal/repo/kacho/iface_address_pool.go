package kacho

import (
	"context"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// AddressPoolFilter — фильтр для списка AddressPool. AddressPool — глобальный
// infrastructure-ресурс (нет project_id), фильтрация по (kind, zone_id).
//
// Wave 5 replicate (KAC-94 A.7 sub-PR 1/6, skill evgeniy §6 G.1-G.7): parity с
// NetworkFilter / SubnetFilter / GatewayFilter — единый CQRS-контракт для всех
// VPC-ресурсов. В `internal/repo/iface.go` остался type-alias
// `AddressPoolFilter = kacho.AddressPoolFilter` (legacy callers).
type AddressPoolFilter struct {
	Kind   domain.AddressPoolKind // 0 = any
	ZoneID string                 // "" = any
}

// AddressPoolReaderIface — read-операции над AddressPool в read-only TX-области.
//
// Wave 5 replicate (KAC-94 A.7 sub-PR 1/6, skill evgeniy §6 G.1-G.7): parity с
// NetworkReaderIface / AddressReaderIface — единый CQRS-контракт. AddressPool —
// admin-only ресурс (cluster-internal listener), все read-методы используются
// или handler'ом (`InternalAddressPoolService.{Get,List,Check,GetUtilization,...}`)
// или resolver'ом (cascade pool-resolve для Address.Create / Allocate*).
type AddressPoolReaderIface interface {
	Get(ctx context.Context, id string) (*AddressPoolRecord, error)
	List(ctx context.Context, f AddressPoolFilter, p Pagination) ([]*AddressPoolRecord, string, error)

	// GetDefaultForZone — вернуть default-pool для (zone, kind). zoneID == ""
	// = глобальный default (`zone_id IS NULL`). ErrNotFound если default не задан.
	// Используется cascade Step 4 (zone_default) / Step 5 (global_default).
	GetDefaultForZone(ctx context.Context, zoneID string, kind domain.AddressPoolKind) (*AddressPoolRecord, error)
	// FindBySelectorMatch — label-cascade резолв (containment: networkSelector ⊆
	// pool.selector_labels). zoneID == "" → только глобальные (zone_id IS NULL),
	// иначе — pool'ы привязанные к zone + глобальные. limit≤0 → 1. Используется
	// cascade Step 3 (label_selector).
	FindBySelectorMatch(ctx context.Context, networkSelector map[string]string, zoneID string, kind domain.AddressPoolKind, limit int) ([]*AddressPoolRecord, error)
	// FindAmbiguousSelectorGroups — diagnostic для `InternalAddressPoolService.Check`:
	// группы pool'ов с identical (zone_id, kind, selector_labels, selector_priority).
	// zoneID == "" → scan по всем зонам.
	FindAmbiguousSelectorGroups(ctx context.Context, zoneID string) ([][]*AddressPoolRecord, error)
	// CountAddressesByPool — admin observability: сколько Address используют pool.
	// Используется `DeleteAddressPoolUseCase` для FailedPrecondition guard и
	// `GetPoolUtilizationUseCase`.
	CountAddressesByPool(ctx context.Context, poolID string) (int64, error)
	// CountAddressesByPoolPerCIDR — для каждого V4CIDR — allocated count. Возвращает
	// V6 CIDR'ы с count=0 placeholder (sparse v6-allocator ведёт свою бухгалтерию
	// через ipv6_pool_cursors).
	CountAddressesByPoolPerCIDR(ctx context.Context, poolID string) (map[string]int64, error)
	// ListAddressesByPool — кросс-folder список Address с IP из pool.
	// folderFilter == "" → без фильтра. Возвращает AddressRecord (repo-leaf).
	ListAddressesByPool(ctx context.Context, poolID, folderFilter string, p Pagination) ([]*AddressRecord, string, error)
}

// AddressPoolWriterIface — write-операции + read (G.2 — writer видит свои writes).
//
// DML-методы НЕ открывают свою TX и НЕ emit'ят outbox — это делает caller
// (use-case) через `RepositoryWriter.Outbox().Emit(...)` после успешного DML.
// Atomicity DML + outbox гарантируется тем, что обе операции идут через одну
// pgx.Tx (writer-instance).
//
// Wave 5 replicate (KAC-94 A.7 sub-PR 1/6): IPAM-операции `PopulateFreelistForPool`
// и `InitIPv6PoolCursor` — backing-data setup для pool, поэтому идут в writer-TX
// CreateAddressPool / UpdateAddressPool. (Раньше — отдельные .Begin() в legacy
// `*AddressPoolRepo` / `*AddressRepo`; теперь — единая writer-TX use-case'а.)
type AddressPoolWriterIface interface {
	AddressPoolReaderIface
	Insert(ctx context.Context, p *domain.AddressPool) (*AddressPoolRecord, error)
	Update(ctx context.Context, p *domain.AddressPool) (*AddressPoolRecord, error)
	Delete(ctx context.Context, id string) error
	// PopulateFreelistForPool — materialise per-IP freelist из V4CIDRBlocks
	// (миграция 0014). Идемпотентно (ON CONFLICT DO NOTHING). V6-блоки идут
	// через sparse counter (см. AddressWriterIface.InitIPv6PoolCursor).
	PopulateFreelistForPool(ctx context.Context, poolID string) error
}
