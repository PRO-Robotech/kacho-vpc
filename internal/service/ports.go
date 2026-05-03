package service

import (
	"context"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// NetworkRepo — port-интерфейс репозитория сетей.
type NetworkRepo interface {
	Get(ctx context.Context, id string) (*domain.Network, error)
	GetByFolderAndName(ctx context.Context, folderID, name string) (*domain.Network, error)
	List(ctx context.Context, filter ListFilter) ([]domain.Network, string, error)
	Create(ctx context.Context, n *domain.Network) error
	Update(ctx context.Context, n *domain.Network) error
	SoftDelete(ctx context.Context, id string) error
	HasDependents(ctx context.Context, id string) (bool, error)
}

// SubnetRepo — port-интерфейс репозитория подсетей.
type SubnetRepo interface {
	Get(ctx context.Context, id string) (*domain.Subnet, error)
	GetByFolderAndName(ctx context.Context, folderID, name string) (*domain.Subnet, error)
	List(ctx context.Context, filter ListFilter) ([]domain.Subnet, string, error)
	Create(ctx context.Context, s *domain.Subnet) error
	Update(ctx context.Context, s *domain.Subnet) error
	SoftDelete(ctx context.Context, id string) error
}

// SecurityGroupRepo — port-интерфейс репозитория групп безопасности.
type SecurityGroupRepo interface {
	Get(ctx context.Context, id string) (*domain.SecurityGroup, error)
	GetByFolderAndName(ctx context.Context, folderID, name string) (*domain.SecurityGroup, error)
	List(ctx context.Context, filter ListFilter) ([]domain.SecurityGroup, string, error)
	Create(ctx context.Context, sg *domain.SecurityGroup) error
	Update(ctx context.Context, sg *domain.SecurityGroup) error
	SoftDelete(ctx context.Context, id string) error
}

// RouteTableRepo — port-интерфейс репозитория таблиц маршрутизации.
type RouteTableRepo interface {
	Get(ctx context.Context, id string) (*domain.RouteTable, error)
	GetByFolderAndName(ctx context.Context, folderID, name string) (*domain.RouteTable, error)
	List(ctx context.Context, filter ListFilter) ([]domain.RouteTable, string, error)
	Create(ctx context.Context, rt *domain.RouteTable) error
	Update(ctx context.Context, rt *domain.RouteTable) error
	SoftDelete(ctx context.Context, id string) error
}

// AddressRepo — port-интерфейс репозитория IP-адресов.
type AddressRepo interface {
	Get(ctx context.Context, id string) (*domain.Address, error)
	GetByFolderAndName(ctx context.Context, folderID, name string) (*domain.Address, error)
	List(ctx context.Context, filter ListFilter) ([]domain.Address, string, error)
	Create(ctx context.Context, a *domain.Address) error
	Update(ctx context.Context, a *domain.Address) error
	SoftDelete(ctx context.Context, id string) error
}

// FolderClient — port для проверки существования Folder в resource-manager.
type FolderClient interface {
	// Exists возвращает (true, nil) если Folder существует и активен.
	// Возвращает (false, nil) если Folder не найден (NOT_FOUND).
	// Возвращает (false, err) при технических ошибках.
	Exists(ctx context.Context, folderID string) (bool, error)
}

// ListFilter — параметры фильтрации/пагинации для List-запросов.
type ListFilter struct {
	FolderID  string
	PageSize  int64
	PageToken string
	Filter    string
	OrderBy   string
}
