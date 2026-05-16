package gateway

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-corelib/operations"
	corevalidate "github.com/PRO-Robotech/kacho-corelib/validate"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// ListGatewaysUseCase — list gateways с пагинацией. folder_id обязателен.
//
// Wave 5 replicate (KAC-94): открывает read-only TX через `repo.Reader(ctx)`.
type ListGatewaysUseCase struct {
	repo Repo
}

// NewListGatewaysUseCase создаёт ListGatewaysUseCase.
func NewListGatewaysUseCase(r Repo) *ListGatewaysUseCase {
	return &ListGatewaysUseCase{repo: r}
}

// Execute — folder_id required (закрыто cross-folder enumeration #C1).
func (u *ListGatewaysUseCase) Execute(ctx context.Context, f GatewayFilter, p Pagination) ([]*kacho.GatewayRecord, string, error) {
	if f.FolderID == "" {
		return nil, "", status.Error(codes.InvalidArgument, "folder_id required")
	}
	rd, err := u.repo.Reader(ctx)
	if err != nil {
		return nil, "", mapRepoErr(err)
	}
	defer func() { _ = rd.Close() }()

	gws, nextToken, lerr := rd.Gateways().List(ctx, f, p)
	if lerr != nil {
		return nil, "", mapRepoErr(lerr)
	}
	return gws, nextToken, nil
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
