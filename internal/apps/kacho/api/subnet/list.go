package subnet

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-corelib/operations"
	corevalidate "github.com/PRO-Robotech/kacho-corelib/validate"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// ListSubnetsUseCase — list subnets с пагинацией. folder_id обязателен
// (R10 #C1 closure).
//
// Wave 5 replicate (KAC-94): использует CQRS Reader.
type ListSubnetsUseCase struct {
	repo Repo
}

// NewListSubnetsUseCase создаёт ListSubnetsUseCase.
func NewListSubnetsUseCase(r Repo) *ListSubnetsUseCase {
	return &ListSubnetsUseCase{repo: r}
}

// Execute — folder_id required (закрыто cross-folder enumeration #C1).
func (u *ListSubnetsUseCase) Execute(ctx context.Context, f SubnetFilter, p Pagination) ([]*kachorepo.SubnetRecord, string, error) {
	if f.FolderID == "" {
		return nil, "", status.Error(codes.InvalidArgument, "folder_id required")
	}
	r, err := u.repo.Reader(ctx)
	if err != nil {
		return nil, "", mapRepoErr(err)
	}
	defer func() { _ = r.Close() }()
	return r.Subnets().List(ctx, f, p)
}

// ListOperationsUseCase — операции, относящиеся к конкретному subnet-id.
// NB: без repo.Get-precondition — операции должны быть доступны и после Delete
// (история операций; rows в `operations` не имеют FK cascade).
type ListOperationsUseCase struct {
	opsRepo operations.Repo
}

// NewListOperationsUseCase создаёт ListOperationsUseCase.
func NewListOperationsUseCase(opsRepo operations.Repo) *ListOperationsUseCase {
	return &ListOperationsUseCase{opsRepo: opsRepo}
}

// Execute — id-валидация + list.
func (u *ListOperationsUseCase) Execute(ctx context.Context, subnetID string, p Pagination) ([]operations.Operation, string, error) {
	if err := corevalidate.ResourceID("subnet", ids.PrefixSubnet, subnetID); err != nil {
		return nil, "", err
	}
	return u.opsRepo.List(ctx, operations.ListFilter{
		ResourceID: subnetID,
		PageSize:   p.PageSize,
		PageToken:  p.PageToken,
	})
}
