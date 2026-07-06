// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package repo_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coredb "github.com/PRO-Robotech/kacho-corelib/db"
	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
	kachopg "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/pg"
)

// walkPoolAddresses прогоняет курсорную пагинацию ListAddressesByPool до пустого
// next-token'а и возвращает id всех вернувшихся Address в порядке обхода. Обход
// ограничен maxPages: баг в next-token (не двигается / зациклился) проявится как
// «слишком много страниц», а не как бесконечный цикл теста.
func walkPoolAddresses(t *testing.T, ctx context.Context, r kacho.Repository, poolID, projectFilter string, pageSize int64) []string {
	t.Helper()
	var out []string
	token := ""
	const maxPages = 100
	for page := 0; ; page++ {
		require.Less(t, page, maxPages, "pagination did not terminate (next-token not advancing?)")
		rd, err := r.Reader(ctx)
		require.NoError(t, err)
		batch, next, err := rd.AddressPools().ListAddressesByPool(
			ctx, poolID, projectFilter, kacho.Pagination{PageToken: token, PageSize: pageSize})
		_ = rd.Close()
		require.NoError(t, err)
		require.LessOrEqual(t, int64(len(batch)), pageSize, "page must never exceed pageSize")
		for _, a := range batch {
			out = append(out, a.ID)
		}
		if next == "" {
			break // последняя страница
		}
		// next-token выдаётся только когда набор не исчерпан → непоследняя страница полна.
		require.Equal(t, pageSize, int64(len(batch)), "non-final page must be exactly pageSize")
		token = next
	}
	return out
}

// TestAddressPool_ListAddressesByPool_CursorWalk доказывает, что курсор
// (created_at,id)-пагинации ListAddressesByPool проходит весь кросс-project набор
// адресов пула РОВНО один раз — без дублей и пропусков на границах страниц — и что
// projectFilter корректно сужает набор. Раньше эта repo-логика (LIMIT pageSize+1,
// граница out[pageSize-1], next-token) валидировалась лишь одним грубым newman-шагом
// (?pageSize=2) — off-by-one в границе/токене им не ловился.
func TestAddressPool_ListAddressesByPool_CursorWalk(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	pgPool, err := coredb.NewPool(ctx, setupTestDB(t))
	require.NoError(t, err)
	defer pgPool.Close()
	r := kachopg.New(pgPool, nil)
	defer r.Close()

	poolID := ids.NewID(ids.PrefixAddressPool)
	_, err = pgPool.Exec(ctx, `
		INSERT INTO address_pools (id, name, v4_cidr_blocks, kind)
		VALUES ($1, $2, ARRAY['203.0.113.0/24']::text[], 1)`, poolID, t.Name())
	require.NoError(t, err)

	const projA = "listaddrprojaaaa000"
	const projB = "listaddrprojbbbb000"
	// 8 адресов из пула (> pageSize=2): 5 в projA, 3 в projB; глобально-уникальные
	// external IPv4 + строго возрастающий created_at (детерминирует порядок обхода).
	seedA := make([]string, 0, 5)
	seedB := make([]string, 0, 3)
	for i := 0; i < 8; i++ {
		project := projA
		if i >= 5 {
			project = projB
		}
		addrID := ids.NewID(ids.PrefixAddress)
		_, err = pgPool.Exec(ctx, `
			INSERT INTO addresses (id, project_id, addr_type, ip_version, created_at, external_ipv4)
			VALUES ($1, $2, 1, 1, now() + $3::interval,
					jsonb_build_object('address', $4::text, 'address_pool_id', $5::text))`,
			addrID, project, fmt.Sprintf("%d seconds", i), fmt.Sprintf("203.0.113.%d", i+1), poolID)
		require.NoError(t, err)
		if project == projA {
			seedA = append(seedA, addrID)
		} else {
			seedB = append(seedB, addrID)
		}
	}
	allSeed := append(append([]string{}, seedA...), seedB...)

	// (1) Кросс-project обход pageSize=2 — курсор обязан вернуть каждый адрес ровно раз.
	got := walkPoolAddresses(t, ctx, r, poolID, "", 2)
	require.Len(t, got, len(allSeed), "walk must return exactly the seeded count (no dup, no gap)")
	assert.ElementsMatch(t, allSeed, got, "cursor walk must cover the whole pool set exactly once")

	// (2) projectFilter — только адреса projA.
	gotA := walkPoolAddresses(t, ctx, r, poolID, projA, 2)
	assert.ElementsMatch(t, seedA, gotA, "project filter must return exactly that project's pooled addresses")

	// (3) projectFilter — только адреса projB (контроль обеих ветвей фильтра).
	gotB := walkPoolAddresses(t, ctx, r, poolID, projB, 2)
	assert.ElementsMatch(t, seedB, gotB)
}

// TestAddressPool_ListAddressesByPool_EmptyPool — пул без адресов: первая же
// страница пуста, next-token пуст (терминальный обход).
func TestAddressPool_ListAddressesByPool_EmptyPool(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	pgPool, err := coredb.NewPool(ctx, setupTestDB(t))
	require.NoError(t, err)
	defer pgPool.Close()
	r := kachopg.New(pgPool, nil)
	defer r.Close()

	poolID := ids.NewID(ids.PrefixAddressPool)
	_, err = pgPool.Exec(ctx, `
		INSERT INTO address_pools (id, name, v4_cidr_blocks, kind)
		VALUES ($1, $2, ARRAY['203.0.113.0/24']::text[], 1)`, poolID, t.Name())
	require.NoError(t, err)

	rd, err := r.Reader(ctx)
	require.NoError(t, err)
	defer func() { _ = rd.Close() }()
	batch, next, err := rd.AddressPools().ListAddressesByPool(ctx, poolID, "", kacho.Pagination{PageSize: 2})
	require.NoError(t, err)
	assert.Empty(t, batch, "empty pool → no addresses")
	assert.Empty(t, next, "empty pool → empty next-token")
}
