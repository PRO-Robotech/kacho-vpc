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

// Wave 5 batch 33/34 (KAC-94, skill evgeniy I.9 / I.10) — integration-тесты для
// CQRS SG-репо. setupTestDB / coredb / migrations reused из
// network_integration_test.go (один пакет).

// helper — создать Network в той же writer-TX (нужен parent для SG.network_id FK).
func insertNetworkInTx(t *testing.T, ctx context.Context, w kacho.RepositoryWriter, folderID, name string) *kacho.NetworkRecord {
	t.Helper()
	n := newNetwork(folderID, name)
	created, err := w.Networks().Insert(ctx, n)
	require.NoError(t, err)
	return created
}

func newDefaultSG(folderID, networkID string) *domain.SecurityGroup {
	sg := domain.NewDefaultSecurityGroup(domain.Network{ID: networkID, ProjectID: folderID})
	sg.ID = ids.NewID(ids.PrefixSecurityGroup)
	return &sg
}

// TestCQRS_SG_InsertCommit_ReaderSees — Writer.Insert(Network) + Insert(SG) в
// одной TX, после Commit Reader видит обе записи.
func TestCQRS_SG_InsertCommit_ReaderSees(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	dsn := setupTestDB(t)
	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	r := kachopg.New(pool, nil)

	// Network + SG в одной writer-TX.
	w, err := r.Writer(ctx)
	require.NoError(t, err)
	net := insertNetworkInTx(t, ctx, w, "folder-sg-1", "net-sg-1")
	require.NoError(t, w.Outbox().Emit(ctx, "Network", net.ID, "CREATED", map[string]any{"id": net.ID}))
	sg := newDefaultSG(net.ProjectID, net.ID)
	createdSG, err := w.SecurityGroups().Insert(ctx, sg)
	require.NoError(t, err)
	assert.Equal(t, sg.ID, createdSG.ID)
	require.NoError(t, w.Outbox().Emit(ctx, "SecurityGroup", createdSG.ID, "CREATED", map[string]any{"id": createdSG.ID}))
	require.NoError(t, w.Commit())

	// Reader видит SG.
	rd, err := r.Reader(ctx)
	require.NoError(t, err)
	defer func() { _ = rd.Close() }()
	got, err := rd.SecurityGroups().Get(ctx, sg.ID)
	require.NoError(t, err)
	assert.Equal(t, sg.ID, got.ID)
	assert.Equal(t, net.ID, got.NetworkID)
	assert.True(t, got.DefaultForNetwork)
}

// TestCQRS_SG_AbortRollback — Abort после Insert(SG) → ни SG, ни Network, ни
// outbox-row не остаются. Это ключевое свойство, ради которого атомарный
// default-SG-creation переехал в одну writer-TX (I.10 + запрет #10).
func TestCQRS_SG_AbortRollback(t *testing.T) {
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
	net := insertNetworkInTx(t, ctx, w, "folder-sg-abort", "net-sg-abort")
	require.NoError(t, w.Outbox().Emit(ctx, "Network", net.ID, "CREATED", map[string]any{"id": net.ID}))
	sg := newDefaultSG(net.ProjectID, net.ID)
	_, err = w.SecurityGroups().Insert(ctx, sg)
	require.NoError(t, err)
	require.NoError(t, w.Outbox().Emit(ctx, "SecurityGroup", sg.ID, "CREATED", map[string]any{"id": sg.ID}))
	w.Abort()

	// Ничего не должно быть видно: ни Network, ни SG, ни outbox-rows.
	var netCount, sgCount, outboxCount int
	require.NoError(t, pool.QueryRow(ctx, "SELECT count(*) FROM networks WHERE id = $1", net.ID).Scan(&netCount))
	require.NoError(t, pool.QueryRow(ctx, "SELECT count(*) FROM security_groups WHERE id = $1", sg.ID).Scan(&sgCount))
	require.NoError(t, pool.QueryRow(ctx, "SELECT count(*) FROM vpc_outbox WHERE resource_id IN ($1, $2)", net.ID, sg.ID).Scan(&outboxCount))
	assert.Equal(t, 0, netCount, "Abort должен откатить Network INSERT")
	assert.Equal(t, 0, sgCount, "Abort должен откатить SG INSERT")
	assert.Equal(t, 0, outboxCount, "Abort должен откатить outbox-rows")
}

