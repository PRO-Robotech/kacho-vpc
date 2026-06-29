// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package pg_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	coredb "github.com/PRO-Robotech/kacho-corelib/db"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	kachopg "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/pg"
)

// IPAM-аллокатор (allocateInternalIPv4 / allocateInternalIPv6 в
// apps/kacho/api/address/create.go) делает retry-on-unique-violation ВНУТРИ
// одной writer-TX: SetIPSpec → 23505 → попробовать другой IP → SetIPSpec.
// Postgres абортит TX на первой же ошибке (последующие стейтменты → 25P02
// in_failed_sql_transaction), поэтому конфликтно-ретраимые writer-методы
// обязаны исполняться под SAVEPOINT — иначе один конфликт фейлит весь Create
// вместо ретрая.
//
// Тесты пинят контракт: после конфликтной попытки writer-TX остается живой,
// следующая попытка проходит, Commit фиксирует результат.

// allocTestFixture — network + subnet (committed) для FK
// addresses_internal_subnet_fkey.
func allocTestFixture(t *testing.T, ctx context.Context, r *kachopg.Repository, v4 []string, v6 []string) string {
	t.Helper()
	w, err := r.Writer(ctx)
	require.NoError(t, err)
	net := newNetwork("project-alloc", "net-alloc-"+t.Name())
	_, err = w.Networks().Insert(ctx, net)
	require.NoError(t, err)
	sub := newSubnet("project-alloc", "sub-alloc-"+t.Name(), net.ID, "zone-a", v4)
	sub.V6CidrBlocks = v6
	created, err := w.Subnets().Insert(ctx, sub)
	require.NoError(t, err)
	require.NoError(t, w.Commit())
	return created.ID
}

// TestCQRS_Address_SetIPSpec_ConflictKeepsTXAlive — конфликт UNIQUE
// (addresses_internal_subnet_ip_uniq) на первой попытке НЕ должен абортить
// writer-TX: вторая попытка с другим IP обязана пройти, Commit — успешен.
func TestCQRS_Address_SetIPSpec_ConflictKeepsTXAlive(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	dsn := setupTestDB(t)
	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	r := kachopg.New(pool, nil)
	subnetID := allocTestFixture(t, ctx, r, []string{"10.77.0.0/24"}, nil)

	// Address A занимает 10.77.0.5 (committed).
	wA, err := r.Writer(ctx)
	require.NoError(t, err)
	a := newAddress("project-alloc", "addr-a", false)
	a.InternalIpv4.SubnetID = subnetID
	_, err = wA.Addresses().Insert(ctx, a)
	require.NoError(t, err)
	_, err = wA.Addresses().SetIPSpec(ctx, a.ID, nil, &domain.InternalIpv4Spec{
		SubnetID: subnetID, Address: "10.77.0.5",
	})
	require.NoError(t, err)
	require.NoError(t, wA.Commit())

	// Address B: первая попытка — конфликт, вторая — другой IP. Все в ОДНОЙ TX.
	wB, err := r.Writer(ctx)
	require.NoError(t, err)
	defer wB.Abort()
	b := newAddress("project-alloc", "addr-b", false)
	b.InternalIpv4.SubnetID = subnetID
	_, err = wB.Addresses().Insert(ctx, b)
	require.NoError(t, err)

	_, err = wB.Addresses().SetIPSpec(ctx, b.ID, nil, &domain.InternalIpv4Spec{
		SubnetID: subnetID, Address: "10.77.0.5", // конфликт с A
	})
	require.Error(t, err, "duplicate internal IP must be rejected")

	// Ключевой контракт: TX не отравлена — retry с другим IP проходит.
	rec, err := wB.Addresses().SetIPSpec(ctx, b.ID, nil, &domain.InternalIpv4Spec{
		SubnetID: subnetID, Address: "10.77.0.6",
	})
	require.NoError(t, err, "writer-TX must stay usable after a unique-violation attempt (savepoint contract)")
	require.Equal(t, "10.77.0.6", rec.InternalIpv4.Address)
	require.NoError(t, wB.Commit())

	rd, err := r.Reader(ctx)
	require.NoError(t, err)
	defer func() { _ = rd.Close() }()
	got, err := rd.Addresses().Get(ctx, b.ID)
	require.NoError(t, err)
	require.Equal(t, "10.77.0.6", got.InternalIpv4.Address)
}

// TestCQRS_Address_SetInternalIPv6_ConflictKeepsTXAlive — тот же контракт для
// v6-аллокатора (addresses_internal_subnet_ipv6_uniq).
func TestCQRS_Address_SetInternalIPv6_ConflictKeepsTXAlive(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	dsn := setupTestDB(t)
	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	r := kachopg.New(pool, nil)
	subnetID := allocTestFixture(t, ctx, r, []string{"10.78.0.0/24"}, []string{"fd00:78::/64"})

	wA, err := r.Writer(ctx)
	require.NoError(t, err)
	a := newAddress("project-alloc", "addr-a6", false)
	a.IpVersion = domain.IpVersionIPv6
	a.InternalIpv4 = nil
	a.InternalIpv6 = &domain.InternalIpv6Spec{SubnetID: subnetID}
	_, err = wA.Addresses().Insert(ctx, a)
	require.NoError(t, err)
	_, err = wA.Addresses().SetInternalIPv6(ctx, a.ID, &domain.InternalIpv6Spec{
		SubnetID: subnetID, Address: "fd00:78::5",
	})
	require.NoError(t, err)
	require.NoError(t, wA.Commit())

	wB, err := r.Writer(ctx)
	require.NoError(t, err)
	defer wB.Abort()
	b := newAddress("project-alloc", "addr-b6", false)
	b.IpVersion = domain.IpVersionIPv6
	b.InternalIpv4 = nil
	b.InternalIpv6 = &domain.InternalIpv6Spec{SubnetID: subnetID}
	_, err = wB.Addresses().Insert(ctx, b)
	require.NoError(t, err)

	_, err = wB.Addresses().SetInternalIPv6(ctx, b.ID, &domain.InternalIpv6Spec{
		SubnetID: subnetID, Address: "fd00:78::5",
	})
	require.Error(t, err, "duplicate internal IPv6 must be rejected")

	rec, err := wB.Addresses().SetInternalIPv6(ctx, b.ID, &domain.InternalIpv6Spec{
		SubnetID: subnetID, Address: "fd00:78::6",
	})
	require.NoError(t, err, "writer-TX must stay usable after a unique-violation attempt (savepoint contract)")
	require.Equal(t, "fd00:78::6", rec.InternalIpv6.Address)
	require.NoError(t, wB.Commit())
}
