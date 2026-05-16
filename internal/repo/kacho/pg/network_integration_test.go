package pg_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	coredb "github.com/PRO-Robotech/kacho-corelib/db"
	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/migrations"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
	kachopg "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/pg"
)

// Wave 5 pilot (KAC-94) — integration-тесты CQRS-impl `internal/repo/kacho/pg`.
//
// Покрывают:
//   - Reader видит Insert после Commit;
//   - Writer без Commit не виден параллельному Reader (read-committed);
//   - Abort() rollback'ит INSERT;
//   - outbox.Emit транзакционен с DML (Abort → outbox-row не вставлена).
//
// Существующие `internal/repo/network_repo_*test.go` остаются (тестируют legacy
// `*repo.NetworkRepo`) и не пересекаются с этими.

func setupTestDB(t testing.TB) string {
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
	require.NoError(t, goose.Up(db, "."))

	return dsn
}

func newNetwork(folderID, name string) *domain.Network {
	return &domain.Network{
		ID:          ids.NewID(ids.PrefixNetwork),
		FolderID:    folderID,
		Name:        domain.RcNameVPC(name),
		Description: domain.RcDescription(""),
		Labels:      domain.LabelsFromMap(nil),
	}
}

// TestCQRS_Network_WriterCommit_ReaderSees — Writer.Insert + Commit; параллельный
// Reader видит запись.
func TestCQRS_Network_WriterCommit_ReaderSees(t *testing.T) {
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

	n := newNetwork("folder-1", "net-1")
	created, err := w.Networks().Insert(ctx, n)
	require.NoError(t, err)
	assert.Equal(t, n.ID, created.ID)
	// outbox emit в той же TX.
	require.NoError(t, w.Outbox().Emit(ctx, "Network", created.ID, "CREATED", map[string]any{"id": created.ID}))
	require.NoError(t, w.Commit())

	// Параллельный Reader видит committed запись.
	rd, err := r.Reader(ctx)
	require.NoError(t, err)
	defer func() { _ = rd.Close() }()
	got, err := rd.Networks().Get(ctx, n.ID)
	require.NoError(t, err)
	assert.Equal(t, n.ID, got.ID)
	assert.Equal(t, domain.RcNameVPC("net-1"), got.Name)
}

// TestCQRS_Network_WriterUncommitted_ReaderNotSees — Writer.Insert без Commit;
// параллельный Reader НЕ видит запись (read-committed).
func TestCQRS_Network_WriterUncommitted_ReaderNotSees(t *testing.T) {
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

	n := newNetwork("folder-1", "net-uncommitted")
	_, err = w.Networks().Insert(ctx, n)
	require.NoError(t, err)
	// Внутри writer'а — видно (G.2).
	gotInWriter, err := w.Networks().Get(ctx, n.ID)
	require.NoError(t, err)
	assert.Equal(t, n.ID, gotInWriter.ID)

	// Снаружи — НЕ видно. Используем СВОЙ Reader, который открыт новой TX
	// на этом же pool (separate connection).
	rd, err := r.Reader(ctx)
	require.NoError(t, err)
	defer func() { _ = rd.Close() }()
	// Установим маленький deadline на Get — если уровень изоляции слабее,
	// мы бы получили запись; здесь должны получить NotFound.
	getCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	_, gerr := rd.Networks().Get(getCtx, n.ID)
	require.Error(t, gerr, "Reader should NOT see uncommitted writer's INSERT (read-committed)")
}

// TestCQRS_Network_WriterAbort_RollbacksInsert — Abort() rollback'ит INSERT;
// запись не появляется в БД.
func TestCQRS_Network_WriterAbort_RollbacksInsert(t *testing.T) {
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

	n := newNetwork("folder-1", "net-abort")
	_, err = w.Networks().Insert(ctx, n)
	require.NoError(t, err)
	w.Abort() // rollback

	// После Abort — Reader не видит запись.
	rd, err := r.Reader(ctx)
	require.NoError(t, err)
	defer func() { _ = rd.Close() }()
	_, gerr := rd.Networks().Get(ctx, n.ID)
	require.Error(t, gerr)
}

