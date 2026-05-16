package addresspool

import (
	"context"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
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

// UpdateAddressPoolUseCase — admin-only частичный Update. Sync flow:
// repo.Get → mutate → CIDR-family-validate (если replace-флаг) → repo.Update.
// Если v6 появилась впервые на pool — init cursor (idempotent).
type UpdateAddressPoolUseCase struct {
	pools    AddressPoolRepo
	addrRepo AddressRepo
}

// NewUpdateAddressPoolUseCase собирает use-case.
func NewUpdateAddressPoolUseCase(pools AddressPoolRepo, addrRepo AddressRepo) *UpdateAddressPoolUseCase {
	return &UpdateAddressPoolUseCase{pools: pools, addrRepo: addrRepo}
}

// Execute применяет частичное обновление. Verbatim семантика legacy-сервиса.
func (u *UpdateAddressPoolUseCase) Execute(ctx context.Context, req UpdatePoolReq) (*domain.AddressPool, error) {
	cur, err := u.pools.Get(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	if req.Name != nil {
		cur.Name = *req.Name
	}
	if req.Description != nil {
		cur.Description = *req.Description
	}
	if req.ReplaceLabels {
		cur.Labels = req.Labels
	}
	// REQ-IPL-UPD-01 / B7 + REQ-IPL-UPD-02 / B8: replace выполняется ТОЛЬКО при
	// явном bool-флаге; пустой массив в запросе без флага игнорируется
	// (REQ-IPL-UPD-06 / B12). При флаге true — body-array становится новым
	// содержимым, включая пустой массив (REQ-IPL-UPD-05 / B11 — очистить один
	// family на dual-stack pool).
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
	// Проверяем только если хотя бы один replace-флаг выставлен (без них state
	// не меняется и проверка не нужна).
	if req.ReplaceV4CIDR || req.ReplaceV6CIDR {
		if len(newV4) == 0 && len(newV6) == 0 {
			return nil, status.Error(codes.InvalidArgument,
				"v4_cidr_blocks and v6_cidr_blocks must not be both empty after update")
		}
	}
	v6Added := req.ReplaceV6CIDR && len(newV6) > 0 && len(cur.V6CIDRBlocks) == 0
	cur.V4CIDRBlocks = newV4
	cur.V6CIDRBlocks = newV6
	// KAC-58: если v6-family появилась на этом pool впервые (был v4-only,
	// стал dual-stack) — инициализируем cursor. InitIPv6PoolCursor идемпотентен,
	// поэтому повторная инициализация безопасна; но защищаем себя от
	// no-op-вызовов когда v6 не менялся.
	if v6Added {
		if err := u.addrRepo.InitIPv6PoolCursor(ctx, cur.ID); err != nil {
			return nil, status.Errorf(codes.Internal, "init ipv6 cursor: %v", err)
		}
	}
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
	return u.pools.Update(ctx, cur)
}
