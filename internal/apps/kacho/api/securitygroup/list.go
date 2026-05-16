package securitygroup

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-corelib/operations"
	corevalidate "github.com/PRO-Robotech/kacho-corelib/validate"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// ListSecurityGroupsUseCase — список SG с пагинацией. folder_id обязателен
// (закрыто cross-folder enumeration #C1).
type ListSecurityGroupsUseCase struct {
	repo SecurityGroupRepo
}

// NewListSecurityGroupsUseCase создаёт ListSecurityGroupsUseCase.
func NewListSecurityGroupsUseCase(repo SecurityGroupRepo) *ListSecurityGroupsUseCase {
	return &ListSecurityGroupsUseCase{repo: repo}
}

// Execute — folder_id required.
func (u *ListSecurityGroupsUseCase) Execute(ctx context.Context, f SecurityGroupFilter, p Pagination) ([]*kacho.SecurityGroupRecord, string, error) {
	if f.FolderID == "" {
		return nil, "", status.Error(codes.InvalidArgument, "folder_id required")
	}
	return u.repo.List(ctx, f, p)
}

// ListOperationsUseCase — операции, относящиеся к конкретному SG.
//
// Семантика: с repo.Get-precondition (verbatim YC для SG — отличается от
// Network/Address: для SG ListOperations предполагает, что SG ещё жив; если
// удалён — handler возвращает sync NotFound через precondition Get в handler'е).
type ListOperationsUseCase struct {
	repo    SecurityGroupRepo
	opsRepo operations.Repo
}

// NewListOperationsUseCase создаёт ListOperationsUseCase.
func NewListOperationsUseCase(repo SecurityGroupRepo, opsRepo operations.Repo) *ListOperationsUseCase {
	return &ListOperationsUseCase{repo: repo, opsRepo: opsRepo}
}

// Execute — id-валидация + existence-check + список операций.
func (u *ListOperationsUseCase) Execute(ctx context.Context, id string, p Pagination) ([]operations.Operation, string, error) {
	if err := corevalidate.ResourceID("security group", ids.PrefixSecurityGroup, id); err != nil {
		return nil, "", err
	}
	if _, err := u.repo.Get(ctx, id); err != nil {
		return nil, "", mapRepoErr(err)
	}
	return u.opsRepo.List(ctx, operations.ListFilter{
		ResourceID: id,
		PageSize:   p.PageSize,
		PageToken:  p.PageToken,
	})
}
