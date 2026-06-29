// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package pg_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coredb "github.com/PRO-Robotech/kacho-corelib/db"
	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/fgaregister"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
	kachopg "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/pg"
)

// Integration-тесты transactional-outbox для FGA: register/unregister-intent
// пишется в той же writer-TX, что и Insert/Delete ресурса (не best-effort
// dual-write после Commit).
//
// Покрывают: intent в writer-tx, Abort → нет intent, Delete → unregister-intent.
// Таблица `kacho_vpc.fga_register_outbox` — отдельная от domain-`vpc_outbox`, чтобы
// изолировать FGA-relay-drainer от Watch-drainer.

type registerRow struct {
	EventType   string
	SubjectID   string
	Relation    string
	Object      string
	SentAtNull  bool
	AttemptZero bool
}

// readRegisterOutbox читает строки fga_register_outbox для assert.
func readRegisterOutbox(t *testing.T, ctx context.Context, pool *pgxpool.Pool) []registerRow {
	t.Helper()
	rows, err := pool.Query(ctx, `
		SELECT event_type,
		       payload->>'subject_id',
		       payload->>'relation',
		       payload->>'object',
		       sent_at IS NULL,
		       attempt_count = 0
		  FROM kacho_vpc.fga_register_outbox
		 ORDER BY id`)
	require.NoError(t, err)
	defer rows.Close()
	var out []registerRow
	for rows.Next() {
		var r registerRow
		require.NoError(t, rows.Scan(&r.EventType, &r.SubjectID, &r.Relation, &r.Object, &r.SentAtNull, &r.AttemptZero))
		out = append(out, r)
	}
	require.NoError(t, rows.Err())
	return out
}

// TestVPC_SEC_D_01_RegisterIntentInWriterTx — Create пишет fga.register-intent
// в той же writer-TX, что и Insert(Network) (+ domain Network/CREATED row).
func TestVPC_SEC_D_01_RegisterIntentInWriterTx(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test requires Docker")
	}
	ctx := context.Background()
	dsn := setupTestDB(t)
	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	repo := kachopg.New(pool, nil)
	n := newNetwork("proj-aaaaaaaaaaaaaaaaa", "net-a")

	w, err := repo.Writer(ctx)
	require.NoError(t, err)
	created, err := w.Networks().Insert(ctx, n)
	require.NoError(t, err)
	require.NoError(t, w.Outbox().Emit(ctx, "Network", created.ID, "CREATED", map[string]any{"id": created.ID}))

	// FGA-register-intent — в той же writer-TX (no dual-write).
	require.NoError(t, w.FGARegister().EmitRegister(ctx, fgaregister.RegisterIntent(fgaregister.ProjectHierarchy(string(n.ProjectID), "vpc_network", created.ID))))
	require.NoError(t, w.Commit())

	got := readRegisterOutbox(t, ctx, pool)
	require.Len(t, got, 1, "ровно одна fga.register строка")
	assert.Equal(t, "fga.register", got[0].EventType)
	assert.Equal(t, "project:proj-aaaaaaaaaaaaaaaaa", got[0].SubjectID)
	assert.Equal(t, "project", got[0].Relation)
	assert.Equal(t, "vpc_network:"+created.ID, got[0].Object)
	assert.True(t, got[0].SentAtNull, "sent_at IS NULL — drainer еще не применил")
	assert.True(t, got[0].AttemptZero, "attempt_count = 0")

	// domain Network/CREATED row тоже виден после одного Commit.
	var domainRows int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM kacho_vpc.vpc_outbox WHERE resource_kind='Network' AND event_type='CREATED'`).
		Scan(&domainRows))
	assert.Equal(t, 1, domainRows)
}

// TestVPC_SEC_D_02_AbortRollsBackRegisterIntent — Insert + register-intent в одной
// TX; Abort() → ни orphan Network, ни orphan register-intent.
func TestVPC_SEC_D_02_AbortRollsBackRegisterIntent(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test requires Docker")
	}
	ctx := context.Background()
	dsn := setupTestDB(t)
	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	repo := kachopg.New(pool, nil)
	n := newNetwork("proj-aaaaaaaaaaaaaaaaa", "net-abort")

	w, err := repo.Writer(ctx)
	require.NoError(t, err)
	created, err := w.Networks().Insert(ctx, n)
	require.NoError(t, err)
	require.NoError(t, w.FGARegister().EmitRegister(ctx, fgaregister.RegisterIntent(fgaregister.ProjectHierarchy(string(n.ProjectID), "vpc_network", created.ID))))
	// Abort вместо Commit — атомарность: intent и Insert откатываются вместе.
	w.Abort()

	got := readRegisterOutbox(t, ctx, pool)
	assert.Empty(t, got, "Abort → нет fga.register строки")

	var netRows int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM kacho_vpc.networks WHERE id=$1`, created.ID).Scan(&netRows))
	assert.Equal(t, 0, netRows, "Abort → нет orphan-строки networks")
}

// TestVPC_SEC_D_03_UnregisterIntentOnDelete — Delete пишет fga.unregister-intent
// в той же writer-TX, что и Delete(Network) (+ domain Network/DELETED).
func TestVPC_SEC_D_03_UnregisterIntentOnDelete(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test requires Docker")
	}
	ctx := context.Background()
	dsn := setupTestDB(t)
	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	repo := kachopg.New(pool, nil)
	n := newNetwork("proj-aaaaaaaaaaaaaaaaa", "net-del")

	// create
	w, err := repo.Writer(ctx)
	require.NoError(t, err)
	created, err := w.Networks().Insert(ctx, n)
	require.NoError(t, err)
	require.NoError(t, w.Commit())

	// delete + unregister-intent in one tx
	w2, err := repo.Writer(ctx)
	require.NoError(t, err)
	require.NoError(t, w2.Networks().Delete(ctx, created.ID))
	require.NoError(t, w2.Outbox().Emit(ctx, "Network", created.ID, "DELETED", map[string]any{"id": created.ID}))
	require.NoError(t, w2.FGARegister().EmitUnregister(ctx, fgaregister.RegisterIntent(fgaregister.ProjectHierarchy(string(n.ProjectID), "vpc_network", created.ID))))
	require.NoError(t, w2.Commit())

	got := readRegisterOutbox(t, ctx, pool)
	require.Len(t, got, 1)
	assert.Equal(t, "fga.unregister", got[0].EventType)
	assert.Equal(t, "vpc_network:"+created.ID, got[0].Object)
	assert.True(t, got[0].SentAtNull)

	var netRows int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM kacho_vpc.networks WHERE id=$1`, created.ID).Scan(&netRows))
	assert.Equal(t, 0, netRows, "строка networks удалена в той же TX")
}

// Держим импорт kacho осмысленным ради читаемости writer-интерфейса.
var _ kacho.RepositoryWriter
var _ = ids.PrefixNetwork
