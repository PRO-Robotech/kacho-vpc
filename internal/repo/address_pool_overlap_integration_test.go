// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package repo_test

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	coredb "github.com/PRO-Robotech/kacho-corelib/db"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/api/addresspool"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	kachopg "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/pg"
)

// DB-level защита от пересечения CIDR в AddressPool (нормализованная child-таблица
// address_pool_cidrs + EXCLUDE gist). Within-service инвариант: два пула с
// пересекающимися CIDR одного kind не должны сосуществовать (иначе IPAM аллоцирует
// один external-IP дважды — разные pool_id обходят per-pool UNIQUE
// addresses_external_pool_ip_uniq).
//
// Покрывает:
//   (a) OverlapAcrossPools     — pool B пересекает pool A → FailedPrecondition;
//                                disjoint pool C → OK.
//   (b) AddCidrOverlapExisting — :addCidrBlocks пересекающий чужой pool → FailedPrecondition.
//   (c) ConcurrentOverlap      — 2 goroutine вставляют пересекающиеся пулы → ровно один успех.
//   (d) RemoveFreesBlock       — после remove CIDR освобождается для нового пула.

func TestIntegration_AddressPoolOverlap_AcrossPools(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	pgPool, err := coredb.NewPool(ctx, setupTestDB(t))
	require.NoError(t, err)
	defer pgPool.Close()
	r := kachopg.New(pgPool, nil)
	defer r.Close()

	createUC := addresspool.NewCreateAddressPoolUseCase(r, nil)

	// pool A {10.0.0.0/24} — OK.
	_, err = createUC.Execute(ctx, addresspool.CreatePoolReq{
		Name: "pool-a-" + t.Name(), Kind: domain.AddressPoolKindExternalPublic,
		ZoneID: "zone-a", V4CIDRBlocks: []string{"10.0.0.0/24"},
	})
	require.NoError(t, err)

	// pool B {10.0.0.128/25} — пересекается с A → FailedPrecondition.
	_, err = createUC.Execute(ctx, addresspool.CreatePoolReq{
		Name: "pool-b-" + t.Name(), Kind: domain.AddressPoolKindExternalPublic,
		ZoneID: "zone-a", V4CIDRBlocks: []string{"10.0.0.128/25"},
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok, "expected gRPC status error, got %v", err)
	assert.Equal(t, codes.FailedPrecondition, st.Code(),
		"overlapping pool create → FailedPrecondition")
	assert.Contains(t, st.Message(), "can not overlap")

	// disjoint pool C {10.1.0.0/24} — OK.
	_, err = createUC.Execute(ctx, addresspool.CreatePoolReq{
		Name: "pool-c-" + t.Name(), Kind: domain.AddressPoolKindExternalPublic,
		ZoneID: "zone-a", V4CIDRBlocks: []string{"10.1.0.0/24"},
	})
	require.NoError(t, err, "disjoint CIDR must succeed")
}

func TestIntegration_AddressPoolOverlap_AddCidrOverlapExisting(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	pgPool, err := coredb.NewPool(ctx, setupTestDB(t))
	require.NoError(t, err)
	defer pgPool.Close()
	r := kachopg.New(pgPool, nil)
	defer r.Close()

	createUC := addresspool.NewCreateAddressPoolUseCase(r, nil)

	_, err = createUC.Execute(ctx, addresspool.CreatePoolReq{
		Name: "pool-a-addcidr-overlap", Kind: domain.AddressPoolKindExternalPublic,
		ZoneID: "zone-a", V4CIDRBlocks: []string{"10.0.0.0/24"},
	})
	require.NoError(t, err)

	poolB, err := createUC.Execute(ctx, addresspool.CreatePoolReq{
		Name: "pool-b-addcidr-overlap", Kind: domain.AddressPoolKindExternalPublic,
		ZoneID: "zone-a", V4CIDRBlocks: []string{"10.1.0.0/24"},
	})
	require.NoError(t, err)

	// AddCidr B {10.0.0.0/25} — пересекается с pool A → FailedPrecondition.
	addUC := addresspool.NewAddCidrBlocksUseCase(r)
	_, err = addUC.Execute(ctx, poolB.ID, []string{"10.0.0.0/25"}, nil)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok, "expected gRPC status error, got %v", err)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
	assert.Contains(t, st.Message(), "can not overlap")
}

