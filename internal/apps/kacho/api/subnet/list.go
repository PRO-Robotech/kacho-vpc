package subnet

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-corelib/operations"
	corevalidate "github.com/PRO-Robotech/kacho-corelib/validate"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/services/listauthz"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// ListSubnetsUseCase — list subnets с пагинацией. project_id обязателен
// (R10 #C1 closure).
//
// Wave 5 replicate (KAC-94): использует CQRS Reader.
//
// KAC-127 Phase 4: integrated FGA-filter (acceptance §5.2). Если authz != nil
// и subject не пуст — ListAllowedIDs → filter result; иначе — legacy list.
type ListSubnetsUseCase struct {
	repo  Repo
	authz listauthz.Port
}

// NewListSubnetsUseCase создаёт ListSubnetsUseCase. authz может быть nil
// (LIST_FILTER_ENABLED=false / dev).
func NewListSubnetsUseCase(r Repo, authz listauthz.Port) *ListSubnetsUseCase {
	return &ListSubnetsUseCase{repo: r, authz: authz}
}

// Execute — project_id required (закрыто cross-folder enumeration #C1).
//
// KAC-127 Phase 4 flow: authz.ListAllowedIDs → in-memory filter после repo.List.
// Trade-off (vs DB-level WHERE id = ANY): добавляет O(N) cost после List; для
// типичной нагрузки ≤1000 subnets/project это <1ms. Dominant cost — FGA-call
// (cached 5s). DB-level filter будет hardware-optimized в следующей итерации.
func (u *ListSubnetsUseCase) Execute(ctx context.Context, subjectID string, f SubnetFilter, p Pagination) ([]*kachorepo.SubnetRecord, string, error) {
	if f.ProjectID == "" {
		return nil, "", status.Error(codes.InvalidArgument, "project_id required")
	}
	r, err := u.repo.Reader(ctx)
	if err != nil {
		return nil, "", mapRepoErr(err)
	}
	defer func() { _ = r.Close() }()

	// KAC-240: project-level List authorization (see network/list.go). repo.List
	// is already project-scoped (the isolation boundary); CanViewProject only
	// decides whether the subject may view the project. View → all rows; no view
	// → empty (fail-closed). No dependency on per-object tuples at List time.
	if u.authz != nil && subjectID != "" {
		ok, cerr := u.authz.CanViewProject(ctx, subjectID, f.ProjectID)
		if cerr != nil {
			return nil, "", listauthz.MapListFilterErr(cerr)
		}
		if !ok {
			return nil, "", nil
		}
	}
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
