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

// FolderClient — port для проверки существования Folder.
type FolderClient interface {
	Exists(ctx context.Context, folderID string) (bool, error)
}

// SubnetClient — port для проверки существования Subnet (используется другими сервисами).
type SubnetExistsChecker interface {
	GetSubnet(ctx context.Context, id string) (*domain.Subnet, error)
}
