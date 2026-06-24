// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package gateway

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

// ListGatewaysUseCase — list gateways с пагинацией. project_id обязателен;
// открывает read-only TX через repo.Reader(ctx). Result фильтруется per-object
// через FGA ListObjects (relation viewer; read==enforce); filter==nil или
// subject=="" → passthrough без фильтрации.
type ListGatewaysUseCase struct {
	repo   Repo
	filter ListFilter
}

// NewListGatewaysUseCase создает ListGatewaysUseCase. filter может быть nil.
func NewListGatewaysUseCase(r Repo, filter ListFilter) *ListGatewaysUseCase {
	return &ListGatewaysUseCase{repo: r, filter: filter}
}

// Execute — project_id required + per-object FGA-filter. Pagination применяется
// ПОСЛЕ фильтра; bypass → repo.List; пустой grant → пустой результат (no-leak);
// iam недоступен → fail-closed Unavailable.
func (u *ListGatewaysUseCase) Execute(ctx context.Context, subjectID string, f GatewayFilter, p Pagination) ([]*kacho.GatewayRecord, string, error) {
	if f.ProjectID == "" {
		return nil, "", status.Error(codes.InvalidArgument, "project_id required")
	}
	rd, err := u.repo.Reader(ctx)
	if err != nil {
		return nil, "", serviceerr.MapRepoErr(err)
	}
	defer func() { _ = rd.Close() }()

	if u.filter == nil || subjectID == "" {
		gws, nextToken, lerr := rd.Gateways().List(ctx, f, p)
		if lerr != nil {
			return nil, "", serviceerr.MapRepoErr(lerr)
		}
		return gws, nextToken, nil
	}
	allowedIDs, bypass, ferr := u.filter.ListAllowedIDs(ctx, subjectID,
		authzfilter.ResourceTypeGateway, authzfilter.ActionGatewayList)
	if ferr != nil {
		return nil, "", ferr
	}
	if bypass {
		gws, nextToken, lerr := rd.Gateways().List(ctx, f, p)
		if lerr != nil {
			return nil, "", serviceerr.MapRepoErr(lerr)
		}
		return gws, nextToken, nil
	}
	if len(allowedIDs) == 0 {
		return nil, "", nil
	}
	gws, nextToken, lerr := rd.Gateways().ListByIDs(ctx, f, allowedIDs, p)
	if lerr != nil {
		return nil, "", serviceerr.MapRepoErr(lerr)
	}
	return gws, nextToken, nil
}

// ListOperationsUseCase — операции, относящиеся к конкретному gateway-id.
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
func (u *ListOperationsUseCase) Execute(ctx context.Context, gwID string, p Pagination) ([]operations.Operation, string, error) {
	if err := corevalidate.ResourceID("gateway", ids.PrefixGateway, gwID); err != nil {
		return nil, "", err
	}
	return u.opsRepo.List(ctx, operations.ListFilter{
		ResourceID: gwID,
		PageSize:   p.PageSize,
		PageToken:  p.PageToken,
	})
}
