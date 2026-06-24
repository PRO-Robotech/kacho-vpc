// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package addresspool

import (
	"context"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/shared/serviceerr"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/helpers"
)

// AddCidrBlocksUseCase — admin-only добавление CIDR-блоков к пулу. AddressPool —
// admin-only ресурс, поток sync (нет Operation): валидация family/host-bits →
// Writer-TX (Get → append с дедупом → Update → AddCidrToFreelist для новой
// v4-дельты → InitIPv6PoolCursor, если v6 появилась впервые → outbox-emit
// UPDATED) → Commit.
//
// Атомарность DML + freelist materialization + outbox гарантируется одной
// pgx.Tx writer'а.
type AddCidrBlocksUseCase struct {
	repo Repo
}

// NewAddCidrBlocksUseCase собирает use-case.
func NewAddCidrBlocksUseCase(r Repo) *AddCidrBlocksUseCase {
	return &AddCidrBlocksUseCase{repo: r}
}

// Execute добавляет v4/v6 CIDR-блоки. Дубли уже существующих блоков
// игнорируются (idempotent append). Возвращает обновленный AddressPool.
func (u *AddCidrBlocksUseCase) Execute(ctx context.Context, id string, v4, v6 []string) (*domain.AddressPool, error) {
	if id == "" {
		return nil, status.Error(codes.InvalidArgument, "address_pool_id required")
	}
	if len(v4) == 0 && len(v6) == 0 {
		return nil, status.Error(codes.InvalidArgument,
			"v4_cidr_blocks or v6_cidr_blocks is required")
	}
	// Family-strict + host-bits=0 (как validateAddressPoolCIDRs на Create).
	if err := validateAddressPoolCIDRs("v4_cidr_blocks", v4, familyV4Strict); err != nil {
		return nil, err
	}
	if err := validateAddressPoolCIDRs("v6_cidr_blocks", v6, familyV6Strict); err != nil {
		return nil, err
	}
	// Sync within-request precheck — добавляемые блоки попарно не пересекаются
	// (InvalidArgument). DB EXCLUDE — backstop для cross-pool.
	if err := checkPoolCIDRsDisjoint(v4, v6); err != nil {
		return nil, err
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

	// Append с дедупом — не добавляем уже присутствующие блоки.
	mergedV4, newV4 := appendNewCIDRs(cur.V4CIDRBlocks, v4)
	mergedV6, newV6 := appendNewCIDRs(cur.V6CIDRBlocks, v6)

	v6First := len(cur.V6CIDRBlocks) == 0 && len(mergedV6) > 0
	cur.V4CIDRBlocks = mergedV4
	cur.V6CIDRBlocks = mergedV6
	cur.ModifiedAt = nowUTC()

	updated, err := w.AddressPools().Update(ctx, &cur)
	if err != nil {
		return nil, err
	}
	// Нормализуем ТОЛЬКО новую дельту блоков в address_pool_cidrs (EXCLUDE gist).
	// Пересечение с другим пулом / уже существующими блоками →
	// FailedPrecondition "address pool CIDRs can not overlap" (atomic rollback
	// в той же writer-TX). Дельта, а не весь набор — уже-существующие блоки уже
	// в таблице (вставлены на Create / прошлом addCidrBlocks).
	if err := w.AddressPools().InsertCidrBlocks(ctx, updated.ID, updated.Kind, newV4, newV6); err != nil {
		return nil, mapCIDROverlap(err)
	}
	// Материализуем freelist ТОЛЬКО для новых v4-CIDR (не реитерируем
	// существующие free_ips). v6 — sparse counter, не freelist.
	if len(newV4) > 0 {
		if err := w.AddressPools().AddCidrToFreelist(ctx, updated.ID, newV4); err != nil {
			return nil, fmt.Errorf("%w: add cidr to freelist: %v", serviceerr.ErrInternal, err)
		}
	}
	// Если v6-family появилась на пуле впервые — инициализируем cursor (идемпотентно).
	if v6First {
		if err := w.Addresses().InitIPv6PoolCursor(ctx, updated.ID); err != nil {
			return nil, fmt.Errorf("%w: init ipv6 cursor: %v", serviceerr.ErrInternal, err)
		}
	}
	// v6-дельта (newV6) уже нормализована в address_pool_cidrs выше; отдельной
	// freelist-materialization не требует (sparse counter, см. InitIPv6PoolCursor).
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
