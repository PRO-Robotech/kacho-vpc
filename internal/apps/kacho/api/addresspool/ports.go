// Package addresspool — use-case-структура admin-only ресурса AddressPool
// (skill evgeniy §2 B.1-B.4 + §6 G.1-G.3).
//
// Wave 5 batch 36 (KAC-94, skill `evgeniy`): миграция из старой «толстой»
// `internal/apps/kacho/services/addresspool/service.go` (AP-B.1) на
// use-case-структуру:
//
//	create.go               — CreateAddressPoolUseCase
//	update.go               — UpdateAddressPoolUseCase
//	delete.go               — DeleteAddressPoolUseCase
//	get.go                  — GetAddressPoolUseCase
//	list.go                 — ListAddressPoolsUseCase
//	check.go                — CheckUseCase (ambiguous-config diag)
//	explain_resolution.go   — ExplainResolutionUseCase
//	bindings.go             — Bind/Unbind use-case'ы + Cloud-selector wrappers
//	resolve.go              — cascade-resolve движок (используется address-UC'ами)
//	utilization.go          — GetPoolUtilization / ListPoolAddresses
//	handler.go              — тонкий gRPC server для InternalAddressPoolService
//	helpers.go              — CIDR-family-validation + IPv4-count helper
//
// AddressPool — admin-only ресурс (не выставляется через external TLS endpoint),
// поэтому Operation-flow и folder-AuthZ ему не нужны: каждый use-case
// синхронный, ответ — `*vpcv1.AddressPool` напрямую. Это сохраняется
// verbatim относительно legacy `*addresspool.AddressPoolService`.
//
// CQRS-Repository extension: AddressPool остаётся на concrete-структурах
// `*repo.AddressPoolRepo` / `*repo.AddressPoolBindingRepo` /
// `*repo.CloudPoolSelectorRepo` (бывшие реализации удалённых
// `*RepoIface`). Use-case-слой описывает узкие port'ы у себя
// (skill evgeniy §6 G.2 — duck-typing). Объяснение: AP — admin resource
// (низкая частота write'ов, нет multi-resource atomic-инвариантов внутри
// одной writer-TX), а pg-репо уже делает own-Begin/emitVPC/Commit и
// гарантирует DML+outbox-атомарность. Расширять `kacho.Repository` на
// AddressPool (как сделано для Network/SG/Address) — следующая итерация
// эпика KAC-94 если/когда это потребуется (например, для cross-resource
// writer-TX с `address_pool_address_override` + `addresses`).
package addresspool

