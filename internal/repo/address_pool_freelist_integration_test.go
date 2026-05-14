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
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
)

// insertTestPoolForFreelist inserts a fresh address_pool with the given IPv4 CIDR.
// kind=1 == EXTERNAL_PUBLIC (см. domain.AddressPoolKindExternalPublic).
func insertTestPoolForFreelist(t testing.TB, ctx context.Context, pool *pgxpool.Pool, cidr string) string {
	t.Helper()
	poolID := ids.NewID("apl")
	_, err := pool.Exec(ctx, `
        INSERT INTO address_pools (id, name, cidr_blocks, kind)
        VALUES ($1, $2, ARRAY[$3]::text[], 1)
    `, poolID, t.Name(), cidr)
	require.NoError(t, err)
	return poolID
}

// insertTestAddressFreelist inserts a barebones external IPv4 address row with
// the minimum NOT-NULL columns satisfied via defaults. Returns its id.
func insertTestAddressFreelist(t testing.TB, ctx context.Context, pool *pgxpool.Pool) string {
	t.Helper()
	addrID := ids.NewID(ids.PrefixAddress)
	_, err := pool.Exec(ctx, `
        INSERT INTO addresses (id, folder_id, addr_type, ip_version, reserved)
        VALUES ($1, 'b1gtestfolder000000', 1, 1, true)
    `, addrID)
	require.NoError(t, err)
	return addrID
}

// TestFreelist_BackfillPopulatesIPs — populating the freelist for a /28
// (16 addresses, 14 usable) yields exactly 14 freelist rows for that pool.
func TestFreelist_BackfillPopulatesIPs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	pgPool, err := coredb.NewPool(ctx, setupTestDB(t))
	require.NoError(t, err)
	defer pgPool.Close()

	poolID := insertTestPoolForFreelist(t, ctx, pgPool, "198.51.100.0/28")
	poolRepo := repo.NewAddressPoolRepo(pgPool)
	require.NoError(t, poolRepo.PopulateFreelistForPool(ctx, poolID))

	var count int
	err = pgPool.QueryRow(ctx,
		`SELECT COUNT(*) FROM address_pool_free_ips WHERE pool_id = $1`,
		poolID,
	).Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 14, count, "expected 14 usable IPs for /28 (16 - network - broadcast)")
}

// TestFreelist_ConcurrentAllocateUnique — N concurrent allocators against a
// pool with N free IPs each get a distinct IP; the (N+1)-th call returns
// ErrPoolExhausted.
func TestFreelist_ConcurrentAllocateUnique(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	pgPool, err := coredb.NewPool(ctx, setupTestDB(t))
	require.NoError(t, err)
	defer pgPool.Close()

	poolID := insertTestPoolForFreelist(t, ctx, pgPool, "198.51.100.0/28")
	addrRepo := repo.NewAddressRepo(pgPool)
	poolRepo := repo.NewAddressPoolRepo(pgPool)
	require.NoError(t, poolRepo.PopulateFreelistForPool(ctx, poolID))

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
			ip, allocErr := addrRepo.AllocateIPFromFreelist(ctx, poolID, addrID)
			if allocErr != nil {
				errsCh <- allocErr
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

	// 15-я попытка — пул пуст → ErrPoolExhausted.
	addr15 := insertTestAddressFreelist(t, ctx, pgPool)
	_, err = addrRepo.AllocateIPFromFreelist(ctx, poolID, addr15)
	require.Truef(t, errors.Is(err, repo.ErrPoolExhausted),
		"expected ErrPoolExhausted, got %v", err)
}

// TestFreelist_DeleteReturnsIP — ReturnIPToFreelist puts an IP back; calling
// it twice is idempotent (ON CONFLICT DO NOTHING).
func TestFreelist_DeleteReturnsIP(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	pgPool, err := coredb.NewPool(ctx, setupTestDB(t))
	require.NoError(t, err)
	defer pgPool.Close()

	poolID := insertTestPoolForFreelist(t, ctx, pgPool, "198.51.100.0/28")
	addrRepo := repo.NewAddressRepo(pgPool)
	poolRepo := repo.NewAddressPoolRepo(pgPool)
	require.NoError(t, poolRepo.PopulateFreelistForPool(ctx, poolID))

	addrID := insertTestAddressFreelist(t, ctx, pgPool)
	ip, err := addrRepo.AllocateIPFromFreelist(ctx, poolID, addrID)
	require.NoError(t, err)

	count := func() int {
		var n int
		require.NoError(t,
			pgPool.QueryRow(ctx, `SELECT COUNT(*) FROM address_pool_free_ips WHERE pool_id=$1`, poolID).Scan(&n),
		)
		return n
	}
	require.Equal(t, 13, count(), "after one allocation expected 13 free")

	require.NoError(t, addrRepo.ReturnIPToFreelist(ctx, poolID, ip))
	require.Equal(t, 14, count(), "after return expected 14 free")

	// Idempotency: returning again is a no-op.
	require.NoError(t, addrRepo.ReturnIPToFreelist(ctx, poolID, ip))
	require.Equal(t, 14, count(), "second return should be no-op")
}
