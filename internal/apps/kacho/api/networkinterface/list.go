package networkinterface

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-corelib/operations"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// ListNetworkInterfacesUseCase — list NICs. folder_id обязателен.
type ListNetworkInterfacesUseCase struct {
	repo NetworkInterfaceRepo
}

// NewListNetworkInterfacesUseCase создаёт ListNetworkInterfacesUseCase.
func NewListNetworkInterfacesUseCase(repo NetworkInterfaceRepo) *ListNetworkInterfacesUseCase {
	return &ListNetworkInterfacesUseCase{repo: repo}
}

// Execute — folder_id required.
func (u *ListNetworkInterfacesUseCase) Execute(ctx context.Context, f NetworkInterfaceFilter, p Pagination) ([]*domain.NetworkInterfaceRecord, string, error) {
	if f.FolderID == "" {
		return nil, "", status.Error(codes.InvalidArgument, "folder_id required")
	}
	out, next, err := u.repo.List(ctx, f, p)
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
