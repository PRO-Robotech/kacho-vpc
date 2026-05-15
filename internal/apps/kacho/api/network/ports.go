// Package network — use-case-структура ресурса Network (skill evgeniy §2 B.1-B.4).
//
// Wave 3a pilot (KAC-94): здесь живёт «бизнес-логика» Network — CreateNetworkUseCase,
// UpdateNetworkUseCase, DeleteNetworkUseCase, MoveNetworkUseCase плюс тонкий
// gRPC-handler. Раньше монолитный `internal/service/network.go` (NetworkService)
// был fat-service со всеми методами в одном файле; теперь use-case'ы локализованы
// рядом с handler'ом (B.4 — локальность), repo-операции делегируются через
// **локальные** port-интерфейсы (ниже).
//
// Локальные интерфейсы (а не type-alias на `internal/ports.NetworkRepo`) — это
// сознательный выбор по skill §6 G.2-G.3: каждый use-case-пакет описывает только
// то, что РЕАЛЬНО использует. Network-use-case'ы не работают со всеми методами
// NetworkRepo (например, SetFolderID нужен только Move), но публиковать здесь
// узкий контракт всё равно полезно — Wave 3b (другие 7 ресурсов) реплицирует
// тот же шаблон. Адаптерами выступают существующие `internal/repo/network_repo.go`
// и `internal/ports/portmock` — они уже реализуют `internal/ports.NetworkRepo`,
// который ⊇ локальному интерфейсу, поэтому Go-типизация работает без shim'ов.
package network

import (
	"context"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/ports"
)

// Pagination, *Filter — пере-используем единые value-объекты `internal/ports`
// (alias'ы, не копии). Иначе пришлось бы дублировать структуры или гонять между
// пакетами через двойную конверсию.
type (
	Pagination          = ports.Pagination
	NetworkFilter       = ports.NetworkFilter
	SubnetFilter        = ports.SubnetFilter
	RouteTableFilter    = ports.RouteTableFilter
	SecurityGroupFilter = ports.SecurityGroupFilter
)

// NetworkRepo — то, что use-case'ам Network нужно от репозитория сетей.
//
// Все методы возвращают `*domain.NetworkRecord` (skill evgeniy §4 D.1 / §7 H.1 —
// repo-entity несёт DB-managed CreatedAt). Insert/Update принимают `*domain.Network`
// (без CreatedAt).
type NetworkRepo interface {
	Get(ctx context.Context, id string) (*domain.NetworkRecord, error)
	List(ctx context.Context, f NetworkFilter, p Pagination) ([]*domain.NetworkRecord, string, error)
	Insert(ctx context.Context, n *domain.Network) (*domain.NetworkRecord, error)
	Update(ctx context.Context, n *domain.Network) (*domain.NetworkRecord, error)
	Delete(ctx context.Context, id string) error
	SetFolderID(ctx context.Context, id, folderID string) (*domain.NetworkRecord, error)
}

// SubnetReader — узкое чтение Subnet, нужное для ListSubnets / checkNetworkEmpty.
type SubnetReader interface {
	List(ctx context.Context, f SubnetFilter, p Pagination) ([]*domain.SubnetRecord, string, error)
}

// RouteTableReader — узкое чтение RouteTable, нужное для ListRouteTables /
// checkNetworkEmpty.
type RouteTableReader interface {
	List(ctx context.Context, f RouteTableFilter, p Pagination) ([]*domain.RouteTableRecord, string, error)
}

// SecurityGroupRepo — то, что use-case'ам Network нужно от репозитория SG: List
// (для checkNetworkEmpty / ListSecurityGroups), Insert (для inline default-SG),
// Delete (для cleanup default-SG при Network.Delete).
type SecurityGroupRepo interface {
	List(ctx context.Context, f SecurityGroupFilter, p Pagination) ([]*domain.SecurityGroupRecord, string, error)
	Insert(ctx context.Context, sg *domain.SecurityGroup) (*domain.SecurityGroupRecord, error)
	Delete(ctx context.Context, id string) error
}

// FolderClient — то, что use-case'ам Network нужно от peer-сервиса
// kacho-resource-manager: проверка существования folder'а на request-path /
// в worker'е.
type FolderClient interface {
	Exists(ctx context.Context, folderID string) (bool, error)
}
