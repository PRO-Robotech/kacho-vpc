// iface.go — value-объекты (Pagination, *Filter) и peer-service port-интерфейсы
// (ProjectClient / ZoneRegistry / SubnetExistsChecker), используемые use-case
// слоем kacho-vpc.
//
// KAC-94 A.7 ultra-final (skill evgeniy §1 A.7 + §6 G.6): legacy port-
// интерфейсы (`NetworkRepoIface` / `SubnetRepoIface` / …) И concrete-структуры
// (`*repo.NetworkRepo` / `*repo.SubnetRepo` / `*repo.AddressRepo` /
// `*repo.RouteTableRepo` / `*repo.SecurityGroupRepo` / `*repo.GatewayRepo` /
// `*repo.PrivateEndpointRepo` / `*repo.NetworkInterfaceRepo`) — **удалены**.
//
// Use-case-слой VPC + admin-services + peer-port'ы работают через
// CQRS-Repository (`internal/repo/kacho`) — `kacho.Repository` с `Reader(ctx)`
// / `Writer(ctx)` разделением (skill evgeniy §6 G.1-G.7). Узкие port'ы admin/
// peer-сервисов получают тонкие adapter'ы поверх `kacho.Repository` из
// пакета `internal/repo/cqrsadapter`.
//
// Этот файл оставлен только под Filter-type-alias'ы (`SubnetFilter` /
// `NetworkFilter` / `AddressFilter` / …) — они проксируются на leaf-пакет
// `internal/repo/kacho` (D.1), и peer-service port'ы (ProjectClient /
// SubnetExistsChecker / ZoneRegistry).

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
// callers (`internal/apps/kacho/api/*/iface.go`, `repomock`, integration-тесты).
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
//
// Wave 5 replicate (KAC-94 D.1): type-alias на `kacho.SubnetFilter` — см. doc на
// Pagination/NetworkFilter выше.
type SubnetFilter = kachorepo.SubnetFilter

// AddressFilter — фильтр для списка адресов.
//
// A.7 sub-PR 2 (KAC-94 D.1): type-alias на `kacho.AddressFilter` (parity с
// NetworkFilter/SubnetFilter/RouteTableFilter/SecurityGroupFilter — все они
// уже были type-alias). После CQRS-миграции Address use-cases ходят в
// `kacho.AddressReaderIface.List(ctx, f kacho.AddressFilter, ...)`, поэтому
// фильтр в legacy-репо и в CQRS-iface должен быть одним типом.
type AddressFilter = kachorepo.AddressFilter

// RouteTableFilter — фильтр для списка таблиц маршрутизации. Wave 5
// replicate (KAC-94 D.1): type-alias на `kacho.RouteTableFilter`.
type RouteTableFilter = kachorepo.RouteTableFilter

// SecurityGroupFilter — фильтр для списка SG.
//
// Wave 5 (KAC-94 D.1): type-alias на `kacho.SecurityGroupFilter` — см. doc на
// Pagination выше.
type SecurityGroupFilter = kachorepo.SecurityGroupFilter

// GatewayFilter — фильтр для списка NAT Gateways.
type GatewayFilter struct {
	ProjectID string
	Name     string
	Filter   string
}

// PrivateEndpointFilter — фильтр для списка PrivateEndpoints. Wave 5
// replicate (KAC-94 D.1): type-alias на `kacho.PrivateEndpointFilter`.
type PrivateEndpointFilter = kachorepo.PrivateEndpointFilter

// AddressPoolFilter — фильтр для списка пулов. AddressPool — глобальный
// infrastructure-ресурс, поэтому folder/cloud/org здесь нет.
//
// Wave 5 replicate (KAC-94 A.7 sub-PR 1/6, skill evgeniy §6 G.1-G.7): переехал
// в leaf-пакет `kacho` вместе с остальными filter-типами. Здесь — type-alias
// для всех существующих callers (`internal/repo/address_pool_repo.go`,
// `internal/apps/kacho/api/addresspool/*`).
type AddressPoolFilter = kachorepo.AddressPoolFilter

// NetworkInterfaceFilter — фильтр для List NIC. Используется repo-уровнем;
// use-case-пакет `internal/apps/kacho/api/networkinterface` экспортирует
// type-alias на него.
type NetworkInterfaceFilter struct {
	ProjectID   string
	InstanceID string
	SubnetID   string
	// NetworkID — не поддерживается фильтром (NIC не хранит network_id), поле
	// оставлено для совместимости с handler-сигнатурой; репо его игнорирует.
	NetworkID string
}

// ProjectClient — port для проверки существования Folder и lookup'а cloud_id.
type ProjectClient interface {
	Exists(ctx context.Context, folderID string) (bool, error)
	// GetCloudID возвращает cloud_id для Folder. Используется в IPAM-cascade
	// (cloud-pool-selector lookup). Empty string + nil error если folder не
	// существует на стороне backend (NotFound пропускается, т.к. caller сам
	// решает что делать).
	GetCloudIDFromProject(ctx context.Context, folderID string) (string, error)
}

// SubnetExistsChecker — port для проверки существования Subnet (используется другими сервисами).
type SubnetExistsChecker interface {
	GetSubnet(ctx context.Context, id string) (*kachorepo.SubnetRecord, error)
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
