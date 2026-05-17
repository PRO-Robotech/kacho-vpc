package repo_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	coredb "github.com/PRO-Robotech/kacho-corelib/db"
	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/helpers"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
	kachopg "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/pg"
)

// KAC-55 — DB-level CHECK инвариант: network_interfaces.v4_address_ids /
// v6_address_ids — массивы длины ≤ 1. Service-слой даёт sync InvalidArgument
// до Operation, эта миграция (0018) — финальный backstop на случай direct
// repo.Insert / direct SQL / race с bypassed-валидатором.
//
// KAC-94 A.7 sub-PR 5/6: переписан на CQRS Writer.
func TestIntegration_NICRepo_AddressCardinality_DBCheck(t *testing.T) {
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

	net := &domain.Network{
		ID: ids.NewID(ids.PrefixNetwork), ProjectID: "folder-card", Name: domain.RcNameVPC("net-card"),
	}
	require.NoError(t, withTx(t, func(w kacho.RepositoryWriter) error {
		_, e := w.Networks().Insert(ctx, net)
		return e
	}))
	sub := &domain.Subnet{
		ID: ids.NewID(ids.PrefixSubnet), ProjectID: "folder-card", Name: domain.RcNameVPC("sub-card"), NetworkID: net.ID, ZoneID: "ru-central1-a",
		V4CidrBlocks: []string{"10.40.0.0/24"},
	}
	require.NoError(t, withTx(t, func(w kacho.RepositoryWriter) error {
		_, e := w.Subnets().Insert(ctx, sub)
		return e
	}))

	mkNIC := func(suffix string, v4 []string, v6 []string) *domain.NetworkInterface {
		return &domain.NetworkInterface{
			ID: ids.NewID(ids.PrefixSubnet), ProjectID: "folder-card",
			Name: domain.RcNameVPC("nic-" + suffix), SubnetID: sub.ID, MAC: "0e:55:55:55:55:" + suffix,
			Status:       domain.NIStatusAvailable,
			V4AddressIDs: v4,
			V6AddressIDs: v6,
		}
	}

	insertNIC := func(t *testing.T, nic *domain.NetworkInterface) error {
		t.Helper()
		return withTx(t, func(w kacho.RepositoryWriter) error {
			_, e := w.NetworkInterfaces().Insert(ctx, nic)
			return e
		})
	}

	// 1. v4=[] / v6=[] — OK.
	require.NoError(t, insertNIC(t, mkNIC("01", nil, nil)), "пустые arrays разрешены")

	// 2. v4=[1] / v6=[] — OK (граница).
	require.NoError(t, insertNIC(t, mkNIC("02", []string{"e9bv4single"}, nil)), "ровно один v4 разрешён")

	// 3. v4=[] / v6=[1] — OK.
	require.NoError(t, insertNIC(t, mkNIC("03", nil, []string{"e9bv6single"})), "ровно один v6 разрешён")

	// 4. v4=[1] / v6=[1] — OK (по одному на тип).
	require.NoError(t, insertNIC(t, mkNIC("04", []string{"e9bv4dual"}, []string{"e9bv6dual"})), "по одному v4 + v6 разрешено")

	// 5. v4=[2] — DB-level CHECK; helpers.WrapPgErr маппит 23514 в helpers.ErrInvalidArg.
	err = insertNIC(t, mkNIC("05", []string{"e9bv4a", "e9bv4b"}, nil))
	require.Error(t, err, "два v4 на одном NIC должны быть отклонены DB-level CHECK")
	require.Truef(t, errors.Is(err, helpers.ErrInvalidArg),
		"expected helpers.ErrInvalidArg from CHECK violation, got: %v", err)

	// 6. v6=[2] — аналогично.
	err = insertNIC(t, mkNIC("06", nil, []string{"e9bv6a", "e9bv6b"}))
	require.Error(t, err, "два v6 на одном NIC должны быть отклонены DB-level CHECK")
	require.Truef(t, errors.Is(err, helpers.ErrInvalidArg),
		"expected helpers.ErrInvalidArg from CHECK violation, got: %v", err)
}
