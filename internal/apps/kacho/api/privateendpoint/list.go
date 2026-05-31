package privateendpoint

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-corelib/operations"
	corevalidate "github.com/PRO-Robotech/kacho-corelib/validate"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/services/listauthz"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// ListPrivateEndpointsUseCase — list PEs. project_id обязателен.
//
// Wave 5 replicate (KAC-94): открывает read-only TX через `repo.Reader(ctx)`
// (parity с network/list.go).
//
// KAC-127 Phase 4: FGA-filtered list. authz==nil → legacy fallback.
type ListPrivateEndpointsUseCase struct {
	repo  Repo
	authz listauthz.Port
}

// NewListPrivateEndpointsUseCase создаёт ListPrivateEndpointsUseCase. authz==nil OK.
func NewListPrivateEndpointsUseCase(r Repo, authz listauthz.Port) *ListPrivateEndpointsUseCase {
	return &ListPrivateEndpointsUseCase{repo: r, authz: authz}
}

// Execute — project_id required + FGA-filter (KAC-127 Phase 4).
func (u *ListPrivateEndpointsUseCase) Execute(ctx context.Context, subjectID string, f PrivateEndpointFilter, p Pagination) ([]*kacho.PrivateEndpointRecord, string, error) {
	if f.ProjectID == "" {
		return nil, "", status.Error(codes.InvalidArgument, "project_id required")
	}
	rd, err := u.repo.Reader(ctx)
	if err != nil {
		return nil, "", mapRepoErr(err)
	}
	defer func() { _ = rd.Close() }()

	// KAC-240: project-level List authorization (see network/list.go).
	if u.authz != nil && subjectID != "" {
		ok, cerr := u.authz.CanViewProject(ctx, subjectID, f.ProjectID)
		if cerr != nil {
			return nil, "", listauthz.MapListFilterErr(cerr)
		}
		if !ok {
			return nil, "", nil
		}
	}
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
