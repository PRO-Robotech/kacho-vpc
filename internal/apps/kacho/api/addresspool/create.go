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
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
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
// → zone-check (опц.) → Writer-TX: Insert + PopulateFreelistForPool +
// InitIPv6PoolCursor (если v6 непустой) + outbox-emit `AddressPool.CREATED` →
// Commit.
//
// Wave 5 A.7 sub-PR 1/6 (skill evgeniy §6 G.5): атомарность DML + freelist
// materialization + outbox-emit гарантируется одной pgx.Tx writer'а
// `kacho.Repository.Writer(ctx)`. Прежняя схема (3 отдельных own-Begin/Commit
// в `*AddressPoolRepo.Insert` → `*AddressPoolRepo.PopulateFreelistForPool` →
// `*AddressRepo.InitIPv6PoolCursor`) могла оставлять orphan-state при crash
// между шагами; теперь либо всё, либо ничего.
type CreateAddressPoolUseCase struct {
	repo    Repo
	zoneReg ZoneRegistry // nil → zone-check skip
}

// NewCreateAddressPoolUseCase собирает use-case.
func NewCreateAddressPoolUseCase(r Repo, zoneReg ZoneRegistry) *CreateAddressPoolUseCase {
	return &CreateAddressPoolUseCase{repo: r, zoneReg: zoneReg}
}

// Execute создаёт AddressPool. Verbatim семантика legacy-сервиса.
func (u *CreateAddressPoolUseCase) Execute(ctx context.Context, req CreatePoolReq) (*domain.AddressPool, error) {
	if req.Kind == domain.AddressPoolKindUnspecified {
		return nil, status.Error(codes.InvalidArgument, "kind must be specified")
	}
	// REQ-IPL-CR-04 / B5: хотя бы одно из v4_cidr_blocks / v6_cidr_blocks непусто.
	if len(req.V4CIDRBlocks) == 0 && len(req.V6CIDRBlocks) == 0 {
		return nil, status.Error(codes.InvalidArgument,
			"v4_cidr_blocks and v6_cidr_blocks must not be both empty")
	}
	// REQ-IPL-CR-05 / B6: family-strict валидация каждого слота.
	if err := validateAddressPoolCIDRs("v4_cidr_blocks", req.V4CIDRBlocks, familyV4Strict); err != nil {
		return nil, err
	}
	if err := validateAddressPoolCIDRs("v6_cidr_blocks", req.V6CIDRBlocks, familyV6Strict); err != nil {
		return nil, err
	}
	// zone_id existence — Geography (Region/Zone) — домен kacho-compute (KAC-15).
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
		ID:               ids.NewID("apl"),
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

	w, err := u.repo.Writer(ctx)
	if err != nil {
		return nil, err
	}
	defer w.Abort()

	created, err := w.AddressPools().Insert(ctx, p)
	if err != nil {
		return nil, err
	}
	// Материализуем per-IP freelist только для V4CIDRBlocks (миграция 0014).
	if err := w.AddressPools().PopulateFreelistForPool(ctx, created.ID); err != nil {
		return nil, status.Errorf(codes.Internal, "populate freelist: %v", err)
	}
	// KAC-58: pool с IPv6 CIDR использует sparse counter-based allocator.
	if len(created.V6CIDRBlocks) > 0 {
		if err := w.Addresses().InitIPv6PoolCursor(ctx, created.ID); err != nil {
			return nil, status.Errorf(codes.Internal, "init ipv6 cursor: %v", err)
		}
	}
	if err := w.Outbox().Emit(ctx, "AddressPool", created.ID, "CREATED",
		repo.AddressPoolDomainPayload(&created.AddressPool)); err != nil {
		return nil, status.Errorf(codes.Internal, "outbox emit: %v", err)
	}
	if err := w.Commit(); err != nil {
		return nil, err
	}
	out := created.AddressPool
	return &out, nil
}
