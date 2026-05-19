package securitygroup

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

// FGA constants — KAC-127 Phase 4 (acceptance §2.1 DSL v2).
const (
	FGAObjectTypeSG = "vpc_security_group"
	FGAActionSGList = "vpc.security_groups.list"
)

// ListSecurityGroupsUseCase — список SG с пагинацией. project_id обязателен
// (закрыто cross-folder enumeration #C1).
//
// Wave 5 replicate (KAC-94, skill evgeniy §6 G.1): CQRS Reader (read-only TX).
//
// KAC-127 Phase 4: FGA-filtered list. authz==nil → legacy unfiltered fallback.
type ListSecurityGroupsUseCase struct {
	repo  Repo
	authz listauthz.Port
}

// NewListSecurityGroupsUseCase создаёт ListSecurityGroupsUseCase. authz может
// быть nil (LIST_FILTER_ENABLED=false / dev).
func NewListSecurityGroupsUseCase(r Repo, authz listauthz.Port) *ListSecurityGroupsUseCase {
	return &ListSecurityGroupsUseCase{repo: r, authz: authz}
}

// Execute — project_id required + FGA-filter (KAC-127 Phase 4).
func (u *ListSecurityGroupsUseCase) Execute(ctx context.Context, subjectID string, f SecurityGroupFilter, p Pagination) ([]*kacho.SecurityGroupRecord, string, error) {
	if f.ProjectID == "" {
		return nil, "", status.Error(codes.InvalidArgument, "project_id required")
	}
	rd, err := u.repo.Reader(ctx)
	if err != nil {
		return nil, "", mapRepoErr(err)
	}
	defer func() { _ = rd.Close() }()

	if u.authz != nil && subjectID != "" {
		allowedIDs, lerr := u.authz.ListAllowedIDs(ctx, subjectID, FGAObjectTypeSG, FGAActionSGList, f.ProjectID)
		if lerr != nil {
			return nil, "", status.Error(codes.Unavailable, "list-filter unavailable: "+lerr.Error())
		}
		if len(allowedIDs) == 0 {
			return nil, "", nil
		}
		rows, nextToken, ferr := rd.SecurityGroups().List(ctx, f, p)
		if ferr != nil {
			return nil, "", ferr
		}
		return listauthz.FilterByAllowedIDs(rows, allowedIDs, func(rec *kacho.SecurityGroupRecord) string { return rec.ID }), nextToken, nil
	}
	return rd.SecurityGroups().List(ctx, f, p)
}

// ListOperationsUseCase — операции, относящиеся к конкретному SG.
//
// Семантика: с repo.Get-precondition (verbatim YC для SG — отличается от
// Network/Address: для SG ListOperations предполагает, что SG ещё жив; если
// удалён — handler возвращает sync NotFound через precondition Get в handler'е).
type ListOperationsUseCase struct {
	repo    Repo
	opsRepo operations.Repo
}

// NewListOperationsUseCase создаёт ListOperationsUseCase.
func NewListOperationsUseCase(r Repo, opsRepo operations.Repo) *ListOperationsUseCase {
	return &ListOperationsUseCase{repo: r, opsRepo: opsRepo}
}

// Execute — id-валидация + existence-check + список операций.
func (u *ListOperationsUseCase) Execute(ctx context.Context, id string, p Pagination) ([]operations.Operation, string, error) {
	if err := corevalidate.ResourceID("security group", ids.PrefixSecurityGroup, id); err != nil {
		return nil, "", err
	}
	rd, err := u.repo.Reader(ctx)
	if err != nil {
		return nil, "", mapRepoErr(err)
	}
	if _, gerr := rd.SecurityGroups().Get(ctx, id); gerr != nil {
		_ = rd.Close()
		return nil, "", mapRepoErr(gerr)
	}
	_ = rd.Close()
	return u.opsRepo.List(ctx, operations.ListFilter{
		ResourceID: id,
		PageSize:   p.PageSize,
		PageToken:  p.PageToken,
	})
}
