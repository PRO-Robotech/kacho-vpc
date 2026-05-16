// iface.go — port-интерфейсы (Clean Architecture boundaries) и связанные с
// ними value-объекты (Pagination, *Filter) для kacho-vpc. Раньше — отдельный
// пакет `internal/repo`; Wave 5 (KAC-94, skill evgeniy §6 G.1): объединён с
// `internal/repo`, чтобы интерфейс и его pgxpool-реализация жили рядом.
//
// Чтобы избежать имя-в-имя коллизии с уже существующими concrete-структурами
// репо (`NetworkRepo struct`, `SubnetRepo struct`, …), интерфейсы переименованы
// с суффиксом `Iface` (`NetworkRepoIface`, `SubnetRepoIface`, …). Семантика и
// сигнатуры не изменились. Mock-реализации — в подпакете
// `internal/repo/repomock/` (раньше — `internal/repo/repomock/`).

package repo

import (
	"context"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// Pagination — постраничная навигация. Wave 5 (KAC-94 D.1) переехала вместе с
// NetworkFilter / SecurityGroupFilter / NetworkRecord в leaf-пакет
// `internal/repo/kacho/`, чтобы избежать import-cycle
// `repo → repo/kacho → repo`. Здесь оставлен type-alias для всех существующих
// callers (`internal/apps/kacho/api/*/ports.go`, `repomock`, integration-тесты).
type Pagination = kachorepo.Pagination

// NetworkFilter — фильтр для списка сетей.
//
// Name — точное совпадение имени (для sync uniqueness-check в Create; verbatim
// YC `name=...` semantics, но без парсинга filter-выражения). См. kacho-vpc#8.
//
// Wave 5 (KAC-94 D.1): type-alias на `kacho.NetworkFilter` — см. doc на
// Pagination выше.
type NetworkFilter = kachorepo.NetworkFilter

// SubnetFilter — фильтр для списка подсетей.
type SubnetFilter struct {
	FolderID  string
	NetworkID string
	Name      string
	Filter    string // raw filter expression (YC-syntax)
}

// AddressFilter — фильтр для списка адресов.
type AddressFilter struct {
	FolderID string
	Name     string
	Filter   string
	// SubnetID — фильтр по подсети: матчит internal_ipv4.subnet_id ИЛИ
	// internal_ipv6.subnet_id (для ListAddresses?subnet_id=). "" = без фильтра.
	SubnetID string
}

// RouteTableFilter — фильтр для списка таблиц маршрутизации.
type RouteTableFilter struct {
	FolderID  string
	NetworkID string
	Name      string
	Filter    string
}

// SecurityGroupFilter — фильтр для списка SG.
//
// Wave 5 (KAC-94 D.1): type-alias на `kacho.SecurityGroupFilter` — см. doc на
// Pagination выше.
type SecurityGroupFilter = kachorepo.SecurityGroupFilter

// GatewayFilter — фильтр для списка NAT Gateways.
type GatewayFilter struct {
	FolderID string
	Name     string
	Filter   string
}

// PrivateEndpointFilter — фильтр для списка PrivateEndpoints.
type PrivateEndpointFilter struct {
	FolderID string
	Name     string
	Filter   string
}

// AddressPoolFilter — фильтр для списка пулов. AddressPool — глобальный
// infrastructure-ресурс, поэтому folder/cloud/org здесь нет.
type AddressPoolFilter struct {
	Kind   domain.AddressPoolKind // 0 = any
	ZoneID string                 // "" = any
}

// NetworkInterfaceFilter — фильтр для List NIC. Используется repo-уровнем;
// use-case-пакет `internal/apps/kacho/api/networkinterface` экспортирует
// type-alias на него.
type NetworkInterfaceFilter struct {
	FolderID   string
	InstanceID string
	SubnetID   string
	// NetworkID — не поддерживается фильтром (NIC не хранит network_id), поле
	// оставлено для совместимости с handler-сигнатурой; репо его игнорирует.
	NetworkID string
}

// NetworkRepo — port-интерфейс репозитория сетей. Возвращает
// `*kachorepo.NetworkRecord` (repo-entity с DB-managed CreatedAt) — skill
// evgeniy §4 D.1 / §6 G.2 / §7 H.1: CreatedAt живёт в repo-проекции, не в
// domain.Network. Insert/Update принимают domain.Network (без CreatedAt) —
// время выставляет репо.
//
// Wave 5 (KAC-94): `NetworkRecord` уехал из domain в repo-leaf
// (`internal/repo/kacho/entity_network.go`). Остальные ресурсы — на месте.
type NetworkRepoIface interface {
	Get(ctx context.Context, id string) (*kachorepo.NetworkRecord, error)
	List(ctx context.Context, f NetworkFilter, p Pagination) ([]*kachorepo.NetworkRecord, string, error)
	Insert(ctx context.Context, n *domain.Network) (*kachorepo.NetworkRecord, error)
	Update(ctx context.Context, n *domain.Network) (*kachorepo.NetworkRecord, error)
	Delete(ctx context.Context, id string) error
	// SetFolderID меняет folder_id ресурса (для :move action). Возвращает обновлённый ресурс.
	SetFolderID(ctx context.Context, id, folderID string) (*kachorepo.NetworkRecord, error)
}

// SubnetRepo — port-интерфейс репозитория подсетей.
//
// Wave 2 batch A (KAC-94): возвращает `*domain.SubnetRecord` (repo-entity с
// DB-managed CreatedAt) вместо `*domain.Subnet`. Insert/Update принимают
// `*domain.Subnet` (без CreatedAt — DB-managed). Skill evgeniy §4 D.1 / §6 G.2 /
// §7 H.1. Parity с NetworkRepo (KAC-99).
type SubnetRepoIface interface {
	Get(ctx context.Context, id string) (*domain.SubnetRecord, error)
	List(ctx context.Context, f SubnetFilter, p Pagination) ([]*domain.SubnetRecord, string, error)
	Insert(ctx context.Context, s *domain.Subnet) (*domain.SubnetRecord, error)
	Update(ctx context.Context, s *domain.Subnet) (*domain.SubnetRecord, error)
	Delete(ctx context.Context, id string) error
	// SetFolderID меняет folder_id ресурса.
	SetFolderID(ctx context.Context, id, folderID string) (*domain.SubnetRecord, error)
	// SetCidrBlocks атомарно обновляет v4_cidr_blocks и v6_cidr_blocks
	// (для AddCidrBlocks/RemoveCidrBlocks). Триггеры EXCLUDE
	// (subnets_no_overlap_v4 / subnets_no_overlap_v6) проверяют overlap по
	// primary CIDR каждого семейства.
	SetCidrBlocks(ctx context.Context, id string, v4, v6 []string) (*domain.SubnetRecord, error)
	// SetZoneID меняет zone_id у подсети (для Relocate).
	SetZoneID(ctx context.Context, id, zoneID string) (*domain.SubnetRecord, error)
	// AddressesBySubnet возвращает Address-ресурсы, привязанные к подсети
	// (через internal_ipv4.subnet_id). Использовалось ListUsedAddresses и
	// AddressService.ListBySubnet.
	AddressesBySubnet(ctx context.Context, subnetID string, p Pagination) ([]*domain.AddressRecord, string, error)
}

// AddressRepo — port-интерфейс репозитория адресов.
//
// Wave 2 batch A (KAC-94): возвращает `*domain.AddressRecord` (repo-entity с
// DB-managed CreatedAt). Insert/Update принимают `*domain.Address` (без CreatedAt).
type AddressRepoIface interface {
	Get(ctx context.Context, id string) (*domain.AddressRecord, error)
	List(ctx context.Context, f AddressFilter, p Pagination) ([]*domain.AddressRecord, string, error)
	Insert(ctx context.Context, a *domain.Address) (*domain.AddressRecord, error)
	Update(ctx context.Context, a *domain.Address) (*domain.AddressRecord, error)
	Delete(ctx context.Context, id string) error
	// ExistsIP проверяет уникальность IP (external) в рамках folder/global.
	ExistsIP(ctx context.Context, ip string) (bool, error)
	// SetFolderID меняет folder_id ресурса.
	SetFolderID(ctx context.Context, id, folderID string) (*domain.AddressRecord, error)
	// GetByValue возвращает Address по конкретному IP (external или internal).
	// scope — опционально subnet_id (для уточнения внутри одной подсети).
	GetByValue(ctx context.Context, externalIP, internalIP, subnetID string) (*domain.AddressRecord, error)
	// SetIPSpec атомарно обновляет external_ipv4 / internal_ipv4 (JSONB-spec)
	// + emit outbox-event Address.UPDATED. Используется AddressAllocator.
	// Передавайте nil для поля, которое не нужно менять; оба nil — no-op.
	SetIPSpec(ctx context.Context, id string, externalIpv4 *domain.ExternalIpv4Spec, internalIpv4 *domain.InternalIpv4Spec) (*domain.AddressRecord, error)
	// SetInternalIPv6 атомарно обновляет internal_ipv6 (JSONB-spec) + emit
	// outbox-event Address.UPDATED. Используется AllocateInternalIPv6 (random-pick
	// + retry на UNIQUE-violation). nil → no-op.
	SetInternalIPv6(ctx context.Context, id string, spec *domain.InternalIpv6Spec) (*domain.AddressRecord, error)

	// AllocateIPFromFreelist атомарно достаёт один свободный IP из
	// address_pool_free_ips для poolID (FOR UPDATE SKIP LOCKED) и проставляет
	// его в addresses.external_ipv4{address, address_pool_id} для addressID.
	// Один SQL-statement → нулевая contention между конкурентными аллокаторами;
	// используется AddressService.AllocateExternalIP (миграция 0014).
	// Возвращает ErrPoolExhausted если freelist пуст.
	AllocateIPFromFreelist(ctx context.Context, poolID, addressID string) (string, error)
	// ReturnIPToFreelist кладёт IP обратно в address_pool_free_ips для poolID.
	// Идемпотентно (ON CONFLICT DO NOTHING). Вызывается AddressService.Delete-
	// worker'ом, чтобы освобождённый IP сразу вернулся в оборот аллокатора.
	ReturnIPToFreelist(ctx context.Context, poolID, ip string) error

	// InitIPv6PoolCursor инициализирует sparse counter-based allocator для
	// IPv6-пула (миграция 0021, KAC-60): INSERT INTO ipv6_pool_cursors
	// (pool_id, next_offset=1) ON CONFLICT DO NOTHING. Идемпотентно.
	// Вызывается AddressPoolService.Create/Update когда поставлен v6 CIDR.
	InitIPv6PoolCursor(ctx context.Context, poolID string) error
	// AllocateExternalIPv6 атомарно выдаёт следующий IPv6 из пула:
	//   1) try pop offset из ipv6_released_offsets (FOR UPDATE SKIP LOCKED) —
	//      переиспользование освобождённых;
	//   2) fallback fresh: UPDATE ipv6_pool_cursors SET next_offset = next_offset+1
	//      RETURNING old next_offset — counter-based monotonic;
	//   3) ip = pool_base + offset (по v6 CIDR пула), INSERT INTO
	//      ipv6_allocated_ips (pool_id, ip, offset, address_id);
	//   4) UPDATE addresses.external_ipv6 JSONB-spec в той же tx (+ outbox emit).
	// Возвращает выделенный IP-литерал. ErrPoolExhausted если пул маркирован
	// исчерпанным (next_offset > 2^N где N — host-bits CIDR'а).
	AllocateExternalIPv6(ctx context.Context, poolID, addressID, zoneID string) (string, error)
	// FreeExternalIPv6 освобождает IPv6 у addressID: ищет ipv6_allocated_ips by
	// address_id, INSERT'ит offset в ipv6_released_offsets (ON CONFLICT DO
	// NOTHING — идемпотент), DELETE'ит из ipv6_allocated_ips. Вызывается
	// AddressService.Delete для address с external_ipv6.
	FreeExternalIPv6(ctx context.Context, addressID string) error

	// SetReference upsert'ит referrer-row адреса И выставляет addresses.used=true
	// в одной tx. ErrNotFound если address не существует.
	SetReference(ctx context.Context, ref *domain.AddressReference) (*domain.AddressReference, error)
	// MarkEphemeralInUse атомарно: addresses.reserved=false + addresses.used=true
	// + upsert referrer-row. ErrNotFound если address не существует. Идемпотентно.
	MarkEphemeralInUse(ctx context.Context, ref *domain.AddressReference) (*domain.AddressReference, error)
	// ClearReference удаляет referrer-row адреса (no-op если нет) И выставляет
	// addresses.used=false в одной tx. ErrNotFound если address не существует.
	ClearReference(ctx context.Context, addressID string) error
	// GetReference возвращает referrer-row адреса. ErrNotFound если address
	// не существует ИЛИ у него нет referrer'а.
	GetReference(ctx context.Context, addressID string) (*domain.AddressReference, error)
	// ReferencesForAddresses возвращает referrer-row'ы для набора address-id
	// (map id→ref; отсутствующие ключи = нет referrer'а). Для batch-обогащения
	// ListUsedAddresses. Пустой вход → пустой map.
	ReferencesForAddresses(ctx context.Context, addressIDs []string) (map[string]*domain.AddressReference, error)
}

// SecurityGroupRepo — port-интерфейс репозитория SG.
//
// Wave 2 batch B (KAC-94): возвращает `*domain.SecurityGroupRecord` (repo-entity с
// DB-managed CreatedAt). Insert/Update принимают `*domain.SecurityGroup` (без CreatedAt).
// Skill evgeniy §4 D.1 / §6 G.2 / §7 H.1. Parity с NetworkRepo (KAC-99).
type SecurityGroupRepoIface interface {
	Get(ctx context.Context, id string) (*domain.SecurityGroupRecord, error)
	List(ctx context.Context, f SecurityGroupFilter, p Pagination) ([]*domain.SecurityGroupRecord, string, error)
	Insert(ctx context.Context, sg *domain.SecurityGroup) (*domain.SecurityGroupRecord, error)
	Update(ctx context.Context, sg *domain.SecurityGroup) (*domain.SecurityGroupRecord, error)
	Delete(ctx context.Context, id string) error
	SetFolderID(ctx context.Context, id, folderID string) (*domain.SecurityGroupRecord, error)
	// UpdateRules атомарно заменяет набор правил SG: удаляет правила с
	// id ∈ deleteIDs и добавляет правила из add (с auto-id если пусто).
	// Возвращает обновлённый SG с актуальным списком правил.
	UpdateRules(ctx context.Context, sgID string, deleteIDs []string, add []domain.SecurityGroupRule) (*domain.SecurityGroupRecord, error)
	// UpdateRule обновляет description/labels единичного правила в SG.
	UpdateRule(ctx context.Context, sgID, ruleID, description string, labels map[string]string, mask []string) (*domain.SecurityGroupRecord, error)
}

// GatewayRepo — port-интерфейс репозитория Gateways.
//
// Wave 2 batch B (KAC-94): возвращает `*domain.GatewayRecord` (repo-entity с
// DB-managed CreatedAt). Insert/Update принимают `*domain.Gateway` (без CreatedAt).
type GatewayRepoIface interface {
	Get(ctx context.Context, id string) (*domain.GatewayRecord, error)
	List(ctx context.Context, f GatewayFilter, p Pagination) ([]*domain.GatewayRecord, string, error)
	Insert(ctx context.Context, g *domain.Gateway) (*domain.GatewayRecord, error)
	Update(ctx context.Context, g *domain.Gateway) (*domain.GatewayRecord, error)
	Delete(ctx context.Context, id string) error
	SetFolderID(ctx context.Context, id, folderID string) (*domain.GatewayRecord, error)
}

// PrivateEndpointRepo — port-интерфейс репозитория PrivateEndpoints.
//
// Wave 2 batch B (KAC-94): возвращает `*domain.PrivateEndpointRecord` (repo-entity
// с DB-managed CreatedAt). Insert/Update принимают `*domain.PrivateEndpoint` (без CreatedAt).
type PrivateEndpointRepoIface interface {
	Get(ctx context.Context, id string) (*domain.PrivateEndpointRecord, error)
	List(ctx context.Context, f PrivateEndpointFilter, p Pagination) ([]*domain.PrivateEndpointRecord, string, error)
	Insert(ctx context.Context, pe *domain.PrivateEndpoint) (*domain.PrivateEndpointRecord, error)
	Update(ctx context.Context, pe *domain.PrivateEndpoint) (*domain.PrivateEndpointRecord, error)
	Delete(ctx context.Context, id string) error
}

// RouteTableRepo — port-интерфейс репозитория таблиц маршрутизации.
//
// Wave 2 batch A (KAC-94): возвращает `*domain.RouteTableRecord` (repo-entity
// с DB-managed CreatedAt). Insert/Update принимают `*domain.RouteTable` (без CreatedAt).
type RouteTableRepoIface interface {
	Get(ctx context.Context, id string) (*domain.RouteTableRecord, error)
	List(ctx context.Context, f RouteTableFilter, p Pagination) ([]*domain.RouteTableRecord, string, error)
	Insert(ctx context.Context, rt *domain.RouteTable) (*domain.RouteTableRecord, error)
	Update(ctx context.Context, rt *domain.RouteTable) (*domain.RouteTableRecord, error)
	Delete(ctx context.Context, id string) error
	// SetFolderID меняет folder_id ресурса.
	SetFolderID(ctx context.Context, id, folderID string) (*domain.RouteTableRecord, error)
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
	GetSubnet(ctx context.Context, id string) (*domain.SubnetRecord, error)
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
type AddressPoolRepoIface interface {
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
	ListAddressesByPool(ctx context.Context, poolID, folderFilter string, p Pagination) ([]*domain.AddressRecord, string, error)

	// PopulateFreelistForPool материализует все usable IPv4 адреса из
	// pool.cidr_blocks в address_pool_free_ips (миграция 0014). Идемпотентно
	// (ON CONFLICT DO NOTHING). IPv6 CIDR'ы пропускаются — sparse v6
	// аллокатор отложен. Вызывается InternalAddressPoolService.Create
	// сразу после Insert, чтобы новый pool был сразу пригоден для
	// PG-native freelist allocator'а.
	PopulateFreelistForPool(ctx context.Context, poolID string) error
}

// AddressPoolBindingRepo — explicit биндинги pool ↔ network/address (per-resource pinning).
type AddressPoolBindingRepoIface interface {
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
type CloudPoolSelectorRepoIface interface {
	// Set — atomic upsert (cloud_id, selector, set_by).
	// Empty selector допустим — это семантически равно отсутствию binding'а
	// (в cascade-resolve skip'аем label-step).
	Set(ctx context.Context, cloudID string, selector map[string]string, setBy string) error
	// Get возвращает selector + metadata. ErrNotFound если binding не задан.
	Get(ctx context.Context, cloudID string) (*domain.CloudPoolSelector, error)
	// Unset удаляет binding (idempotent — no error if missing).
	Unset(ctx context.Context, cloudID string) error
}
