package addresspool

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// PoolUtilization — расчётная статистика использования pool'а.
type PoolUtilization struct {
	PoolID      string
	TotalIPs    int64
	UsedIPs     int64
	FreeIPs     int64
	UsedPercent int32
	CIDRs       []CIDRUsage
}

// CIDRUsage — usage конкретного CIDR-блока внутри pool'а.
type CIDRUsage struct {
	CIDR  string
	Total int64
	Used  int64
}

// GetPoolUtilizationUseCase — total/used/free + per-CIDR breakdown. Admin-only.
//
// KAC-71: utilization считается ТОЛЬКО для V4CIDRBlocks (sparse v6-allocator
// ведёт свою бухгалтерию через ipv6_pool_cursors / ipv6_allocated_ips —
// отдельный observability path). Чтобы admin-UI видел v6-CIDR'ы в списке,
// добавляем их с Total=Used=0 (placeholder, реальная v6-стата — TBD).
type GetPoolUtilizationUseCase struct {
	pools AddressPoolRepo
}

// NewGetPoolUtilizationUseCase собирает use-case.
func NewGetPoolUtilizationUseCase(pools AddressPoolRepo) *GetPoolUtilizationUseCase {
	return &GetPoolUtilizationUseCase{pools: pools}
}

// Execute считает utilization для pool'а.
func (u *GetPoolUtilizationUseCase) Execute(ctx context.Context, poolID string) (*PoolUtilization, error) {
	pool, err := u.pools.Get(ctx, poolID)
	if err != nil {
		return nil, err
	}
	perCIDR, err := u.pools.CountAddressesByPoolPerCIDR(ctx, poolID)
	if err != nil {
		return nil, err
	}
	out := &PoolUtilization{PoolID: poolID}
	for _, c := range pool.V4CIDRBlocks {
		total := usableIPv4Count(c)
		used := perCIDR[c]
		out.CIDRs = append(out.CIDRs, CIDRUsage{CIDR: c, Total: total, Used: used})
		out.TotalIPs += total
		out.UsedIPs += used
	}
	for _, c := range pool.V6CIDRBlocks {
		out.CIDRs = append(out.CIDRs, CIDRUsage{CIDR: c, Total: 0, Used: 0})
	}
	out.FreeIPs = out.TotalIPs - out.UsedIPs
	if out.FreeIPs < 0 {
		out.FreeIPs = 0
	}
	if out.TotalIPs > 0 {
		out.UsedPercent = int32(out.UsedIPs * 100 / out.TotalIPs)
	}
	return out, nil
}

// ListPoolAddressesUseCase — кросс-folder список Address с IP из pool.
type ListPoolAddressesUseCase struct {
	pools AddressPoolRepo
}

// NewListPoolAddressesUseCase собирает use-case.
func NewListPoolAddressesUseCase(pools AddressPoolRepo) *ListPoolAddressesUseCase {
	return &ListPoolAddressesUseCase{pools: pools}
}

// Execute возвращает страницу Address-ресурсов + next-page token.
func (u *ListPoolAddressesUseCase) Execute(ctx context.Context, poolID, folderFilter string, p Pagination) ([]*kachorepo.AddressRecord, string, error) {
	if poolID == "" {
		return nil, "", status.Error(codes.InvalidArgument, "pool_id required")
	}
	return u.pools.ListAddressesByPool(ctx, poolID, folderFilter, p)
}
