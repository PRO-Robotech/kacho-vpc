// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package gateway

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
// пуст и gateway id вне accessible-set (того же FGA grant-set, что и List) →
// NotFound с тем же текстом, что и для несуществующего gateway (без утечки факта
// существования). FGA-ошибка → fail-closed (Unavailable).
func enforceGetVisible(ctx context.Context, filter ListFilter, subjectID, id, resourceName string) error {
	var port authzfilter.UseCasePort
	if filter != nil {
		port = filter
	}
	visible, err := authzfilter.EnforceVisible(ctx, port, subjectID,
		authzfilter.ResourceTypeGateway, authzfilter.ActionGatewayList, id)
	if err != nil {
		return err
	}
	if !visible {
		return serviceerr.MapRepoErr(fmt.Errorf("%w: %s %s not found", serviceerr.ErrNotFound, resourceName, id))
	}
	return nil
}

// GetGatewayUseCase — простой read; единственная «логика» — id-валидация, перевод
// repo-sentinel в gRPC status и per-object no-leak enforce.
//
// Открывает read-only TX через `repo.Reader(ctx)`; закрытие читателя — defer
// `rd.Close()` (no-op rollback на read-only TX, освобождает соединение).
//
// Per-object no-leak: если filter != nil и subject не пуст — после repo.Get
// проверяем, что id входит в тот же FGA grant-set, что и List. filter == nil /
// subject == "" → enforce делает per-RPC interceptor (dev / system-principal).
type GetGatewayUseCase struct {
	repo   Repo
	filter ListFilter
}

// NewGetGatewayUseCase создает GetGatewayUseCase. filter может быть nil
// (list-filter disabled / dev) → no-leak enforce пропускается.
func NewGetGatewayUseCase(r Repo, filter ListFilter) *GetGatewayUseCase {
	return &GetGatewayUseCase{repo: r, filter: filter}
}

// Execute возвращает repo-entity Gateway. NotFound → mapRepoErr → gRPC NotFound.
// Per-object no-leak: subject без гранта на gateway → NotFound.
func (u *GetGatewayUseCase) Execute(ctx context.Context, subjectID, id string) (*kacho.GatewayRecord, error) {
	if err := corevalidate.ResourceID("gateway", ids.PrefixGateway, id); err != nil {
		return nil, err
	}
	rd, err := u.repo.Reader(ctx)
	if err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	defer func() { _ = rd.Close() }()

	g, err := rd.Gateways().Get(ctx, id)
	if err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	if err := enforceGetVisible(ctx, u.filter, subjectID, id, "Gateway"); err != nil {
		return nil, err
	}
	return g, nil
}
