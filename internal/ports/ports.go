// Package ports содержит port-интерфейсы (Clean Architecture boundaries) и
// связанные с ними value-объекты (Pagination, *Filter) для kacho-vpc.
//
// Это leaf-пакет: импортирует только `internal/domain` и `kacho-corelib`-типы.
// Импортируется `internal/service` (use-cases), `internal/repo` / `internal/clients`
// (adapters реализуют эти интерфейсы) и `internal/ports/portmock` (общие fake'и
// для unit-тестов). Так избегается дублирование mock-реализаций по test-файлам
// и не создаётся import-cycle (service → ports ← portmock; portmock не зависит
// от service). См. TODO #12.
//
// `internal/service` ре-экспортирует эти типы через type-alias'ы, поэтому
// существующий код (`service.NetworkRepo`, `service.Pagination`, ...) работает
// без изменений.
package ports

/*//NOTES
у меня когнитивный диссонанс от названия "ports" хотя здесь только абстракции для "repository"
*/

import (
	"context"
	"time"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// Pagination — постраничная навигация.
type Pagination struct {
	PageToken string
	PageSize  int64
}

// NetworkFilter — фильтр для списка сетей.
type NetworkFilter struct {
	FolderID string
	// Filter — raw filter expression (YC-syntax: `name="<value>"`).
	// Парсится в repo с whitelist allowedFields=["name"].
	Filter string
}

// SubnetFilter — фильтр для списка подсетей.
type SubnetFilter struct {
	FolderID  string
	NetworkID string
	Filter    string // raw filter expression (YC-syntax)
}

// AddressFilter — фильтр для списка адресов.
type AddressFilter struct {
	FolderID string
	Filter   string
}

// RouteTableFilter — фильтр для списка таблиц маршрутизации.
type RouteTableFilter struct {
	FolderID  string
	NetworkID string
	Filter    string
}

// SecurityGroupFilter — фильтр для списка SG.
type SecurityGroupFilter struct {
	FolderID  string
	NetworkID string
	Filter    string
}

// GatewayFilter — фильтр для списка NAT Gateways.
type GatewayFilter struct {
	FolderID string
	Filter   string
}

// PrivateEndpointFilter — фильтр для списка PrivateEndpoints.
type PrivateEndpointFilter struct {
	FolderID string
	Filter   string
}

// AddressPoolFilter — фильтр для списка пулов. AddressPool — глобальный
// infrastructure-ресурс, поэтому folder/cloud/org здесь нет.
type AddressPoolFilter struct {
	Kind   domain.AddressPoolKind // 0 = any
	ZoneID string                 // "" = any
}

// NetworkRepo — port-интерфейс репозитория сетей.
type NetworkRepo interface {
	Get(ctx context.Context, id string) (*domain.Network, error)
	List(ctx context.Context, f NetworkFilter, p Pagination) ([]*domain.Network, string, error)
	Insert(ctx context.Context, n *domain.Network) (*domain.Network, error)
	Update(ctx context.Context, n *domain.Network) (*domain.Network, error)
	Delete(ctx context.Context, id string) error
	// SetFolderID меняет folder_id ресурса (для :move action). Возвращает обновлённый ресурс.
	SetFolderID(ctx context.Context, id, folderID string) (*domain.Network, error)
}

/*//  для примера будет взят ресурс Network и его репозиторий
согласно CQRS нужно сразу разделять Reader и Writer интерфейсы
см ниже

- зачем это нужно ? - для того чтобы сразу заложить схему оптимизации на читающие
и пишущие запросы
- обычно все читающие запросы идут на slave-реплики БД а пишущие на master-ноды
*/

type Network = struct {
	domain.Network
	CreatedAt time.Time
	//^^^^^^^^^^^^^^^^^ мне кажется что это поле уместно именно тут как и все поля CreatedAt в типАх из доменной модели
}

// NetworkReaderIface -
type NetworkReaderIface interface {
	Get(ctx context.Context, id string) (Network, error)
	List(ctx context.Context, f NetworkFilter, p Pagination) ([]Network, string, error)
}

// NetworkWriterIface -
type NetworkWriterIface interface {
	NetworkReaderIface
	Insert(ctx context.Context, n domain.Network) (Network, error)
	Update(ctx context.Context, n domain.Network) (Network, error)
	Delete(ctx context.Context, id string) error
	Move2Folder(ctx context.Context, id, folderID string) (Network, error)
}

type RepositoryReader interface {
	Networks() NetworkReaderIface
	//...
	Close() error
}

type RepositoryWriter interface {
	Networks() NetworkWriterIface
	//....
	Commit() error
	Abort()
}

type Repository interface {
	Reader(context.Context) RepositoryReader // открывает транзакцию на чтение
	Writer(context.Context) RepositoryWriter // открывает транзакцию на запись
	Close() error
}

// SubnetRepo — port-интерфейс репозитория подсетей.
type SubnetRepo interface {
	Get(ctx context.Context, id string) (*domain.Subnet, error)
	List(ctx context.Context, f SubnetFilter, p Pagination) ([]*domain.Subnet, string, error)
	Insert(ctx context.Context, s *domain.Subnet) (*domain.Subnet, error)
	Update(ctx context.Context, s *domain.Subnet) (*domain.Subnet, error)
	Delete(ctx context.Context, id string) error
	// SetFolderID меняет folder_id ресурса.
	SetFolderID(ctx context.Context, id, folderID string) (*domain.Subnet, error)
	// SetCidrBlocks атомарно обновляет v4_cidr_blocks (для AddCidrBlocks/RemoveCidrBlocks).
	// Триггеры EXCLUDE проверяют overlap по primary CIDR.
	SetCidrBlocks(ctx context.Context, id string, v4 []string) (*domain.Subnet, error)
	// SetZoneID меняет zone_id у подсети (для Relocate).
	SetZoneID(ctx context.Context, id, zoneID string) (*domain.Subnet, error)
	// AddressesBySubnet возвращает Address-ресурсы, привязанные к подсети
	// (через internal_ipv4.subnet_id). Использовалось ListUsedAddresses и
	// AddressService.ListBySubnet.
	AddressesBySubnet(ctx context.Context, subnetID string, p Pagination) ([]*domain.Address, string, error)
}

// AddressRepo — port-интерфейс репозитория адресов.
type AddressRepo interface {
	Get(ctx context.Context, id string) (*domain.Address, error)
	List(ctx context.Context, f AddressFilter, p Pagination) ([]*domain.Address, string, error)
	Insert(ctx context.Context, a *domain.Address) (*domain.Address, error)
	Update(ctx context.Context, a *domain.Address) (*domain.Address, error)
	Delete(ctx context.Context, id string) error
	// ExistsIP проверяет уникальность IP (external) в рамках folder/global.
	ExistsIP(ctx context.Context, ip string) (bool, error)
	// SetFolderID меняет folder_id ресурса.
	SetFolderID(ctx context.Context, id, folderID string) (*domain.Address, error)
	// GetByValue возвращает Address по конкретному IP (external или internal).
	// scope — опционально subnet_id (для уточнения внутри одной подсети).
	GetByValue(ctx context.Context, externalIP, internalIP, subnetID string) (*domain.Address, error)
	// SetIPSpec атомарно обновляет external_ipv4 / internal_ipv4 (JSONB-spec)
	// + emit outbox-event Address.UPDATED. Используется AddressAllocator.
	// Передавайте nil для поля, которое не нужно менять; оба nil — no-op.
	SetIPSpec(ctx context.Context, id string, externalIpv4 *domain.ExternalIpv4Spec, internalIpv4 *domain.InternalIpv4Spec) (*domain.Address, error)
}

// SecurityGroupRepo — port-интерфейс репозитория SG.
type SecurityGroupRepo interface {
	Get(ctx context.Context, id string) (*domain.SecurityGroup, error)
	List(ctx context.Context, f SecurityGroupFilter, p Pagination) ([]*domain.SecurityGroup, string, error)
	Insert(ctx context.Context, sg *domain.SecurityGroup) (*domain.SecurityGroup, error)
	Update(ctx context.Context, sg *domain.SecurityGroup) (*domain.SecurityGroup, error)
	Delete(ctx context.Context, id string) error
	SetFolderID(ctx context.Context, id, folderID string) (*domain.SecurityGroup, error)
	// UpdateRules атомарно заменяет набор правил SG: удаляет правила с
	// id ∈ deleteIDs и добавляет правила из add (с auto-id если пусто).
	// Возвращает обновлённый SG с актуальным списком правил.
	UpdateRules(ctx context.Context, sgID string, deleteIDs []string, add []domain.SecurityGroupRule) (*domain.SecurityGroup, error)
	// UpdateRule обновляет description/labels единичного правила в SG.
	UpdateRule(ctx context.Context, sgID, ruleID, description string, labels map[string]string, mask []string) (*domain.SecurityGroup, error)
}

// GatewayRepo — port-интерфейс репозитория Gateways.
type GatewayRepo interface {
	Get(ctx context.Context, id string) (*domain.Gateway, error)
	List(ctx context.Context, f GatewayFilter, p Pagination) ([]*domain.Gateway, string, error)
	Insert(ctx context.Context, g *domain.Gateway) (*domain.Gateway, error)
	Update(ctx context.Context, g *domain.Gateway) (*domain.Gateway, error)
	Delete(ctx context.Context, id string) error
	SetFolderID(ctx context.Context, id, folderID string) (*domain.Gateway, error)
}

// PrivateEndpointRepo — port-интерфейс репозитория PrivateEndpoints.
type PrivateEndpointRepo interface {
	Get(ctx context.Context, id string) (*domain.PrivateEndpoint, error)
	List(ctx context.Context, f PrivateEndpointFilter, p Pagination) ([]*domain.PrivateEndpoint, string, error)
	Insert(ctx context.Context, pe *domain.PrivateEndpoint) (*domain.PrivateEndpoint, error)
	Update(ctx context.Context, pe *domain.PrivateEndpoint) (*domain.PrivateEndpoint, error)
	Delete(ctx context.Context, id string) error
}

// RouteTableRepo — port-интерфейс репозитория таблиц маршрутизации.
type RouteTableRepo interface {
	Get(ctx context.Context, id string) (*domain.RouteTable, error)
	List(ctx context.Context, f RouteTableFilter, p Pagination) ([]*domain.RouteTable, string, error)
	Insert(ctx context.Context, rt *domain.RouteTable) (*domain.RouteTable, error)
	Update(ctx context.Context, rt *domain.RouteTable) (*domain.RouteTable, error)
	Delete(ctx context.Context, id string) error
	// SetFolderID меняет folder_id ресурса.
	SetFolderID(ctx context.Context, id, folderID string) (*domain.RouteTable, error)
}

// FolderClient — port для проверки существования Folder и lookup'а cloud_id.
type FolderClient interface {
	Exists(ctx context.Context, folderID string) (bool, error)
	// GetCloudID возвращает cloud_id для Folder. Используется в IPAM-cascade
	// (cloud-pool-selector lookup). Empty string + nil error если folder не
	// существует на стороне backend (NotFound пропускается, т.к. caller сам
	// решает что делать).
	GetCloudID(ctx context.Context, folderID string) (string, error)
}

// SubnetExistsChecker — port для проверки существования Subnet (используется другими сервисами).
type SubnetExistsChecker interface {
	GetSubnet(ctx context.Context, id string) (*domain.Subnet, error)
}

// ZoneRegistry — port для проверки существования зоны (таблица `zones`).
//
// Используется SubnetService для валидации `zone_id` в `Create` и `Relocate`:
// списка допустимых zone-id больше нет в хардкоде (раньше был whitelist
// `ru-central1-{a,b,c,d}` в kacho-corelib/validate). Источник истины — БД.
//
// Get возвращает ErrNotFound для несуществующей зоны.
// ListIDs возвращает идентификаторы всех зарегистрированных зон — нужен
// для формирования динамического сообщения `must be one of: ...`. Без
// пагинации (зон в системе — единицы; даже при росте до десятков
// fits in single SELECT).
type ZoneRegistry interface {
	Get(ctx context.Context, id string) (*domain.Zone, error)
	ListIDs(ctx context.Context) ([]string, error)
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
