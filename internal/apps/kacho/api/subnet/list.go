// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package subnet

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

// ListSubnetsUseCase — list subnets с пагинацией; project_id обязателен (закрывает
// cross-project enumeration).
//
// Per-object filtered List: filter != nil и subject не пуст → ListAllowedIDs(viewer)
// → repo.ListByIDs (WHERE id=ANY), pagination ПОСЛЕ фильтра; bypass (wildcard
// scope_grant) → обычный repo.List; empty grant → пустой List (no-leak); iam
// недоступен → fail-closed Unavailable. filter == nil / subject == "" (system
// principal) → unfiltered passthrough.
type ListSubnetsUseCase struct {
	repo   Repo
	filter ListFilter
}

// NewListSubnetsUseCase создает ListSubnetsUseCase. filter может быть nil
// (list-filter disabled / dev) → unfiltered passthrough.
func NewListSubnetsUseCase(r Repo, filter ListFilter) *ListSubnetsUseCase {
	return &ListSubnetsUseCase{repo: r, filter: filter}
}

// Execute — project_id required (закрывает cross-project enumeration).
//
// Поток: per-object FGA ListObjects → repo.ListByIDs (WHERE id=ANY) → pagination
// применяется к ОТФИЛЬТРОВАННОМУ набору (плотные страницы).
func (u *ListSubnetsUseCase) Execute(ctx context.Context, subjectID string, f SubnetFilter, p Pagination) ([]*kachorepo.SubnetRecord, string, error) {
	if f.ProjectID == "" {
		return nil, "", status.Error(codes.InvalidArgument, "project_id required")
	}
	r, err := u.repo.Reader(ctx)
	if err != nil {
		return nil, "", serviceerr.MapRepoErr(err)
	}
	defer func() { _ = r.Close() }()

	if u.filter == nil || subjectID == "" {
		// list-filter disabled (dev) или system principal → unfiltered passthrough.
		return r.Subnets().List(ctx, f, p)
	}

	allowedIDs, bypass, ferr := u.filter.ListAllowedIDs(ctx, subjectID,
		authzfilter.ResourceTypeSubnet, authzfilter.ActionSubnetList)
	if ferr != nil {
		// iam недоступен / ошибка → fail-closed (Unavailable), НЕ unfiltered.
		return nil, "", ferr
	}
	if bypass {
		// wildcard scope_grant → все project-scoped строки.
		return r.Subnets().List(ctx, f, p)
	}
	if len(allowedIDs) == 0 {
		// нет гранта → пустой List (no-leak).
		return nil, "", nil
	}
	// pagination ПОСЛЕ фильтра (WHERE id = ANY).
	return r.Subnets().ListByIDs(ctx, f, allowedIDs, p)
}

// ListOperationsUseCase — операции, относящиеся к конкретному subnet-id.
// NB: без repo.Get-precondition — операции должны быть доступны и после Delete
// (история операций; rows в `operations` не имеют FK cascade).
type ListOperationsUseCase struct {
	opsRepo operations.Repo
}

// NewListOperationsUseCase создает ListOperationsUseCase.
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
