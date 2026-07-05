// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package repo_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coredb "github.com/PRO-Robotech/kacho-corelib/db"
	"github.com/PRO-Robotech/kacho-corelib/ids"
	addressapp "github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/api/address"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/api/addresspool"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/cqrsadapter"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
	kachopg "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/pg"
)

// TestIntegration_IPAM_Cascade поднимает реальный pgxpool + CQRS-repo и
// прогоняет резолв пула AddressPool end-to-end. Cascade: network_default →
// zone_default → global_default.
func TestIntegration_IPAM_Cascade(t *testing.T) {
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

	const zone = "zone-a"

	mkPool := func(name, zoneID string, isDefault bool, cidr string) *domain.AddressPool {
		p := &domain.AddressPool{
			ID:           ids.NewID("apl"),
			Name:         domain.RcNameVPC(name),
			V4CIDRBlocks: []string{cidr},
			Kind:         domain.AddressPoolKindExternalPublic,
			ZoneID:       zoneID,
			IsDefault:    isDefault,
		}
		require.NoError(t, withTx(t, func(w kacho.RepositoryWriter) error {
			_, e := w.AddressPools().Insert(ctx, p)
			return e
		}))
		return p
	}

	globalPool := mkPool("global-default", "", true, "198.18.0.0/24")
	zonePool := mkPool("zone-default", zone, true, "198.18.1.0/24")
	networkBindingPool := mkPool("network-bound", zone, false, "198.18.3.0/24")

	for _, p := range []*domain.AddressPool{globalPool, zonePool, networkBindingPool} {
		pID := p.ID
		require.NoError(t, withTx(t, func(w kacho.RepositoryWriter) error {
			return w.AddressPools().PopulateFreelistForPool(ctx, pID)
		}))
	}

	net := &domain.Network{ID: ids.NewID(ids.PrefixNetwork), ProjectID: "project-netdef", Name: domain.RcNameVPC("net-netdef")}
	require.NoError(t, withTx(t, func(w kacho.RepositoryWriter) error {
		_, e := w.Networks().Insert(ctx, net)
		return e
	}))
	sub := &domain.Subnet{
		ID: ids.NewID(ids.PrefixSubnet), ProjectID: "project-netdef",
		Name: domain.RcNameVPC("sub-netdef"), NetworkID: net.ID, PlacementType: domain.PlacementZonal, ZoneID: zone, V4CidrBlocks: []string{"10.10.0.0/24"},
	}
	require.NoError(t, withTx(t, func(w kacho.RepositoryWriter) error {
		_, e := w.Subnets().Insert(ctx, sub)
		return e
	}))
	require.NoError(t, withTx(t, func(w kacho.RepositoryWriter) error {
		return w.AddressPoolBindings().SetNetworkDefault(ctx, net.ID, networkBindingPool.ID)
	}))

	// ResolverService + AllocateUseCase принимают `kacho.Repository` напрямую.
	subnetAdapter := cqrsadapter.NewSubnet(r)
	addrAdapter := cqrsadapter.NewAddress(r)
	apResolver := addresspool.NewResolverService(r, addrAdapter, subnetAdapter)
	addrSvc := addressapp.NewAllocateUseCase(r, subnetAdapter, apResolver)

	mkAddr := func(projectID, name string, typ domain.AddressType, ext *domain.ExternalIpv4Spec, intSpec *domain.InternalIpv4Spec) *domain.Address {
		return &domain.Address{
			ID: ids.NewID(ids.PrefixAddress), ProjectID: projectID, Name: domain.RcNameVPC(name),
			Type: typ, IpVersion: domain.IpVersionIPv4, ExternalIpv4: ext, InternalIpv4: intSpec,
		}
	}

	insertAddr := func(a *domain.Address) {
		require.NoError(t, withTx(t, func(w kacho.RepositoryWriter) error {
			_, e := w.Addresses().Insert(ctx, a)
			return e
		}))
	}

	// network_default — internal address в subnet'е этой сети.
	aNetDef := mkAddr("project-netdef", "a-netdef", domain.AddressTypeInternal, nil, &domain.InternalIpv4Spec{SubnetID: sub.ID})
	insertAddr(aNetDef)

	aZone := mkAddr("project-zone", "a-zone", domain.AddressTypeExternal, &domain.ExternalIpv4Spec{ZoneID: zone}, nil)
	insertAddr(aZone)

	aGlobal := mkAddr("project-global", "a-global", domain.AddressTypeExternal, &domain.ExternalIpv4Spec{ZoneID: ""}, nil)
	insertAddr(aGlobal)

	// --- resolve: ResolvePoolForAddressObjFamily выбирает ожидаемый pool и MatchedVia ---
	resolveCase := func(t *testing.T, addressID, wantPoolID, wantVia string) {
		t.Helper()
		rd, err := r.Reader(ctx)
		require.NoError(t, err)
		rec, err := rd.Addresses().Get(ctx, addressID)
		_ = rd.Close()
		require.NoError(t, err)
		res, rerr := apResolver.ResolvePoolForAddressObjFamily(ctx, rec, addresspool.FamilyV4)
		require.NoError(t, rerr)
		require.NotNil(t, res)
		assert.Equal(t, wantPoolID, res.Pool.ID, "wrong pool resolved")
		assert.Equal(t, wantVia, res.MatchedVia, "wrong cascade step matched")
	}
	t.Run("network_default", func(t *testing.T) { resolveCase(t, aNetDef.ID, networkBindingPool.ID, "network_default") })
	t.Run("zone_default", func(t *testing.T) { resolveCase(t, aZone.ID, zonePool.ID, "zone_default") })
	t.Run("global_default", func(t *testing.T) { resolveCase(t, aGlobal.ID, globalPool.ID, "global_default") })

	// --- allocate (external addresses) ---
	for _, tc := range []struct {
		name       string
		addressID  string
		wantPoolID string
	}{
		{"allocate_zone", aZone.ID, zonePool.ID},
		{"allocate_global", aGlobal.ID, globalPool.ID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, aerr := addrSvc.AllocateExternalIP(ctx, tc.addressID)
			require.NoError(t, aerr)
			require.NotNil(t, res)
			assert.NotEmpty(t, res.IP, "an IP must be allocated")
			assert.Equal(t, tc.wantPoolID, res.PoolID, "IP must come from the cascade-resolved pool")
			res2, aerr2 := addrSvc.AllocateExternalIP(ctx, tc.addressID)
			require.NoError(t, aerr2)
			assert.Equal(t, res.IP, res2.IP)
			assert.True(t, res2.AlreadyAllocated)
		})
	}
}
