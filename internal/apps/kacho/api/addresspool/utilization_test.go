// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package addresspool

// Unit-тесты арифметики GetPoolUtilizationUseCase через mock-Repo. Раньше эта
// вычислительная логика (integer-division UsedPercent, free-clamp,
// multi-CIDR-суммирование, v6-placeholder) покрывалась только e2e newman-кейсом с
// usedIps==0 — где percent и free тривиально 0/total, а вторых CIDR-блоков и v6
// нет. Здесь пинятся все нетривиальные ветки.
//
// kachomock отдаёт CountAddressesByPoolPerCIDR == nil (не даёт задать used-counts),
// поэтому оборачиваем Reader → AddressPools()-reader, подменяя только per-CIDR
// count (тот же decorator-паттерн, что у freelistRecordingRepo).

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/kachomock"
)

// perCIDRRepo — kachomock.Repository с управляемым CountAddressesByPoolPerCIDR.
type perCIDRRepo struct {
	inner  *kachomock.Repository
	counts map[string]int64
}

func (r *perCIDRRepo) Reader(ctx context.Context) (kachorepo.RepositoryReader, error) {
	rd, err := r.inner.Reader(ctx)
	if err != nil {
		return nil, err
	}
	return &perCIDRReader{RepositoryReader: rd, counts: r.counts}, nil
}

func (r *perCIDRRepo) Writer(ctx context.Context) (kachorepo.RepositoryWriter, error) {
	return r.inner.Writer(ctx)
}

func (r *perCIDRRepo) Close() {}

type perCIDRReader struct {
	kachorepo.RepositoryReader
	counts map[string]int64
}

func (rd *perCIDRReader) AddressPools() kachorepo.AddressPoolReaderIface {
	return &perCIDRPoolReader{
		AddressPoolReaderIface: rd.RepositoryReader.AddressPools(),
		counts:                 rd.counts,
	}
}

type perCIDRPoolReader struct {
	kachorepo.AddressPoolReaderIface
	counts map[string]int64
}

func (pr *perCIDRPoolReader) CountAddressesByPoolPerCIDR(_ context.Context, _ string) (map[string]int64, error) {
	return pr.counts, nil
}

// seedUtilPool кладёт pool в kachomock-state и возвращает его id.
func seedUtilPool(t *testing.T, inner *kachomock.Repository, v4, v6 []string) string {
	t.Helper()
	now := time.Now().UTC()
	id := ids.NewID(ids.PrefixAddressPool)
	inner.SeedAddressPool(&kachorepo.AddressPoolRecord{
		AddressPool: domain.AddressPool{
			ID:           id,
			Name:         domain.RcNameVPC("util-pool"),
			V4CIDRBlocks: v4,
			V6CIDRBlocks: v6,
			Kind:         domain.AddressPoolKindExternalPublic,
		},
		CreatedAt:  now,
		ModifiedAt: now,
	})
	return id
}

func TestGetPoolUtilization_NonZeroUsage_SingleV4(t *testing.T) {
	inner := kachomock.NewRepository()
	pid := seedUtilPool(t, inner, []string{"10.0.0.0/24"}, nil)
	// /24 usable = 254. used = 27 → percent = 27*100/254 = 10 (integer division).
	r := &perCIDRRepo{inner: inner, counts: map[string]int64{"10.0.0.0/24": 27}}

	got, err := NewGetPoolUtilizationUseCase(r).Execute(context.Background(), pid)
	require.NoError(t, err)
	assert.Equal(t, int64(254), got.TotalIPs)
	assert.Equal(t, int64(27), got.UsedIPs)
	assert.Equal(t, int64(227), got.FreeIPs)
	assert.Equal(t, int32(10), got.UsedPercent, "27*100/254 = 10 via integer division (multiply before divide)")
	require.Len(t, got.CIDRs, 1)
	assert.Equal(t, CIDRUsage{CIDR: "10.0.0.0/24", Total: 254, Used: 27}, got.CIDRs[0])
}

