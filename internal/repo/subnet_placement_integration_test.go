// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package repo_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coredb "github.com/PRO-Robotech/kacho-corelib/db"
	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/helpers"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
	kachopg "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/pg"
)

// TestIntegration_Subnet_Placement_Regional_OK — REGIONAL-подсеть из «серого»
// CIDR коммитится; placement_type='REGIONAL', region_id задан, zone_id пуст.
// Подтверждает прохождение CHECK subnets_placement_payload_chk на корректной паре.
func TestIntegration_Subnet_Placement_Regional_OK(t *testing.T) {
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
	require.NoError(t, legacyWithTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		if _, e := w.Networks().Insert(ctx, &domain.Network{ID: netID, ProjectID: "f-reg", Name: domain.RcNameVPC("n-reg")}); e != nil {
			return e
		}
		_, e := w.Subnets().Insert(ctx, &domain.Subnet{
			ID: subID, ProjectID: "f-reg", Name: domain.RcNameVPC("s-reg"),
			NetworkID: netID, PlacementType: domain.PlacementRegional, RegionID: "region-1",
			V4CidrBlocks: []string{"192.168.0.0/24"},
		})
		return e
	}))

	rd, err := r.Reader(ctx)
	require.NoError(t, err)
	defer func() { _ = rd.Close() }()
	got, err := rd.Subnets().Get(ctx, subID)
	require.NoError(t, err)
	assert.Equal(t, domain.PlacementRegional, got.PlacementType)
	assert.Equal(t, "region-1", got.RegionID)
	assert.Empty(t, got.ZoneID)
}

// TestIntegration_Subnet_Placement_CheckRejectsInconsistent — DB-CHECK
// (subnets_placement_payload_chk) отвергает несогласованную пару: ZONAL с
// непустым region_id. Repo маппит 23514 → ErrInvalidArg (raw PG не утекает).
func TestIntegration_Subnet_Placement_CheckRejectsInconsistent(t *testing.T) {
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
	require.NoError(t, legacyWithTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		_, e := w.Networks().Insert(ctx, &domain.Network{ID: netID, ProjectID: "f-chk", Name: domain.RcNameVPC("n-chk")})
		return e
	}))

	err = legacyWithTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		// ZONAL, но с заданным region_id — нарушает биусловный CHECK.
		_, e := w.Subnets().Insert(ctx, &domain.Subnet{
			ID: ids.NewID(ids.PrefixSubnet), ProjectID: "f-chk", Name: domain.RcNameVPC("s-bad"),
			NetworkID: netID, PlacementType: domain.PlacementZonal, ZoneID: "zone-a", RegionID: "region-1",
			V4CidrBlocks: []string{"10.0.0.0/24"},
		})
		return e
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, helpers.ErrInvalidArg), "CHECK violation must map to ErrInvalidArg, got %v", err)
}

// TestIntegration_Subnet_Placement_ListFilterByPlacement — server-side
// `filter=placement_type="REGIONAL"` возвращает только региональные подсети
// (whitelist фильтра включает placement_type; SQL-предикат параметризован).
func TestIntegration_Subnet_Placement_ListFilterByPlacement(t *testing.T) {
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
	require.NoError(t, legacyWithTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		if _, e := w.Networks().Insert(ctx, &domain.Network{ID: netID, ProjectID: "f-flt", Name: domain.RcNameVPC("n-flt")}); e != nil {
			return e
		}
		if _, e := w.Subnets().Insert(ctx, &domain.Subnet{
			ID: ids.NewID(ids.PrefixSubnet), ProjectID: "f-flt", Name: domain.RcNameVPC("s-zonal"),
			NetworkID: netID, PlacementType: domain.PlacementZonal, ZoneID: "zone-a",
			V4CidrBlocks: []string{"10.0.0.0/24"},
		}); e != nil {
			return e
		}
		_, e := w.Subnets().Insert(ctx, &domain.Subnet{
			ID: ids.NewID(ids.PrefixSubnet), ProjectID: "f-flt", Name: domain.RcNameVPC("s-regional"),
			NetworkID: netID, PlacementType: domain.PlacementRegional, RegionID: "region-1",
			V4CidrBlocks: []string{"192.168.0.0/24"},
		})
		return e
	}))

	rd, err := r.Reader(ctx)
	require.NoError(t, err)
	defer func() { _ = rd.Close() }()

	regional, _, err := rd.Subnets().List(ctx,
		kacho.SubnetFilter{ProjectID: "f-flt", Filter: `placement_type="REGIONAL"`},
		kacho.Pagination{})
	require.NoError(t, err)
	require.Len(t, regional, 1, "filter placement_type=REGIONAL must return exactly the regional subnet")
	assert.Equal(t, domain.PlacementRegional, regional[0].PlacementType)

	zonal, _, err := rd.Subnets().List(ctx,
		kacho.SubnetFilter{ProjectID: "f-flt", Filter: `placement_type="ZONAL"`},
		kacho.Pagination{})
	require.NoError(t, err)
	require.Len(t, zonal, 1)
	assert.Equal(t, domain.PlacementZonal, zonal[0].PlacementType)
}

// TestIntegration_Subnet_Placement_RegionalOverlapsZonal_PerNetworkExclude —
// REGIONAL-подсеть с CIDR, пересекающимся с существующей ZONAL-подсетью той же
// сети, отвергается per-network EXCLUDE (subnet_cidr_blocks). Подтверждает: единый
// non-overlap инвариант покрывает обе разновидности размещения (отдельный
// conditional EXCLUDE не нужен).
func TestIntegration_Subnet_Placement_RegionalOverlapsZonal_PerNetworkExclude(t *testing.T) {
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
	require.NoError(t, legacyWithTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		if _, e := w.Networks().Insert(ctx, &domain.Network{ID: netID, ProjectID: "f-ovl", Name: domain.RcNameVPC("n-ovl")}); e != nil {
			return e
		}
		// ZONAL 10.0.0.0/24.
		_, e := w.Subnets().Insert(ctx, &domain.Subnet{
			ID: ids.NewID(ids.PrefixSubnet), ProjectID: "f-ovl", Name: domain.RcNameVPC("s-zonal"),
			NetworkID: netID, PlacementType: domain.PlacementZonal, ZoneID: "zone-a",
			V4CidrBlocks: []string{"10.0.0.0/24"},
		})
		return e
	}))

	// REGIONAL 10.0.0.0/25 — пересекается с зональной 10.0.0.0/24 в той же сети.
	err = legacyWithTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		_, e := w.Subnets().Insert(ctx, &domain.Subnet{
			ID: ids.NewID(ids.PrefixSubnet), ProjectID: "f-ovl", Name: domain.RcNameVPC("s-regional"),
			NetworkID: netID, PlacementType: domain.PlacementRegional, RegionID: "region-1",
			V4CidrBlocks: []string{"10.0.0.0/25"},
		})
		return e
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, helpers.ErrFailedPrecondition), "overlap must map to ErrFailedPrecondition, got %v", err)
}
