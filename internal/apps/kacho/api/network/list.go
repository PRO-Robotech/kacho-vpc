package network

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

// FGAObjectTypeNetwork — FGA object type для Network (acceptance §2.1, DSL v2).
const FGAObjectTypeNetwork = "vpc_network"

// FGAActionNetworkList — domain action для ListNetworks (acceptance §2.1).
const FGAActionNetworkList = "vpc.networks.list"

// ListNetworksUseCase — list networks с пагинацией. project_id обязателен.
//
// Wave 5 pilot (KAC-94): использует CQRS Reader.
//
// KAC-127 Phase 4: интегрирован FGA-filtered list. Если authz != nil:
//  1. Получаем allowed-ids set через ListObjects (cache+FGA).
//  2. Empty allowed-set → return empty response (acceptance D-4, no DB query).
//  3. Non-empty → repo.ListByIDs(ids).
//
// Если authz == nil (LIST_FILTER_ENABLED=false / dev-fallback) — старое
// поведение repo.List без filter.
type ListNetworksUseCase struct {
	repo  Repo
	authz ListAuthorizer
}

// NewListNetworksUseCase создаёт ListNetworksUseCase. authz может быть nil
// (dev / list-filter disabled).
func NewListNetworksUseCase(r Repo, authz ListAuthorizer) *ListNetworksUseCase {
	return &ListNetworksUseCase{repo: r, authz: authz}
}

// Execute — project_id required (закрыто cross-folder enumeration #C1).
//
// Параметры:
//   - subjectID: FGA-subject ("user:usr_xxx"). Empty → fall-back to legacy
//     List behaviour (no FGA filter); ожидается в production-mode что caller
//     всегда выставит principal (api-gateway Phase 2 + interceptor).
func (u *ListNetworksUseCase) Execute(ctx context.Context, subjectID string, f NetworkFilter, p Pagination) ([]*kachorepo.NetworkRecord, string, error) {
	if f.ProjectID == "" {
		return nil, "", status.Error(codes.InvalidArgument, "project_id required")
	}
	r, err := u.repo.Reader(ctx)
	if err != nil {
		return nil, "", mapRepoErr(err)
	}
	defer func() { _ = r.Close() }()

	// KAC-127 Phase 4: FGA-filtered list.
	if u.authz != nil && subjectID != "" {
		allowedIDs, lerr := u.authz.ListAllowedIDs(ctx, subjectID, FGAObjectTypeNetwork, FGAActionNetworkList, f.ProjectID)
		if lerr != nil {
			return nil, "", listauthz.MapListFilterErr(lerr)
		}
		if len(allowedIDs) == 0 {
			// Empty grant — return 200 OK + empty list (acceptance §3 D-4).
			return nil, "", nil
		}
		return r.Networks().ListByIDs(ctx, f, allowedIDs, p)
	}

	// Fallback: legacy unfiltered list (LIST_FILTER_ENABLED=false / dev).
	return r.Networks().List(ctx, f, p)
}

// ListSubnetsUseCase — список Subnets конкретной Network. CQRS pilot: Network-
// existence-check идёт через Reader; SubnetReader — пока legacy (replicate-фаза).
type ListSubnetsUseCase struct {
	repo         Repo
	subnetReader SubnetReader
}

// NewListSubnetsUseCase создаёт ListSubnetsUseCase.
func NewListSubnetsUseCase(r Repo, subnetReader SubnetReader) *ListSubnetsUseCase {
	return &ListSubnetsUseCase{repo: r, subnetReader: subnetReader}
}

// Execute — id validate → existence check → list subnets.
func (u *ListSubnetsUseCase) Execute(ctx context.Context, networkID string, p Pagination) ([]*kachorepo.SubnetRecord, string, error) {
	if err := corevalidate.ResourceID("network", ids.PrefixNetwork, networkID); err != nil {
		return nil, "", err
	}
	rd, err := u.repo.Reader(ctx)
	if err != nil {
		return nil, "", mapRepoErr(err)
	}
	defer func() { _ = rd.Close() }()
	if _, err := rd.Networks().Get(ctx, networkID); err != nil {
		return nil, "", mapRepoErr(err)
	}
	if u.subnetReader == nil {
		return nil, "", nil
	}
	return u.subnetReader.List(ctx, SubnetFilter{NetworkID: networkID}, p)
}

