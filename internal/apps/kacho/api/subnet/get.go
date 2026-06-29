// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package subnet

import (
	"context"
	"fmt"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	corevalidate "github.com/PRO-Robotech/kacho-corelib/validate"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/shared/serviceerr"
	"github.com/PRO-Robotech/kacho-vpc/internal/authzfilter"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// enforceGetVisible применяет per-object no-leak: если filter != nil, subject не
// пуст и subnet id вне accessible-set (того же FGA grant-set, что и List —
// read==enforce) → NotFound с тем же текстом, что и несуществующий subnet (нет
// existence-leak). FGA-ошибка → fail-closed (Unavailable).
func enforceGetVisible(ctx context.Context, filter ListFilter, subjectID, id, resourceName string) error {
	var port authzfilter.UseCasePort
	if filter != nil {
		port = filter
	}
	visible, err := authzfilter.EnforceVisible(ctx, port, subjectID,
		authzfilter.ResourceTypeSubnet, authzfilter.ActionSubnetList, id)
	if err != nil {
		return err
	}
	if !visible {
		return serviceerr.MapRepoErr(fmt.Errorf("%w: %s %s not found", serviceerr.ErrNotFound, resourceName, id))
	}
	return nil
}

// GetSubnetUseCase — простой read; единственная «логика» — id-валидация,
// перевод repo-sentinel в gRPC status и per-object no-leak enforce.
//
// Открывает Reader-TX явно через `repo.Reader(ctx)` — routing на slave-реплику
// станет автоматическим, когда та появится; пока на той же мастер-pool.
//
// Per-object no-leak: если filter != nil и subject не пуст — после repo.Get
// проверяем, что id входит в accessible-set того же FGA grant-set, что и List
// (read==enforce). id вне set → NotFound с тем же текстом, что и несуществующий
// subnet (caller не различает «нет доступа» и «не существует»). filter == nil /
// subject == "" → enforce делает per-RPC interceptor (dev / system-principal).
type GetSubnetUseCase struct {
	repo   Repo
	filter ListFilter
}

// NewGetSubnetUseCase создает GetSubnetUseCase. filter может быть nil
// (list-filter disabled / dev) → no-leak enforce пропускается.
func NewGetSubnetUseCase(r Repo, filter ListFilter) *GetSubnetUseCase {
	return &GetSubnetUseCase{repo: r, filter: filter}
}

// Execute возвращает repo-entity Subnet. NotFound → mapRepoErr → gRPC NotFound.
// Per-object no-leak: subject без гранта на subnet → NotFound.
func (u *GetSubnetUseCase) Execute(ctx context.Context, subjectID, id string) (*kachorepo.SubnetRecord, error) {
	if err := corevalidate.ResourceID("subnet", ids.PrefixSubnet, id); err != nil {
		return nil, err
	}
	r, err := u.repo.Reader(ctx)
	if err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	defer func() { _ = r.Close() }()
	s, err := r.Subnets().Get(ctx, id)
	if err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	if err := enforceGetVisible(ctx, u.filter, subjectID, id, "Subnet"); err != nil {
		return nil, err
	}
	return s, nil
}
