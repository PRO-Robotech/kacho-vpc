// Package routetable — use-case-структура ресурса RouteTable (skill evgeniy §2 B.1-B.4).
//
// Wave 3b (KAC-94): replicate Wave 3a pilot Network → RouteTable. Бизнес-логика
// CreateRouteTableUseCase / UpdateRouteTableUseCase / DeleteRouteTableUseCase /
// MoveRouteTableUseCase / GetRouteTableUseCase / ListRouteTablesUseCase / ListOperationsUseCase
// плюс тонкий gRPC-handler.
//
// Локальные port-интерфейсы (а не type-alias на `internal/ports.RouteTableRepo`)
// — skill §6 G.2-G.3: каждый use-case-пакет описывает только то, что РЕАЛЬНО
// использует.
package routetable

import (
	"context"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/ports"
)

// Pagination, RouteTableFilter — пере-используем единые value-объекты `internal/ports`.
type (
	Pagination       = ports.Pagination
	RouteTableFilter = ports.RouteTableFilter
)

// RouteTableRepo — то, что use-case'ам RouteTable нужно от репозитория RT.
type RouteTableRepo interface {
	Get(ctx context.Context, id string) (*domain.RouteTableRecord, error)
	List(ctx context.Context, f RouteTableFilter, p Pagination) ([]*domain.RouteTableRecord, string, error)
	Insert(ctx context.Context, rt *domain.RouteTable) (*domain.RouteTableRecord, error)
	Update(ctx context.Context, rt *domain.RouteTable) (*domain.RouteTableRecord, error)
	Delete(ctx context.Context, id string) error
	SetFolderID(ctx context.Context, id, folderID string) (*domain.RouteTableRecord, error)
}

// NetworkReader — узкий read-интерфейс для проверки parent Network.Existence в
// async-worker'е Create.
type NetworkReader interface {
	Get(ctx context.Context, id string) (*domain.NetworkRecord, error)
}

// FolderClient — то, что use-case'ам RouteTable нужно от peer-сервиса
// kacho-resource-manager.
type FolderClient interface {
	Exists(ctx context.Context, folderID string) (bool, error)
}