// ListSecurityGroupsUseCase — список SG, привязанных к Network.
type ListSecurityGroupsUseCase struct {
	repo   Repo
	sgRepo SecurityGroupRepo
}

// NewListSecurityGroupsUseCase создаёт ListSecurityGroupsUseCase.
func NewListSecurityGroupsUseCase(r Repo, sgRepo SecurityGroupRepo) *ListSecurityGroupsUseCase {
	return &ListSecurityGroupsUseCase{repo: r, sgRepo: sgRepo}
}

// Execute — id validate → existence check → list SG.
func (u *ListSecurityGroupsUseCase) Execute(ctx context.Context, networkID string, p Pagination) ([]*kachorepo.SecurityGroupRecord, string, error) {
	if err := corevalidate.ResourceID("network", ids.PrefixNetwork, networkID); err != nil {
		return nil, "", err
	}
	rd, err := u.repo.Reader(ctx)
	if err != nil {
		return nil, "", mapRepoErr(err)
	}
	defer func() { _ = rd.Close() }()
	if _, err := rd.Networks().Get(ctx, networkID); err != nil {
		return nil, "", mapRepoErr(err)
	}
	if u.sgRepo == nil {
		return nil, "", nil
	}
	return u.sgRepo.List(ctx, SecurityGroupFilter{NetworkID: networkID}, p)
}

// ListRouteTablesUseCase — список RT в Network.
type ListRouteTablesUseCase struct {
	repo           Repo
	routeTableRead RouteTableReader
}

// NewListRouteTablesUseCase создаёт ListRouteTablesUseCase.
func NewListRouteTablesUseCase(r Repo, routeTableRead RouteTableReader) *ListRouteTablesUseCase {
	return &ListRouteTablesUseCase{repo: r, routeTableRead: routeTableRead}
}

// Execute — id validate → existence check → list RT.
func (u *ListRouteTablesUseCase) Execute(ctx context.Context, networkID string, p Pagination) ([]*kachorepo.RouteTableRecord, string, error) {
	if err := corevalidate.ResourceID("network", ids.PrefixNetwork, networkID); err != nil {
		return nil, "", err
	}
	rd, err := u.repo.Reader(ctx)
	if err != nil {
		return nil, "", mapRepoErr(err)
	}
	defer func() { _ = rd.Close() }()
	if _, err := rd.Networks().Get(ctx, networkID); err != nil {
		return nil, "", mapRepoErr(err)
	}
	if u.routeTableRead == nil {
		return nil, "", nil
	}
	return u.routeTableRead.List(ctx, RouteTableFilter{NetworkID: networkID}, p)
}

// ListOperationsUseCase — операции, относящиеся к конкретному network-id.
// NB: без repo.Get-precondition — операции должны быть доступны и после Delete
// (история).
type ListOperationsUseCase struct {
	opsRepo operations.Repo
}

// NewListOperationsUseCase создаёт ListOperationsUseCase.
func NewListOperationsUseCase(opsRepo operations.Repo) *ListOperationsUseCase {
	return &ListOperationsUseCase{opsRepo: opsRepo}
}

// Execute — id-валидация (любой prefix принимается; ListOperations используется
// и сразу после Delete, поэтому existence-check не делаем) + list.
func (u *ListOperationsUseCase) Execute(ctx context.Context, networkID string, p Pagination) ([]operations.Operation, string, error) {
	if err := corevalidate.ResourceID("network", ids.PrefixNetwork, networkID); err != nil {
		return nil, "", err
	}
	return u.opsRepo.List(ctx, operations.ListFilter{
		ResourceID: networkID,
		PageSize:   p.PageSize,
		PageToken:  p.PageToken,
	})
}