// TestCQRS_Network_AtomicDefaultSGCreate — full-cycle: Insert(Network) +
// Insert(SG) + SetDefaultSGID(Network, sg.ID) + 3 outbox-event'а в одной writer-TX.
// После Commit Network.default_security_group_id заполнен. Это и есть то, что
// атомизирует Network.Create в use-case'е.
func TestCQRS_Network_AtomicDefaultSGCreate(t *testing.T) {
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

	net := insertNetworkInTx(t, ctx, w, "folder-atomic", "net-atomic")
	require.NoError(t, w.Outbox().Emit(ctx, "Network", net.ID, "CREATED", map[string]any{"id": net.ID}))

	sg := newDefaultSG(net.ProjectID, net.ID)
	createdSG, err := w.SecurityGroups().Insert(ctx, sg)
	require.NoError(t, err)
	require.NoError(t, w.Outbox().Emit(ctx, "SecurityGroup", createdSG.ID, "CREATED", map[string]any{"id": createdSG.ID}))

	upd, err := w.Networks().SetDefaultSGID(ctx, net.ID, createdSG.ID)
	require.NoError(t, err)
	assert.Equal(t, createdSG.ID, upd.DefaultSecurityGroupID)
	require.NoError(t, w.Outbox().Emit(ctx, "Network", upd.ID, "UPDATED", map[string]any{"id": upd.ID}))

	require.NoError(t, w.Commit())

	// Проверка committed-state.
	rd, err := r.Reader(ctx)
	require.NoError(t, err)
	defer func() { _ = rd.Close() }()

	gotNet, err := rd.Networks().Get(ctx, net.ID)
	require.NoError(t, err)
	assert.Equal(t, createdSG.ID, gotNet.DefaultSecurityGroupID)

	gotSG, err := rd.SecurityGroups().Get(ctx, createdSG.ID)
	require.NoError(t, err)
	assert.True(t, gotSG.DefaultForNetwork)

	// 3 outbox-row'a (по resource_id Network и SG).
	var outboxCount int
	require.NoError(t, pool.QueryRow(ctx,
		"SELECT count(*) FROM vpc_outbox WHERE resource_id IN ($1, $2)", net.ID, createdSG.ID,
	).Scan(&outboxCount))
	assert.Equal(t, 3, outboxCount, "ожидаем Network.CREATED + SecurityGroup.CREATED + Network.UPDATED")
}

// TestCQRS_Network_AtomicDefaultSGCreate_AbortOnSG — Abort после Insert(Network)+
// Insert(SG) (имитируем crash) — НИ Network, НИ SG, НИ outbox не должны остаться.
// Это закрывает orphan-window прежней three-TX-схемы.
func TestCQRS_Network_AtomicDefaultSGCreate_AbortOnSG(t *testing.T) {
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
	net := insertNetworkInTx(t, ctx, w, "folder-abort", "net-abort")
	require.NoError(t, w.Outbox().Emit(ctx, "Network", net.ID, "CREATED", map[string]any{"id": net.ID}))
	sg := newDefaultSG(net.ProjectID, net.ID)
	_, err = w.SecurityGroups().Insert(ctx, sg)
	require.NoError(t, err)
	w.Abort() // имитируем crash после SG.Insert

	var netCount, sgCount int
	require.NoError(t, pool.QueryRow(ctx, "SELECT count(*) FROM networks WHERE id = $1", net.ID).Scan(&netCount))
	require.NoError(t, pool.QueryRow(ctx, "SELECT count(*) FROM security_groups WHERE id = $1", sg.ID).Scan(&sgCount))
	assert.Equal(t, 0, netCount)
	assert.Equal(t, 0, sgCount)
}

// TestCQRS_SG_UpdateDelete — full-cycle SG: Insert → Update → Delete каждое в
// своей writer-TX, между шагами Reader видит committed state.
func TestCQRS_SG_UpdateDelete(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	dsn := setupTestDB(t)
	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	r := kachopg.New(pool, nil)

	// Insert Network + SG.
	w1, err := r.Writer(ctx)
	require.NoError(t, err)
	net := insertNetworkInTx(t, ctx, w1, "folder-cycle", "net-cycle")
	require.NoError(t, w1.Outbox().Emit(ctx, "Network", net.ID, "CREATED", map[string]any{"id": net.ID}))
	sg := newDefaultSG(net.ProjectID, net.ID)
	created, err := w1.SecurityGroups().Insert(ctx, sg)
	require.NoError(t, err)
	require.NoError(t, w1.Outbox().Emit(ctx, "SecurityGroup", created.ID, "CREATED", map[string]any{"id": created.ID}))
	require.NoError(t, w1.Commit())

	// Update.
	w2, err := r.Writer(ctx)
	require.NoError(t, err)
	created.Name = domain.RcNameVPC("renamed-sg")
	upd, err := w2.SecurityGroups().Update(ctx, &created.SecurityGroup)
	require.NoError(t, err)
	assert.Equal(t, domain.RcNameVPC("renamed-sg"), upd.Name)
	require.NoError(t, w2.Outbox().Emit(ctx, "SecurityGroup", upd.ID, "UPDATED", map[string]any{"id": upd.ID}))
	require.NoError(t, w2.Commit())

	// Delete.
	w3, err := r.Writer(ctx)
	require.NoError(t, err)
	require.NoError(t, w3.SecurityGroups().Delete(ctx, created.ID))
	require.NoError(t, w3.Outbox().Emit(ctx, "SecurityGroup", created.ID, "DELETED", map[string]any{"id": created.ID}))
	require.NoError(t, w3.Commit())

	// Reader не видит SG.
	rd, err := r.Reader(ctx)
	require.NoError(t, err)
	defer func() { _ = rd.Close() }()
	_, gerr := rd.SecurityGroups().Get(ctx, created.ID)
	require.Error(t, gerr)
}
