// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package repo_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	coredb "github.com/PRO-Robotech/kacho-corelib/db"
	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
	kachopg "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/pg"
)

// Lost-update regression для трёх ресурсов, у которых doUpdate раньше делал
// read-modify-write через голый Get (без FOR UPDATE): Address / NetworkInterface
// / AddressPool. Choreography идентична update_lost_update_integration_test.go
// (RouteTable/Network): TX-A берёт row-lock через GetForUpdate, TX-B в горутине
// блокируется на своём GetForUpdate до commit TX-A, затем читает уже обновлённый
// row и применяет disjoint-поле поверх → оба поля сохраняются.
//
// На голом Get TX-B прочитала бы snapshot до записи TX-A и затёрла бы un-masked
// поле A (second-writer-wins) — нарушение project-rule #10. GetForUpdate
// (SELECT ... FOR UPDATE) сериализует read-modify-write на уровне БД.

func TestIntegration_Address_ConcurrentDisjointUpdate_NoLostUpdate(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	dsn := setupTestDB(t)
	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()
	r := kachopg.New(pool, nil)
	defer r.Close()

	addrID := ids.NewID(ids.PrefixAddress)
	require.NoError(t, legacyWithTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		_, e := w.Addresses().Insert(ctx, &domain.Address{
			ID: addrID, ProjectID: "f-adr", Name: domain.RcNameVPC("name0"),
			Description: domain.RcDescription("desc0"),
			Type:        domain.AddressTypeExternal, IpVersion: domain.IpVersionIPv4,
			ExternalIpv4: &domain.ExternalIpv4Spec{Address: "198.51.100.7", ZoneID: "zone-a"},
		})
		return e
	}))

	wa, err := r.Writer(ctx)
	require.NoError(t, err)
	recA, err := wa.Addresses().GetForUpdate(ctx, addrID)
	require.NoError(t, err)

	bDone := make(chan error, 1)
	go func() {
		wb, err := r.Writer(ctx)
		if err != nil {
			bDone <- err
			return
		}
		defer wb.Abort()
		recB, err := wb.Addresses().GetForUpdate(ctx, addrID) // блокируется до commit TX-A
		if err != nil {
			bDone <- err
			return
		}
		recB.Description = domain.RcDescription("descB")
		if _, err := wb.Addresses().Update(ctx, &recB.Address); err != nil {
			bDone <- err
			return
		}
		bDone <- wb.Commit()
	}()

	// Дождаться реального lock-contention (детерминированно вместо фиксированного сна).
	waitForLockWaiter(t, ctx, pool)

	recA.Name = domain.RcNameVPC("nameA")
	_, err = wa.Addresses().Update(ctx, &recA.Address)
	require.NoError(t, err)
	require.NoError(t, wa.Commit())

	require.NoError(t, <-bDone)

	rd, err := r.Reader(ctx)
	require.NoError(t, err)
	defer func() { _ = rd.Close() }()
	got, err := rd.Addresses().Get(ctx, addrID)
	require.NoError(t, err)
	require.Equal(t, domain.RcNameVPC("nameA"), got.Name,
		"name set by TX-A must persist (no lost-update)")
	require.Equal(t, domain.RcDescription("descB"), got.Description,
		"description set by TX-B must persist (no lost-update)")
}

