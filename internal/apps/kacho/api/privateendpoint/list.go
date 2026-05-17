package privateendpoint

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-corelib/operations"
	corevalidate "github.com/PRO-Robotech/kacho-corelib/validate"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// ListPrivateEndpointsUseCase — list PEs. project_id обязателен.
//
// Wave 5 replicate (KAC-94): открывает read-only TX через `repo.Reader(ctx)`
// (parity с network/list.go).
type ListPrivateEndpointsUseCase struct {
	repo Repo
}

// NewListPrivateEndpointsUseCase создаёт ListPrivateEndpointsUseCase.
func NewListPrivateEndpointsUseCase(r Repo) *ListPrivateEndpointsUseCase {
	return &ListPrivateEndpointsUseCase{repo: r}
}

// Execute — project_id required.
func (u *ListPrivateEndpointsUseCase) Execute(ctx context.Context, f PrivateEndpointFilter, p Pagination) ([]*kacho.PrivateEndpointRecord, string, error) {
	if f.ProjectID == "" {
		return nil, "", status.Error(codes.InvalidArgument, "project_id required")
	}
	rd, err := u.repo.Reader(ctx)
	if err != nil {
		return nil, "", mapRepoErr(err)
	}
	defer func() { _ = rd.Close() }()
	return rd.PrivateEndpoints().List(ctx, f, p)
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
