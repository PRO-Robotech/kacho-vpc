// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package addresspool

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-corelib/safeconv"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// PoolUtilization — расчетная статистика использования pool'а.
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
// Utilization считается ТОЛЬКО для V4CIDRBlocks: sparse v6-allocator ведет
// собственный учет через ipv6_pool_cursors / ipv6_allocated_ips (отдельный
// observability path). Чтобы admin-UI видел v6-CIDR'ы в списке, они попадают в
// breakdown как placeholder с Total=Used=0.
type GetPoolUtilizationUseCase struct {
	repo Repo
}

// NewGetPoolUtilizationUseCase собирает use-case.
func NewGetPoolUtilizationUseCase(r Repo) *GetPoolUtilizationUseCase {
	return &GetPoolUtilizationUseCase{repo: r}
}

// Execute считает utilization для pool'а.
func (u *GetPoolUtilizationUseCase) Execute(ctx context.Context, poolID string) (*PoolUtilization, error) {
	rd, err := u.repo.Reader(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rd.Close() }()

	pool, err := rd.AddressPools().Get(ctx, poolID)
	if err != nil {
		return nil, err
	}
	perCIDR, err := rd.AddressPools().CountAddressesByPoolPerCIDR(ctx, poolID)
	if err != nil {
		return nil, err
	}
	out := &PoolUtilization{PoolID: poolID}
	for _, c := range pool.V4CIDRBlocks {
		total := domain.UsableIPv4Count(c)
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
		out.UsedPercent = safeconv.ClampInt32(out.UsedIPs * 100 / out.TotalIPs)
	}
	return out, nil
}

// ListPoolAddressesUseCase — кросс-project список Address с IP из pool.
type ListPoolAddressesUseCase struct {
	repo Repo
}

// NewListPoolAddressesUseCase собирает use-case.
func NewListPoolAddressesUseCase(r Repo) *ListPoolAddressesUseCase {
	return &ListPoolAddressesUseCase{repo: r}
}

// Execute возвращает страницу Address-ресурсов + next-page token.
func (u *ListPoolAddressesUseCase) Execute(ctx context.Context, poolID, projectFilter string, p Pagination) ([]*kachorepo.AddressRecord, string, error) {
	if poolID == "" {
		return nil, "", status.Error(codes.InvalidArgument, "pool_id required")
	}
	rd, err := u.repo.Reader(ctx)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = rd.Close() }()

	return rd.AddressPools().ListAddressesByPool(ctx, poolID, projectFilter, p)
}
