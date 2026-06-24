// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package routetable

import (
	"context"
	"fmt"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	corevalidate "github.com/PRO-Robotech/kacho-corelib/validate"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/shared/serviceerr"
	"github.com/PRO-Robotech/kacho-vpc/internal/authzfilter"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// enforceGetVisible применяет per-object no-leak: если filter != nil, subject не
// пуст и route-table id вне accessible-set (того же FGA grant-set, что и List —
// read==enforce) → NotFound с тем же текстом, что и несуществующий route table
// (no existence leak). FGA-ошибка → fail-closed (Unavailable).
func enforceGetVisible(ctx context.Context, filter ListFilter, subjectID, id, resourceName string) error {
	var port authzfilter.UseCasePort
	if filter != nil {
		port = filter
	}
	visible, err := authzfilter.EnforceVisible(ctx, port, subjectID,
		authzfilter.ResourceTypeRouteTable, authzfilter.ActionRouteTableList, id)
	if err != nil {
		return err
	}
	if !visible {
		return serviceerr.MapRepoErr(fmt.Errorf("%w: %s %s not found", serviceerr.ErrNotFound, resourceName, id))
	}
	return nil
}

// GetRouteTableUseCase — простой read через CQRS Reader + per-object no-leak
// enforce. Возвращает `*kacho.RouteTableRecord` (repo-leaf entity).
//
// Если filter != nil и subject не пуст — после repo.Get проверяем, что id входит
// в accessible-set того же FGA grant-set, что и List (read==enforce).
// filter == nil / subject == "" → enforce делает per-RPC interceptor
// (dev / system-principal).
type GetRouteTableUseCase struct {
	repo   Repo
	filter ListFilter
}

// NewGetRouteTableUseCase создает GetRouteTableUseCase. filter может быть nil
// (list-filter disabled / dev) → no-leak enforce пропускается.
func NewGetRouteTableUseCase(r Repo, filter ListFilter) *GetRouteTableUseCase {
	return &GetRouteTableUseCase{repo: r, filter: filter}
}

// Execute возвращает repo-entity RouteTable. Per-object no-leak: subject без
// гранта на route table → NotFound.
func (u *GetRouteTableUseCase) Execute(ctx context.Context, subjectID, id string) (*kacho.RouteTableRecord, error) {
	if err := corevalidate.ResourceID("route table", ids.PrefixRouteTable, id); err != nil {
		return nil, err
	}
	rd, err := u.repo.Reader(ctx)
	if err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	defer func() { _ = rd.Close() }()
	rt, gerr := rd.RouteTables().Get(ctx, id)
	if gerr != nil {
		return nil, serviceerr.MapRepoErr(gerr)
	}
	if err := enforceGetVisible(ctx, u.filter, subjectID, id, "Route table"); err != nil {
		return nil, err
	}
	return rt, nil
}
