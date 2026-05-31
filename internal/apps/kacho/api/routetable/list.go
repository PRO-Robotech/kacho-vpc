package routetable

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

// ListRouteTablesUseCase — list RTs с пагинацией. project_id обязателен.
//
// Wave 5 replicate (KAC-94): использует CQRS Reader.
//
// KAC-127 Phase 4: FGA-filtered list. authz==nil → legacy fallback.
type ListRouteTablesUseCase struct {
	repo  Repo
	authz listauthz.Port
}

// NewListRouteTablesUseCase создаёт ListRouteTablesUseCase. authz может быть nil.
func NewListRouteTablesUseCase(r Repo, authz listauthz.Port) *ListRouteTablesUseCase {
	return &ListRouteTablesUseCase{repo: r, authz: authz}
}

// Execute — project_id required + FGA-filter (KAC-127 Phase 4).
func (u *ListRouteTablesUseCase) Execute(ctx context.Context, subjectID string, f RouteTableFilter, p Pagination) ([]*kacho.RouteTableRecord, string, error) {
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
	return rd.RouteTables().List(ctx, f, p)
}

// ListOperationsUseCase — операции, относящиеся к конкретному route-table id.
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
func (u *ListOperationsUseCase) Execute(ctx context.Context, rtID string, p Pagination) ([]operations.Operation, string, error) {
	if err := corevalidate.ResourceID("route table", ids.PrefixRouteTable, rtID); err != nil {
		return nil, "", err
	}
	return u.opsRepo.List(ctx, operations.ListFilter{
		ResourceID: rtID,
		PageSize:   p.PageSize,
		PageToken:  p.PageToken,
	})
}
