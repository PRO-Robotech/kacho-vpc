// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package repo_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	coredb "github.com/PRO-Robotech/kacho-corelib/db"
	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
	kachopg "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/pg"
)

// CountAddressesByPool обязан учитывать выделенные адреса ОБЕИХ семей: иначе
// пул, раздавший только external IPv6, считался бы пустым (если смотреть лишь
// external_ipv4) и Delete оставил бы dangling external_ipv6.address_pool_id.
func TestAddressPool_CountIncludesV6(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	pgPool, err := coredb.NewPool(ctx, setupTestDB(t))
	require.NoError(t, err)
	defer pgPool.Close()
	r := kachopg.New(pgPool, nil)
	defer r.Close()

	// Пул с v6 CIDR.
	poolID := ids.NewID("apl")
	_, err = pgPool.Exec(ctx, `
        INSERT INTO address_pools (id, name, v6_cidr_blocks, kind)
        VALUES ($1, $2, ARRAY['2001:db8:abcd::/48']::text[], 1)`, poolID, t.Name())
	require.NoError(t, err)

	// Адрес с выделенным external IPv6 из этого пула.
	addrID := ids.NewID(ids.PrefixAddress)
	_, err = pgPool.Exec(ctx, `
        INSERT INTO addresses (id, project_id, addr_type, ip_version, reserved, external_ipv6)
        VALUES ($1, 'b1gtestproject00000', 1, 2, true,
                jsonb_build_object('address', '2001:db8:abcd::5', 'address_pool_id', $2::text))`,
		addrID, poolID)
	require.NoError(t, err)

	rd, err := r.Reader(ctx)
	require.NoError(t, err)
	defer func() { _ = rd.Close() }()
	n, err := rd.AddressPools().CountAddressesByPool(ctx, poolID)
	require.NoError(t, err)
	require.Equal(t, int64(1), n, "v6-allocated address must be counted (pool not empty)")
}

// Контроль: пул без выделений → count 0 (удаляем без проблем).
func TestAddressPool_CountZeroWhenEmpty(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	pgPool, err := coredb.NewPool(ctx, setupTestDB(t))
	require.NoError(t, err)
	defer pgPool.Close()
	r := kachopg.New(pgPool, nil)
	defer r.Close()

	poolID := ids.NewID("apl")
	_, err = pgPool.Exec(ctx, `
        INSERT INTO address_pools (id, name, v4_cidr_blocks, kind)
        VALUES ($1, $2, ARRAY['198.51.100.0/24']::text[], 1)`, poolID, t.Name())
	require.NoError(t, err)

	require.NoError(t, legacyWithTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		n, e := w.AddressPools().CountAddressesByPool(ctx, poolID)
		require.NoError(t, e)
		require.Equal(t, int64(0), n)
		// LockForUpdate на существующем пуле — без ошибки.
		return w.AddressPools().LockForUpdate(ctx, poolID)
	}))
}
