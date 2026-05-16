package pg_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coredb "github.com/PRO-Robotech/kacho-corelib/db"
	kachopg "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/pg"
)

// Wave 5 G.4 (KAC-94, skill evgeniy §6 G.4): slave-pool wiring.
//
// Эти integration-тесты проверяют структурный задел маршрутизации Reader/Writer
// по разным pgxpool'ам. Реальная streaming replica в kacho-deploy ещё не
// поднята; задача тестов — зафиксировать поведение pg-impl:
//   - Repository.Reader(ctx) идёт на slave-pool, если тот настроен;
//   - Reader-fallback на master, если slave=nil;
//   - Writer всегда идёт на master.
//
// Когда реальная реплика появится, эти же тесты будут продолжать проходить —
// семантика API уже зафиксирована.

// TestCQRS_SlavePool_FallbackOnNil — slave=nil → Reader использует master.
// Запись в master через Writer видна через Reader (которому передан nil slave).
func TestCQRS_SlavePool_FallbackOnNil(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	dsn := setupTestDB(t)
	master, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer master.Close()

	// Явно nil — Repository.Reader должен fallback'нуть на master.
	r := kachopg.New(master, nil)

	w, err := r.Writer(ctx)
	require.NoError(t, err)

	n := newNetwork("folder-slave-fallback", "net-fallback")
	_, err = w.Networks().Insert(ctx, n)
	require.NoError(t, err)
	require.NoError(t, w.Commit())

	rd, err := r.Reader(ctx)
	require.NoError(t, err)
	defer func() { _ = rd.Close() }()
	got, err := rd.Networks().Get(ctx, n.ID)
	require.NoError(t, err)
	assert.Equal(t, n.ID, got.ID)
}

// TestCQRS_SlavePool_RouterUsesSlavePool — slave настроен (отдельный pool,
// указывающий на тот же мастер-контейнер: replication ещё не настроена в test
// env, поэтому "реплика" — это второй pool на ту же физическую БД).
// Проверяет, что Reader-TX **берёт connection из slave-pool'а**, а не из
// master'а — это видно через `pool.Stat().AcquiredConns()`.
func TestCQRS_SlavePool_RouterUsesSlavePool(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	dsn := setupTestDB(t)

	master, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer master.Close()

	slave, err := coredb.NewPool(ctx, dsn) // второй pool на тот же контейнер
	require.NoError(t, err)
	defer slave.Close()

	r := kachopg.New(master, slave)

	// Открываем Writer и сразу Commit'ним INSERT — чтобы Reader потом мог
	// прочитать committed запись из slave-pool'а (через тот же физический PG).
	w, err := r.Writer(ctx)
	require.NoError(t, err)
	n := newNetwork("folder-slave-routed", "net-routed")
	_, err = w.Networks().Insert(ctx, n)
	require.NoError(t, err)
	require.NoError(t, w.Commit())

	// Снимаем baseline на slave-pool (acquired conns) до Reader.BeginTx.
	slaveBefore := slave.Stat().AcquiredConns()
	masterBefore := master.Stat().AcquiredConns()

	rd, err := r.Reader(ctx)
	require.NoError(t, err)

	// Reader держит TX → slave-pool должен иметь +1 acquired connection.
	slaveDuring := slave.Stat().AcquiredConns()
	masterDuring := master.Stat().AcquiredConns()
	assert.Equal(t, slaveBefore+1, slaveDuring,
		"Reader.BeginTx должен взять conn из slave-pool")
	assert.Equal(t, masterBefore, masterDuring,
		"Reader.BeginTx НЕ должен трогать master-pool")

	// Sanity: Reader действительно читает данные (через slave-pool — те же
	// данные, т.к. в test env это та же физическая БД).
	got, err := rd.Networks().Get(ctx, n.ID)
	require.NoError(t, err)
	assert.Equal(t, n.ID, got.ID)

	require.NoError(t, rd.Close())

	// После Close — conn возвращён в slave-pool.
	assert.Equal(t, slaveBefore, slave.Stat().AcquiredConns(),
		"после rd.Close() conn должен вернуться в slave-pool")
}

// TestCQRS_SlavePool_WriterAlwaysMaster — даже если slave настроен, Writer-TX
// идёт на master (writes на реплику ошибочны: streaming replica read-only).
func TestCQRS_SlavePool_WriterAlwaysMaster(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	dsn := setupTestDB(t)

	master, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer master.Close()

	slave, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer slave.Close()

	r := kachopg.New(master, slave)

	slaveBefore := slave.Stat().AcquiredConns()
	masterBefore := master.Stat().AcquiredConns()

	w, err := r.Writer(ctx)
	require.NoError(t, err)
	defer w.Abort()

	assert.Equal(t, masterBefore+1, master.Stat().AcquiredConns(),
		"Writer.BeginTx должен взять conn из master-pool")
	assert.Equal(t, slaveBefore, slave.Stat().AcquiredConns(),
		"Writer.BeginTx НЕ должен трогать slave-pool")
}
