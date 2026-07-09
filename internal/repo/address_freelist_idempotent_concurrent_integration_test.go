// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package repo_test

// Concurrent-idempotency integration-тест v4 freelist-аллокатора
// (AllocateIPFromFreelist). Зеркало TestExternalIPv6_ConcurrentAllocateSameAddress
// для v4-пути: N goroutine аллоцируют external_ipv4 для ОДНОГО и того же
// address_id (idempotent-retry — напр. nlb→vpc IPAM-ребро ретраит исходный
// запрос, гоняясь с ним).
//
// Контракт (project-rule #10, module «Idempotent» в allocate.go): выделение
// external_ipv4 для адреса атомарно и идемпотентно — все конкурентные вызовы
// сходятся на ОДНОМ IP, ни один не получает ложный ErrPoolExhausted, и из
// freelist вынут ровно ОДИН IP (проигравшие дубликаты не жгут лишние).
//
// До фикса: target-CTE freelist-SQL резолвился пустым у проигравшего (адрес уже
// получил external_ipv4 в TOCTOU-окне) → 0 строк → pgx.ErrNoRows →
// ErrPoolExhausted, т.е. проигравший дубликат получал «pool exhausted» для
// адреса, которому IP ВЫДАН. Фикс — re-read address FOR UPDATE внутри writer-TX
// (зеркало AllocateExternalIPv6): непустой external_ipv4 → возврат существующего
// IP идемпотентно.

import (
	"context"
	"sync"
	"testing"

	coredb "github.com/PRO-Robotech/kacho-corelib/db"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
	kachopg "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/pg"
	"github.com/stretchr/testify/require"
)

func TestFreelist_ConcurrentAllocateSameAddress(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	pgPool, err := coredb.NewPool(ctx, setupTestDB(t))
	require.NoError(t, err)
	defer pgPool.Close()

	r := kachopg.New(pgPool, nil)
	defer r.Close()

	// /28 = 14 usable IP — exhaustion не мешает (все гоняют один address → 1 pop).
	poolID := insertTestPoolForFreelist(t, ctx, pgPool, "198.51.100.0/28")
	require.NoError(t, freelistWithTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		return w.AddressPools().PopulateFreelistForPool(ctx, poolID)
	}))

	addrID := insertTestAddressFreelist(t, ctx, pgPool)

	const N = 16 // все гоняют один и тот же address_id (idempotent-retry, IPAM race)
	var (
		mu   sync.Mutex
		ips  = make(map[string]bool)
		errs []error
		wg   sync.WaitGroup
	)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var ip string
			aerr := freelistWithTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
				var e error
				ip, e = w.Addresses().AllocateIPFromFreelist(ctx, poolID, addrID)
				return e
			})
			mu.Lock()
			defer mu.Unlock()
			if aerr != nil {
				errs = append(errs, aerr)
				return
			}
			ips[ip] = true
		}()
	}
	wg.Wait()

	require.Empty(t, errs, "concurrent same-address v4 allocate must be idempotent, not report pool-exhausted")
	require.Len(t, ips, 1, "all concurrent allocations for one address must converge on a single IP")

	// Ровно один IP вынут из freelist (14 - 1 = 13) — проигравшие дубликаты не
	// жгут лишние IP.
	var freeAfter int
	require.NoError(t, pgPool.QueryRow(ctx,
		`SELECT COUNT(*) FROM address_pool_free_ips WHERE pool_id = $1`, poolID).Scan(&freeAfter))
	require.Equal(t, 13, freeAfter, "exactly one IP consumed for one address; no leaked pops")

	// addresses.external_ipv4 указывает на тот единственный сошедшийся IP.
	var addrIP string
	require.NoError(t, pgPool.QueryRow(ctx,
		`SELECT external_ipv4->>'address' FROM addresses WHERE id = $1`, addrID).Scan(&addrIP))
	require.NotEmpty(t, addrIP)
	for ip := range ips {
		require.Equal(t, ip, addrIP, "converged IP must match addresses.external_ipv4")
	}
}
