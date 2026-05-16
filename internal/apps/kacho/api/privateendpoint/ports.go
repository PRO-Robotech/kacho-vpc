// Package privateendpoint — use-case-структура ресурса PrivateEndpoint
// (skill evgeniy §2 B.1-B.4).
//
// Wave 3b (KAC-94): replicate Wave 3a pilot Network → PrivateEndpoint.
// Бизнес-логика CreatePrivateEndpointUseCase / UpdatePrivateEndpointUseCase /
// DeletePrivateEndpointUseCase / GetPrivateEndpointUseCase / ListPrivateEndpointsUseCase /
// ListOperationsUseCase плюс тонкий gRPC-handler.
//
// NB: у PrivateEndpoint нет Move RPC (он folder-level, но в YC verbatim API
// нет MovePrivateEndpoint). При появлении — добавить move.go.
package privateendpoint

import (
	"context"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
)

// Pagination, PrivateEndpointFilter — пере-используем единые value-объекты `internal/repo`.
type (
	Pagination            = repo.Pagination
	PrivateEndpointFilter = repo.PrivateEndpointFilter
)

// PrivateEndpointRepo — то, что use-case'ам PE нужно от репозитория PE.
type PrivateEndpointRepo interface {
	Get(ctx context.Context, id string) (*domain.PrivateEndpointRecord, error)
	List(ctx context.Context, f PrivateEndpointFilter, p Pagination) ([]*domain.PrivateEndpointRecord, string, error)
	Insert(ctx context.Context, pe *domain.PrivateEndpoint) (*domain.PrivateEndpointRecord, error)
	Update(ctx context.Context, pe *domain.PrivateEndpoint) (*domain.PrivateEndpointRecord, error)
	Delete(ctx context.Context, id string) error
}

// NetworkReader — узкий read-интерфейс для проверки parent Network.
type NetworkReader interface {
	Get(ctx context.Context, id string) (*domain.NetworkRecord, error)
}

// SubnetReader — узкий read-интерфейс для проверки parent Subnet.
type SubnetReader interface {
	Get(ctx context.Context, id string) (*domain.SubnetRecord, error)
}

// FolderClient — peer-сервис kacho-resource-manager.
type FolderClient interface {
	Exists(ctx context.Context, folderID string) (bool, error)
}
