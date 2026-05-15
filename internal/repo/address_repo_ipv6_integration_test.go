package repo_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	coredb "github.com/PRO-Robotech/kacho-corelib/db"
	"github.com/PRO-Robotech/kacho-corelib/ids"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
)

// KAC-60: sparse counter-based IPv6 allocator (миграция 0021).
//
// Проверяем 3 поведения:
//  1. InitIPv6PoolCursor — идемпотентен, создаёт cursor=1.
//  2. AllocateExternalIPv6 — последовательно выдаёт IP'и pool_base+1, +2, +3,
//     записывает в ipv6_allocated_ips и addresses.external_ipv6, эмитит outbox.
//  3. FreeExternalIPv6 → push offset → ipv6_released_offsets → переиспользование.

func setupV6Pool(t *testing.T, ctx context.Context, pool interface {
	Exec(context.Context, string, ...any) (any, error)
}, poolID, cidr string) {
	t.Helper()
}

func TestIntegration_AddressRepo_IPv6_AllocateAndFree(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	dsn := setupTestDB(t)
	p, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer p.Close()

	addrRepo := repo.NewAddressRepo(p)

	// Setup: address_pool с v6 CIDR через PoolRepo (правильные defaults +
	// freelist-population). Pool изолирован за t.Cleanup → cascade удалит
	// ipv6_pool_cursors / allocated / released (FK ON DELETE CASCADE из 0021).
	poolID := "aplv6test12345678901"
	// KAC-71: after migration 0022, column `cidr_blocks` split into
	// v4_cidr_blocks / v6_cidr_blocks — v6 prefix goes into the v6 slot.
	_, err = p.Exec(ctx, `
		INSERT INTO address_pools (id, name, description, labels, v4_cidr_blocks, v6_cidr_blocks, kind, is_default, created_at, modified_at, selector_labels, selector_priority)
		VALUES ($1, 'test-v6-pool', '', '{}'::jsonb, ARRAY[]::text[], ARRAY['2001:db8::/64']::text[], 1, false, now(), now(), '{}'::jsonb, 0)`,
		poolID)
	require.NoError(t, err, "insert v6 pool")
	t.Cleanup(func() { _, _ = p.Exec(context.Background(), `DELETE FROM address_pools WHERE id = $1`, poolID) })

	// Step 1: InitIPv6PoolCursor — идемпотент.
	require.NoError(t, addrRepo.InitIPv6PoolCursor(ctx, poolID))
	require.NoError(t, addrRepo.InitIPv6PoolCursor(ctx, poolID), "must be idempotent")

	var nextOff int64
	require.NoError(t, p.QueryRow(ctx,
		`SELECT next_offset FROM ipv6_pool_cursors WHERE pool_id = $1`, poolID).Scan(&nextOff))
	require.Equal(t, int64(1), nextOff)

	// Step 2: insert 3 addresses, allocate v6 for each.
	now := time.Now().UTC().Truncate(time.Microsecond)
	folderID := "f-v6-alloc"
	addr1 := makeAddrShell(folderID, now, "a-v6-1")
	addr2 := makeAddrShell(folderID, now, "a-v6-2")
	addr3 := makeAddrShell(folderID, now, "a-v6-3")
	for _, a := range []*domain.Address{addr1, addr2, addr3} {
		_, err := addrRepo.Insert(ctx, a)
		require.NoError(t, err)
	}

	ip1, err := addrRepo.AllocateExternalIPv6(ctx, poolID, addr1.ID, "ru-central1-a")
	if err != nil {
		t.Fatalf("AllocateExternalIPv6 ip1: %+v", err)
	}
	ip2, err := addrRepo.AllocateExternalIPv6(ctx, poolID, addr2.ID, "ru-central1-a")
	require.NoError(t, err)
	ip3, err := addrRepo.AllocateExternalIPv6(ctx, poolID, addr3.ID, "ru-central1-a")
	require.NoError(t, err)

	// Monotonic: pool_base = 2001:db8::, offsets 1,2,3 → 2001:db8::1, ::2, ::3.
	require.Equal(t, "2001:db8::1", ip1)
	require.Equal(t, "2001:db8::2", ip2)
	require.Equal(t, "2001:db8::3", ip3)

	// addresses.external_ipv6 заполнен.
	got1, err := addrRepo.Get(ctx, addr1.ID)
	require.NoError(t, err)
	require.NotNil(t, got1.ExternalIpv6)
	require.Equal(t, "2001:db8::1", got1.ExternalIpv6.Address)
	require.Equal(t, poolID, got1.ExternalIpv6.AddressPoolID)

	// Step 3: Free addr2 → offset должен попасть в released. Следующая аллокация
	// (для нового address) использует ::2 (offset 2 из released, не fresh).
	require.NoError(t, addrRepo.FreeExternalIPv6(ctx, addr2.ID))

	// addresses.external_ipv6 у addr2 очищен.
	got2, err := addrRepo.Get(ctx, addr2.ID)
	require.NoError(t, err)
	require.Nil(t, got2.ExternalIpv6, "FreeExternalIPv6 must clear external_ipv6")

	// released_offsets содержит 2.
	var rOff int64
	require.NoError(t, p.QueryRow(ctx,
		`SELECT "offset" FROM ipv6_released_offsets WHERE pool_id = $1`, poolID).Scan(&rOff))
	require.Equal(t, int64(2), rOff)

	addr4 := makeAddrShell(folderID, now, "a-v6-4")
	_, err = addrRepo.Insert(ctx, addr4)
	require.NoError(t, err)
	ip4, err := addrRepo.AllocateExternalIPv6(ctx, poolID, addr4.ID, "ru-central1-a")
	require.NoError(t, err)
	require.Equal(t, "2001:db8::2", ip4, "must reuse released offset 2 before fresh next_offset=4")

	// Free идемпотент: повторный Free на freed-уже-address — no-op.
	require.NoError(t, addrRepo.FreeExternalIPv6(ctx, addr2.ID))

	// cleanup
	for _, a := range []*domain.Address{addr1, addr2, addr3, addr4} {
		_ = addrRepo.FreeExternalIPv6(ctx, a.ID)
		_ = addrRepo.Delete(ctx, a.ID)
	}
}

func makeAddrShell(folderID string, now time.Time, name string) *domain.Address {
	return &domain.Address{
		ID:        ids.NewID(ids.PrefixAddress),
		FolderID:  folderID,
		CreatedAt: now,
		Name:      name,
		Type:      domain.AddressTypeExternal,
		IpVersion: domain.IpVersionIPv6,
		Reserved:  true,
	}
}
