package privateendpoint

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-corelib/operations"
	corevalidate "github.com/PRO-Robotech/kacho-corelib/validate"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// ListPrivateEndpointsUseCase — list PEs. folder_id обязателен.
type ListPrivateEndpointsUseCase struct {
	repo PrivateEndpointRepo
}

// NewListPrivateEndpointsUseCase создаёт ListPrivateEndpointsUseCase.
func NewListPrivateEndpointsUseCase(repo PrivateEndpointRepo) *ListPrivateEndpointsUseCase {
	return &ListPrivateEndpointsUseCase{repo: repo}
}

// Execute — folder_id required.
func (u *ListPrivateEndpointsUseCase) Execute(ctx context.Context, f PrivateEndpointFilter, p Pagination) ([]*domain.PrivateEndpointRecord, string, error) {
	if f.FolderID == "" {
		return nil, "", status.Error(codes.InvalidArgument, "folder_id required")
	}
	return u.repo.List(ctx, f, p)
}

// ListOperationsUseCase — операции для конкретного PE.
type ListOperationsUseCase struct {
	opsRepo operations.Repo
}

// NewListOperationsUseCase создаёт ListOperationsUseCase.
func NewListOperationsUseCase(opsRepo operations.Repo) *ListOperationsUseCase {
	return &ListOperationsUseCase{opsRepo: opsRepo}
}

// Execute — id-валидация + list.
func (u *ListOperationsUseCase) Execute(ctx context.Context, peID string, p Pagination) ([]operations.Operation, string, error) {
	if err := corevalidate.ResourceID("private endpoint", ids.PrefixPrivateEndpoint, peID); err != nil {
		return nil, "", err
	}
	return u.opsRepo.List(ctx, operations.ListFilter{
		ResourceID: peID,
		PageSize:   p.PageSize,
		PageToken:  p.PageToken,
	})
}
