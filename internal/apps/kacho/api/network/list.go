// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package network

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-corelib/operations"
	corevalidate "github.com/PRO-Robotech/kacho-corelib/validate"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/shared/serviceerr"
	"github.com/PRO-Robotech/kacho-vpc/internal/authzfilter"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// ListNetworksUseCase — list networks с пагинацией. project_id обязателен.
//
// Per-object filtered List через FGA ListObjects (relation viewer; read==enforce).
// filter != nil и subject не пуст → ListAllowedIDs → repo.ListByIDs (WHERE id=ANY),
// pagination ПОСЛЕ фильтра; bypass (wildcard scope_grant) → обычный repo.List;
// empty grant → пустой List (no-leak); iam недоступен → fail-closed Unavailable.
// filter == nil / subject == "" (system principal) → unfiltered passthrough.
type ListNetworksUseCase struct {
	repo   Repo
	filter ListFilter
}

// NewListNetworksUseCase создает ListNetworksUseCase. filter может быть nil
// (list-filter disabled / dev) → unfiltered passthrough.
func NewListNetworksUseCase(r Repo, filter ListFilter) *ListNetworksUseCase {
	return &ListNetworksUseCase{repo: r, filter: filter}
}

// Execute — project_id required (закрывает cross-project enumeration).
//
// Параметры:
//   - subjectID: FGA-subject ("user:usr_xxx"). Empty → unfiltered passthrough
//     (system principal); ожидается в production-mode что caller всегда выставит
//     principal (api-gateway interceptor).
func (u *ListNetworksUseCase) Execute(ctx context.Context, subjectID string, f NetworkFilter, p Pagination) ([]*kachorepo.NetworkRecord, string, error) {
	if f.ProjectID == "" {
		return nil, "", status.Error(codes.InvalidArgument, "project_id required")
	}
	r, err := u.repo.Reader(ctx)
	if err != nil {
		return nil, "", serviceerr.MapRepoErr(err)
	}
	defer func() { _ = r.Close() }()

	if subjectID == "" && u.filter != nil {
		// identity не извлечён (anon / gateway не проставил principal) при
		// включённом фильтре → fail-closed (пустой список), НЕ unfiltered
		// passthrough: «не знаю, кто ты» ≠ «доверенный system-вызов» (no-leak).
		return nil, "", nil
	}
	if u.filter == nil || subjectID == authzfilter.SystemSubject {
		return r.Networks().List(ctx, f, p)
	}

	allowedIDs, bypass, ferr := u.filter.ListAllowedIDs(ctx, subjectID,
		authzfilter.ResourceTypeNetwork, authzfilter.ActionNetworkList)
	if ferr != nil {
		return nil, "", ferr
	}
	if bypass {
		return r.Networks().List(ctx, f, p)
	}
	if len(allowedIDs) == 0 {
		return nil, "", nil
	}
	return r.Networks().ListByIDs(ctx, f, allowedIDs, p)
}

// ListSubnetsUseCase — список Subnets конкретной Network. Network-existence-check
// идет через CQRS-Reader; SubnetReader — узкий read-порт Subnet.
type ListSubnetsUseCase struct {
	repo         Repo
	subnetReader SubnetReader
}

// NewListSubnetsUseCase создает ListSubnetsUseCase.
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
		return nil, "", serviceerr.MapRepoErr(err)
	}
	defer func() { _ = rd.Close() }()
	if _, err := rd.Networks().Get(ctx, networkID); err != nil {
		return nil, "", serviceerr.MapRepoErr(err)
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

// NewListSecurityGroupsUseCase создает ListSecurityGroupsUseCase.
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
		return nil, "", serviceerr.MapRepoErr(err)
	}
	defer func() { _ = rd.Close() }()
	if _, err := rd.Networks().Get(ctx, networkID); err != nil {
		return nil, "", serviceerr.MapRepoErr(err)
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

// NewListRouteTablesUseCase создает ListRouteTablesUseCase.
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
		return nil, "", serviceerr.MapRepoErr(err)
	}
	defer func() { _ = rd.Close() }()
	if _, err := rd.Networks().Get(ctx, networkID); err != nil {
		return nil, "", serviceerr.MapRepoErr(err)
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

// NewListOperationsUseCase создает ListOperationsUseCase.
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
