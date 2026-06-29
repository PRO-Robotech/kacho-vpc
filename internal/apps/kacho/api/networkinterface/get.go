// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package networkinterface

import (
	"context"
	"fmt"

	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/shared/serviceerr"
	"github.com/PRO-Robotech/kacho-vpc/internal/authzfilter"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// enforceGetVisible применяет per-object no-leak: если filter != nil, subject не
// пуст и NIC id вне accessible-set (того же FGA grant-set, что и List — read
// enforce-ится так же, как List) → NotFound с тем же текстом, что и для
// несуществующего NIC (без existence-leak). FGA-ошибка → fail-closed (Unavailable).
func enforceGetVisible(ctx context.Context, filter ListFilter, subjectID, id, resourceName string) error {
	var port authzfilter.UseCasePort
	if filter != nil {
		port = filter
	}
	visible, err := authzfilter.EnforceVisible(ctx, port, subjectID,
		authzfilter.ResourceTypeNetworkInterface, authzfilter.ActionNetworkInterfaceList, id)
	if err != nil {
		return err
	}
	if !visible {
		return serviceerr.MapRepoErr(fmt.Errorf("%w: %s %s not found", serviceerr.ErrNotFound, resourceName, id))
	}
	return nil
}

// GetNetworkInterfaceUseCase — простой read + per-object no-leak enforce.
//
// Открывает reader-TX через CQRS-iface; Reader идет на master-pool, а при наличии
// slave-реплики kacho.Repository.Reader будет роутить туда.
//
// Per-object no-leak: если filter != nil и subject не пуст — после repo.Get
// проверяем, что id входит в accessible-set того же FGA grant-set, что и List.
// filter == nil / subject == "" → enforce делает per-RPC interceptor.
type GetNetworkInterfaceUseCase struct {
	repo   Repo
	filter ListFilter
}

// NewGetNetworkInterfaceUseCase создает GetNetworkInterfaceUseCase. filter может
// быть nil (list-filter disabled / dev) → no-leak enforce пропускается.
func NewGetNetworkInterfaceUseCase(r Repo, filter ListFilter) *GetNetworkInterfaceUseCase {
	return &GetNetworkInterfaceUseCase{repo: r, filter: filter}
}

// Execute возвращает repo-entity NIC. Per-object no-leak: subject без гранта на
// NIC → NotFound.
func (u *GetNetworkInterfaceUseCase) Execute(ctx context.Context, subjectID, id string) (*kachorepo.NetworkInterfaceRecord, error) {
	if err := niResourceID(id); err != nil {
		return nil, err
	}
	rd, err := u.repo.Reader(ctx)
	if err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	defer func() { _ = rd.Close() }()
	got, err := rd.NetworkInterfaces().Get(ctx, id)
	if err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	if err := enforceGetVisible(ctx, u.filter, subjectID, id, "Network interface"); err != nil {
		return nil, err
	}
	return got, nil
}
