package networkinterface

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-corelib/operations"

	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/services/listauthz"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// ListNetworkInterfacesUseCase — list NICs. project_id обязателен.
//
// Wave 5 replicate (KAC-94, NIC batch): открывает reader-TX через CQRS-iface.
//
// KAC-127 Phase 4: FGA-filtered list. authz==nil → legacy fallback.
type ListNetworkInterfacesUseCase struct {
	repo  Repo
	authz listauthz.Port
}

// NewListNetworkInterfacesUseCase создаёт ListNetworkInterfacesUseCase. authz==nil OK.
func NewListNetworkInterfacesUseCase(r Repo, authz listauthz.Port) *ListNetworkInterfacesUseCase {
	return &ListNetworkInterfacesUseCase{repo: r, authz: authz}
}

// Execute — project_id required + FGA-filter (KAC-127 Phase 4).
func (u *ListNetworkInterfacesUseCase) Execute(ctx context.Context, subjectID string, f NetworkInterfaceFilter, p Pagination) ([]*kachorepo.NetworkInterfaceRecord, string, error) {
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

	out, next, err := rd.NetworkInterfaces().List(ctx, f, p)
	if err != nil {
		return nil, "", mapRepoErr(err)
	}
	return out, next, nil
}

// ListOperationsUseCase — операции, относящиеся к конкретному NIC.
type ListOperationsUseCase struct {
	opsRepo operations.Repo
}

// NewListOperationsUseCase создаёт ListOperationsUseCase.
func NewListOperationsUseCase(opsRepo operations.Repo) *ListOperationsUseCase {
	return &ListOperationsUseCase{opsRepo: opsRepo}
}

// Execute — id-валидация + list. NB: no repo.Get precondition — operation
// history must remain reachable after the resource is deleted (operations rows
// have no FK cascade).
func (u *ListOperationsUseCase) Execute(ctx context.Context, niID string, p Pagination) ([]operations.Operation, string, error) {
	if err := niResourceID(niID); err != nil {
		return nil, "", err
	}
	return u.opsRepo.List(ctx, operations.ListFilter{
		ResourceID: niID,
		PageSize:   p.PageSize,
		PageToken:  p.PageToken,
	})
}
