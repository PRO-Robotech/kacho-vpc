package addresspool

import (
	"context"
	"errors"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-corelib/ids"

	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/shared/serviceerr"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// CreatePoolReq — параметры создания пула.
//
// KAC-71: cidr_blocks split на v4_cidr_blocks + v6_cidr_blocks. Хотя бы одно
// поле должно быть непустым (валидация B5; миграция 0022 имеет defensive guard
// на pre-existing rows). Family каждого блока обязательна (REQ-IPL-CR-05 / B6):
// IPv6-префикс в V4CIDRBlocks → InvalidArgument, и симметрично.
type CreatePoolReq struct {
	Name             string
	Description      string
	Labels           map[string]string
	V4CIDRBlocks     []string
	V6CIDRBlocks     []string
	Kind             domain.AddressPoolKind
	ZoneID           string // ru-central1-a; "" = глобальный пул (default fallback)
	IsDefault        bool
	SelectorLabels   map[string]string
	SelectorPriority int32
}

// CreateAddressPoolUseCase — admin-only Create. Sync flow (нет Operation —
// AP не выставляется на external endpoint, никакого async-worker'а): валидация
// → zone-check (опц.) → Insert → materialize freelist (v4) / init cursor (v6).
//
// Семантика идентична legacy `*AddressPoolService.Create`.
type CreateAddressPoolUseCase struct {
	pools    AddressPoolRepo
	addrRepo AddressRepo
	zoneReg  ZoneRegistry // nil → zone-check skip
}

// NewCreateAddressPoolUseCase собирает use-case. addrRepo нужен для
// InitIPv6PoolCursor (sparse v6 allocator, KAC-58).
func NewCreateAddressPoolUseCase(pools AddressPoolRepo, addrRepo AddressRepo, zoneReg ZoneRegistry) *CreateAddressPoolUseCase {
	return &CreateAddressPoolUseCase{pools: pools, addrRepo: addrRepo, zoneReg: zoneReg}
}

// Execute создаёт AddressPool. Verbatim семантика legacy-сервиса.
func (u *CreateAddressPoolUseCase) Execute(ctx context.Context, req CreatePoolReq) (*domain.AddressPool, error) {
	if req.Kind == domain.AddressPoolKindUnspecified {
		return nil, status.Error(codes.InvalidArgument, "kind must be specified")
	}
	// REQ-IPL-CR-04 / B5: хотя бы одно из v4_cidr_blocks / v6_cidr_blocks
	// непусто. Pool без CIDR — бессмысленен (нельзя ни v4-, ни v6-аллокацию).
	if len(req.V4CIDRBlocks) == 0 && len(req.V6CIDRBlocks) == 0 {
		return nil, status.Error(codes.InvalidArgument,
			"v4_cidr_blocks and v6_cidr_blocks must not be both empty")
	}
	// REQ-IPL-CR-05 / B6: family-strict валидация каждого слота. IPv6-prefix в
	// V4CIDRBlocks или IPv4 в V6CIDRBlocks → InvalidArgument с verbatim текстом
	// (см. acceptance §0 «CIDR family detection в API-слое»).
	if err := validateAddressPoolCIDRs("v4_cidr_blocks", req.V4CIDRBlocks, familyV4Strict); err != nil {
		return nil, err
	}
	if err := validateAddressPoolCIDRs("v6_cidr_blocks", req.V6CIDRBlocks, familyV6Strict); err != nil {
		return nil, err
	}
	// zone_id existence — Geography (Region/Zone) — домен kacho-compute (эпик KAC-15):
	// FK address_pools.zone_id → zones убрана; существование зоны проверяем вызовом
	// compute.v1.ZoneService.Get через ZoneRegistry. "" = глобальный пул (zone не нужна).
	if req.ZoneID != "" && u.zoneReg != nil {
		if _, err := u.zoneReg.Get(ctx, req.ZoneID); err != nil {
			if errors.Is(err, ErrNotFound) {
				return nil, status.Errorf(codes.FailedPrecondition, "unknown zone id '%s'", req.ZoneID)
			}
			return nil, serviceerr.MapRepoErr(err)
		}
	}
	now := time.Now().UTC()
	p := &domain.AddressPool{
		ID:               ids.NewID("apl"), // 3-char prefix per YC convention
		Name:             req.Name,
		Description:      req.Description,
		Labels:           req.Labels,
		V4CIDRBlocks:     req.V4CIDRBlocks,
		V6CIDRBlocks:     req.V6CIDRBlocks,
		Kind:             req.Kind,
		ZoneID:           req.ZoneID,
		IsDefault:        req.IsDefault,
		SelectorLabels:   req.SelectorLabels,
		SelectorPriority: req.SelectorPriority,
		CreatedAt:        now,
		ModifiedAt:       now,
	}
	created, err := u.pools.Insert(ctx, p)
	if err != nil {
		return nil, err
	}
	// Материализуем per-IP freelist только для V4CIDRBlocks (миграция 0015):
	// PG-native AllocateIPFromFreelist полагается на эту таблицу. v6-блоки
	// идут через sparse counter в ipv6_pool_cursors (см. ниже).
	// PopulateFreelistForPool сам читает только v4_cidr_blocks (KAC-71) —
	// для v4-only / dual-stack pool заполнит ровно v4-CIDR'ы; для v6-only
	// pool это no-op.
	if err := u.pools.PopulateFreelistForPool(ctx, created.ID); err != nil {
		return nil, status.Errorf(codes.Internal, "populate freelist: %v", err)
	}
	// KAC-58: pool с IPv6 CIDR использует sparse counter-based allocator
	// (миграция 0020). Initialise next_offset=1 если pool имеет v6-блоки.
	if len(created.V6CIDRBlocks) > 0 {
		if err := u.addrRepo.InitIPv6PoolCursor(ctx, created.ID); err != nil {
			return nil, status.Errorf(codes.Internal, "init ipv6 cursor: %v", err)
		}
	}
	return created, nil
}
