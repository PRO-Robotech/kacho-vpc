// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package routetable

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-corelib/operations"
	corevalidate "github.com/PRO-Robotech/kacho-corelib/validate"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/shared/serviceerr"
	"github.com/PRO-Robotech/kacho-vpc/internal/authzfilter"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// ListRouteTablesUseCase — list route-table'ов с пагинацией; project_id обязателен.
// Читает через CQRS Reader и фильтрует результат per-object через FGA ListObjects
// (relation viewer; read==enforce). При filter==nil или пустом subject — passthrough
// без обращения к FGA.
type ListRouteTablesUseCase struct {
	repo   Repo
	filter ListFilter
}

// NewListRouteTablesUseCase создает ListRouteTablesUseCase. filter может быть nil.
func NewListRouteTablesUseCase(r Repo, filter ListFilter) *ListRouteTablesUseCase {
	return &ListRouteTablesUseCase{repo: r, filter: filter}
}

// Execute — проверяет project_id, затем фильтрует выдачу per-object через FGA.
// Пагинация применяется ПОСЛЕ фильтра; bypass (wildcard-grant) → repo.List целиком;
// пустой grant → пустой результат (no-leak); iam недоступен → fail-closed Unavailable.
func (u *ListRouteTablesUseCase) Execute(ctx context.Context, subjectID string, f RouteTableFilter, p Pagination) ([]*kacho.RouteTableRecord, string, error) {
	if f.ProjectID == "" {
		return nil, "", status.Error(codes.InvalidArgument, "project_id required")
	}
	rd, err := u.repo.Reader(ctx)
	if err != nil {
		return nil, "", serviceerr.MapRepoErr(err)
	}
	defer func() { _ = rd.Close() }()

	if subjectID == "" && u.filter != nil {
		// identity не извлечён (anon) при включённом фильтре → fail-closed (no-leak).
		return nil, "", nil
	}
	if u.filter == nil || subjectID == authzfilter.SystemSubject {
		return rd.RouteTables().List(ctx, f, p)
	}
	allowedIDs, bypass, ferr := u.filter.ListAllowedIDs(ctx, subjectID,
		authzfilter.ResourceTypeRouteTable, authzfilter.ActionRouteTableList)
	if ferr != nil {
		return nil, "", ferr
	}
	if bypass {
		return rd.RouteTables().List(ctx, f, p)
	}
	if len(allowedIDs) == 0 {
		return nil, "", nil
	}
	return rd.RouteTables().ListByIDs(ctx, f, allowedIDs, p)
}

// ListOperationsUseCase — операции, относящиеся к конкретному route-table id.
// NB: без repo.Get-precondition — операции должны быть доступны и после Delete
// (история).
type ListOperationsUseCase struct {
	opsRepo operations.Repo
}

// NewListOperationsUseCase создает ListOperationsUseCase.
func NewListOperationsUseCase(opsRepo operations.Repo) *ListOperationsUseCase {
	return &ListOperationsUseCase{opsRepo: opsRepo}
}

// Execute — id-валидация + list.
func (u *ListOperationsUseCase) Execute(ctx context.Context, rtID string, p Pagination) ([]operations.Operation, string, error) {
	if err := corevalidate.ResourceID("route table", ids.PrefixRouteTable, rtID); err != nil {
		return nil, "", err
	}
	return u.opsRepo.List(ctx, operations.ListFilter{
		ResourceID: rtID,
		PageSize:   p.PageSize,
		PageToken:  p.PageToken,
	})
}
