package networkinterface

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-corelib/operations"

	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// ListNetworkInterfacesUseCase — list NICs. folder_id обязателен.
//
// Wave 5 replicate (KAC-94, NIC batch): открывает reader-TX через CQRS-iface.
type ListNetworkInterfacesUseCase struct {
	repo Repo
}

// NewListNetworkInterfacesUseCase создаёт ListNetworkInterfacesUseCase.
func NewListNetworkInterfacesUseCase(r Repo) *ListNetworkInterfacesUseCase {
	return &ListNetworkInterfacesUseCase{repo: r}
}

// Execute — folder_id required.
func (u *ListNetworkInterfacesUseCase) Execute(ctx context.Context, f NetworkInterfaceFilter, p Pagination) ([]*kachorepo.NetworkInterfaceRecord, string, error) {
	if f.FolderID == "" {
		return nil, "", status.Error(codes.InvalidArgument, "folder_id required")
	}
	rd, err := u.repo.Reader(ctx)
	if err != nil {
		return nil, "", mapRepoErr(err)
	}
	defer func() { _ = rd.Close() }()
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
