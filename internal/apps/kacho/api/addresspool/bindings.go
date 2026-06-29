// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package addresspool

import (
	"context"
	"fmt"

	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/shared/serviceerr"
)

// BindAsNetworkDefaultUseCase — назначить pool как default для Network.
// Family-agnostic: family-фильтр применяется на resolve-этапе, не на bind.
//
// Проверка существования Network/Pool, Set binding и outbox-emit идут в одной
// writer-TX kacho.Repository.Writer(ctx).
type BindAsNetworkDefaultUseCase struct {
	repo    Repo
	netRepo NetworkRepo
}

// NewBindAsNetworkDefaultUseCase собирает use-case.
func NewBindAsNetworkDefaultUseCase(r Repo, netRepo NetworkRepo) *BindAsNetworkDefaultUseCase {
	return &BindAsNetworkDefaultUseCase{repo: r, netRepo: netRepo}
}

// Execute проверяет Network и AddressPool существуют, затем upsert'ит binding.
func (u *BindAsNetworkDefaultUseCase) Execute(ctx context.Context, networkID, poolID string) error {
	if _, err := u.netRepo.Get(ctx, networkID); err != nil {
		return err
	}
	w, err := u.repo.Writer(ctx)
	if err != nil {
		return err
	}
	defer w.Abort()

	if _, err := w.AddressPools().Get(ctx, poolID); err != nil {
		return err
	}
	if err := w.AddressPoolBindings().SetNetworkDefault(ctx, networkID, poolID); err != nil {
		return err
	}
	if err := w.Outbox().Emit(ctx, "AddressPoolNetworkDefault", networkID, "UPDATED",
		map[string]any{"network_id": networkID, "pool_id": poolID}); err != nil {
		return fmt.Errorf("%w: outbox emit: %v", serviceerr.ErrInternal, err)
	}
	return w.Commit()
}

// UnbindNetworkDefaultUseCase — снятие per-network binding'а (идемпотентно).
type UnbindNetworkDefaultUseCase struct {
	repo Repo
}

// NewUnbindNetworkDefaultUseCase собирает use-case.
func NewUnbindNetworkDefaultUseCase(r Repo) *UnbindNetworkDefaultUseCase {
	return &UnbindNetworkDefaultUseCase{repo: r}
}

// Execute удаляет binding. Idempotent — no error если binding не задан.
func (u *UnbindNetworkDefaultUseCase) Execute(ctx context.Context, networkID string) error {
	w, err := u.repo.Writer(ctx)
	if err != nil {
		return err
	}
	defer w.Abort()

	if err := w.AddressPoolBindings().UnsetNetworkDefault(ctx, networkID); err != nil {
		return err
	}
	if err := w.Outbox().Emit(ctx, "AddressPoolNetworkDefault", networkID, "DELETED",
		map[string]any{"network_id": networkID}); err != nil {
		return fmt.Errorf("%w: outbox emit: %v", serviceerr.ErrInternal, err)
	}
	return w.Commit()
}
