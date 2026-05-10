package service

import (
	"context"

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

// SecurityGroupFilter — фильтр для списка SG.
type SecurityGroupFilter struct {
	FolderID  string
	NetworkID string
	Filter    string
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

// GatewayFilter — фильтр для списка NAT Gateways.
type GatewayFilter struct {
	FolderID string
	Filter   string
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

// PrivateEndpointFilter — фильтр для списка PrivateEndpoints.
type PrivateEndpointFilter struct {
	FolderID string
	Filter   string
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

// SubnetClient — port для проверки существования Subnet (используется другими сервисами).
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
