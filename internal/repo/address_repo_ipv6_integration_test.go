package repo_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	coredb "github.com/PRO-Robotech/kacho-corelib/db"
	"github.com/PRO-Robotech/kacho-corelib/ids"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
	kachopg "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/pg"
)

// KAC-60: sparse counter-based IPv6 allocator (миграция 0021).
//
// Проверяем 3 поведения:
//  1. InitIPv6PoolCursor — идемпотентен, создаёт cursor=1.
//  2. AllocateExternalIPv6 — последовательно выдаёт IP'и pool_base+1, +2, +3,
//     записывает в ipv6_allocated_ips и addresses.external_ipv6, эмитит outbox.
//  3. FreeExternalIPv6 → push offset → ipv6_released_offsets → переиспользование.
//
// KAC-94 A.7 sub-PR 5/6: переписан на CQRS Writer.

func TestIntegration_AddressRepo_IPv6_AllocateAndFree(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	dsn := setupTestDB(t)
	p, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer p.Close()

	r := kachopg.New(p, nil)
	defer r.Close()

	withTx := func(t *testing.T, fn func(kacho.RepositoryWriter) error) error {
		t.Helper()
		w, err := r.Writer(ctx)
		require.NoError(t, err)
		if err := fn(w); err != nil {
			w.Abort()
			return err
		}
		return w.Commit()
	}

	// Setup: address_pool с v6 CIDR через raw SQL (правильные defaults).
	poolID := "aplv6test12345678901"
	_, err = p.Exec(ctx, `
		INSERT INTO address_pools (id, name, description, labels, v4_cidr_blocks, v6_cidr_blocks, kind, is_default, created_at, modified_at, selector_labels, selector_priority)
		VALUES ($1, 'test-v6-pool', '', '{}'::jsonb, ARRAY[]::text[], ARRAY['2001:db8::/64']::text[], 1, false, now(), now(), '{}'::jsonb, 0)`,
		poolID)
	require.NoError(t, err, "insert v6 pool")
	t.Cleanup(func() { _, _ = p.Exec(context.Background(), `DELETE FROM address_pools WHERE id = $1`, poolID) })

	// Step 1: InitIPv6PoolCursor — идемпотент.
	require.NoError(t, withTx(t, func(w kacho.RepositoryWriter) error {
		return w.Addresses().InitIPv6PoolCursor(ctx, poolID)
	}))
	require.NoError(t, withTx(t, func(w kacho.RepositoryWriter) error {
		return w.Addresses().InitIPv6PoolCursor(ctx, poolID)
	}), "must be idempotent")

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
		ad := a
		require.NoError(t, withTx(t, func(w kacho.RepositoryWriter) error {
			_, e := w.Addresses().Insert(ctx, ad)
			return e
		}))
	}

	allocV6 := func(addrID string) string {
		var ip string
		require.NoError(t, withTx(t, func(w kacho.RepositoryWriter) error {
			var e error
			ip, e = w.Addresses().AllocateExternalIPv6(ctx, poolID, addrID, "ru-central1-a")
			return e
		}))
		return ip
	}

	require.Equal(t, "2001:db8::1", allocV6(addr1.ID))
	require.Equal(t, "2001:db8::2", allocV6(addr2.ID))
	require.Equal(t, "2001:db8::3", allocV6(addr3.ID))

	// addresses.external_ipv6 заполнен.
	rd, err := r.Reader(ctx)
	require.NoError(t, err)
	got1, err := rd.Addresses().Get(ctx, addr1.ID)
	require.NoError(t, rd.Close())
	require.NoError(t, err)
	require.NotNil(t, got1.ExternalIpv6)
	require.Equal(t, "2001:db8::1", got1.ExternalIpv6.Address)
	require.Equal(t, poolID, got1.ExternalIpv6.AddressPoolID)

	// Step 3: Free addr2 → offset должен попасть в released.
	require.NoError(t, withTx(t, func(w kacho.RepositoryWriter) error {
		return w.Addresses().FreeExternalIPv6(ctx, addr2.ID)
	}))

	// addresses.external_ipv6 у addr2 очищен.
	rd2, err := r.Reader(ctx)
	require.NoError(t, err)
	got2, err := rd2.Addresses().Get(ctx, addr2.ID)
	require.NoError(t, rd2.Close())
	require.NoError(t, err)
	require.Nil(t, got2.ExternalIpv6, "FreeExternalIPv6 must clear external_ipv6")

	// released_offsets содержит 2.
	var rOff int64
	require.NoError(t, p.QueryRow(ctx,
		`SELECT "offset" FROM ipv6_released_offsets WHERE pool_id = $1`, poolID).Scan(&rOff))
	require.Equal(t, int64(2), rOff)

	addr4 := makeAddrShell(folderID, now, "a-v6-4")
	require.NoError(t, withTx(t, func(w kacho.RepositoryWriter) error {
		_, e := w.Addresses().Insert(ctx, addr4)
		return e
	}))
	ip4 := allocV6(addr4.ID)
	require.Equal(t, "2001:db8::2", ip4, "must reuse released offset 2 before fresh next_offset=4")

	// Free идемпотент.
	require.NoError(t, withTx(t, func(w kacho.RepositoryWriter) error {
		return w.Addresses().FreeExternalIPv6(ctx, addr2.ID)
	}))

	// cleanup
	for _, a := range []*domain.Address{addr1, addr2, addr3, addr4} {
		ad := a
		_ = withTx(t, func(w kacho.RepositoryWriter) error {
			_ = w.Addresses().FreeExternalIPv6(ctx, ad.ID)
			return w.Addresses().Delete(ctx, ad.ID)
		})
	}
}

func makeAddrShell(folderID string, _ time.Time, name string) *domain.Address {
	return &domain.Address{
		ID:        ids.NewID(ids.PrefixAddress),
		FolderID:  folderID,
		Name:      domain.RcNameVPC(name),
		Type:      domain.AddressTypeExternal,
		IpVersion: domain.IpVersionIPv6,
		Reserved:  true,
	}
}
