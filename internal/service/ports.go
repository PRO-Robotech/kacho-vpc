package service

import (
	"context"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// Pagination описывает параметры постраничной выборки.
type Pagination struct {
	PageToken string
	PageSize  int32
}

// Selector описывает один фильтр при операции List.
type Selector struct {
	Name           string
	FolderID       string
	CloudID        string
	OrganizationID string
	NetworkID      string // для Subnet/SG/RT
	Labels         map[string]string
}

// NetworkRepo — port-интерфейс для репозитория сетей.
type NetworkRepo interface {
	GetByUID(ctx context.Context, uid string) (*domain.Network, error)
	GetByFolderAndName(ctx context.Context, folderID, name string) (*domain.Network, error)
	List(ctx context.Context, selectors []Selector, page Pagination) ([]*domain.Network, string, int64, error)
	SnapshotResourceVersion(ctx context.Context) (int64, error)
	Insert(ctx context.Context, network *domain.Network) (*domain.Network, error)
	Update(ctx context.Context, network *domain.Network) (*domain.Network, error)
	HardDelete(ctx context.Context, uid string) error
	HasDependents(ctx context.Context, uid string) (bool, error)
}

// SubnetRepo — port-интерфейс для репозитория подсетей.
type SubnetRepo interface {
	GetByUID(ctx context.Context, uid string) (*domain.Subnet, error)
	GetByFolderAndName(ctx context.Context, folderID, name string) (*domain.Subnet, error)
	List(ctx context.Context, selectors []Selector, page Pagination) ([]*domain.Subnet, string, int64, error)
	SnapshotResourceVersion(ctx context.Context) (int64, error)
	Insert(ctx context.Context, subnet *domain.Subnet) (*domain.Subnet, error)
	Update(ctx context.Context, subnet *domain.Subnet) (*domain.Subnet, error)
	HardDelete(ctx context.Context, uid string) error
}

// SecurityGroupRepo — port-интерфейс для репозитория групп безопасности.
type SecurityGroupRepo interface {
	GetByUID(ctx context.Context, uid string) (*domain.SecurityGroup, error)
	GetByFolderAndName(ctx context.Context, folderID, name string) (*domain.SecurityGroup, error)
	List(ctx context.Context, selectors []Selector, page Pagination) ([]*domain.SecurityGroup, string, int64, error)
	SnapshotResourceVersion(ctx context.Context) (int64, error)
	Insert(ctx context.Context, sg *domain.SecurityGroup) (*domain.SecurityGroup, error)
	Update(ctx context.Context, sg *domain.SecurityGroup) (*domain.SecurityGroup, error)
	HardDelete(ctx context.Context, uid string) error
	HasDependents(ctx context.Context, uid string) (bool, error)
}

// RouteTableRepo — port-интерфейс для репозитория таблиц маршрутизации.
type RouteTableRepo interface {
	GetByUID(ctx context.Context, uid string) (*domain.RouteTable, error)
	GetByFolderAndName(ctx context.Context, folderID, name string) (*domain.RouteTable, error)
	List(ctx context.Context, selectors []Selector, page Pagination) ([]*domain.RouteTable, string, int64, error)
	SnapshotResourceVersion(ctx context.Context) (int64, error)
	Insert(ctx context.Context, rt *domain.RouteTable) (*domain.RouteTable, error)
	Update(ctx context.Context, rt *domain.RouteTable) (*domain.RouteTable, error)
	HardDelete(ctx context.Context, uid string) error
	HasDependents(ctx context.Context, uid string) (bool, error)
}

// AddressRepo — port-интерфейс для репозитория адресов.
type AddressRepo interface {
	GetByUID(ctx context.Context, uid string) (*domain.Address, error)
	GetByFolderAndName(ctx context.Context, folderID, name string) (*domain.Address, error)
	List(ctx context.Context, selectors []Selector, page Pagination) ([]*domain.Address, string, int64, error)
	SnapshotResourceVersion(ctx context.Context) (int64, error)
	Insert(ctx context.Context, addr *domain.Address) (*domain.Address, error)
	Update(ctx context.Context, addr *domain.Address) (*domain.Address, error)
	UpdateStatus(ctx context.Context, uid, state string) error
	HardDelete(ctx context.Context, uid string) error
	HasDependents(ctx context.Context, uid string) (bool, error)
}

// FolderClient — port для проверки существования Folder в resource-manager.
type FolderClient interface {
	Exists(ctx context.Context, folderID string) (bool, error)
}
