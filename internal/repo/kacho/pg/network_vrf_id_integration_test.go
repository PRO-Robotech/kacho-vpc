// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package pg_test

import (
	"context"
	"database/sql"
	"strconv"
	"sync"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	coredb "github.com/PRO-Robotech/kacho-corelib/db"
	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/migrations"
	kachopg "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/pg"
)

func newNetID() string  { return ids.NewID(ids.PrefixNetwork) }
func itoa(i int) string { return strconv.Itoa(i) }

// setupTestDBUpTo поднимает testcontainers Postgres и применяет миграции ТОЛЬКО
// до версии `version` (для backfill-теста: вставить строки до 0007, затем UpTo 7).
func setupTestDBUpTo(t testing.TB, version int64) string {
	t.Helper()
	ctx := context.Background()
	pgc, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("kacho_vpc_test"),
		postgres.WithUsername("vpc"),
		postgres.WithPassword("vpc"),
		postgres.BasicWaitStrategies(),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = pgc.Terminate(ctx) })

	dsn, err := pgc.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	db, err := sql.Open("pgx", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	goose.SetBaseFS(migrations.FS)
	require.NoError(t, goose.SetDialect("postgres"))
	require.NoError(t, goose.UpTo(db, ".", version))
	return appendSearchPathOptions(dsn)
}

// Аллокация Network.vrf_id и internal-read: уникальность под concurrency,
// монотонность без переиспользования, стабильность при Update, чтение vrf_id
// тем же read-путем Networks().Get, что отдает InternalNetworkService.GetNetwork,
// и backfill уже существующих строк.

func insertNetwork(t *testing.T, r *kachopg.Repository, projectID, name string) *domain.Network {
	t.Helper()
	ctx := context.Background()
	w, err := r.Writer(ctx)
	require.NoError(t, err)
	defer w.Abort()
	n := &domain.Network{
		ID:          ids.NewID(ids.PrefixNetwork),
		ProjectID:   projectID,
		Name:        domain.RcNameVPC(name),
		Description: domain.RcDescription(""),
		Labels:      domain.LabelsFromMap(nil),
	}
	_, err = w.Networks().Insert(ctx, n)
	require.NoError(t, err)
	require.NoError(t, w.Commit())
	return n
}

// Read-путь (Networks().Get — то, что отдает InternalNetworkService.GetNetwork)
// возвращает vrf_id >= 1.
func TestNetwork_CIL0_06_GetInternalReturnsVrfId(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	dsn := setupTestDB(t)
	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()
	r := kachopg.New(pool, nil)

	n := insertNetwork(t, r, "project-cil0", "net-vrf-06")

	rd, err := r.Reader(ctx)
	require.NoError(t, err)
	defer func() { _ = rd.Close() }()
	rec, err := rd.Networks().Get(ctx, n.ID)
	require.NoError(t, err)
	assert.Equal(t, n.ID, rec.ID)
	assert.GreaterOrEqual(t, rec.VRFID, uint32(1), "vrf_id must be allocated (>=1, 0 reserved)")
}

// N конкурентных Create → все vrf_id различны (DB-level uniqueness).
func TestNetwork_CIL0_02_VrfIdUniqueUnderConcurrency(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	dsn := setupTestDB(t)
	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()
	r := kachopg.New(pool, nil)

	const N = 20
	ids := make([]string, N)
	var wg sync.WaitGroup
	errs := make([]error, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			w, err := r.Writer(ctx)
			if err != nil {
				errs[i] = err
				return
			}
			defer w.Abort()
			n := &domain.Network{
				ID:          newNetID(),
				ProjectID:   "project-cil0-conc",
				Name:        domain.RcNameVPC("net-conc-" + itoa(i)),
				Description: domain.RcDescription(""),
				Labels:      domain.LabelsFromMap(nil),
			}
			if _, err := w.Networks().Insert(ctx, n); err != nil {
				errs[i] = err
				return
			}
			if err := w.Commit(); err != nil {
				errs[i] = err
				return
			}
			ids[i] = n.ID
		}(i)
	}
	wg.Wait()
	for i, e := range errs {
		require.NoErrorf(t, e, "create %d", i)
	}

	rd, err := r.Reader(ctx)
	require.NoError(t, err)
	defer func() { _ = rd.Close() }()
	seen := make(map[uint32]string, N)
	for _, id := range ids {
		rec, err := rd.Networks().Get(ctx, id)
		require.NoError(t, err)
		if prev, dup := seen[rec.VRFID]; dup {
			t.Fatalf("duplicate vrf_id %d for %s and %s", rec.VRFID, prev, id)
		}
		seen[rec.VRFID] = id
	}
	assert.Len(t, seen, N, "all %d networks must have distinct vrf_id", N)
}

