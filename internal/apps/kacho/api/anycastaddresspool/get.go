// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package anycastaddresspool

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	corevalidate "github.com/PRO-Robotech/kacho-corelib/validate"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/shared/serviceerr"
	"github.com/PRO-Robotech/kacho-vpc/internal/authzfilter"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// enforceGetVisible — per-object existence-hiding: filter != nil, subject не пуст
// и id вне accessible-set (того же FGA grant-set, что и List) → NotFound с тем же
// текстом, что и несуществующий пул (deny→404, не PermissionDenied). FGA-ошибка →
// fail-closed (Unavailable).
func enforceGetVisible(ctx context.Context, filter ListFilter, subjectID, id string) error {
	var port authzfilter.UseCasePort
	if filter != nil {
		port = filter
	}
	visible, err := authzfilter.EnforceVisible(ctx, port, subjectID,
		authzfilter.ResourceTypeAnycastAddressPool, authzfilter.ActionAnycastAddressPoolList, id)
	if err != nil {
		return err
	}
	if !visible {
		return serviceerr.MapRepoErr(fmt.Errorf("%w: Anycast address pool %s not found", serviceerr.ErrNotFound, id))
	}
	return nil
}

// GetAnycastAddressPoolUseCase — sync read + id-валидация + per-object no-leak.
type GetAnycastAddressPoolUseCase struct {
	repo   Repo
	filter ListFilter
}

// NewGetAnycastAddressPoolUseCase создаёт use-case. filter nil → no-leak enforce
// пропускается (dev / system-principal).
func NewGetAnycastAddressPoolUseCase(r Repo, filter ListFilter) *GetAnycastAddressPoolUseCase {
	return &GetAnycastAddressPoolUseCase{repo: r, filter: filter}
}

// Execute — malformed id → sync InvalidArgument первым стейтментом; well-formed-
// но-нет → NotFound; не виден subject'у → NotFound (existence-hiding).
func (u *GetAnycastAddressPoolUseCase) Execute(ctx context.Context, subjectID, id string) (*kachorepo.AnycastAddressPoolRecord, error) {
	if err := corevalidate.ResourceID("anycast address pool", ids.PrefixAnycastPool, id); err != nil {
		return nil, err
	}
	rd, err := u.repo.Reader(ctx)
	if err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	defer func() { _ = rd.Close() }()
	rec, err := rd.AnycastAddressPools().Get(ctx, id)
	if err != nil {
		// Контрактное сообщение "Anycast address pool <id> not found" — единое и в
		// pg-пути (WrapPgErr), и в mock-пути (bare sentinel) — формируем явно.
		if errors.Is(err, repo.ErrNotFound) {
			return nil, status.Errorf(codes.NotFound, "Anycast address pool %s not found", id)
		}
		return nil, serviceerr.MapRepoErr(err)
	}
	if err := enforceGetVisible(ctx, u.filter, subjectID, id); err != nil {
		return nil, err
	}
	return rec, nil
}
