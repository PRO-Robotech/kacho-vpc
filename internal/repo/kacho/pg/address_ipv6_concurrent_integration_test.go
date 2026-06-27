// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package pg_test

// Concurrent-race integration-тесты sparse-counter v6 IPAM-аллокатора
// (AllocateExternalIPv6). Аллокатор имеет две гонко-чувствительные ветки:
//   - cursor-path: атомарный bump ipv6_pool_cursors (row-lock сериализует);
//   - released-path: pop освобождённого offset через FOR UPDATE SKIP LOCKED.
// Уникальность выданных адресов держится на DB-уровне (ipv6_allocated_ips
// PRIMARY KEY (pool_id, ip) + UNIQUE (pool_id, offset) + addresses
// addresses_external_v6_pool_ip_uniq). Эти тесты пинят инвариант под нагрузкой:
// N goroutine из одного пула → ни одного дубля, ровно столько успехов, сколько
// свободных слотов; освобождённые offset'ы переиспользуются без коллизий.

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	coredb "github.com/PRO-Robotech/kacho-corelib/db"
	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/helpers"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
	kachopg "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/pg"
)

// insertV6Pool — прямой INSERT external-public AddressPool (kind=1) с одним
// v6-CIDR. address_pool_cidrs не заполняем: AllocateExternalIPv6 читает
// v6_cidr_blocks напрямую из address_pools.
func insertV6Pool(t testing.TB, ctx context.Context, pool *pgxpool.Pool, cidr string) string {
	t.Helper()
	poolID := ids.NewID("apl")
	_, err := pool.Exec(ctx, `
        INSERT INTO address_pools (id, name, v6_cidr_blocks, kind)
        VALUES ($1, 'v6concpool', ARRAY[$2]::text[], 1)
    `, poolID, cidr)
	require.NoError(t, err)
	return poolID
}

// insertV6Address — минимальная addresses-строка под external-v6 allocate
// (AllocateExternalIPv6 делает UPDATE addresses SET external_ipv6 WHERE id=$1).
func insertV6Address(t testing.TB, ctx context.Context, pool *pgxpool.Pool) string {
	t.Helper()
	addrID := ids.NewID(ids.PrefixAddress)
	_, err := pool.Exec(ctx, `
        INSERT INTO addresses (id, project_id, addr_type, ip_version, reserved)
        VALUES ($1, 'b1gtestproject00000', 1, 1, true)
    `, addrID)
	require.NoError(t, err)
	return addrID
}

// v6Tx — writer-TX обёртка: Commit на успехе, Abort на ошибке (откатывает в т.ч.
// cursor-bump неудачной exhausted-попытки — поэтому слот не «теряется»).
func v6Tx(t *testing.T, ctx context.Context, r *kachopg.Repository, fn func(kacho.RepositoryWriter) error) error {
	t.Helper()
	w, err := r.Writer(ctx)
	require.NoError(t, err)
	if err := fn(w); err != nil {
		w.Abort()
		return err
	}
	return w.Commit()
}

// TestExternalIPv6_ConcurrentAllocateUnique — N goroutine аллоцируют из одного
// малого v6-пула (/124). Контракт: каждый выданный адрес уникален; ровно
// `capacity` успехов (остальные — clean ErrPoolExhausted); пул после прогона
// исчерпан (ни одного потерянного/недовыданного слота).
func TestExternalIPv6_ConcurrentAllocateUnique(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	pgPool, err := coredb.NewPool(ctx, setupTestDB(t))
	require.NoError(t, err)
	defer pgPool.Close()

	r := kachopg.New(pgPool, nil)

	// /124 = 16 адресов; offset 0 зарезервирован (cursor стартует с next_offset=1),
	// поэтому ёмкость = 16 - 1 = 15 (offset'ы 1..15).
	const prefixLen = 124
	capacity := (1 << (128 - prefixLen)) - 1
	poolID := insertV6Pool(t, ctx, pgPool, fmt.Sprintf("fd00:dead:beef::/%d", prefixLen))
	require.NoError(t, v6Tx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		return w.Addresses().InitIPv6PoolCursor(ctx, poolID)
	}))

	const N = 24 // > capacity → гарантированная contention на границе исчерпания
	addrIDs := make([]string, N)
	for i := range addrIDs {
		addrIDs[i] = insertV6Address(t, ctx, pgPool)
	}

	var (
		mu        sync.Mutex
		ips       = make(map[string]bool, N)
		successes int
		exhausted int
		other     []error
		wg        sync.WaitGroup
	)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(addrID string) {
			defer wg.Done()
			var ip string
			aerr := v6Tx(t, ctx, r, func(w kacho.RepositoryWriter) error {
				var e error
				ip, e = w.Addresses().AllocateExternalIPv6(ctx, poolID, addrID, "zone-a")
				return e
			})
			mu.Lock()
			defer mu.Unlock()
			switch {
			case aerr == nil:
				if ips[ip] {
					other = append(other, fmt.Errorf("duplicate IP returned: %s", ip))
					return
				}
				ips[ip] = true
				successes++
			case errors.Is(aerr, helpers.ErrPoolExhausted):
				exhausted++
			default:
				other = append(other, aerr)
			}
		}(addrIDs[i])
	}
	wg.Wait()

	require.Empty(t, other, "no duplicate IPs and no unexpected errors under concurrency")
	require.Equal(t, successes, len(ips), "every success is a unique address")
	require.Equal(t, N, successes+exhausted, "each goroutine either allocated or got pool-exhausted")
	require.Equal(t, capacity, successes, "exactly as many successes as free slots (capacity)")
	require.Greater(t, exhausted, 0, "N oversubscribes capacity → some goroutines hit exhaustion")

	// Пул полностью выбран: следующая аллокация — ErrPoolExhausted (ни один слот не
	// «потерян» из-за rollback'а exhausted-попыток).
	extra := insertV6Address(t, ctx, pgPool)
	err = v6Tx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		_, e := w.Addresses().AllocateExternalIPv6(ctx, poolID, extra, "zone-a")
		return e
	})
	require.Truef(t, errors.Is(err, helpers.ErrPoolExhausted),
		"drained pool must report ErrPoolExhausted, got %v", err)
}