func TestIntegration_NetworkInterface_ConcurrentDisjointUpdate_NoLostUpdate(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	dsn := setupTestDB(t)
	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()
	r := kachopg.New(pool, nil)
	defer r.Close()

	netID := ids.NewID(ids.PrefixNetwork)
	subID := ids.NewID(ids.PrefixSubnet)
	nicID := ids.NewID(ids.PrefixNetworkInterface)
	require.NoError(t, legacyWithTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		if _, e := w.Networks().Insert(ctx, &domain.Network{ID: netID, ProjectID: "f-nic", Name: domain.RcNameVPC("n-nic")}); e != nil {
			return e
		}
		if _, e := w.Subnets().Insert(ctx, &domain.Subnet{
			ID: subID, ProjectID: "f-nic", Name: domain.RcNameVPC("s-nic"),
			NetworkID: netID, PlacementType: domain.PlacementZonal, ZoneID: "zone-a", V4CidrBlocks: []string{"10.11.0.0/24"},
		}); e != nil {
			return e
		}
		_, e := w.NetworkInterfaces().Insert(ctx, &domain.NetworkInterface{
			ID: nicID, ProjectID: "f-nic", Name: domain.RcNameVPC("name0"),
			Description: domain.RcDescription("desc0"), SubnetID: subID,
			MAC: "0e:aa:bb:cc:dd:ee", Status: domain.NIStatusAvailable,
		})
		return e
	}))

	wa, err := r.Writer(ctx)
	require.NoError(t, err)
	recA, err := wa.NetworkInterfaces().GetForUpdate(ctx, nicID)
	require.NoError(t, err)

	bDone := make(chan error, 1)
	go func() {
		wb, err := r.Writer(ctx)
		if err != nil {
			bDone <- err
			return
		}
		defer wb.Abort()
		recB, err := wb.NetworkInterfaces().GetForUpdate(ctx, nicID) // блокируется до commit TX-A
		if err != nil {
			bDone <- err
			return
		}
		recB.Description = domain.RcDescription("descB")
		if _, err := wb.NetworkInterfaces().UpdateMeta(ctx, &recB.NetworkInterface); err != nil {
			bDone <- err
			return
		}
		bDone <- wb.Commit()
	}()

	// Дождаться реального lock-contention (детерминированно вместо фиксированного сна).
	waitForLockWaiter(t, ctx, pool)

	recA.Name = domain.RcNameVPC("nameA")
	_, err = wa.NetworkInterfaces().UpdateMeta(ctx, &recA.NetworkInterface)
	require.NoError(t, err)
	require.NoError(t, wa.Commit())

	require.NoError(t, <-bDone)

	rd, err := r.Reader(ctx)
	require.NoError(t, err)
	defer func() { _ = rd.Close() }()
	got, err := rd.NetworkInterfaces().Get(ctx, nicID)
	require.NoError(t, err)
	require.Equal(t, domain.RcNameVPC("nameA"), got.Name,
		"name set by TX-A must persist (no lost-update)")
	require.Equal(t, domain.RcDescription("descB"), got.Description,
		"description set by TX-B must persist (no lost-update)")
}

func TestIntegration_AddressPool_ConcurrentDisjointUpdate_NoLostUpdate(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	dsn := setupTestDB(t)
	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()
	r := kachopg.New(pool, nil)
	defer r.Close()

	poolID := ids.NewID("apl")
	require.NoError(t, legacyWithTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		_, e := w.AddressPools().Insert(ctx, &domain.AddressPool{
			ID: poolID, Name: domain.RcNameVPC("name0"),
			Description:  domain.RcDescription("desc0"),
			V4CIDRBlocks: []string{"203.0.113.0/28"},
			Kind:         domain.AddressPoolKindExternalPublic, ZoneID: "zone-c",
		})
		return e
	}))

	wa, err := r.Writer(ctx)
	require.NoError(t, err)
	recA, err := wa.AddressPools().GetForUpdate(ctx, poolID)
	require.NoError(t, err)

	bDone := make(chan error, 1)
	go func() {
		wb, err := r.Writer(ctx)
		if err != nil {
			bDone <- err
			return
		}
		defer wb.Abort()
		recB, err := wb.AddressPools().GetForUpdate(ctx, poolID) // блокируется до commit TX-A
		if err != nil {
			bDone <- err
			return
		}
		p := recB.AddressPool
		p.Description = domain.RcDescription("descB")
		if _, err := wb.AddressPools().Update(ctx, &p); err != nil {
			bDone <- err
			return
		}
		bDone <- wb.Commit()
	}()

	// Дождаться реального lock-contention (детерминированно вместо фиксированного сна).
	waitForLockWaiter(t, ctx, pool)

	pa := recA.AddressPool
	pa.Name = domain.RcNameVPC("nameA")
	_, err = wa.AddressPools().Update(ctx, &pa)
	require.NoError(t, err)
	require.NoError(t, wa.Commit())

	require.NoError(t, <-bDone)

	rd, err := r.Reader(ctx)
	require.NoError(t, err)
	defer func() { _ = rd.Close() }()
	got, err := rd.AddressPools().Get(ctx, poolID)
	require.NoError(t, err)
	require.Equal(t, domain.RcNameVPC("nameA"), got.Name,
		"name set by TX-A must persist (no lost-update)")
	require.Equal(t, domain.RcDescription("descB"), got.Description,
		"description set by TX-B must persist (no lost-update)")
}