// vrf_id монотонен и не переиспользуется после delete.
func TestNetwork_CIL0_05_VrfIdNoReuseMonotonic(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	dsn := setupTestDB(t)
	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()
	r := kachopg.New(pool, nil)

	a := insertNetwork(t, r, "project-cil0-reuse", "net-a")
	rd, err := r.Reader(ctx)
	require.NoError(t, err)
	recA, err := rd.Networks().Get(ctx, a.ID)
	require.NoError(t, err)
	_ = rd.Close()

	// delete A
	w, err := r.Writer(ctx)
	require.NoError(t, err)
	require.NoError(t, w.Networks().Delete(ctx, a.ID))
	require.NoError(t, w.Commit())

	b := insertNetwork(t, r, "project-cil0-reuse", "net-b")
	rd2, err := r.Reader(ctx)
	require.NoError(t, err)
	defer func() { _ = rd2.Close() }()
	recB, err := rd2.Networks().Get(ctx, b.ID)
	require.NoError(t, err)

	assert.NotEqual(t, recA.VRFID, recB.VRFID, "vrf_id must not be reused after delete")
	assert.Greater(t, recB.VRFID, recA.VRFID, "vrf_id must be monotonic")
}

// vrf_id стабилен при Update (rename/labels).
func TestNetwork_CIL0_03_VrfIdStableAcrossUpdate(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	dsn := setupTestDB(t)
	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()
	r := kachopg.New(pool, nil)

	n := insertNetwork(t, r, "project-cil0-upd", "net-before")
	rd, err := r.Reader(ctx)
	require.NoError(t, err)
	rec0, err := rd.Networks().Get(ctx, n.ID)
	require.NoError(t, err)
	_ = rd.Close()

	w, err := r.Writer(ctx)
	require.NoError(t, err)
	n.Name = domain.RcNameVPC("net-after")
	_, err = w.Networks().Update(ctx, n)
	require.NoError(t, err)
	require.NoError(t, w.Commit())

	rd2, err := r.Reader(ctx)
	require.NoError(t, err)
	defer func() { _ = rd2.Close() }()
	rec1, err := rd2.Networks().Get(ctx, n.ID)
	require.NoError(t, err)
	assert.Equal(t, rec0.VRFID, rec1.VRFID, "vrf_id must be immutable across Update")
}

// backfill — строки, созданные ДО миграции 0007, получают уникальные vrf_id.
func TestNetwork_CIL0_13_BackfillUniqueVrfId(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	// Спец-харнес: миграции до 0006, insert raw networks, затем 0007.
	dsn := setupTestDBUpTo(t, 6)
	db, err := sql.Open("pgx", dsn)
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	for i := 0; i < 5; i++ {
		_, err := db.ExecContext(ctx,
			`INSERT INTO kacho_vpc.networks (id, project_id, name) VALUES ($1,$2,$3)`,
			newNetID(), "proj-backfill", "net-pre-"+itoa(i))
		require.NoError(t, err)
	}

	// Применяем 0007.
	goose.SetBaseFS(migrations.FS)
	require.NoError(t, goose.SetDialect("postgres"))
	require.NoError(t, goose.UpTo(db, ".", 7))

	rows, err := db.QueryContext(ctx, `SELECT vrf_id FROM kacho_vpc.networks`)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	seen := map[int64]bool{}
	count := 0
	for rows.Next() {
		var v int64
		require.NoError(t, rows.Scan(&v))
		assert.GreaterOrEqual(t, v, int64(1))
		assert.False(t, seen[v], "duplicate vrf_id %d after backfill", v)
		seen[v] = true
		count++
	}
	require.NoError(t, rows.Err())
	assert.Equal(t, 5, count)
}