// TestCQRS_Network_OutboxAtomicityWithDML — Emit в той же TX, что и DML;
// Abort выкидывает и outbox-row.
func TestCQRS_Network_OutboxAtomicityWithDML(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	dsn := setupTestDB(t)
	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	r := kachopg.New(pool)

	// 1) Insert + Emit + Commit → outbox-row есть.
	w, err := r.Writer(ctx)
	require.NoError(t, err)
	n := newNetwork("folder-1", "net-outbox-commit")
	_, err = w.Networks().Insert(ctx, n)
	require.NoError(t, err)
	require.NoError(t, w.Outbox().Emit(ctx, "Network", n.ID, "CREATED", map[string]any{"id": n.ID}))
	require.NoError(t, w.Commit())

	// Проверяем outbox через прямой SQL — pkg pg/network.go не экспортирует
	// outbox read API (это домен Watch handler'а).
	var count1 int
	err = pool.QueryRow(ctx, "SELECT count(*) FROM vpc_outbox WHERE resource_id = $1", n.ID).Scan(&count1)
	require.NoError(t, err)
	assert.Equal(t, 1, count1, "committed Emit должен вставить outbox-row")

	// 2) Insert + Emit + Abort → НИ DML, НИ outbox-row не должны остаться.
	w2, err := r.Writer(ctx)
	require.NoError(t, err)
	n2 := newNetwork("folder-1", "net-outbox-abort")
	_, err = w2.Networks().Insert(ctx, n2)
	require.NoError(t, err)
	require.NoError(t, w2.Outbox().Emit(ctx, "Network", n2.ID, "CREATED", map[string]any{"id": n2.ID}))
	w2.Abort()

	var count2 int
	err = pool.QueryRow(ctx, "SELECT count(*) FROM vpc_outbox WHERE resource_id = $1", n2.ID).Scan(&count2)
	require.NoError(t, err)
	assert.Equal(t, 0, count2, "aborted Emit не должен вставить outbox-row")

	// И запись network — тоже отсутствует.
	var nCount int
	err = pool.QueryRow(ctx, "SELECT count(*) FROM networks WHERE id = $1", n2.ID).Scan(&nCount)
	require.NoError(t, err)
	assert.Equal(t, 0, nCount, "aborted Insert не должен вставить network-row")
}

// TestCQRS_Network_UpdateDelete_FullCycle — Insert → Update → Delete, проверяем
// что каждый шаг через writer виден после Commit.
func TestCQRS_Network_UpdateDelete_FullCycle(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	dsn := setupTestDB(t)
	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	r := kachopg.New(pool)

	// Insert.
	w1, err := r.Writer(ctx)
	require.NoError(t, err)
	n := newNetwork("folder-1", "net-cycle")
	created, err := w1.Networks().Insert(ctx, n)
	require.NoError(t, err)
	require.NoError(t, w1.Outbox().Emit(ctx, "Network", created.ID, "CREATED", map[string]any{"id": created.ID}))
	require.NoError(t, w1.Commit())

	// Update.
	w2, err := r.Writer(ctx)
	require.NoError(t, err)
	created.Name = domain.RcNameVPC("net-cycle-updated")
	updated, err := w2.Networks().Update(ctx, &created.Network)
	require.NoError(t, err)
	assert.Equal(t, domain.RcNameVPC("net-cycle-updated"), updated.Name)
	require.NoError(t, w2.Outbox().Emit(ctx, "Network", updated.ID, "UPDATED", map[string]any{"id": updated.ID}))
	require.NoError(t, w2.Commit())

	// Delete.
	w3, err := r.Writer(ctx)
	require.NoError(t, err)
	require.NoError(t, w3.Networks().Delete(ctx, n.ID))
	require.NoError(t, w3.Outbox().Emit(ctx, "Network", n.ID, "DELETED", map[string]any{"id": n.ID}))
	require.NoError(t, w3.Commit())

	// Reader не видит запись после Delete.
	rd, err := r.Reader(ctx)
	require.NoError(t, err)
	defer func() { _ = rd.Close() }()
	_, gerr := rd.Networks().Get(ctx, n.ID)
	require.Error(t, gerr)
}

// TestCQRS_Network_SetDefaultSGID_AtomicWithSG — узкий update-помощник
// SetDefaultSGID работает в той же writer-TX, в которой создан и сам SG (atomic
// default-SG-creation, Wave 5 batch 33/34, KAC-94). Без SetDefaultSGID пришлось
// бы делать полноценный Networks().Update(...) — и риск перезаписать
// name/description/labels чем-то промежуточным.
func TestCQRS_Network_SetDefaultSGID_AtomicWithSG(t *testing.T) {
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

	n := newNetwork("folder-setdefault", "net-setdefault")
	created, err := w.Networks().Insert(ctx, n)
	require.NoError(t, err)
	originalName := created.Name

	// Insert default SG first (FK Network.default_security_group_id →
	// security_groups.id), затем SetDefaultSGID.
	sgDom := domain.NewDefaultSecurityGroup(created.Network)
	sgRec, err := w.SecurityGroups().Insert(ctx, &sgDom)
	require.NoError(t, err)

	upd, err := w.Networks().SetDefaultSGID(ctx, created.ID, sgRec.ID)
	require.NoError(t, err)
	assert.Equal(t, sgRec.ID, upd.DefaultSecurityGroupID)
	// SetDefaultSGID — узкий UPDATE; name/description не должны меняться.
	assert.Equal(t, originalName, upd.Name, "SetDefaultSGID не должен менять name")
	require.NoError(t, w.Commit())

	// Проверка committed-state.
	rd, err := r.Reader(ctx)
	require.NoError(t, err)
	defer func() { _ = rd.Close() }()
	got, err := rd.Networks().Get(ctx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, sgRec.ID, got.DefaultSecurityGroupID)
	assert.Equal(t, originalName, got.Name)
}

// Assertion: реализация удовлетворяет интерфейсу (compile-time check).
var _ kacho.Repository = (*kachopg.Repository)(nil)
