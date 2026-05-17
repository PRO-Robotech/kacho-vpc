package repo_test

import (
	"context"
	"errors"
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

// KAC-94 A.7 sub-PR 5/6: переписан на CQRS Writer.

func insertTestPoolForFreelist(t testing.TB, ctx context.Context, pool *pgxpool.Pool, cidr string) string {
	t.Helper()
	poolID := ids.NewID("apl")
	_, err := pool.Exec(ctx, `
        INSERT INTO address_pools (id, name, v4_cidr_blocks, kind)
        VALUES ($1, $2, ARRAY[$3]::text[], 1)
    `, poolID, t.Name(), cidr)
	require.NoError(t, err)
	return poolID
}

func insertTestAddressFreelist(t testing.TB, ctx context.Context, pool *pgxpool.Pool) string {
	t.Helper()
	addrID := ids.NewID(ids.PrefixAddress)
	_, err := pool.Exec(ctx, `
        INSERT INTO addresses (id, project_id, addr_type, ip_version, reserved)
        VALUES ($1, 'b1gtestfolder000000', 1, 1, true)
    `, addrID)
	require.NoError(t, err)
	return addrID
}

func freelistWithTx(t *testing.T, ctx context.Context, r kacho.Repository, fn func(kacho.RepositoryWriter) error) error {
	t.Helper()
	w, err := r.Writer(ctx)
	require.NoError(t, err)
	if err := fn(w); err != nil {
		w.Abort()
		return err
	}
	return w.Commit()
}

func TestFreelist_BackfillPopulatesIPs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	pgPool, err := coredb.NewPool(ctx, setupTestDB(t))
	require.NoError(t, err)
	defer pgPool.Close()

	r := kachopg.New(pgPool, nil)
	defer r.Close()

	poolID := insertTestPoolForFreelist(t, ctx, pgPool, "198.51.100.0/28")
	require.NoError(t, freelistWithTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		return w.AddressPools().PopulateFreelistForPool(ctx, poolID)
	}))

	var count int
	err = pgPool.QueryRow(ctx,
		`SELECT COUNT(*) FROM address_pool_free_ips WHERE pool_id = $1`,
		poolID,
	).Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 14, count, "expected 14 usable IPs for /28 (16 - network - broadcast)")
}

func TestFreelist_ConcurrentAllocateUnique(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	pgPool, err := coredb.NewPool(ctx, setupTestDB(t))
	require.NoError(t, err)
	defer pgPool.Close()

	r := kachopg.New(pgPool, nil)
	defer r.Close()

	poolID := insertTestPoolForFreelist(t, ctx, pgPool, "198.51.100.0/28")
	require.NoError(t, freelistWithTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		return w.AddressPools().PopulateFreelistForPool(ctx, poolID)
	}))

	const N = 14
	addrIDs := make([]string, N)
	for i := range addrIDs {
		addrIDs[i] = insertTestAddressFreelist(t, ctx, pgPool)
	}

	var (
		mu     sync.Mutex
		ips    = make(map[string]bool, N)
		errsCh = make(chan error, N)
		wg     sync.WaitGroup
	)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(addrID string) {
			defer wg.Done()
			var ip string
			err := freelistWithTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
				var e error
				ip, e = w.Addresses().AllocateIPFromFreelist(ctx, poolID, addrID)
				return e
			})
			if err != nil {
				errsCh <- err
				return
			}
			mu.Lock()
			defer mu.Unlock()
			if ips[ip] {
				errsCh <- errors.New("duplicate IP returned: " + ip)
				return
			}
			ips[ip] = true
		}(addrIDs[i])
	}
	wg.Wait()
	close(errsCh)
	for e := range errsCh {
		t.Fatalf("concurrent allocate error: %v", e)
	}
	require.Equal(t, N, len(ips), "expected %d unique IPs", N)

	// 15-я попытка — пул пуст → helpers.ErrPoolExhausted.
	addr15 := insertTestAddressFreelist(t, ctx, pgPool)
	err = freelistWithTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		_, e := w.Addresses().AllocateIPFromFreelist(ctx, poolID, addr15)
		return e
	})
	require.Truef(t, errors.Is(err, helpers.ErrPoolExhausted),
		"expected helpers.ErrPoolExhausted, got %v", err)
}

func TestFreelist_DeleteReturnsIP(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	pgPool, err := coredb.NewPool(ctx, setupTestDB(t))
	require.NoError(t, err)
	defer pgPool.Close()

	r := kachopg.New(pgPool, nil)
	defer r.Close()

	poolID := insertTestPoolForFreelist(t, ctx, pgPool, "198.51.100.0/28")
	require.NoError(t, freelistWithTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		return w.AddressPools().PopulateFreelistForPool(ctx, poolID)
	}))

	addrID := insertTestAddressFreelist(t, ctx, pgPool)
	var ip string
	require.NoError(t, freelistWithTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		var e error
		ip, e = w.Addresses().AllocateIPFromFreelist(ctx, poolID, addrID)
		return e
	}))

	count := func() int {
		var n int
		require.NoError(t,
			pgPool.QueryRow(ctx, `SELECT COUNT(*) FROM address_pool_free_ips WHERE pool_id=$1`, poolID).Scan(&n),
		)
		return n
	}
	require.Equal(t, 13, count(), "after one allocation expected 13 free")

	require.NoError(t, freelistWithTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		return w.Addresses().ReturnIPToFreelist(ctx, poolID, ip)
	}))
	require.Equal(t, 14, count(), "after return expected 14 free")

	// Idempotency.
	require.NoError(t, freelistWithTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		return w.Addresses().ReturnIPToFreelist(ctx, poolID, ip)
	}))
	require.Equal(t, 14, count(), "second return should be no-op")
}