// TestExternalIPv6_ConcurrentReleasedOffsetReuse — освобождённые offset'ы
// переиспользуются под нагрузкой без дублей. Сначала аллоцируем M адресов из
// большого пула и освобождаем их (offset'ы → ipv6_released_offsets), затем M
// goroutine параллельно реаллоцируют. FOR UPDATE SKIP LOCKED гарантирует, что ни
// один released-offset не достанется двум TX одновременно (упавший на locked-row
// уходит в cursor-path) — итоговые адреса уникальны.
func TestExternalIPv6_ConcurrentReleasedOffsetReuse(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	pgPool, err := coredb.NewPool(ctx, setupTestDB(t))
	require.NoError(t, err)
	defer pgPool.Close()

	r := kachopg.New(pgPool, nil)

	// /116 = 4096 адресов — exhaustion не мешает (тестируем released-path, не границу).
	poolID := insertV6Pool(t, ctx, pgPool, "fd00:beef:cafe::/116")
	require.NoError(t, v6Tx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		return w.Addresses().InitIPv6PoolCursor(ctx, poolID)
	}))

	const M = 16
	// Фаза 1: аллоцируем M адресов (последовательно) и освобождаем их → M
	// released-offset'ов в пуле.
	freedAddrIDs := make([]string, M)
	for i := 0; i < M; i++ {
		addrID := insertV6Address(t, ctx, pgPool)
		freedAddrIDs[i] = addrID
		require.NoError(t, v6Tx(t, ctx, r, func(w kacho.RepositoryWriter) error {
			_, e := w.Addresses().AllocateExternalIPv6(ctx, poolID, addrID, "zone-a")
			return e
		}))
	}
	for _, addrID := range freedAddrIDs {
		require.NoError(t, v6Tx(t, ctx, r, func(w kacho.RepositoryWriter) error {
			return w.Addresses().FreeExternalIPv6(ctx, addrID)
		}))
	}

	// Фаза 2: M goroutine параллельно реаллоцируют (released-path под SKIP LOCKED +
	// cursor-fallback). Контракт: все M успешны и уникальны.
	addrIDs := make([]string, M)
	for i := range addrIDs {
		addrIDs[i] = insertV6Address(t, ctx, pgPool)
	}
	var (
		mu    sync.Mutex
		ips   = make(map[string]bool, M)
		errsc = make(chan error, M)
		wg    sync.WaitGroup
	)
	for i := 0; i < M; i++ {
		wg.Add(1)
		go func(addrID string) {
			defer wg.Done()
			var ip string
			aerr := v6Tx(t, ctx, r, func(w kacho.RepositoryWriter) error {
				var e error
				ip, e = w.Addresses().AllocateExternalIPv6(ctx, poolID, addrID, "zone-a")
				return e
			})
			if aerr != nil {
				errsc <- aerr
				return
			}
			mu.Lock()
			defer mu.Unlock()
			if ips[ip] {
				errsc <- fmt.Errorf("duplicate IP on released-offset reuse: %s", ip)
				return
			}
			ips[ip] = true
		}(addrIDs[i])
	}
	wg.Wait()
	close(errsc)
	for e := range errsc {
		t.Fatalf("concurrent released-offset reuse error: %v", e)
	}
	require.Equal(t, M, len(ips), "all %d concurrent reallocations produced unique addresses", M)
}
