package pg_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coredb "github.com/PRO-Robotech/kacho-corelib/db"
	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	kachopg "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/pg"
)

// Wave 5 replicate (KAC-94) — integration-тесты CQRS-impl Subnet в
// `internal/repo/kacho/pg`. Parity с Network-tests; покрывает:
//   - Reader видит Insert+Commit;
//   - Writer.Abort rollback'ит Insert + outbox emit;
//   - EXCLUDE constraint subnets_no_overlap_v4 → ErrFailedPrecondition (через
//     IsExclusionViolation в pg/subnet.go).
//
// Существующие `internal/repo/integration_test.go::TestIntegration_SubnetRepo_*`
// остаются (тестируют legacy `*repo.SubnetRepo`) и не пересекаются с этими.

func newSubnet(folderID, name, networkID, zone string, v4 []string) *domain.Subnet {
	return &domain.Subnet{
		ID:           ids.NewID(ids.PrefixSubnet),
		FolderID:     folderID,
		Name:         domain.RcNameVPC(name),
		Description:  domain.RcDescription(""),
		Labels:       domain.LabelsFromMap(nil),
		NetworkID:    networkID,
		ZoneID:       zone,
		V4CidrBlocks: v4,
	}
}

// TestCQRS_Subnet_WriterCommit_ReaderSees — Insert(Subnet) + Commit → reader
// видит запись. Insert(parent Network) идёт в той же writer-TX (FK constraint).
func TestCQRS_Subnet_WriterCommit_ReaderSees(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	dsn := setupTestDB(t)
	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	r := kachopg.New(pool, nil)

	// Сначала создаём parent network в одной writer-TX (FK constraint).
	w1, err := r.Writer(ctx)
	require.NoError(t, err)
	net := newNetwork("folder-sub", "net-for-subnet")
	_, err = w1.Networks().Insert(ctx, net)
	require.NoError(t, err)
	require.NoError(t, w1.Commit())

	// Теперь — Insert(Subnet) + outbox.Emit + Commit.
	w2, err := r.Writer(ctx)
	require.NoError(t, err)
	sub := newSubnet("folder-sub", "sub-1", net.ID, "ru-central1-a", []string{"10.0.0.0/24"})
	created, err := w2.Subnets().Insert(ctx, sub)
	require.NoError(t, err)
	assert.Equal(t, sub.ID, created.ID)
	assert.Equal(t, []string{"10.0.0.0/24"}, created.V4CidrBlocks)
	require.NoError(t, w2.Outbox().Emit(ctx, "Subnet", created.ID, "CREATED", map[string]any{"id": created.ID}))
	require.NoError(t, w2.Commit())

	// Параллельный Reader видит committed запись.
	rd, err := r.Reader(ctx)
	require.NoError(t, err)
	defer func() { _ = rd.Close() }()
	got, err := rd.Subnets().Get(ctx, sub.ID)
	require.NoError(t, err)
	assert.Equal(t, sub.ID, got.ID)
	assert.Equal(t, domain.RcNameVPC("sub-1"), got.Name)
}

// TestCQRS_Subnet_OutboxAtomicityWithDML — Emit + DML атомарны в writer-TX.
// Abort выкидывает и subnet-row, и outbox-row.
func TestCQRS_Subnet_OutboxAtomicityWithDML(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	dsn := setupTestDB(t)
	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	r := kachopg.New(pool, nil)

	// Seed parent network.
	wn, err := r.Writer(ctx)
	require.NoError(t, err)
	net := newNetwork("folder-atomic", "net-atomic")
	_, err = wn.Networks().Insert(ctx, net)
	require.NoError(t, err)
	require.NoError(t, wn.Commit())

	// Insert(Subnet) + Emit + Abort → НИ subnet-row, НИ outbox-row.
	w, err := r.Writer(ctx)
	require.NoError(t, err)
	sub := newSubnet("folder-atomic", "sub-abort", net.ID, "ru-central1-a", []string{"10.10.0.0/24"})
	_, err = w.Subnets().Insert(ctx, sub)
	require.NoError(t, err)
	require.NoError(t, w.Outbox().Emit(ctx, "Subnet", sub.ID, "CREATED", map[string]any{"id": sub.ID}))
	w.Abort()

	// Проверки.
	var subCount int
	err = pool.QueryRow(ctx, "SELECT count(*) FROM subnets WHERE id = $1", sub.ID).Scan(&subCount)
	require.NoError(t, err)
	assert.Equal(t, 0, subCount, "aborted Insert не должен вставить subnet-row")

	var outCount int
	err = pool.QueryRow(ctx, "SELECT count(*) FROM vpc_outbox WHERE resource_id = $1", sub.ID).Scan(&outCount)
	require.NoError(t, err)
	assert.Equal(t, 0, outCount, "aborted Emit не должен вставить outbox-row")
}

// TestCQRS_Subnet_CIDROverlap_ExclusionViolation — EXCLUDE constraint
// subnets_no_overlap_v4 ловит overlap на CIDR primary → SQLSTATE 23P01 →
// ErrFailedPrecondition через IsExclusionViolation.
func TestCQRS_Subnet_CIDROverlap_ExclusionViolation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	dsn := setupTestDB(t)
	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	r := kachopg.New(pool, nil)

	// Seed parent network.
	wn, err := r.Writer(ctx)
	require.NoError(t, err)
	net := newNetwork("folder-overlap", "net-overlap")
	_, err = wn.Networks().Insert(ctx, net)
	require.NoError(t, err)
	require.NoError(t, wn.Commit())

	// Insert первой подсети с 10.0.0.0/24.
	w1, err := r.Writer(ctx)
	require.NoError(t, err)
	s1 := newSubnet("folder-overlap", "sub-1", net.ID, "ru-central1-a", []string{"10.0.0.0/24"})
	_, err = w1.Subnets().Insert(ctx, s1)
	require.NoError(t, err)
	require.NoError(t, w1.Commit())

	// Insert второй подсети с пересекающимся 10.0.0.0/25 → EXCLUDE constraint
	// ловит → ErrFailedPrecondition "Subnet CIDRs can not overlap".
	w2, err := r.Writer(ctx)
	require.NoError(t, err)
	defer w2.Abort()
	s2 := newSubnet("folder-overlap", "sub-2", net.ID, "ru-central1-a", []string{"10.0.0.0/25"})
	_, err = w2.Subnets().Insert(ctx, s2)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Subnet CIDRs can not overlap")
}
