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

// Wave 5 replicate (KAC-94) — integration-тесты CQRS-impl PrivateEndpoint в
// `internal/repo/kacho/pg`. Базовый CRUD + FK-check (миграция 0024:
// network_id → networks(id) ON DELETE RESTRICT).
//
// Parity с network_integration_test.go.

func newPrivateEndpoint(folderID, networkID, name string) *domain.PrivateEndpoint {
	return &domain.PrivateEndpoint{
		ID:          ids.NewID(ids.PrefixPrivateEndpoint),
		FolderID:    folderID,
		NetworkID:   networkID,
		Name:        domain.RcNameVPC(name),
		Description: domain.RcDescription(""),
		Labels:      domain.LabelsFromMap(nil),
		ServiceType: domain.PrivateEndpointServiceTypeObjectStorage,
		Status:      domain.PrivateEndpointStatusAvailable,
	}
}

// TestCQRS_PrivateEndpoint_WriterCommit_ReaderSees — Writer.Insert + Commit;
// параллельный Reader видит запись через CQRS-Reader.PrivateEndpoints().Get.
func TestCQRS_PrivateEndpoint_WriterCommit_ReaderSees(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	dsn := setupTestDB(t)
	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	r := kachopg.New(pool)

	// Seed parent Network — FK private_endpoints.network_id → networks(id) RESTRICT.
	w0, err := r.Writer(ctx)
	require.NoError(t, err)
	netRec := newNetwork("folder-pe-1", "net-for-pe")
	_, err = w0.Networks().Insert(ctx, netRec)
	require.NoError(t, err)
	require.NoError(t, w0.Commit())

	// Insert PE.
	w, err := r.Writer(ctx)
	require.NoError(t, err)

	pe := newPrivateEndpoint("folder-pe-1", netRec.ID, "pe-1")
	created, err := w.PrivateEndpoints().Insert(ctx, pe)
	require.NoError(t, err)
	assert.Equal(t, pe.ID, created.ID)
	require.NoError(t, w.Outbox().Emit(ctx, "PrivateEndpoint", created.ID, "CREATED", map[string]any{"id": created.ID}))
	require.NoError(t, w.Commit())

	// Параллельный Reader видит committed запись.
	rd, err := r.Reader(ctx)
	require.NoError(t, err)
	defer func() { _ = rd.Close() }()
	got, err := rd.PrivateEndpoints().Get(ctx, pe.ID)
	require.NoError(t, err)
	assert.Equal(t, pe.ID, got.ID)
	assert.Equal(t, domain.RcNameVPC("pe-1"), got.Name)
	assert.Equal(t, netRec.ID, got.NetworkID)
}

// TestCQRS_PrivateEndpoint_FKViolation_NetworkMissing — FK
// private_endpoints.network_id → networks(id) проверяется Postgres'ом на INSERT.
// Если parent Network не существует — INSERT падает с 23503 (FK violation), что
// repo-обёртка маппит на ошибку (wrapPgErr).
func TestCQRS_PrivateEndpoint_FKViolation_NetworkMissing(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	dsn := setupTestDB(t)
	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	r := kachopg.New(pool)

	w, err := r.Writer(ctx)
	require.NoError(t, err)
	defer w.Abort()

	pe := newPrivateEndpoint("folder-x", "enp-missing-network-id-x", "pe-fk")
	_, err = w.PrivateEndpoints().Insert(ctx, pe)
	require.Error(t, err, "Insert PE с отсутствующим network_id должен упасть на FK constraint")
}

// TestCQRS_PrivateEndpoint_UpdateDelete_FullCycle — Insert → Update → Delete,
// проверяем что каждый шаг через writer-TX виден после Commit.
func TestCQRS_PrivateEndpoint_UpdateDelete_FullCycle(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	dsn := setupTestDB(t)
	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	r := kachopg.New(pool)

	// Seed parent Network.
	w0, err := r.Writer(ctx)
	require.NoError(t, err)
	netRec := newNetwork("folder-pe-cycle", "net-cycle")
	_, err = w0.Networks().Insert(ctx, netRec)
	require.NoError(t, err)
	require.NoError(t, w0.Commit())

	// Insert.
	w1, err := r.Writer(ctx)
	require.NoError(t, err)
	pe := newPrivateEndpoint("folder-pe-cycle", netRec.ID, "pe-cycle")
	created, err := w1.PrivateEndpoints().Insert(ctx, pe)
	require.NoError(t, err)
	require.NoError(t, w1.Outbox().Emit(ctx, "PrivateEndpoint", created.ID, "CREATED", map[string]any{"id": created.ID}))
	require.NoError(t, w1.Commit())

	// Update.
	w2, err := r.Writer(ctx)
	require.NoError(t, err)
	created.Name = domain.RcNameVPC("pe-cycle-updated")
	created.Description = domain.RcDescription("desc-updated")
	updated, err := w2.PrivateEndpoints().Update(ctx, &created.PrivateEndpoint)
	require.NoError(t, err)
	assert.Equal(t, domain.RcNameVPC("pe-cycle-updated"), updated.Name)
	assert.Equal(t, domain.RcDescription("desc-updated"), updated.Description)
	require.NoError(t, w2.Outbox().Emit(ctx, "PrivateEndpoint", updated.ID, "UPDATED", map[string]any{"id": updated.ID}))
	require.NoError(t, w2.Commit())

	// Delete.
	w3, err := r.Writer(ctx)
	require.NoError(t, err)
	require.NoError(t, w3.PrivateEndpoints().Delete(ctx, pe.ID))
	require.NoError(t, w3.Outbox().Emit(ctx, "PrivateEndpoint", pe.ID, "DELETED", map[string]any{"id": pe.ID}))
	require.NoError(t, w3.Commit())

	// Reader не видит запись после Delete.
	rd, err := r.Reader(ctx)
	require.NoError(t, err)
	defer func() { _ = rd.Close() }()
	_, gerr := rd.PrivateEndpoints().Get(ctx, pe.ID)
	require.Error(t, gerr, "PE должен быть удалён после Commit Delete")
}

// Assertion: реализация удовлетворяет интерфейсу (compile-time check).
var _ kacho.PrivateEndpointReaderIface = (*pgPrivateEndpointReaderAssert)(nil)

type pgPrivateEndpointReaderAssert struct {
	kacho.PrivateEndpointReaderIface
}