func TestGetPoolUtilization_MultiV4Block_Summation(t *testing.T) {
	inner := kachomock.NewRepository()
	pid := seedUtilPool(t, inner, []string{"10.0.0.0/24", "10.0.1.0/25"}, nil)
	// /24 usable=254, /25 usable=126 → total = 380. used = 10 + 6 = 16.
	r := &perCIDRRepo{inner: inner, counts: map[string]int64{
		"10.0.0.0/24": 10,
		"10.0.1.0/25": 6,
	}}

	got, err := NewGetPoolUtilizationUseCase(r).Execute(context.Background(), pid)
	require.NoError(t, err)
	assert.Equal(t, int64(380), got.TotalIPs, "summation across multiple v4 cidr blocks")
	assert.Equal(t, int64(16), got.UsedIPs)
	assert.Equal(t, int64(364), got.FreeIPs)
	assert.Equal(t, int32(16*100/380), got.UsedPercent) // 4
	require.Len(t, got.CIDRs, 2)
	assert.Equal(t, int64(254), got.CIDRs[0].Total)
	assert.Equal(t, int64(126), got.CIDRs[1].Total)
}

func TestGetPoolUtilization_DualStack_V6Placeholder(t *testing.T) {
	inner := kachomock.NewRepository()
	pid := seedUtilPool(t, inner, []string{"10.0.0.0/24"}, []string{"2001:db8::/64"})
	r := &perCIDRRepo{inner: inner, counts: map[string]int64{"10.0.0.0/24": 254}}

	got, err := NewGetPoolUtilizationUseCase(r).Execute(context.Background(), pid)
	require.NoError(t, err)
	// v6 CIDR НЕ входит в TotalIPs (sparse-allocator ведёт свой учёт).
	assert.Equal(t, int64(254), got.TotalIPs, "v6 cidr excluded from TotalIPs")
	assert.Equal(t, int64(254), got.UsedIPs)
	assert.Equal(t, int64(0), got.FreeIPs)
	assert.Equal(t, int32(100), got.UsedPercent)
	require.Len(t, got.CIDRs, 2, "v6 cidr appears as placeholder in breakdown")
	// последний CIDR — v6-placeholder Total=Used=0.
	v6 := got.CIDRs[len(got.CIDRs)-1]
	assert.Equal(t, "2001:db8::/64", v6.CIDR)
	assert.Equal(t, int64(0), v6.Total)
	assert.Equal(t, int64(0), v6.Used)
}

func TestGetPoolUtilization_UsedExceedsTotal_FreeClampsToZero(t *testing.T) {
	inner := kachomock.NewRepository()
	pid := seedUtilPool(t, inner, []string{"10.0.0.0/24"}, nil)
	// Оборонительная ветка: used (300) > total (254) → free не должен уйти в минус.
	r := &perCIDRRepo{inner: inner, counts: map[string]int64{"10.0.0.0/24": 300}}

	got, err := NewGetPoolUtilizationUseCase(r).Execute(context.Background(), pid)
	require.NoError(t, err)
	assert.Equal(t, int64(254), got.TotalIPs)
	assert.Equal(t, int64(300), got.UsedIPs)
	assert.Equal(t, int64(0), got.FreeIPs, "free must clamp to 0, never negative")
}

func TestGetPoolUtilization_ZeroTotal_NoDivByZero(t *testing.T) {
	inner := kachomock.NewRepository()
	// v6-only pool: нет V4CIDR → TotalIPs=0 → percent-ветка (guard TotalIPs>0)
	// не выполняется, деления на ноль нет.
	pid := seedUtilPool(t, inner, nil, []string{"2001:db8::/64"})
	r := &perCIDRRepo{inner: inner, counts: nil}

	got, err := NewGetPoolUtilizationUseCase(r).Execute(context.Background(), pid)
	require.NoError(t, err)
	assert.Equal(t, int64(0), got.TotalIPs)
	assert.Equal(t, int64(0), got.UsedIPs)
	assert.Equal(t, int64(0), got.FreeIPs)
	assert.Equal(t, int32(0), got.UsedPercent)
	require.Len(t, got.CIDRs, 1)
}
