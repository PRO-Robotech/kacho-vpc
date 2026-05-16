package addresspool

import (
	"context"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
)

// UpdatePoolReq — частичное обновление; nil-пойнтеры/false-flags = no-op.
//
// KAC-71: CIDR-поля — детерминированный replace через explicit bool-флаги
// `ReplaceV4CIDR` / `ReplaceV6CIDR`. Body-array значимо ТОЛЬКО при выставленном
// флаге (даже пустой массив — это явный «очистить» при флаге true). Без флага
// V4CIDRBlocks / V6CIDRBlocks в запросе игнорируется (REQ-IPL-UPD-06 / B12).
// Это позволяет изменять один family, не трогая второй; и явно очищать family
// (превращая dual-stack pool в v4-only или v6-only). Очистить оба family
// одновременно (или единственный непустой family) запрещено invariant'ом
// "v4 ∪ v6 ≠ ∅ after update" (REQ-IPL-UPD-03 / B10).
type UpdatePoolReq struct {
	ID                     string
	Name                   *string
	Description            *string
	ReplaceLabels          bool
	Labels                 map[string]string
	ReplaceV4CIDR          bool
	V4CIDRBlocks           []string
	ReplaceV6CIDR          bool
	V6CIDRBlocks           []string
	UpdateIsDefault        bool
	IsDefault              bool
	ReplaceSelectorLabels  bool
	SelectorLabels         map[string]string
	UpdateSelectorPriority bool
	SelectorPriority       int32
}

// UpdateAddressPoolUseCase — admin-only частичный Update.
//
// Wave 5 A.7 sub-PR 1/6 (skill evgeniy §6 G.5): Get + mutate + Update +
// InitIPv6PoolCursor (если v6 появилась впервые) + outbox-emit идут в одной
// writer-TX `kacho.Repository.Writer(ctx)` — атомарность гарантирована.
type UpdateAddressPoolUseCase struct {
	repo Repo
}

// NewUpdateAddressPoolUseCase собирает use-case.
func NewUpdateAddressPoolUseCase(r Repo) *UpdateAddressPoolUseCase {
	return &UpdateAddressPoolUseCase{repo: r}
}

// Execute применяет частичное обновление. Verbatim семантика legacy-сервиса.
func (u *UpdateAddressPoolUseCase) Execute(ctx context.Context, req UpdatePoolReq) (*domain.AddressPool, error) {
	w, err := u.repo.Writer(ctx)
	if err != nil {
		return nil, err
	}
	defer w.Abort()

	curRec, err := w.AddressPools().Get(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	cur := curRec.AddressPool
	if req.Name != nil {
		cur.Name = *req.Name
	}
	if req.Description != nil {
		cur.Description = *req.Description
	}
	if req.ReplaceLabels {
		cur.Labels = req.Labels
	}
	// REQ-IPL-UPD-01/02 + REQ-IPL-UPD-05/06: replace ТОЛЬКО при флаге.
	newV4 := cur.V4CIDRBlocks
	newV6 := cur.V6CIDRBlocks
	if req.ReplaceV4CIDR {
		if err := validateAddressPoolCIDRs("v4_cidr_blocks", req.V4CIDRBlocks, familyV4Strict); err != nil {
			return nil, err
		}
		newV4 = req.V4CIDRBlocks
	}
	if req.ReplaceV6CIDR {
		if err := validateAddressPoolCIDRs("v6_cidr_blocks", req.V6CIDRBlocks, familyV6Strict); err != nil {
			return nil, err
		}
		newV6 = req.V6CIDRBlocks
	}
	// REQ-IPL-UPD-03 / B10: post-update invariant — хотя бы один family непуст.
	if req.ReplaceV4CIDR || req.ReplaceV6CIDR {
		if len(newV4) == 0 && len(newV6) == 0 {
			return nil, status.Error(codes.InvalidArgument,
				"v4_cidr_blocks and v6_cidr_blocks must not be both empty after update")
		}
	}
	v6Added := req.ReplaceV6CIDR && len(newV6) > 0 && len(cur.V6CIDRBlocks) == 0
	cur.V4CIDRBlocks = newV4
	cur.V6CIDRBlocks = newV6
	if req.UpdateIsDefault {
		cur.IsDefault = req.IsDefault
	}
	if req.ReplaceSelectorLabels {
		cur.SelectorLabels = req.SelectorLabels
	}
	if req.UpdateSelectorPriority {
		cur.SelectorPriority = req.SelectorPriority
	}
	cur.ModifiedAt = time.Now().UTC()

	updated, err := w.AddressPools().Update(ctx, &cur)
	if err != nil {
		return nil, err
	}
	// KAC-58: если v6-family появилась на этом pool впервые (был v4-only,
	// стал dual-stack) — инициализируем cursor (идемпотентно).
	if v6Added {
		if err := w.Addresses().InitIPv6PoolCursor(ctx, updated.ID); err != nil {
			return nil, status.Errorf(codes.Internal, "init ipv6 cursor: %v", err)
		}
	}
	if err := w.Outbox().Emit(ctx, "AddressPool", updated.ID, "UPDATED",
		repo.AddressPoolDomainPayload(&updated.AddressPool)); err != nil {
		return nil, status.Errorf(codes.Internal, "outbox emit: %v", err)
	}
	if err := w.Commit(); err != nil {
		return nil, err
	}
	out := updated.AddressPool
	return &out, nil
}
