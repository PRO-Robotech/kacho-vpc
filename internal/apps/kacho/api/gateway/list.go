package gateway

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-corelib/operations"
	corevalidate "github.com/PRO-Robotech/kacho-corelib/validate"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// ListGatewaysUseCase — list gateways с пагинацией. folder_id обязателен.
type ListGatewaysUseCase struct {
	repo GatewayRepo
}

// NewListGatewaysUseCase создаёт ListGatewaysUseCase.
func NewListGatewaysUseCase(repo GatewayRepo) *ListGatewaysUseCase {
	return &ListGatewaysUseCase{repo: repo}
}

// Execute — folder_id required (закрыто cross-folder enumeration #C1).
func (u *ListGatewaysUseCase) Execute(ctx context.Context, f GatewayFilter, p Pagination) ([]*domain.GatewayRecord, string, error) {
	if f.FolderID == "" {
		return nil, "", status.Error(codes.InvalidArgument, "folder_id required")
	}
	return u.repo.List(ctx, f, p)
}

// ListOperationsUseCase — операции, относящиеся к конкретному gateway-id.
// NB: без repo.Get-precondition — операции должны быть доступны и после Delete
// (история).
type ListOperationsUseCase struct {
	opsRepo operations.Repo
}

// NewListOperationsUseCase создаёт ListOperationsUseCase.
func NewListOperationsUseCase(opsRepo operations.Repo) *ListOperationsUseCase {
	return &ListOperationsUseCase{opsRepo: opsRepo}
}

// Execute — id-валидация + list.
func (u *ListOperationsUseCase) Execute(ctx context.Context, gwID string, p Pagination) ([]operations.Operation, string, error) {
	if err := corevalidate.ResourceID("gateway", ids.PrefixGateway, gwID); err != nil {
		return nil, "", err
	}
	return u.opsRepo.List(ctx, operations.ListFilter{
		ResourceID: gwID,
		PageSize:   p.PageSize,
		PageToken:  p.PageToken,
	})
}