import (
	"context"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// Pagination / Filter — переиспользуем единые value-объекты `internal/repo`
// (alias'ы, не копии): caller-code и handler работают с привычными типами без
// двойной конверсии.
type (
	Pagination        = repo.Pagination
	AddressPoolFilter = repo.AddressPoolFilter
)

// Re-export sentinel-ошибок repo-слоя как `var`. errors.Is(err,
// addresspool.ErrPoolNotResolved) совпадает с repo.ErrPoolNotResolved —
// одна и та же error-value. Нужно: handler делает
// `errors.Is(err, addresspool.ErrPoolNotResolved)` (см.
// `internal/handler/internal_address_pool_handler.go::ExplainResolution`).
var (
	ErrNotFound        = repo.ErrNotFound
	ErrPoolNotResolved = repo.ErrPoolNotResolved
)

// AddressPoolRepo — то, что use-case'ам AddressPool нужно от
// pgxpool-репо (`*repo.AddressPoolRepo`). Локальная декларация (skill evgeniy
// §6 G.2: каждый use-case-пакет описывает узкий port). В KAC-94 finalize
// общий `repo.AddressPoolRepoIface` удалён — concrete-структура
// `*repo.AddressPoolRepo` удовлетворяет этому интерфейсу duck-typing'ом.
// Расширение CQRS-Repository на AddressPool — отдельная итерация (admin-resource,
// низкая частота write'ов, нет multi-resource atomic-инвариантов в одной
// writer-TX; pg-репо уже делает own-Begin/emit/Commit и гарантирует DML+outbox
// атомарность).
type AddressPoolRepo interface {
	Get(ctx context.Context, id string) (*domain.AddressPool, error)
	List(ctx context.Context, f AddressPoolFilter, p Pagination) ([]*domain.AddressPool, string, error)
	Insert(ctx context.Context, p *domain.AddressPool) (*domain.AddressPool, error)
	Update(ctx context.Context, p *domain.AddressPool) (*domain.AddressPool, error)
	Delete(ctx context.Context, id string) error
	GetDefaultForZone(ctx context.Context, zoneID string, kind domain.AddressPoolKind) (*domain.AddressPool, error)
	FindBySelectorMatch(ctx context.Context, networkSelector map[string]string, zoneID string, kind domain.AddressPoolKind, limit int) ([]*domain.AddressPool, error)
	FindAmbiguousSelectorGroups(ctx context.Context, zoneID string) ([][]*domain.AddressPool, error)
	CountAddressesByPool(ctx context.Context, poolID string) (int64, error)
	CountAddressesByPoolPerCIDR(ctx context.Context, poolID string) (map[string]int64, error)
	ListAddressesByPool(ctx context.Context, poolID, folderFilter string, p Pagination) ([]*kachorepo.AddressRecord, string, error)
	PopulateFreelistForPool(ctx context.Context, poolID string) error
}

// AddressPoolBindingRepo — узкий port для explicit-биндингов (per-resource pinning).
type AddressPoolBindingRepo interface {
	SetNetworkDefault(ctx context.Context, networkID, poolID string) error
	GetNetworkDefault(ctx context.Context, networkID string) (string, error)
	UnsetNetworkDefault(ctx context.Context, networkID string) error
	SetAddressOverride(ctx context.Context, addressID, poolID string) error
	GetAddressOverride(ctx context.Context, addressID string) (string, error)
	UnsetAddressOverride(ctx context.Context, addressID string) error
}

// CloudPoolSelectorRepo — admin-controlled routing-labels for Cloud.
// folder_id → cloud_id резолвится через FolderClient.GetCloudID на cascade-path.
type CloudPoolSelectorRepo interface {
	Set(ctx context.Context, cloudID string, selector map[string]string, setBy string) error
	Get(ctx context.Context, cloudID string) (*domain.CloudPoolSelector, error)
	Unset(ctx context.Context, cloudID string) error
}

// AddressRepo — узкое чтение Address для cascade-resolve + IPv6 cursor init.
// AddressPool ↔ Address связаны через JSONB external_ipv4.address_pool_id /
// external_ipv6.address_pool_id; FK на стороне БД отсутствует (см. §16 в
// `kacho-vpc/CLAUDE.md`).
type AddressRepo interface {
	Get(ctx context.Context, id string) (*kachorepo.AddressRecord, error)
	// InitIPv6PoolCursor — sparse v6 counter init (миграция 0021, KAC-60).
	// Идемпотентно (ON CONFLICT DO NOTHING). Вызывается на Create / Update
	// если pool получил v6 CIDR впервые.
	InitIPv6PoolCursor(ctx context.Context, poolID string) error
}

// NetworkRepo — узкое чтение Network для BindAsNetworkDefault (FK-валидация).
// Возвращает kacho.NetworkRecord (repo-leaf): bind-use-case'у нужен сам факт
// existence + folder_id (не нужен) — поэтому здесь обобщённый Get.
type NetworkRepo interface {
	Get(ctx context.Context, id string) (*kachorepo.NetworkRecord, error)
}

// SubnetReader — узкое чтение Subnet для cascade-resolve (internal IP path:
// subnet → network → network_default bind).
type SubnetReader interface {
	Get(ctx context.Context, id string) (*kachorepo.SubnetRecord, error)
}

// FolderClient — то, что use-case'ам AddressPool нужно от peer-сервиса
// kacho-resource-manager (для cascade-resolve Step 3: folder_id → cloud_id).
type FolderClient interface {
	GetCloudID(ctx context.Context, folderID string) (string, error)
}

// ZoneRegistry — port для проверки существования zone_id (compute domain после
// KAC-15: Geography — домен kacho-compute). nil-инстанс на composition root —
// валидно: zone-check тогда пропускается.
type ZoneRegistry interface {
	Get(ctx context.Context, id string) (*domain.Zone, error)
}
