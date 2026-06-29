// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package addresspool

import (
	"context"
	"fmt"

	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/shared/serviceerr"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// DeleteAddressPoolUseCase — admin-only Delete. Bindings (network_default /
// address_override) держатся ON DELETE RESTRICT FK и блокируют delete: caller
// должен сначала Unbind.
//
// Delete пула запрещен, если из него выделены IP (есть Address с
// external_ipv4.address_pool_id = id и непустым address). FK здесь невозможен
// (адрес ссылается через JSONB, не через колонку) — нужен service-level guard.
//
// Get + CountAddressesByPool + Delete + outbox-emit идут в одной writer-TX
// kacho.Repository.Writer(ctx).
type DeleteAddressPoolUseCase struct {
	repo Repo
}

// NewDeleteAddressPoolUseCase собирает use-case.
func NewDeleteAddressPoolUseCase(r Repo) *DeleteAddressPoolUseCase {
	return &DeleteAddressPoolUseCase{repo: r}
}

// Execute удаляет AddressPool.
func (u *DeleteAddressPoolUseCase) Execute(ctx context.Context, id string) error {
	w, err := u.repo.Writer(ctx)
	if err != nil {
		return err
	}
	defer w.Abort()

	// Row-lock pool (FOR UPDATE) до count-проверки — external-allocate берет
	// FOR SHARE на тот же pool, поэтому Delete ждет завершения in-flight allocate
	// и видит консистентный count (закрывает TOCTOU между allocate и delete).
	if err := w.AddressPools().LockForUpdate(ctx, id); err != nil {
		return err
	}
	n, err := w.AddressPools().CountAddressesByPool(ctx, id)
	if err != nil {
		return err
	}
	if n > 0 {
		return status.Errorf(codes.FailedPrecondition,
			"AddressPool %s is not empty (%d allocated addresses); release IPs first", id, n)
	}
	if err := w.AddressPools().Delete(ctx, id); err != nil {
		return err
	}
	if err := w.Outbox().Emit(ctx, "AddressPool", id, "DELETED", map[string]any{"id": id}); err != nil {
		return fmt.Errorf("%w: outbox emit: %v", serviceerr.ErrInternal, err)
	}
	return w.Commit()
}
