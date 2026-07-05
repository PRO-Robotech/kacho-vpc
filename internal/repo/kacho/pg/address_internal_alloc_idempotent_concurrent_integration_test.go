// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package pg_test

// Concurrent-idempotency integration-тесты internal-IPAM аллокатора
// (AllocateUseCase.AllocateInternalIP / AllocateInternalIPv6, sync internal-only
// RPC). Идемпотентность там держится на read-modify-write одной addresses-строки:
// «Address уже имеет IP → вернуть его». Ключевой инвариант — конкурентные
// дублирующие allocate ОДНОГО И ТОГО ЖЕ address'а обязаны вернуть ОДИН И ТОТ ЖЕ
// IP, совпадающий с тем, что лег в БД (никакого second-writer-wins /
// control-plane↔DB расхождения).
//
// Гонко-безопасность обеспечивается row-lock'ом (GetForUpdate) на входе allocate:
// второй конкурентный вызов блокируется до commit первого, затем видит уже
// проставленный internal_ipv4/ipv6 и возвращает его идемпотентно (project-rule
// #10: within-service инвариант — на DB-уровне, не software Get→check→Update).

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	coredb "github.com/PRO-Robotech/kacho-corelib/db"
	addressapp "github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/api/address"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/cqrsadapter"
	kachopg "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/pg"
)

// runConcurrentAllocate — N goroutine, стартующих одновременно (barrier), вызывают
// одну и ту же allocate-функцию для одного address'а. Возвращает выданные IP и
// ошибки.
func runConcurrentAllocate(n int, allocate func() (*domain.AllocateResult, error)) (ips []string, errs []error) {
	var (
		start sync.WaitGroup
		wg    sync.WaitGroup
		mu    sync.Mutex
	)
	start.Add(1)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			start.Wait() // barrier: максимизируем перекрытие TOCTOU-окна
			res, err := allocate()
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, err)
				return
			}
			ips = append(ips, res.IP)
		}()
	}
	start.Done()
	wg.Wait()
	return ips, errs
}

// TestAllocateInternalIP_ConcurrentIdempotent — N дублирующих AllocateInternalIP
// одного address'а обязаны вернуть ОДИН IP; БД совпадает с выданным значением.
// Под багом (plain Get → check → unconditional Set) каждая goroutine выдаёт свой
// random IP и последний writer перезатирает БД → выданные IP расходятся.
func TestAllocateInternalIP_ConcurrentIdempotent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	dsn := setupTestDB(t)
	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	r := kachopg.New(pool, nil)
	subnetID := allocTestFixture(t, ctx, r, []string{"10.90.0.0/24"}, nil)

	a := newAddress("project-alloc", "addr-idem-v4", false)
	a.InternalIpv4.SubnetID = subnetID
	w, err := r.Writer(ctx)
	require.NoError(t, err)
	_, err = w.Addresses().Insert(ctx, a)
	require.NoError(t, err)
	require.NoError(t, w.Commit())

	uc := addressapp.NewAllocateUseCase(r, cqrsadapter.NewSubnet(r), nil)

	const N = 8
	ips, errs := runConcurrentAllocate(N, func() (*domain.AllocateResult, error) {
		return uc.AllocateInternalIP(ctx, a.ID)
	})

	require.Empty(t, errs, "concurrent idempotent allocate must not error")
	require.Len(t, ips, N)
	for _, ip := range ips {
		require.Equalf(t, ips[0], ip,
			"all concurrent AllocateInternalIP must return the same IP (idempotent); divergent = %v", ips)
	}

	rd, err := r.Reader(ctx)
	require.NoError(t, err)
	defer func() { _ = rd.Close() }()
	got, err := rd.Addresses().Get(ctx, a.ID)
	require.NoError(t, err)
	require.Equal(t, ips[0], got.InternalIpv4.Address,
		"DB internal_ipv4 must equal the IP reported to every caller (no silent second-writer overwrite)")
}

// TestAllocateInternalIPv6_ConcurrentIdempotent — то же для v6-пути
// (AllocateInternalIPv6 / SetInternalIPv6).
func TestAllocateInternalIPv6_ConcurrentIdempotent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	dsn := setupTestDB(t)
	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	r := kachopg.New(pool, nil)
	subnetID := allocTestFixture(t, ctx, r, []string{"10.91.0.0/24"}, []string{"fd00:91::/64"})

	a := newAddress("project-alloc", "addr-idem-v6", false)
	a.IpVersion = domain.IpVersionIPv6
	a.InternalIpv4 = nil
	a.InternalIpv6 = &domain.InternalIpv6Spec{SubnetID: subnetID}
	w, err := r.Writer(ctx)
	require.NoError(t, err)
	_, err = w.Addresses().Insert(ctx, a)
	require.NoError(t, err)
	require.NoError(t, w.Commit())

	uc := addressapp.NewAllocateUseCase(r, cqrsadapter.NewSubnet(r), nil)

	const N = 8
	ips, errs := runConcurrentAllocate(N, func() (*domain.AllocateResult, error) {
		return uc.AllocateInternalIPv6(ctx, a.ID)
	})

	require.Empty(t, errs, "concurrent idempotent v6 allocate must not error")
	require.Len(t, ips, N)
	for _, ip := range ips {
		require.Equalf(t, ips[0], ip,
			"all concurrent AllocateInternalIPv6 must return the same IP (idempotent); divergent = %v", ips)
	}

	rd, err := r.Reader(ctx)
	require.NoError(t, err)
	defer func() { _ = rd.Close() }()
	got, err := rd.Addresses().Get(ctx, a.ID)
	require.NoError(t, err)
	require.Equal(t, ips[0], got.InternalIpv6.Address,
		"DB internal_ipv6 must equal the IP reported to every caller (no silent second-writer overwrite)")
}
