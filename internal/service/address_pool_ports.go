package service

import (
	"context"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// AddressPoolFilter — фильтр для списка пулов. AddressPool — глобальный
// infrastructure-ресурс, поэтому folder/cloud/org здесь нет.
type AddressPoolFilter struct {
	Kind   domain.AddressPoolKind // 0 = any
	ZoneID string                 // "" = any
}

// AddressPoolRepo — port-интерфейс репозитория пулов адресов.
type AddressPoolRepo interface {
	Get(ctx context.Context, id string) (*domain.AddressPool, error)
	List(ctx context.Context, f AddressPoolFilter, p Pagination) ([]*domain.AddressPool, string, error)
	Insert(ctx context.Context, p *domain.AddressPool) (*domain.AddressPool, error)
	Update(ctx context.Context, p *domain.AddressPool) (*domain.AddressPool, error)
	Delete(ctx context.Context, id string) error
	// GetDefaultForZone возвращает default-pool для (zone_id, kind).
	// Empty zoneID = глобальный default (zone_id IS NULL).
	// ErrNotFound если default-pool не настроен.
	GetDefaultForZone(ctx context.Context, zoneID string, kind domain.AddressPoolKind) (*domain.AddressPool, error)
	// FindBySelectorMatch — label-cascade резолв.
	// Match-семантика: `networkSelector ⊆ pool.selector_labels` (containment).
	// ORDER BY:
	//   1. (jsonb_object_size(selector_labels) - len(networkSelector)) ASC — diff (точнее лучше)
	//   2. selector_priority DESC — explicit tie-break
	// При equal-diff и equal-priority — Postgres вернёт первую row в physical
	// order (admin's responsibility; см. Check для diagnostic).
	// Если ничего не match'ается — ErrNotFound.
	// Если limit > 1 — возвращает столько top результатов (для ExplainResolution).
	FindBySelectorMatch(ctx context.Context, networkSelector map[string]string, zoneID string, kind domain.AddressPoolKind, limit int) ([]*domain.AddressPool, error)
	// FindAmbiguousSelectorGroups — diagnostic для Check: возвращает группы
	// pools с одинаковым (zone_id, kind, selector_labels, selector_priority),
	// при которых cascade-resolve выдаёт undefined order.
	FindAmbiguousSelectorGroups(ctx context.Context, zoneID string) ([][]*domain.AddressPool, error)

	// CountAddressesByPool — кол-во Address с external_ipv4.address_pool_id = poolID.
	// Для GetUtilization. Возвращает 0 если pool не используется.
	CountAddressesByPool(ctx context.Context, poolID string) (int64, error)
	// CountAddressesByPoolPerCIDR — кол-во Address с IP в каждом CIDR pool'а.
	// Map CIDR-string → count. Используется для CIDR-breakdown в utilization.
	CountAddressesByPoolPerCIDR(ctx context.Context, poolID string) (map[string]int64, error)
	// ListAddressesByPool — все Address (cross-folder) использующие pool.
	// Pagination через page_size+token.
	ListAddressesByPool(ctx context.Context, poolID, folderFilter string, p Pagination) ([]*domain.Address, string, error)
}

// AddressPoolBindingRepo — explicit биндинги pool ↔ network/address (per-resource pinning).
type AddressPoolBindingRepo interface {
	// SetNetworkDefault — атомарный upsert (network_id, pool_id).
	SetNetworkDefault(ctx context.Context, networkID, poolID string) error
	// GetNetworkDefault возвращает поль pool_id, привязанный к Network как default.
	// ErrNotFound если bind не задан.
	GetNetworkDefault(ctx context.Context, networkID string) (string, error)
	// UnsetNetworkDefault удаляет binding (idempotent — no error if missing).
	UnsetNetworkDefault(ctx context.Context, networkID string) error

	// SetAddressOverride — атомарный upsert (address_id, pool_id).
	// Если у address уже allocated external IP — caller должен предварительно
	// проверить и вернуть FailedPrecondition.
	SetAddressOverride(ctx context.Context, addressID, poolID string) error
	GetAddressOverride(ctx context.Context, addressID string) (string, error)
	UnsetAddressOverride(ctx context.Context, addressID string) error
}

// CloudPoolSelectorRepo — admin-controlled routing-labels for Cloud.
// Хранится в internal-only таблице cloud_pool_selector (миграция 0022).
//
// Selector привязан к Cloud (а не Network), чтобы cascade-resolve мог
// найти подходящий pool для external Address (у которых нет network_id).
// folder_id → cloud_id резолвится через FolderClient.GetCloudID.
type CloudPoolSelectorRepo interface {
	// Set — atomic upsert (cloud_id, selector, set_by).
	// Empty selector допустим — это семантически равно отсутствию binding'а
	// (в cascade-resolve skip'аем label-step).
	Set(ctx context.Context, cloudID string, selector map[string]string, setBy string) error
	// Get возвращает selector + metadata. ErrNotFound если binding не задан.
	Get(ctx context.Context, cloudID string) (*domain.CloudPoolSelector, error)
	// Unset удаляет binding (idempotent — no error if missing).
	Unset(ctx context.Context, cloudID string) error
}