func TestIntegration_AddressPoolOverlap_ConcurrentOverlap(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	pgPool, err := coredb.NewPool(ctx, setupTestDB(t))
	require.NoError(t, err)
	defer pgPool.Close()
	r := kachopg.New(pgPool, nil)
	defer r.Close()

	createUC := addresspool.NewCreateAddressPoolUseCase(r, nil)

	// 2 goroutine'ы вставляют пулы с пересекающимся CIDR одновременно.
	// EXCLUDE gist race-free by construction → ровно один успех.
	var (
		wg   sync.WaitGroup
		errs [2]error
	)
	wg.Add(2)
	for i := 0; i < 2; i++ {
		go func(idx int) {
			defer wg.Done()
			_, errs[idx] = createUC.Execute(ctx, addresspool.CreatePoolReq{
				Name: "pool-conc-" + t.Name(), Kind: domain.AddressPoolKindExternalPublic,
				ZoneID: "zone-a", V4CIDRBlocks: []string{"10.0.0.0/24"},
			})
		}(i)
	}
	wg.Wait()

	okCount := 0
	for _, e := range errs {
		if e == nil {
			okCount++
		} else {
			st, ok := status.FromError(e)
			require.True(t, ok, "expected gRPC status error, got %v", e)
			assert.Equal(t, codes.FailedPrecondition, st.Code(),
				"loser must get FailedPrecondition")
		}
	}
	assert.Equal(t, 1, okCount, "exactly one concurrent overlapping insert must win")
}

func TestIntegration_AddressPoolOverlap_RemoveFreesBlock(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	pgPool, err := coredb.NewPool(ctx, setupTestDB(t))
	require.NoError(t, err)
	defer pgPool.Close()
	r := kachopg.New(pgPool, nil)
	defer r.Close()

	createUC := addresspool.NewCreateAddressPoolUseCase(r, nil)

	// pool A {10.0.0.0/24, 10.2.0.0/24}.
	poolA, err := createUC.Execute(ctx, addresspool.CreatePoolReq{
		Name: "pool-a-" + t.Name(), Kind: domain.AddressPoolKindExternalPublic,
		ZoneID: "zone-a", V4CIDRBlocks: []string{"10.0.0.0/24", "10.2.0.0/24"},
	})
	require.NoError(t, err)

	// Перед remove новый пул с 10.0.0.0/24 пересекается → FailedPrecondition.
	_, err = createUC.Execute(ctx, addresspool.CreatePoolReq{
		Name: "pool-pre-" + t.Name(), Kind: domain.AddressPoolKindExternalPublic,
		ZoneID: "zone-a", V4CIDRBlocks: []string{"10.0.0.0/24"},
	})
	require.Error(t, err, "pre-remove overlap must be rejected")

	// RemoveCidr A {10.0.0.0/24} → блок освобожден.
	rmUC := addresspool.NewRemoveCidrBlocksUseCase(r)
	_, err = rmUC.Execute(ctx, poolA.ID, []string{"10.0.0.0/24"}, nil)
	require.NoError(t, err)

	// Теперь pool B {10.0.0.0/24} — OK (block освобожден removal'ом).
	_, err = createUC.Execute(ctx, addresspool.CreatePoolReq{
		Name: "pool-b-" + t.Name(), Kind: domain.AddressPoolKindExternalPublic,
		ZoneID: "zone-a", V4CIDRBlocks: []string{"10.0.0.0/24"},
	})
	require.NoError(t, err, "removed CIDR must become reusable")
}
