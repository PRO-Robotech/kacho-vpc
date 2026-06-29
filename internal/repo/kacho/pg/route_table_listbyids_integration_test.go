// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package pg_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coredb "github.com/PRO-Robotech/kacho-corelib/db"
	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
	kachopg "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/pg"
)

// RouteTableReaderIface.ListByIDs обязан ограничивать строки allowed-set'ом
// (WHERE id = ANY) и пагинировать ИМЕННО отфильтрованный набор (плотные
// страницы). Аналогично subnet_listbyids_integration_test.go.

// seedRouteTablesForFilter вставляет родительскую Network (FK route_tables.network_id)
// + n route table'ов в одном проекте, каждый с уникальным именем, и возвращает их
// id в порядке создания (created_at ASC, id ASC tie-break).
func seedRouteTablesForFilter(t *testing.T, r kacho.Repository, projectID string, n int) []string {
	t.Helper()
	ctx := context.Background()
	w, err := r.Writer(ctx)
	require.NoError(t, err)
	net := newNetwork(projectID, "net-rt-filter")
	createdNet, err := w.Networks().Insert(ctx, net)
	require.NoError(t, err)

	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		rt := &domain.RouteTable{
			ID:        ids.NewID(ids.PrefixRouteTable),
			ProjectID: projectID,
			NetworkID: createdNet.ID,
			Name:      domain.RcNameVPC(fmt.Sprintf("rt-%03d", i)),
			Labels:    domain.LabelsFromMap(nil),
		}
		created, ierr := w.RouteTables().Insert(ctx, rt)
		require.NoError(t, ierr)
		out = append(out, created.ID)
	}
	require.NoError(t, w.Commit())
	return out
}

func TestRouteTableListByIDs_FiltersToAllowedSet(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test (testcontainers); skipped in -short")
	}
	ctx := context.Background()
	dsn := setupTestDB(t)
	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	r := kachopg.New(pool, nil)
	all := seedRouteTablesForFilter(t, r, "prj_filter", 6)
	allowed := []string{all[0], all[2], all[4]}

	rd, err := r.Reader(ctx)
	require.NoError(t, err)
	defer func() { _ = rd.Close() }()

	got, next, err := rd.RouteTables().ListByIDs(ctx, kacho.RouteTableFilter{ProjectID: "prj_filter"}, allowed, kacho.Pagination{})
	require.NoError(t, err)
	assert.Empty(t, next)
	require.Len(t, got, 3)
	gotIDs := map[string]bool{}
	for _, rt := range got {
		gotIDs[rt.ID] = true
	}
	for _, id := range allowed {
		assert.True(t, gotIDs[id], "allowed id %s must be present", id)
	}
	assert.False(t, gotIDs[all[1]], "non-allowed id must be absent (no-leak)")
}

func TestRouteTableListByIDs_EmptyAllowedShortCircuit(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test (testcontainers); skipped in -short")
	}
	ctx := context.Background()
	dsn := setupTestDB(t)
	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	r := kachopg.New(pool, nil)
	_ = seedRouteTablesForFilter(t, r, "prj_filter", 3)

	rd, err := r.Reader(ctx)
	require.NoError(t, err)
	defer func() { _ = rd.Close() }()

	got, next, err := rd.RouteTables().ListByIDs(ctx, kacho.RouteTableFilter{ProjectID: "prj_filter"}, nil, kacho.Pagination{})
	require.NoError(t, err)
	assert.Empty(t, got)
	assert.Empty(t, next)
}

// Пагинация по ОТФИЛЬТРОВАННОМУ набору с page_size=2: 5 allowed из 8 всего →
// страницы 2+2+1, плотные.
func TestRouteTableListByIDs_PaginationAfterFilter(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test (testcontainers); skipped in -short")
	}
	ctx := context.Background()
	dsn := setupTestDB(t)
	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	r := kachopg.New(pool, nil)
	all := seedRouteTablesForFilter(t, r, "prj_pg", 8)
	allowed := []string{all[0], all[2], all[4], all[6], all[7]}

	rd, err := r.Reader(ctx)
	require.NoError(t, err)
	defer func() { _ = rd.Close() }()

	var collected []string
	token := ""
	pages := 0
	for {
		got, next, lerr := rd.RouteTables().ListByIDs(ctx, kacho.RouteTableFilter{ProjectID: "prj_pg"}, allowed,
			kacho.Pagination{PageSize: 2, PageToken: token})
		require.NoError(t, lerr)
		pages++
		assert.LessOrEqual(t, len(got), 2, "page must not exceed page_size")
		for _, rt := range got {
			collected = append(collected, rt.ID)
		}
		if next == "" {
			break
		}
		token = next
		require.LessOrEqual(t, pages, 10, "pagination must terminate")
	}

	assert.Equal(t, 3, pages, "5 allowed / page_size 2 → 3 pages (2+2+1)")
	require.Len(t, collected, 5, "exactly the 5 allowed ids, dense pages")
	want := map[string]bool{}
	for _, id := range allowed {
		want[id] = true
	}
	for _, id := range collected {
		assert.True(t, want[id], "page must only contain allowed ids")
	}
}

// Мусорный page_token → InvalidArgument (как и в List).
func TestRouteTableListByIDs_GarbageToken(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test (testcontainers); skipped in -short")
	}
	ctx := context.Background()
	dsn := setupTestDB(t)
	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	r := kachopg.New(pool, nil)
	all := seedRouteTablesForFilter(t, r, "prj_tok", 2)

	rd, err := r.Reader(ctx)
	require.NoError(t, err)
	defer func() { _ = rd.Close() }()

	_, _, err = rd.RouteTables().ListByIDs(ctx, kacho.RouteTableFilter{ProjectID: "prj_tok"}, all,
		kacho.Pagination{PageSize: 1, PageToken: "!!!not-base64!!!"})
	require.Error(t, err)
}
