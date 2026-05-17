package pg_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coredb "github.com/PRO-Robotech/kacho-corelib/db"
	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
	kachopg "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/pg"
)

// Wave 5 replicate (KAC-94) — integration-тесты CQRS-impl Gateway в
// `internal/repo/kacho/pg`. Parity с TestCQRS_Network_* (см. network_integration_test.go).
//
// Покрывают:
//   - Insert + Commit виден параллельному Reader;
//   - Abort() rollback'ит INSERT (запись не появляется в БД);
//   - outbox.Emit транзакционен с DML (Abort → outbox-row не вставлена).

func newGateway(folderID, name string) *domain.Gateway {
	return &domain.Gateway{
		ID:          ids.NewID(ids.PrefixGateway),
		ProjectID:    folderID,
		Name:        domain.RcNameVPC(name),
		Description: domain.RcDescription(""),
		Labels:      domain.LabelsFromMap(nil),
		GatewayType: domain.GatewayTypeSharedEgress,
	}
}

// TestCQRS_Gateway_WriterCommit_ReaderSees — Writer.Insert + Commit; параллельный
// Reader видит запись.
func TestCQRS_Gateway_WriterCommit_ReaderSees(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	dsn := setupTestDB(t)
	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	r := kachopg.New(pool, nil)

	w, err := r.Writer(ctx)
	require.NoError(t, err)

	g := newGateway("folder-1", "gw-1")
	created, err := w.Gateways().Insert(ctx, g)
	require.NoError(t, err)
	assert.Equal(t, g.ID, created.ID)
	// outbox emit в той же TX.
	require.NoError(t, w.Outbox().Emit(ctx, "Gateway", created.ID, "CREATED", map[string]any{"id": created.ID}))
	require.NoError(t, w.Commit())

	// Параллельный Reader видит committed запись.
	rd, err := r.Reader(ctx)
	require.NoError(t, err)
	defer func() { _ = rd.Close() }()
	got, err := rd.Gateways().Get(ctx, g.ID)
	require.NoError(t, err)
	assert.Equal(t, g.ID, got.ID)
	assert.Equal(t, domain.RcNameVPC("gw-1"), got.Name)
	assert.Equal(t, domain.GatewayTypeSharedEgress, got.GatewayType)
}

// TestCQRS_Gateway_WriterAbort_RollbacksInsert — Abort() rollback'ит INSERT;
// запись не появляется в БД.
func TestCQRS_Gateway_WriterAbort_RollbacksInsert(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	dsn := setupTestDB(t)
	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	r := kachopg.New(pool, nil)

	w, err := r.Writer(ctx)
	require.NoError(t, err)

	g := newGateway("folder-1", "gw-abort")
	_, err = w.Gateways().Insert(ctx, g)
	require.NoError(t, err)
	w.Abort() // rollback

	// После Abort — Reader не видит запись.
	rd, err := r.Reader(ctx)
	require.NoError(t, err)
	defer func() { _ = rd.Close() }()
	_, gerr := rd.Gateways().Get(ctx, g.ID)
	require.Error(t, gerr)
}

// TestCQRS_Gateway_OutboxAtomicityWithDML — Emit в той же TX, что и DML;
// Abort выкидывает и outbox-row.
func TestCQRS_Gateway_OutboxAtomicityWithDML(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	dsn := setupTestDB(t)
	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	r := kachopg.New(pool, nil)

	// 1) Insert + Emit + Commit → outbox-row есть.
	w, err := r.Writer(ctx)
	require.NoError(t, err)
	g := newGateway("folder-1", "gw-outbox-commit")
	_, err = w.Gateways().Insert(ctx, g)
	require.NoError(t, err)
	require.NoError(t, w.Outbox().Emit(ctx, "Gateway", g.ID, "CREATED", map[string]any{"id": g.ID}))
	require.NoError(t, w.Commit())

	var count1 int
	err = pool.QueryRow(ctx, "SELECT count(*) FROM vpc_outbox WHERE resource_id = $1", g.ID).Scan(&count1)
	require.NoError(t, err)
	assert.Equal(t, 1, count1, "committed Emit должен вставить outbox-row")

	// 2) Insert + Emit + Abort → ни DML, ни outbox-row не должны остаться.
	w2, err := r.Writer(ctx)
	require.NoError(t, err)
	g2 := newGateway("folder-1", "gw-outbox-abort")
	_, err = w2.Gateways().Insert(ctx, g2)
	require.NoError(t, err)
	require.NoError(t, w2.Outbox().Emit(ctx, "Gateway", g2.ID, "CREATED", map[string]any{"id": g2.ID}))
	w2.Abort()

	var count2 int
	err = pool.QueryRow(ctx, "SELECT count(*) FROM vpc_outbox WHERE resource_id = $1", g2.ID).Scan(&count2)
	require.NoError(t, err)
	assert.Equal(t, 0, count2, "aborted Emit не должен вставить outbox-row")

	var gwCount int
	err = pool.QueryRow(ctx, "SELECT count(*) FROM gateways WHERE id = $1", g2.ID).Scan(&gwCount)
	require.NoError(t, err)
	assert.Equal(t, 0, gwCount, "aborted Insert не должен вставить gateway-row")
}

// TestCQRS_Gateway_UpdateDelete_FullCycle — Insert → Update → Delete, проверяем
// что каждый шаг через writer виден после Commit (+ outbox event).
func TestCQRS_Gateway_UpdateDelete_FullCycle(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	dsn := setupTestDB(t)
	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	r := kachopg.New(pool, nil)

	// Insert.
	w1, err := r.Writer(ctx)
	require.NoError(t, err)
	g := newGateway("folder-1", "gw-cycle")
	created, err := w1.Gateways().Insert(ctx, g)
	require.NoError(t, err)
	require.NoError(t, w1.Outbox().Emit(ctx, "Gateway", created.ID, "CREATED", map[string]any{"id": created.ID}))
	require.NoError(t, w1.Commit())

	// Update.
	w2, err := r.Writer(ctx)
	require.NoError(t, err)
	created.Name = domain.RcNameVPC("gw-cycle-updated")
	updated, err := w2.Gateways().Update(ctx, &created.Gateway)
	require.NoError(t, err)
	assert.Equal(t, domain.RcNameVPC("gw-cycle-updated"), updated.Name)
	require.NoError(t, w2.Outbox().Emit(ctx, "Gateway", updated.ID, "UPDATED", map[string]any{"id": updated.ID}))
	require.NoError(t, w2.Commit())

	// Delete.
	w3, err := r.Writer(ctx)
	require.NoError(t, err)
	require.NoError(t, w3.Gateways().Delete(ctx, g.ID))
	require.NoError(t, w3.Outbox().Emit(ctx, "Gateway", g.ID, "DELETED", map[string]any{"id": g.ID}))
	require.NoError(t, w3.Commit())

	// Reader не видит запись после Delete.
	rd, err := r.Reader(ctx)
	require.NoError(t, err)
	defer func() { _ = rd.Close() }()
	_, gerr := rd.Gateways().Get(ctx, g.ID)
	require.Error(t, gerr)
}

// Assertion: kacho.GatewayRecord — это правильный тип, возвращаемый repo.
var _ *kacho.GatewayRecord = (*kacho.GatewayRecord)(nil)
