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

// NetworkInterfaceReaderIface.ListByIDs обязан ограничивать выборку разрешенным
// id-set (WHERE id = ANY) и пагинировать уже отфильтрованный набор (плотные
// страницы).

// seedNICsForFilter вставляет parent Subnet (FK network_interfaces.subnet_id) +
// n NIC в одном проекте, каждый со своим именем и своим MAC (cloud-wide UNIQUE),
// и возвращает их id в порядке создания (created_at ASC, id ASC tie-break).
func seedNICsForFilter(t *testing.T, ctx context.Context, dsn string, r kacho.Repository, n int) (projectID string, nicIDs []string) {
	t.Helper()
	projectID, subnetID := insertSubnetForNIC(t, ctx, dsn)

	w, err := r.Writer(ctx)
	require.NoError(t, err)
	nicIDs = make([]string, 0, n)
	for i := 0; i < n; i++ {
		nic := &domain.NetworkInterface{
			ID:        ids.NewID(ids.PrefixNetworkInterface),
			ProjectID: projectID,
			Name:      domain.RcNameVPC(fmt.Sprintf("nic-%03d", i)),
			Labels:    domain.LabelsFromMap(nil),
			SubnetID:  subnetID,
			MAC:       fmt.Sprintf("0e:00:00:00:%02x:%02x", i/256, i%256),
			Status:    domain.NIStatusAvailable,
		}
		created, ierr := w.NetworkInterfaces().Insert(ctx, nic)
		require.NoError(t, ierr)
		nicIDs = append(nicIDs, created.ID)
	}
	require.NoError(t, w.Commit())
	return projectID, nicIDs
}

func TestNetworkInterfaceListByIDs_FiltersToAllowedSet(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test (testcontainers); skipped in -short")
	}
	ctx := context.Background()
	dsn := setupTestDB(t)
	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	r := kachopg.New(pool, nil)
	projectID, all := seedNICsForFilter(t, ctx, dsn, r, 6)
	allowed := []string{all[0], all[2], all[4]}

	rd, err := r.Reader(ctx)
	require.NoError(t, err)
	defer func() { _ = rd.Close() }()

	got, next, err := rd.NetworkInterfaces().ListByIDs(ctx, kacho.NetworkInterfaceFilter{ProjectID: projectID}, allowed, kacho.Pagination{})
	require.NoError(t, err)
	assert.Empty(t, next)
	require.Len(t, got, 3)
	gotIDs := map[string]bool{}
	for _, n := range got {
		gotIDs[n.ID] = true
	}
	for _, id := range allowed {
		assert.True(t, gotIDs[id], "allowed id %s must be present", id)
	}
	assert.False(t, gotIDs[all[1]], "non-allowed id must be absent (no-leak)")
}

func TestNetworkInterfaceListByIDs_EmptyAllowedShortCircuit(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test (testcontainers); skipped in -short")
	}
	ctx := context.Background()
	dsn := setupTestDB(t)
	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	r := kachopg.New(pool, nil)
	projectID, _ := seedNICsForFilter(t, ctx, dsn, r, 3)

	rd, err := r.Reader(ctx)
	require.NoError(t, err)
	defer func() { _ = rd.Close() }()

	got, next, err := rd.NetworkInterfaces().ListByIDs(ctx, kacho.NetworkInterfaceFilter{ProjectID: projectID}, nil, kacho.Pagination{})
	require.NoError(t, err)
	assert.Empty(t, got)
	assert.Empty(t, next)
}

// Пагинация по отфильтрованному набору с page_size=2: 5 allowed из 8 →
// страницы 2+2+1, плотные.
func TestNetworkInterfaceListByIDs_PaginationAfterFilter(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test (testcontainers); skipped in -short")
	}
	ctx := context.Background()
	dsn := setupTestDB(t)
	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	r := kachopg.New(pool, nil)
	projectID, all := seedNICsForFilter(t, ctx, dsn, r, 8)
	allowed := []string{all[0], all[2], all[4], all[6], all[7]}

	rd, err := r.Reader(ctx)
	require.NoError(t, err)
	defer func() { _ = rd.Close() }()

	var collected []string
	token := ""
	pages := 0
	for {
		got, next, lerr := rd.NetworkInterfaces().ListByIDs(ctx, kacho.NetworkInterfaceFilter{ProjectID: projectID}, allowed,
			kacho.Pagination{PageSize: 2, PageToken: token})
		require.NoError(t, lerr)
		pages++
		assert.LessOrEqual(t, len(got), 2, "page must not exceed page_size")
		for _, n := range got {
			collected = append(collected, n.ID)
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

// Битый page_token → InvalidArgument (как в List).
func TestNetworkInterfaceListByIDs_GarbageToken(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test (testcontainers); skipped in -short")
	}
	ctx := context.Background()
	dsn := setupTestDB(t)
	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	r := kachopg.New(pool, nil)
	projectID, all := seedNICsForFilter(t, ctx, dsn, r, 2)

	rd, err := r.Reader(ctx)
	require.NoError(t, err)
	defer func() { _ = rd.Close() }()

	_, _, err = rd.NetworkInterfaces().ListByIDs(ctx, kacho.NetworkInterfaceFilter{ProjectID: projectID}, all,
		kacho.Pagination{PageSize: 1, PageToken: "!!!not-base64!!!"})
	require.Error(t, err)
}
