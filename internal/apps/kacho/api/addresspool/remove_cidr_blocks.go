// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package addresspool

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/shared/serviceerr"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/helpers"
)

// RemoveCidrBlocksUseCase — admin-only удаление CIDR-блоков из пула
// (по форме как Subnet :removeCidrBlocks). Sync (нет Operation).
//
// Гарантии:
//   - CIDR-блок отсутствует в пуле → FailedPrecondition.
//   - В удаляемом CIDR есть выделенные external-IP → FailedPrecondition
//     "address pool CIDR <cidr> has allocated addresses" (use-check).
//   - Удаление опустошит пул (v4 ∪ v6 = ∅) → InvalidArgument.
//   - Чисто → убрать CIDR из пула + удалить соответствующие free_ips.
//
// Атомарность (см. notes на DeleteFreelistForCidrs в repo): в одной writer-TX
// сперва DELETE free_ips удаляемых CIDR (берет row-lock, блокирует/сериализует
// конкурентный alloc на тех же строках), затем use-check count по addresses.
// Если конкурентный alloc успел закоммитить IP в addresses — count > 0 →
// FailedPrecondition → TX abort → DELETE откатывается. Иначе — free_ips удалены,
// pool обновлен, новые аллокации из удаленных CIDR невозможны (free_ips нет).
type RemoveCidrBlocksUseCase struct {
	repo Repo
}

// NewRemoveCidrBlocksUseCase собирает use-case.
func NewRemoveCidrBlocksUseCase(r Repo) *RemoveCidrBlocksUseCase {
	return &RemoveCidrBlocksUseCase{repo: r}
}

// Execute удаляет v4/v6 CIDR-блоки. Возвращает обновленный AddressPool.
func (u *RemoveCidrBlocksUseCase) Execute(ctx context.Context, id string, v4, v6 []string) (*domain.AddressPool, error) {
	if id == "" {
		return nil, status.Error(codes.InvalidArgument, "address_pool_id required")
	}
	if len(v4) == 0 && len(v6) == 0 {
		return nil, status.Error(codes.InvalidArgument,
			"v4_cidr_blocks or v6_cidr_blocks is required")
	}

	w, err := u.repo.Writer(ctx)
	if err != nil {
		return nil, err
	}
	defer w.Abort()

	curRec, err := w.AddressPools().Get(ctx, id)
	if err != nil {
		return nil, err
	}
	cur := curRec.AddressPool

	remainingV4, removedV4 := subtractCIDRSet(cur.V4CIDRBlocks, v4)
	remainingV6, removedV6 := subtractCIDRSet(cur.V6CIDRBlocks, v6)
	if removedV4 != len(v4) || removedV6 != len(v6) {
		return nil, status.Error(codes.FailedPrecondition,
			"one or more CIDR blocks not found in address pool")
	}
	// Пул не может стать пустым.
	if len(remainingV4) == 0 && len(remainingV6) == 0 {
		return nil, status.Error(codes.InvalidArgument,
			"v4_cidr_blocks and v6_cidr_blocks must not be both empty after removal")
	}

	// Атомарность: DELETE free_ips первым (row-lock сериализует против alloc),
	// потом use-check по addresses. v6-CIDR в DeleteFreelistForCidrs безвредны
	// (free_ips всегда v4). Use-check считает только external_ipv4 — для v6
	// CIDR'ов он вернет 0, и v6-IP блокируются на DB-уровне через
	// ipv6_allocated_ips (вне scope этого guard'а; v6-removal — best-effort,
	// см. notes).
	if err := w.AddressPools().DeleteFreelistForCidrs(ctx, id, v4); err != nil {
		return nil, err
	}
	allocated, err := w.AddressPools().CountAllocatedInCidrs(ctx, id, v4)
	if err != nil {
		return nil, err
	}
	if allocated > 0 {
		// verbatim-стиль (как Subnet "network is not empty").
		return nil, status.Error(codes.FailedPrecondition,
			fmt.Sprintf("address pool CIDR %s has allocated addresses", strings.Join(v4, ", ")))
	}

	cur.V4CIDRBlocks = remainingV4
	cur.V6CIDRBlocks = remainingV6
	cur.ModifiedAt = nowUTC()

	updated, err := w.AddressPools().Update(ctx, &cur)
	if err != nil {
		return nil, err
	}
	// Удаляем снятые блоки из address_pool_cidrs — освобождаем CIDR-диапазон
	// для будущих пулов (EXCLUDE больше не блокирует их). В той же writer-TX →
	// согласовано с use-check выше.
	if err := w.AddressPools().DeleteCidrBlocks(ctx, id, v4, v6); err != nil {
		return nil, err
	}
	if err := w.Outbox().Emit(ctx, "AddressPool", updated.ID, "UPDATED",
		helpers.AddressPoolDomainPayload(&updated.AddressPool)); err != nil {
		return nil, fmt.Errorf("%w: outbox emit: %v", serviceerr.ErrInternal, err)
	}
	if err := w.Commit(); err != nil {
		return nil, err
	}
	out := updated.AddressPool
	return &out, nil
}
