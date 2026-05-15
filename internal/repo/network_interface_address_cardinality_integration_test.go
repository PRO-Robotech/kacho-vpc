package repo_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	coredb "github.com/PRO-Robotech/kacho-corelib/db"
	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	"github.com/PRO-Robotech/kacho-vpc/internal/service"
)

// KAC-55 — DB-level CHECK инвариант: network_interfaces.v4_address_ids /
// v6_address_ids — массивы длины ≤ 1. Service-слой даёт sync InvalidArgument
// до Operation, эта миграция (0018) — финальный backstop на случай direct
// repo.Insert / direct SQL / race с bypassed-валидатором.
//
// Тест проверяет, что DB-side CHECK реально срабатывает. Идём в обход service
// — напрямую через repo.Insert с длинным массивом — должны получить SQLSTATE
// 23514 (CHECK violation).
func TestIntegration_NICRepo_AddressCardinality_DBCheck(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx := context.Background()
	dsn := setupTestDB(t)
	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	netRepo := repo.NewNetworkRepo(pool)
	subnetRepo := repo.NewSubnetRepo(pool)
	nicRepo := repo.NewNetworkInterfaceRepo(pool)

	now := time.Now().UTC().Truncate(time.Microsecond)
	net := &domain.Network{
		ID: ids.NewID(ids.PrefixNetwork), FolderID: "folder-card", Name: domain.RcNameVPC("net-card"),
	}
	_, err = netRepo.Insert(ctx, net)
	require.NoError(t, err)
	sub := &domain.Subnet{
		ID: ids.NewID(ids.PrefixSubnet), FolderID: "folder-card", CreatedAt: now,
		Name: "sub-card", NetworkID: net.ID, ZoneID: "ru-central1-a",
		V4CidrBlocks: []string{"10.40.0.0/24"},
	}
	_, err = subnetRepo.Insert(ctx, sub)
	require.NoError(t, err)

	mkNIC := func(suffix string, v4 []string, v6 []string) *domain.NetworkInterface {
		return &domain.NetworkInterface{
			ID: ids.NewID(ids.PrefixSubnet), FolderID: "folder-card", CreatedAt: now,
			Name: "nic-" + suffix, SubnetID: sub.ID, MAC: "0e:55:55:55:55:" + suffix,
			Status:       domain.NIStatusAvailable,
			V4AddressIDs: v4,
			V6AddressIDs: v6,
		}
	}

	// 1. v4=[] / v6=[] — OK.
	_, err = nicRepo.Insert(ctx, mkNIC("01", nil, nil))
	require.NoError(t, err, "пустые arrays разрешены")

	// 2. v4=[1] / v6=[] — OK (граница).
	_, err = nicRepo.Insert(ctx, mkNIC("02", []string{"e9bv4single"}, nil))
	require.NoError(t, err, "ровно один v4 разрешён")

	// 3. v4=[] / v6=[1] — OK.
	_, err = nicRepo.Insert(ctx, mkNIC("03", nil, []string{"e9bv6single"}))
	require.NoError(t, err, "ровно один v6 разрешён")

	// 4. v4=[1] / v6=[1] — OK (по одному на тип).
	_, err = nicRepo.Insert(ctx, mkNIC("04", []string{"e9bv4dual"}, []string{"e9bv6dual"}))
	require.NoError(t, err, "по одному v4 + v6 разрешено")

	// 5. v4=[2] — должен сработать DB-level CHECK; wrapPgErr маппит 23514 в ErrInvalidArg.
	_, err = nicRepo.Insert(ctx, mkNIC("05", []string{"e9bv4a", "e9bv4b"}, nil))
	require.Error(t, err, "два v4 на одном NIC должны быть отклонены DB-level CHECK")
	require.Truef(t, errors.Is(err, service.ErrInvalidArg),
		"expected ErrInvalidArg from CHECK violation, got: %v", err)

	// 6. v6=[2] — аналогично.
	_, err = nicRepo.Insert(ctx, mkNIC("06", nil, []string{"e9bv6a", "e9bv6b"}))
	require.Error(t, err, "два v6 на одном NIC должны быть отклонены DB-level CHECK")
	require.Truef(t, errors.Is(err, service.ErrInvalidArg),
		"expected ErrInvalidArg from CHECK violation, got: %v", err)
}
