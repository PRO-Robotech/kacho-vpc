// iface.go — value-объекты (Pagination, *Filter) и peer-service port-интерфейсы
// (FolderClient / ZoneRegistry / SubnetExistsChecker), используемые use-case
// слоем kacho-vpc.
//
// Wave 5 finalize (KAC-94, skill evgeniy §1 A.7 + §6 G.6): legacy port-
// интерфейсы `NetworkRepoIface` / `SubnetRepoIface` / `AddressRepoIface` /
// `RouteTableRepoIface` / `SecurityGroupRepoIface` / `GatewayRepoIface` /
// `PrivateEndpointRepoIface` / `AddressPoolRepoIface` /
// `AddressPoolBindingRepoIface` / `CloudPoolSelectorRepoIface` — **удалены**.
//
// Use-case-слой VPC переехал на CQRS-Repository (`internal/repo/kacho`) —
// Network / Subnet / Address / RouteTable / SecurityGroup / Gateway /
// PrivateEndpoint / NetworkInterface работают через `kacho.Repository` с
// `Reader(ctx)` / `Writer(ctx)` разделением (skill evgeniy §6 G.1-G.7).
//
// Admin-сервисы, которым нужны старые `*Repo` (`networkinternal.Service`,
// addresspool use-case'ы, `addressref.Service`) описывают **узкие** port-
// интерфейсы у себя в пакете (skill evgeniy §6 G.2 — «каждый use-case-пакет
// описывает узкий port»). Concrete-структуры `*repo.NetworkRepo` /
// `*repo.SubnetRepo` / `*repo.AddressRepo` / … / `*repo.AddressPoolRepo` /
// `*repo.AddressPoolBindingRepo` / `*repo.CloudPoolSelectorRepo` остаются как
// pgxpool-impl этих узких port'ов; общий port-интерфейс в этом файле — больше
// не нужен.
//
// Concrete-структуры **не удалены** только из-за integration-тестов
// `internal/repo/*_integration_test.go`, которые конструируют их напрямую
// (`repo.NewNetworkRepo(pool)`) и проверяют SQL-сторону репо — это
// acceptable per workspace-CLAUDE.md «Within-service refs — DB-уровень»
// (raw-pgx-test of constraints + indices). Когда integration-test-слой
// тоже переедет на CQRS-pg-impl (`internal/repo/kacho/pg`), legacy
// файлы будут удалены вторым PR.

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
	FolderID string
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
	FolderID   string
	InstanceID string
	SubnetID   string
	// NetworkID — не поддерживается фильтром (NIC не хранит network_id), поле
	// оставлено для совместимости с handler-сигнатурой; репо его игнорирует.
	NetworkID string
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
