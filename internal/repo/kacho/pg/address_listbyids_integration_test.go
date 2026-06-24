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
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
	kachopg "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/pg"
)

// AddressReaderIface.ListByIDs обязан ограничивать строки разрешенным набором id
// (WHERE id = ANY) и пагинировать ОТФИЛЬТРОВАННЫЙ набор (плотные страницы, а не
// «дырявые» из сырого project-набора).

// seedAddressesForFilter вставляет n external-адресов в один project, каждому дает
// уникальное имя и возвращает их id в порядке создания (== порядок List,
// created_at ASC, id ASC tie-break).
func seedAddressesForFilter(t *testing.T, r kacho.Repository, projectID string, n int) []string {
	t.Helper()
	ctx := context.Background()
	w, err := r.Writer(ctx)
	require.NoError(t, err)

	ids := make([]string, 0, n)
	for i := 0; i < n; i++ {
		a := newAddress(projectID, fmt.Sprintf("addr-%03d", i), true)
		created, ierr := w.Addresses().Insert(ctx, a)
		require.NoError(t, ierr)
		ids = append(ids, created.ID)
	}
	require.NoError(t, w.Commit())
	return ids
}

func TestAddressListByIDs_FiltersToAllowedSet(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test (testcontainers); skipped in -short")
	}
	ctx := context.Background()
	dsn := setupTestDB(t)
	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	r := kachopg.New(pool, nil)
	all := seedAddressesForFilter(t, r, "prj_filter", 6)
	allowed := []string{all[0], all[2], all[4]}

	rd, err := r.Reader(ctx)
	require.NoError(t, err)
	defer func() { _ = rd.Close() }()

	got, next, err := rd.Addresses().ListByIDs(ctx, kacho.AddressFilter{ProjectID: "prj_filter"}, allowed, kacho.Pagination{})
	require.NoError(t, err)
	assert.Empty(t, next)
	require.Len(t, got, 3)
	gotIDs := map[string]bool{}
	for _, a := range got {
		gotIDs[a.ID] = true
	}
	for _, id := range allowed {
		assert.True(t, gotIDs[id], "allowed id %s must be present", id)
	}
	assert.False(t, gotIDs[all[1]], "non-allowed id must be absent (no-leak)")
}

func TestAddressListByIDs_EmptyAllowedShortCircuit(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test (testcontainers); skipped in -short")
	}
	ctx := context.Background()
	dsn := setupTestDB(t)
	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	r := kachopg.New(pool, nil)
	_ = seedAddressesForFilter(t, r, "prj_filter", 3)

	rd, err := r.Reader(ctx)
	require.NoError(t, err)
	defer func() { _ = rd.Close() }()

	got, next, err := rd.Addresses().ListByIDs(ctx, kacho.AddressFilter{ProjectID: "prj_filter"}, nil, kacho.Pagination{})
	require.NoError(t, err)
	assert.Empty(t, got)
	assert.Empty(t, next)
}

// Пагинация по ОТФИЛЬТРОВАННОМУ набору при page_size=2: 5 разрешенных из 8 →
// страницы 2+2+1, плотные, ровно эти 5 разрешенных id.
func TestAddressListByIDs_PaginationAfterFilter(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test (testcontainers); skipped in -short")
	}
	ctx := context.Background()
	dsn := setupTestDB(t)
	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	r := kachopg.New(pool, nil)
	all := seedAddressesForFilter(t, r, "prj_pg", 8)
	allowed := []string{all[0], all[2], all[4], all[6], all[7]}

	rd, err := r.Reader(ctx)
	require.NoError(t, err)
	defer func() { _ = rd.Close() }()

	var collected []string
	token := ""
	pages := 0
	for {
		got, next, lerr := rd.Addresses().ListByIDs(ctx, kacho.AddressFilter{ProjectID: "prj_pg"}, allowed,
			kacho.Pagination{PageSize: 2, PageToken: token})
		require.NoError(t, lerr)
		pages++
		assert.LessOrEqual(t, len(got), 2, "page must not exceed page_size")
		for _, a := range got {
			collected = append(collected, a.ID)
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

// garbage page_token → InvalidArgument (parity with List).
func TestAddressListByIDs_GarbageToken(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test (testcontainers); skipped in -short")
	}
	ctx := context.Background()
	dsn := setupTestDB(t)
	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	r := kachopg.New(pool, nil)
	all := seedAddressesForFilter(t, r, "prj_tok", 2)

	rd, err := r.Reader(ctx)
	require.NoError(t, err)
	defer func() { _ = rd.Close() }()

	_, _, err = rd.Addresses().ListByIDs(ctx, kacho.AddressFilter{ProjectID: "prj_tok"}, all,
		kacho.Pagination{PageSize: 1, PageToken: "!!!not-base64!!!"})
	require.Error(t, err)
}
