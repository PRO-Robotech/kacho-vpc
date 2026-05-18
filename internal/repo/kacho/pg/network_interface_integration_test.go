package pg_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coredb "github.com/PRO-Robotech/kacho-corelib/db"
	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	kachopg "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/pg"
)

// Wave 5 replicate (KAC-94, NIC batch) — integration-тесты CQRS NIC-репо.
//
// Покрывают:
//   - Insert + Commit, Reader видит запись;
//   - AttachToInstance — atomic CAS (KAC-52): два concurrent attach'а к одному
//     NIC должны привести к ровно одному успеху, второй — ErrFailedPrecondition.
//
// Существующий legacy `internal/repo/network_interface_attach_race_integration_test.go`
// остаётся (тестирует `*repo.NetworkInterfaceRepo.SetUsedBy`) и не пересекается с
// этими — pg-CQRS-impl использует ту же SQL-семантику (single-statement
// conditional UPDATE), но через writer-TX.

// helper — создать parent Subnet через legacy SubnetRepo (NIC требует FK).
// Subnet — пока legacy CQRS (replicate-фаза, следующая итерация); используем
// legacy repo для setup.
func insertSubnetForNIC(t *testing.T, ctx context.Context, dsn string) (folderID, subnetID string) {
	t.Helper()
	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	folderID = "folder-nic-cqrs"
	subnetID = ids.NewID(ids.PrefixSubnet)
	// network parent для Subnet FK
	netID := ids.NewID(ids.PrefixNetwork)
	_, err = pool.Exec(ctx, `INSERT INTO networks(id, project_id, name, description, labels) VALUES ($1,$2,$3,$4,'{}'::jsonb)`,
		netID, folderID, "net-nic", "")
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO subnets(id, project_id, network_id, zone_id, name, description, labels, v4_cidr_blocks, v6_cidr_blocks) VALUES ($1,$2,$3,$4,$5,$6,'{}'::jsonb, ARRAY['10.0.0.0/24']::text[], ARRAY[]::text[])`,
		subnetID, folderID, netID, "ru-central1-a", "sn-nic", "")
	require.NoError(t, err)
	return folderID, subnetID
}

// TestCQRS_NIC_InsertCommit_ReaderSees — sanity: Writer.Insert + Commit, Reader видит.
func TestCQRS_NIC_InsertCommit_ReaderSees(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	dsn := setupTestDB(t)
	folderID, subnetID := insertSubnetForNIC(t, ctx, dsn)

	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()
	r := kachopg.New(pool, nil)

	w, err := r.Writer(ctx)
	require.NoError(t, err)
	nic := &domain.NetworkInterface{
		ID:          ids.NewID(ids.PrefixSubnet),
		ProjectID:   folderID,
		Name:        domain.RcNameVPC("nic-cqrs"),
		Description: domain.RcDescription(""),
		Labels:      domain.LabelsFromMap(nil),
		SubnetID:    subnetID,
		MAC:         "0e:11:22:33:44:55",
		Status:      domain.NIStatusAvailable,
	}
	created, err := w.NetworkInterfaces().Insert(ctx, nic)
	require.NoError(t, err)
	require.NoError(t, w.Outbox().Emit(ctx, "NetworkInterface", created.ID, "CREATED", map[string]any{"id": created.ID}))
	require.NoError(t, w.Commit())

	rd, err := r.Reader(ctx)
	require.NoError(t, err)
	defer func() { _ = rd.Close() }()
	got, err := rd.NetworkInterfaces().Get(ctx, nic.ID)
	require.NoError(t, err)
	assert.Equal(t, nic.ID, got.ID)
	assert.Equal(t, subnetID, got.SubnetID)
	assert.Equal(t, "0e:11:22:33:44:55", got.MAC)
}

// TestCQRS_NIC_AttachToInstance_CAS_Race — два concurrent attach'а к одному NIC:
// ровно один — успех, остальные — ErrFailedPrecondition (KAC-52, workspace
// CLAUDE.md §«Within-service refs — DB-уровень обязателен», запрет #10).
func TestCQRS_NIC_AttachToInstance_CAS_Race(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	dsn := setupTestDB(t)
	folderID, subnetID := insertSubnetForNIC(t, ctx, dsn)

	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()
	r := kachopg.New(pool, nil)

	// Setup: создать NIC в AVAILABLE state.
	nicID := ids.NewID(ids.PrefixSubnet)
	{
		w, err := r.Writer(ctx)
		require.NoError(t, err)
		_, err = w.NetworkInterfaces().Insert(ctx, &domain.NetworkInterface{
			ID: nicID, ProjectID: folderID, Name: domain.RcNameVPC("nic-cas"),
			SubnetID: subnetID, MAC: "0e:11:22:33:44:aa", Status: domain.NIStatusAvailable,
		})
		require.NoError(t, err)
		require.NoError(t, w.Commit())
	}

	// Action: 5 goroutine'ов concurrent attach к разным instance'ам.
	const n = 5
	var wg sync.WaitGroup
	results := make([]error, n)
	wg.Add(n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			<-start
			w, werr := r.Writer(ctx)
			if werr != nil {
				results[i] = werr
				return
			}
			defer w.Abort()
			_, aerr := w.NetworkInterfaces().AttachToInstance(ctx, nicID, "compute_instance",
				ids.NewID(ids.PrefixSubnet), "")
			if aerr != nil {
				results[i] = aerr
				return
			}
			results[i] = w.Commit()
		}(i)
	}
	close(start)
	wg.Wait()

	// Assert: ровно одна попытка успешна, остальные — ErrFailedPrecondition.
	var success, failed int
	for _, err := range results {
		switch {
		case err == nil:
			success++
		case errors.Is(err, repo.ErrFailedPrecondition):
			failed++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	assert.Equal(t, 1, success, "ровно одна Attach попытка должна была закоммититься")
	assert.Equal(t, n-1, failed, "остальные должны вернуть ErrFailedPrecondition (CAS-конфликт)")

	// State consistency: NIC должен быть attached к ровно одному owner-у.
	rd, err := r.Reader(ctx)
	require.NoError(t, err)
	defer func() { _ = rd.Close() }()
	got, err := rd.NetworkInterfaces().Get(ctx, nicID)
	require.NoError(t, err)
	assert.NotEmpty(t, got.UsedByID, "NIC должен быть attached")
	assert.Equal(t, "compute_instance", got.UsedByType)
	assert.Equal(t, domain.NIStatusActive, got.Status)
}

// TestCQRS_NIC_AttachToInstance_IdempotentReattach — attach с тем же owner-id
// поверх уже-attached NIC должен пройти (idempotent re-attach, parity с
// legacy SetUsedBy).
func TestCQRS_NIC_AttachToInstance_IdempotentReattach(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	dsn := setupTestDB(t)
	folderID, subnetID := insertSubnetForNIC(t, ctx, dsn)

	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()
	r := kachopg.New(pool, nil)

	nicID := ids.NewID(ids.PrefixSubnet)
	instanceID := ids.NewID(ids.PrefixSubnet)
	// Setup + первый attach.
	{
		w, err := r.Writer(ctx)
		require.NoError(t, err)
		_, err = w.NetworkInterfaces().Insert(ctx, &domain.NetworkInterface{
			ID: nicID, ProjectID: folderID, Name: domain.RcNameVPC("nic-reattach"),
			SubnetID: subnetID, MAC: "0e:11:22:33:44:bb", Status: domain.NIStatusAvailable,
		})
		require.NoError(t, err)
		_, err = w.NetworkInterfaces().AttachToInstance(ctx, nicID, "compute_instance", instanceID, "")
		require.NoError(t, err)
		require.NoError(t, w.Commit())
	}
	// Idempotent re-attach к тому же instanceID.
	w, err := r.Writer(ctx)
	require.NoError(t, err)
	defer w.Abort()
	got, err := w.NetworkInterfaces().AttachToInstance(ctx, nicID, "compute_instance", instanceID, "")
	require.NoError(t, err, "idempotent re-attach к тому же owner должен пройти")
	assert.Equal(t, instanceID, got.UsedByID)
	require.NoError(t, w.Commit())

}
