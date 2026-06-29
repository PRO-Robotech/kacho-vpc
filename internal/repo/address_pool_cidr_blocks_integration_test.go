// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package repo_test

import (
	"context"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	coredb "github.com/PRO-Robotech/kacho-corelib/db"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/api/addresspool"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
	kachopg "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/pg"
)

// Integration-тесты AddressPool :addCidrBlocks / :removeCidrBlocks против
// реального Postgres (testcontainers). Покрывают:
//   (a) AddCidrBlocks → freelist пополнен для новой v4-дельты;
//   (b) RemoveCidrBlocks с выделенным external-IP → FailedPrecondition;
//   (c) RemoveCidrBlocks чистого CIDR → free_ips удалены;
//   (d) concurrent alloc-vs-remove → ровно один исход (alloc или remove).

func countFreeIPs(t *testing.T, ctx context.Context, pool *pgxpool.Pool, poolID string) int {
	t.Helper()
	var n int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM address_pool_free_ips WHERE pool_id = $1`, poolID).Scan(&n))
	return n
}

func mkCidrPool(t *testing.T, ctx context.Context, r kacho.Repository, name string, v4 []string) *kacho.AddressPoolRecord {
	t.Helper()
	// Через use-case: Insert + PopulateFreelistForPool + outbox в одной TX.
	uc := addresspool.NewCreateAddressPoolUseCase(r, nil) // nil zoneReg → skip zone-check
	p, err := uc.Execute(ctx, addresspool.CreatePoolReq{
		Name:         name,
		Kind:         domain.AddressPoolKindExternalPublic,
		ZoneID:       "zone-a",
		V4CIDRBlocks: v4,
	})
	require.NoError(t, err)
	return p
}

func TestIntegration_AddressPoolCIDR_AddCidrBlocks_PopulatesFreelist(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	pgPool, err := coredb.NewPool(ctx, setupTestDB(t))
	require.NoError(t, err)
	defer pgPool.Close()
	r := kachopg.New(pgPool, nil)
	defer r.Close()

	p := mkCidrPool(t, ctx, r, t.Name(), []string{"198.51.100.0/28"})
	require.Equal(t, 14, countFreeIPs(t, ctx, pgPool, p.ID), "create /28 → 14 free")

	addUC := addresspool.NewAddCidrBlocksUseCase(r)
	updated, err := addUC.Execute(ctx, p.ID, []string{"203.0.113.0/28"}, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"198.51.100.0/28", "203.0.113.0/28"}, updated.V4CIDRBlocks)

	// Freelist пополнен дельтой (14 старых + 14 новых = 28).
	require.Equal(t, 28, countFreeIPs(t, ctx, pgPool, p.ID),
		"add /28 → +14 free (delta materialised, no dup)")

	// Идемпотентный re-add того же блока — состав и freelist не меняются.
	again, err := addUC.Execute(ctx, p.ID, []string{"203.0.113.0/28"}, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"198.51.100.0/28", "203.0.113.0/28"}, again.V4CIDRBlocks)
	require.Equal(t, 28, countFreeIPs(t, ctx, pgPool, p.ID), "re-add → no change")
}

func TestIntegration_AddressPoolCIDR_RemoveInUse_FailedPrecondition(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	pgPool, err := coredb.NewPool(ctx, setupTestDB(t))
	require.NoError(t, err)
	defer pgPool.Close()
	r := kachopg.New(pgPool, nil)
	defer r.Close()

	p := mkCidrPool(t, ctx, r, t.Name(), []string{"198.51.100.0/28", "203.0.113.0/28"})

	// Аллоцируем external-IP из CIDR 198.51.100.0/28.
	addrID := insertTestAddressFreelist(t, ctx, pgPool)
	var allocIP string
	require.NoError(t, func() error {
		w, e := r.Writer(ctx)
		require.NoError(t, e)
		allocIP, e = w.Addresses().AllocateIPFromFreelist(ctx, p.ID, addrID)
		if e != nil {
			w.Abort()
			return e
		}
		return w.Commit()
	}())
	require.NotEmpty(t, allocIP)

	// RemoveCidrBlocks на CIDR с выделенным IP → FailedPrecondition.
	rmUC := addresspool.NewRemoveCidrBlocksUseCase(r)
	_, err = rmUC.Execute(ctx, p.ID, []string{"198.51.100.0/28"}, nil)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
	assert.Contains(t, st.Message(), "has allocated addresses")

	// TX abort: pool НЕ изменился, free_ips удаленного CIDR НЕ затронуты.
	rd, err := r.Reader(ctx)
	require.NoError(t, err)
	rec, err := rd.AddressPools().Get(ctx, p.ID)
	require.NoError(t, err)
	_ = rd.Close()
	assert.ElementsMatch(t, []string{"198.51.100.0/28", "203.0.113.0/28"}, rec.V4CIDRBlocks)
	// 14+14 - 1 allocated = 27 free; remove aborted → still 27.
	require.Equal(t, 27, countFreeIPs(t, ctx, pgPool, p.ID))
}

func TestIntegration_AddressPoolCIDR_RemoveClean_DeletesFreeIPs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	pgPool, err := coredb.NewPool(ctx, setupTestDB(t))
	require.NoError(t, err)
	defer pgPool.Close()
	r := kachopg.New(pgPool, nil)
	defer r.Close()

	p := mkCidrPool(t, ctx, r, t.Name(), []string{"198.51.100.0/28", "203.0.113.0/28"})
	require.Equal(t, 28, countFreeIPs(t, ctx, pgPool, p.ID))

	rmUC := addresspool.NewRemoveCidrBlocksUseCase(r)
	updated, err := rmUC.Execute(ctx, p.ID, []string{"203.0.113.0/28"}, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"198.51.100.0/28"}, updated.V4CIDRBlocks)

	// free_ips удаленного CIDR убраны — остались только 14 IP первого CIDR.
	require.Equal(t, 14, countFreeIPs(t, ctx, pgPool, p.ID))
	// Все оставшиеся free_ips принадлежат 198.51.100.0/28.
	var inOther int
	require.NoError(t, pgPool.QueryRow(ctx,
		`SELECT count(*) FROM address_pool_free_ips WHERE pool_id=$1 AND ip <<= '203.0.113.0/28'::cidr`,
		p.ID).Scan(&inOther))
	require.Equal(t, 0, inOther, "no free_ips left in removed CIDR")
}

// (d) concurrent alloc-vs-remove: один из них выигрывает. Либо remove проходит
// (тогда alloc уже исчерпан или попал в FailedPrecondition), либо alloc успел
// (тогда remove получает FailedPrecondition — use-guard). Никогда не должно
// получиться: remove успешен И существует выделенный IP в удаленном CIDR.
func TestIntegration_AddressPoolCIDR_ConcurrentAllocVsRemove(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	pgPool, err := coredb.NewPool(ctx, setupTestDB(t))
	require.NoError(t, err)
	defer pgPool.Close()
	r := kachopg.New(pgPool, nil)
	defer r.Close()

	// Один CIDR + второй, чтобы remove не опустошал пул.
	p := mkCidrPool(t, ctx, r, t.Name(), []string{"198.51.100.0/30", "203.0.113.0/28"})
	addrID := insertTestAddressFreelist(t, ctx, pgPool)

	rmUC := addresspool.NewRemoveCidrBlocksUseCase(r)

	var (
		wg                  sync.WaitGroup
		allocErr, removeErr error
		allocIP             string
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		w, e := r.Writer(ctx)
		if e != nil {
			allocErr = e
			return
		}
		ip, ae := w.Addresses().AllocateIPFromFreelist(ctx, p.ID, addrID)
		if ae != nil {
			w.Abort()
			allocErr = ae
			return
		}
		allocIP = ip
		allocErr = w.Commit()
	}()
	go func() {
		defer wg.Done()
		_, removeErr = rmUC.Execute(ctx, p.ID, []string{"198.51.100.0/30"}, nil)
	}()
	wg.Wait()

	// Финальная инвариант-проверка: НЕ может быть «remove успешен И есть
	// выделенный external-IP внутри удаленного CIDR».
	rd, err := r.Reader(ctx)
	require.NoError(t, err)
	rec, err := rd.AddressPools().Get(ctx, p.ID)
	require.NoError(t, err)
	_ = rd.Close()

	removeSucceeded := removeErr == nil
	allocSucceeded := allocErr == nil && allocIP != ""

	if removeSucceeded {
		// CIDR удален из пула.
		assert.NotContains(t, rec.V4CIDRBlocks, "198.51.100.0/30")
		// Не должно остаться выделенного IP в удаленном CIDR.
		var allocatedInRemoved int
		require.NoError(t, pgPool.QueryRow(ctx, `
			SELECT count(*) FROM addresses
			WHERE external_ipv4 ->> 'address_pool_id' = $1
			  AND coalesce(external_ipv4 ->> 'address','') <> ''
			  AND (external_ipv4 ->> 'address')::inet <<= '198.51.100.0/30'::cidr`,
			p.ID).Scan(&allocatedInRemoved))
		assert.Equal(t, 0, allocatedInRemoved,
			"remove succeeded but found allocated IP in removed CIDR — race!")
	} else {
		// Remove проиграл гонку (FailedPrecondition) — CIDR на месте.
		assert.Contains(t, rec.V4CIDRBlocks, "198.51.100.0/30")
		if allocSucceeded {
			st, _ := status.FromError(removeErr)
			assert.Equal(t, codes.FailedPrecondition, st.Code())
		}
	}
}
