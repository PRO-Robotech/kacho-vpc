// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package repo_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	coredb "github.com/PRO-Robotech/kacho-corelib/db"
	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
	kachopg "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/pg"
)

// AllocateIPFromFreelist обязан брать IP из freelist ТОЛЬКО если адрес еще не
// имеет external_ipv4.address (target-guard). Иначе повторный allocate / allocate
// для несуществующего адреса вынимал бы IP из пула, никому его не присваивая
// (утечка адресного пространства).

func poolFreeCount(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, poolID string) int {
	t.Helper()
	var n int
	require.NoError(t, pgPool.QueryRow(ctx,
		`SELECT COUNT(*) FROM address_pool_free_ips WHERE pool_id = $1`, poolID).Scan(&n))
	return n
}

// Повторный allocate того же адреса (уже с IP) идемпотентен: возвращает тот же
// external_ipv4 и НЕ вынимает второй IP из freelist (no double-pop / no leak).
// Раньше отдавал ErrPoolExhausted — ложный «exhausted» для адреса, которому IP
// уже выдан; теперь зеркалит идемпотентный AllocateExternalIPv6 (project-rule #10):
// re-read address FOR UPDATE внутри writer-TX → существующий IP.
func TestFreelist_NoDoublePop_SameAddress(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	pgPool, err := coredb.NewPool(ctx, setupTestDB(t))
	require.NoError(t, err)
	defer pgPool.Close()
	r := kachopg.New(pgPool, nil)
	defer r.Close()

	poolID := insertTestPoolForFreelist(t, ctx, pgPool, "198.51.100.0/28") // 14 usable
	require.NoError(t, freelistWithTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		return w.AddressPools().PopulateFreelistForPool(ctx, poolID)
	}))
	addrID := insertTestAddressFreelist(t, ctx, pgPool)

	var firstIP string
	require.NoError(t, freelistWithTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		ip, e := w.Addresses().AllocateIPFromFreelist(ctx, poolID, addrID)
		firstIP = ip
		return e
	}))
	require.NotEmpty(t, firstIP)

	countBefore := poolFreeCount(t, ctx, pgPool, poolID)

	// Повторный allocate того же (уже выделенного) адреса → тот же IP
	// идемпотентно, размер freelist не меняется (нет второго pop).
	var secondIP string
	require.NoError(t, freelistWithTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		ip, e := w.Addresses().AllocateIPFromFreelist(ctx, poolID, addrID)
		secondIP = ip
		return e
	}))
	require.Equal(t, firstIP, secondIP, "repeat allocate of same address returns the same IP idempotently")
	require.Equal(t, countBefore, poolFreeCount(t, ctx, pgPool, poolID), "second allocate must not pop another IP")
}

// Allocate для НЕсуществующего address_id → ErrPoolExhausted и НЕ вынимает IP
// (раньше pop происходил, а UPDATE не матчился → IP терялся).
func TestFreelist_NoPop_MissingAddress(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	pgPool, err := coredb.NewPool(ctx, setupTestDB(t))
	require.NoError(t, err)
	defer pgPool.Close()
	r := kachopg.New(pgPool, nil)
	defer r.Close()

	poolID := insertTestPoolForFreelist(t, ctx, pgPool, "198.51.100.0/28")
	require.NoError(t, freelistWithTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		return w.AddressPools().PopulateFreelistForPool(ctx, poolID)
	}))
	countBefore := poolFreeCount(t, ctx, pgPool, poolID)

	err = freelistWithTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		_, e := w.Addresses().AllocateIPFromFreelist(ctx, poolID, ids.NewID(ids.PrefixAddress))
		return e
	})
	require.Error(t, err)
	require.True(t, errors.Is(err, repo.ErrPoolExhausted))
	require.Equal(t, countBefore, poolFreeCount(t, ctx, pgPool, poolID), "allocate for missing address must not pop an IP")
}
