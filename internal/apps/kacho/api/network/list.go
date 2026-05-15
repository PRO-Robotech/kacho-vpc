package network

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-corelib/operations"
	corevalidate "github.com/PRO-Robotech/kacho-corelib/validate"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// ListNetworksUseCase — list networks с пагинацией. folder_id обязателен.
type ListNetworksUseCase struct {
	repo NetworkRepo
}

// NewListNetworksUseCase создаёт ListNetworksUseCase.
func NewListNetworksUseCase(repo NetworkRepo) *ListNetworksUseCase {
	return &ListNetworksUseCase{repo: repo}
}

// Execute — folder_id required (закрыто cross-folder enumeration #C1).
func (u *ListNetworksUseCase) Execute(ctx context.Context, f NetworkFilter, p Pagination) ([]*domain.NetworkRecord, string, error) {
	if f.FolderID == "" {
		return nil, "", status.Error(codes.InvalidArgument, "folder_id required")
	}
	return u.repo.List(ctx, f, p)
}

// ListSubnetsUseCase — список Subnets конкретной Network.
type ListSubnetsUseCase struct {
	repo         NetworkRepo
	subnetReader SubnetReader
}

// NewListSubnetsUseCase создаёт ListSubnetsUseCase.
func NewListSubnetsUseCase(repo NetworkRepo, subnetReader SubnetReader) *ListSubnetsUseCase {
	return &ListSubnetsUseCase{repo: repo, subnetReader: subnetReader}
}

// Execute — id validate → existence check → list subnets.
func (u *ListSubnetsUseCase) Execute(ctx context.Context, networkID string, p Pagination) ([]*domain.SubnetRecord, string, error) {
	if err := corevalidate.ResourceID("network", ids.PrefixNetwork, networkID); err != nil {
		return nil, "", err
	}
	if _, err := u.repo.Get(ctx, networkID); err != nil {
		return nil, "", mapRepoErr(err)
	}
	if u.subnetReader == nil {
		return nil, "", nil
	}
	return u.subnetReader.List(ctx, SubnetFilter{NetworkID: networkID}, p)
}

// ListSecurityGroupsUseCase — список SG, привязанных к Network.
type ListSecurityGroupsUseCase struct {
	repo   NetworkRepo
	sgRepo SecurityGroupRepo
}

// NewListSecurityGroupsUseCase создаёт ListSecurityGroupsUseCase.
func NewListSecurityGroupsUseCase(repo NetworkRepo, sgRepo SecurityGroupRepo) *ListSecurityGroupsUseCase {
	return &ListSecurityGroupsUseCase{repo: repo, sgRepo: sgRepo}
}

// Execute — id validate → existence check → list SG.
func (u *ListSecurityGroupsUseCase) Execute(ctx context.Context, networkID string, p Pagination) ([]*domain.SecurityGroupRecord, string, error) {
	if err := corevalidate.ResourceID("network", ids.PrefixNetwork, networkID); err != nil {
		return nil, "", err
	}
	if _, err := u.repo.Get(ctx, networkID); err != nil {
		return nil, "", mapRepoErr(err)
	}
	if u.sgRepo == nil {
		return nil, "", nil
	}
	return u.sgRepo.List(ctx, SecurityGroupFilter{NetworkID: networkID}, p)
}

// ListRouteTablesUseCase — список RT в Network.
type ListRouteTablesUseCase struct {
	repo           NetworkRepo
	routeTableRead RouteTableReader
}

// NewListRouteTablesUseCase создаёт ListRouteTablesUseCase.
func NewListRouteTablesUseCase(repo NetworkRepo, routeTableRead RouteTableReader) *ListRouteTablesUseCase {
	return &ListRouteTablesUseCase{repo: repo, routeTableRead: routeTableRead}
}

// Execute — id validate → existence check → list RT.
func (u *ListRouteTablesUseCase) Execute(ctx context.Context, networkID string, p Pagination) ([]*domain.RouteTableRecord, string, error) {
	if err := corevalidate.ResourceID("network", ids.PrefixNetwork, networkID); err != nil {
		return nil, "", err
	}
	if _, err := u.repo.Get(ctx, networkID); err != nil {
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
