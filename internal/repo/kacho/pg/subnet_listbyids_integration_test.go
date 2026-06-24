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
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
	kachopg "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/pg"
)

// Пагинация применяется ПОСЛЕ per-object фильтра. SubnetReaderIface.ListByIDs
// ограничивает строки разрешенным id-set (WHERE id = ANY) и пагинирует уже
// ОТФИЛЬТРОВАННЫЙ набор — плотные страницы, а не «дырявые», полученные из сырого
// project-набора. Зеркалит NetworkReaderIface.ListByIDs.

// seedSubnetsForFilter создает n подсетей в одном project/network, каждую с
// уникальным непересекающимся /24 (EXCLUDE constraint), и возвращает их id в
// порядке создания (== порядок List, created_at ASC, id ASC при tie-break).
func seedSubnetsForFilter(t *testing.T, r kacho.Repository, projectID, networkID string, n int) []string {
	t.Helper()
	ctx := context.Background()
	w, err := r.Writer(ctx)
	require.NoError(t, err)
	net := &domain.Network{
		ID:        networkID,
		ProjectID: projectID,
		Name:      domain.RcNameVPC("net-filter"),
		Labels:    domain.LabelsFromMap(nil),
	}
	_, err = w.Networks().Insert(ctx, net)
	require.NoError(t, err)

	ids := make([]string, 0, n)
	for i := 0; i < n; i++ {
		cidr := fmt.Sprintf("10.%d.%d.0/24", i/256, i%256)
		sub := newSubnet(projectID, fmt.Sprintf("sub-%03d", i), networkID, "zone-a", []string{cidr})
		created, ierr := w.Subnets().Insert(ctx, sub)
		require.NoError(t, ierr)
		ids = append(ids, created.ID)
	}
	require.NoError(t, w.Commit())
	return ids
}

func TestSubnetListByIDs_FiltersToAllowedSet(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test (testcontainers); skipped in -short")
	}
	ctx := context.Background()
	dsn := setupTestDB(t)
	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	r := kachopg.New(pool, nil)
	all := seedSubnetsForFilter(t, r, "prj_filter", "enp_netfilter", 6)
	allowed := []string{all[0], all[2], all[4]}

	rd, err := r.Reader(ctx)
	require.NoError(t, err)
	defer func() { _ = rd.Close() }()

	got, next, err := rd.Subnets().ListByIDs(ctx, kacho.SubnetFilter{ProjectID: "prj_filter"}, allowed, kacho.Pagination{})
	require.NoError(t, err)
	assert.Empty(t, next)
	require.Len(t, got, 3)
	gotIDs := map[string]bool{}
	for _, s := range got {
		gotIDs[s.ID] = true
	}
	for _, id := range allowed {
		assert.True(t, gotIDs[id], "allowed id %s must be present", id)
	}
	assert.False(t, gotIDs[all[1]], "non-allowed id must be absent (no-leak)")
}

func TestSubnetListByIDs_EmptyAllowedShortCircuit(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test (testcontainers); skipped in -short")
	}
	ctx := context.Background()
	dsn := setupTestDB(t)
	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	r := kachopg.New(pool, nil)
	_ = seedSubnetsForFilter(t, r, "prj_filter", "enp_netfilter", 3)

	rd, err := r.Reader(ctx)
	require.NoError(t, err)
	defer func() { _ = rd.Close() }()

	got, next, err := rd.Subnets().ListByIDs(ctx, kacho.SubnetFilter{ProjectID: "prj_filter"}, nil, kacho.Pagination{})
	require.NoError(t, err)
	assert.Empty(t, got)
	assert.Empty(t, next)
}

// Пагинация по ОТФИЛЬТРОВАННОМУ набору с page_size=2: 5 разрешенных из 8 всего
// → страницы 2+2+1, плотные, покрывают ровно 5 разрешенных id без «дырявых» страниц.
func TestSubnetListByIDs_PaginationAfterFilter(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test (testcontainers); skipped in -short")
	}
	ctx := context.Background()
	dsn := setupTestDB(t)
	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	r := kachopg.New(pool, nil)
	all := seedSubnetsForFilter(t, r, "prj_pg", "enp_netpg", 8)
	// allowed = каждая вторая подсеть → 4 из 8 (индексы 0,2,4,6) плюс еще одна (7)
	allowed := []string{all[0], all[2], all[4], all[6], all[7]}

	rd, err := r.Reader(ctx)
	require.NoError(t, err)
	defer func() { _ = rd.Close() }()

	var collected []string
	token := ""
	pages := 0
	for {
		got, next, lerr := rd.Subnets().ListByIDs(ctx, kacho.SubnetFilter{ProjectID: "prj_pg"}, allowed,
			kacho.Pagination{PageSize: 2, PageToken: token})
		require.NoError(t, lerr)
		pages++
		assert.LessOrEqual(t, len(got), 2, "page must not exceed page_size")
		for _, s := range got {
			collected = append(collected, s.ID)
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

// Мусорный page_token → InvalidArgument (паритет с List).
func TestSubnetListByIDs_GarbageToken(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test (testcontainers); skipped in -short")
	}
	ctx := context.Background()
	dsn := setupTestDB(t)
	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	r := kachopg.New(pool, nil)
	all := seedSubnetsForFilter(t, r, "prj_tok", "enp_nettok", 2)

	rd, err := r.Reader(ctx)
	require.NoError(t, err)
	defer func() { _ = rd.Close() }()

	_, _, err = rd.Subnets().ListByIDs(ctx, kacho.SubnetFilter{ProjectID: "prj_tok"}, all,
		kacho.Pagination{PageSize: 1, PageToken: "!!!not-base64!!!"})
	require.Error(t, err)
}
