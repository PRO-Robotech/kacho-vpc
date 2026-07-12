// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package pg_test

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	coredb "github.com/PRO-Robotech/kacho-corelib/db"
	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/migrations"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/helpers"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
	kachopg "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/pg"
)

// Integration-тесты NIC↔Instance attach-CAS (InternalNetworkInterfaceService side,
// §3a): multi-NIC на инстанс, NIC→≤1 инстанс (in-use), zone-coherence + anycast,
// auto-index concurrency, идемпотентность. Testcontainers Postgres + goose (включая
// 0014_network_interface_used_by_index).

// nicAttachEnv — общий контейнер БД + repo для одного теста.
type nicAttachEnv struct {
	ctx  context.Context
	dsn  string
	pool *pgxpool.Pool
	repo kacho.Repository
}

func newNICAttachEnv(t *testing.T) *nicAttachEnv {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	dsn := setupTestDB(t)
	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return &nicAttachEnv{ctx: ctx, dsn: dsn, pool: pool, repo: kachopg.New(pool, nil)}
}

// makeProjectNetwork — создаёт project + network, возвращает их id.
func (e *nicAttachEnv) makeNetwork(t *testing.T, projectID string) string {
	t.Helper()
	netID := ids.NewID(ids.PrefixNetwork)
	_, err := e.pool.Exec(e.ctx,
		`INSERT INTO networks(id, project_id, name, description, labels) VALUES ($1,$2,$3,'','{}'::jsonb)`,
		netID, projectID, "net-attach")
	require.NoError(t, err)
	return netID
}

// makeZonalSubnet — ZONAL-подсеть в зоне zone с CIDR cidr (per-network non-overlap
// EXCLUDE требует различных CIDR у подсетей одной сети).
func (e *nicAttachEnv) makeZonalSubnet(t *testing.T, projectID, netID, zone, cidr string) string {
	t.Helper()
	subnetID := ids.NewID(ids.PrefixSubnet)
	_, err := e.pool.Exec(e.ctx,
		`INSERT INTO subnets(id, project_id, network_id, zone_id, region_id, placement_type, name, description, labels, v4_cidr_blocks, v6_cidr_blocks)
		 VALUES ($1,$2,$3,$4,'', 'ZONAL', $5,'','{}'::jsonb, ARRAY[$6]::text[], ARRAY[]::text[])`,
		subnetID, projectID, netID, zone, "", cidr)
	require.NoError(t, err)
	return subnetID
}

// makeRegionalSubnet — REGIONAL(anycast)-подсеть в регионе region (zone_id=”).
func (e *nicAttachEnv) makeRegionalSubnet(t *testing.T, projectID, netID, region, cidr string) string {
	t.Helper()
	subnetID := ids.NewID(ids.PrefixSubnet)
	_, err := e.pool.Exec(e.ctx,
		`INSERT INTO subnets(id, project_id, network_id, zone_id, region_id, placement_type, name, description, labels, v4_cidr_blocks, v6_cidr_blocks)
		 VALUES ($1,$2,$3,'', $4, 'REGIONAL', $5,'','{}'::jsonb, ARRAY[$6]::text[], ARRAY[]::text[])`,
		subnetID, projectID, netID, region, "", cidr)
	require.NoError(t, err)
	return subnetID
}

// makeFreeNIC — DETACHED NIC (used_by_id=”) в подсети subnetID.
func (e *nicAttachEnv) makeFreeNIC(t *testing.T, projectID, subnetID, mac string) string {
	t.Helper()
	nicID := ids.NewID(ids.PrefixNetworkInterface)
	_, err := e.pool.Exec(e.ctx,
		`INSERT INTO network_interfaces(id, project_id, name, description, labels, subnet_id, security_group_ids, v4_address_ids, v6_address_ids, status, mac_address, used_by_type, used_by_id, used_by_name)
		 VALUES ($1,$2,'','','{}'::jsonb,$3,'[]'::jsonb,'[]'::jsonb,'[]'::jsonb,'AVAILABLE',$4,'','','')`,
		nicID, projectID, subnetID, mac)
	require.NoError(t, err)
	return nicID
}

func (e *nicAttachEnv) writer(t *testing.T) kacho.RepositoryWriter {
	t.Helper()
	w, err := e.repo.Writer(e.ctx)
	require.NoError(t, err)
	return w
}

// attach — открывает Writer, делает AttachToInstance, commit при успехе.
func (e *nicAttachEnv) attach(p kacho.AttachNICParams) (*kacho.NetworkInterfaceRecord, error) {
	w, err := e.repo.Writer(e.ctx)
	if err != nil {
		return nil, err
	}
	rec, err := w.NetworkInterfaces().AttachToInstance(e.ctx, p)
	if err != nil {
		w.Abort()
		return nil, err
	}
	if cerr := w.Commit(); cerr != nil {
		return nil, cerr
	}
	return rec, nil
}

// TestNICAttach_MultiNIC_OK [S4-01/A10] — два NIC на один инстанс, auto-index → 0 и 1.
func TestNICAttach_MultiNIC_OK(t *testing.T) {
	e := newNICAttachEnv(t)
	projectID := "prj-multi-nic"
	netID := e.makeNetwork(t, projectID)
	subnetID := e.makeZonalSubnet(t, projectID, netID, "zone-a", "10.0.0.0/24")
	nic1 := e.makeFreeNIC(t, projectID, subnetID, "0e:00:00:00:00:01")
	nic2 := e.makeFreeNIC(t, projectID, subnetID, "0e:00:00:00:00:02")
	const instanceID = "epdinst0000000001"

	r1, err := e.attach(kacho.AttachNICParams{NICID: nic1, InstanceID: instanceID, InstanceName: "vm-1", InstanceZoneID: "zone-a", ProjectID: projectID, Index: kacho.AutoIndex})
	require.NoError(t, err)
	assert.Equal(t, instanceID, r1.UsedByID)
	assert.Equal(t, "compute_instance", r1.UsedByType)
	assert.Equal(t, domain.NIStatusActive, r1.Status)

	r2, err := e.attach(kacho.AttachNICParams{NICID: nic2, InstanceID: instanceID, InstanceName: "vm-1", InstanceZoneID: "zone-a", ProjectID: projectID, Index: kacho.AutoIndex})
	require.NoError(t, err)
	assert.Equal(t, instanceID, r2.UsedByID)

	// Оба привязаны, слоты 0 и 1 (проверяем через ListByInstanceIDs).
	rd, err := e.repo.Reader(e.ctx)
	require.NoError(t, err)
	defer func() { _ = rd.Close() }()
	att, err := rd.NetworkInterfaces().ListByInstanceIDs(e.ctx, []string{instanceID})
	require.NoError(t, err)
	require.Len(t, att, 2)
	slots := map[int32]bool{att[0].Index: true, att[1].Index: true}
	assert.True(t, slots[0] && slots[1], "auto-index должен назначить слоты 0 и 1, got %v", slots)
}

// TestNICAttach_InUse_Concurrent [S4-04/A11] — один свободный NIC, два инстанса
// конкурентно: ровно один OK, другой ErrNICInUse. Детерминированный start-gate
// (barrier), не time.Sleep. Под -race.
func TestNICAttach_InUse_Concurrent(t *testing.T) {
	e := newNICAttachEnv(t)
	projectID := "prj-inuse"
	netID := e.makeNetwork(t, projectID)
	subnetID := e.makeZonalSubnet(t, projectID, netID, "zone-a", "10.0.0.0/24")
	nic := e.makeFreeNIC(t, projectID, subnetID, "0e:00:00:00:0a:01")

	instances := []string{"epdinsta000000001", "epdinstb000000002"}
	var start sync.WaitGroup
	start.Add(1) // barrier: обе горутины ждут одновременного старта
	var wg sync.WaitGroup
	results := make([]error, 2)
	for i := range instances {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			start.Wait() // детерминированный старт: оба стартуют вместе
			_, err := e.attach(kacho.AttachNICParams{NICID: nic, InstanceID: instances[idx], InstanceName: "vm", InstanceZoneID: "zone-a", ProjectID: projectID, Index: kacho.AutoIndex})
			results[idx] = err
		}(i)
	}
	start.Done() // release barrier
	wg.Wait()

	okCount, inUseCount := 0, 0
	for _, err := range results {
		switch {
		case err == nil:
			okCount++
		case errors.Is(err, helpers.ErrNICInUse):
			inUseCount++
		default:
			t.Fatalf("unexpected attach error: %v", err)
		}
	}
	assert.Equal(t, 1, okCount, "ровно один attach должен пройти")
	assert.Equal(t, 1, inUseCount, "второй → ErrNICInUse (NIC → ≤1 инстанс)")
}

// TestNICAttach_ZoneCoherence [S4-03/A13] — ZONAL чужой зоны → zone-mismatch;
// REGIONAL(anycast) → OK (zone-check пропущен); ZONAL своей зоны → OK.
func TestNICAttach_ZoneCoherence(t *testing.T) {
	e := newNICAttachEnv(t)
	projectID := "prj-zone"
	netID := e.makeNetwork(t, projectID)
	subZ1 := e.makeZonalSubnet(t, projectID, netID, "zone-1", "10.0.1.0/24")
	subZ2 := e.makeZonalSubnet(t, projectID, netID, "zone-2", "10.0.2.0/24")
	subReg := e.makeRegionalSubnet(t, projectID, netID, "region-1", "10.0.3.0/24")
	nicZ1 := e.makeFreeNIC(t, projectID, subZ1, "0e:00:00:00:0b:01")
	nicZ2 := e.makeFreeNIC(t, projectID, subZ2, "0e:00:00:00:0b:02")
	nicReg := e.makeFreeNIC(t, projectID, subReg, "0e:00:00:00:0b:03")
	const instanceZone = "zone-1"

	// ZONAL чужой зоны (zone-2 != instance zone-1) → NICZoneMismatchError.
	_, err := e.attach(kacho.AttachNICParams{NICID: nicZ2, InstanceID: "epdinstz000000001", InstanceName: "vm", InstanceZoneID: instanceZone, ProjectID: projectID, Index: kacho.AutoIndex})
	var zerr *helpers.NICZoneMismatchError
	require.True(t, errors.As(err, &zerr), "ожидался NICZoneMismatchError, got %v", err)
	assert.Equal(t, "zone-2", zerr.SubnetZone)
	assert.Equal(t, "zone-1", zerr.InstanceZone)

	// REGIONAL(anycast) → OK (zone-check пропущен).
	_, err = e.attach(kacho.AttachNICParams{NICID: nicReg, InstanceID: "epdinstr000000002", InstanceName: "vm", InstanceZoneID: instanceZone, ProjectID: projectID, Index: kacho.AutoIndex})
	require.NoError(t, err, "REGIONAL/anycast subnet — zone-check пропущен")

	// ZONAL своей зоны (zone-1 == instance zone-1) → OK.
	_, err = e.attach(kacho.AttachNICParams{NICID: nicZ1, InstanceID: "epdinstz100000003", InstanceName: "vm", InstanceZoneID: instanceZone, ProjectID: projectID, Index: kacho.AutoIndex})
	require.NoError(t, err, "ZONAL той же зоны → OK")
}

// TestNICAttach_AutoIndex_Concurrent [S4-02] — два NIC auto-index конкурентно на
// один инстанс → распределены разные слоты (0 и 1), lost-update нет
// (partial UNIQUE + retry). Под -race, детерминированный start-gate.
func TestNICAttach_AutoIndex_Concurrent(t *testing.T) {
	e := newNICAttachEnv(t)
	projectID := "prj-autoidx"
	netID := e.makeNetwork(t, projectID)
	subnetID := e.makeZonalSubnet(t, projectID, netID, "zone-a", "10.0.0.0/24")
	nicA := e.makeFreeNIC(t, projectID, subnetID, "0e:00:00:00:0c:01")
	nicB := e.makeFreeNIC(t, projectID, subnetID, "0e:00:00:00:0c:02")
	const instanceID = "epdinstc000000001"

	nics := []string{nicA, nicB}
	var start sync.WaitGroup
	start.Add(1)
	var wg sync.WaitGroup
	errs := make([]error, 2)
	// Attach с service-уровня auto-index retry: см. nicinternal.Service. На repo-уровне
	// одиночная попытка может вернуть ErrNICIndexTaken при коллизии слота — здесь
	// эмулируем service-retry небольшим циклом (детерминированно завершается).
	attachWithRetry := func(nicID string) error {
		for i := 0; i < 16; i++ {
			_, err := e.attach(kacho.AttachNICParams{NICID: nicID, InstanceID: instanceID, InstanceName: "vm", InstanceZoneID: "zone-a", ProjectID: projectID, Index: kacho.AutoIndex})
			if errors.Is(err, helpers.ErrNICIndexTaken) {
				continue
			}
			return err
		}
		return errors.New("auto-index retry exhausted")
	}
	for i := range nics {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			start.Wait()
			errs[idx] = attachWithRetry(nics[idx])
		}(i)
	}
	start.Done()
	wg.Wait()
	require.NoError(t, errs[0])
	require.NoError(t, errs[1])

	rd, err := e.repo.Reader(e.ctx)
	require.NoError(t, err)
	defer func() { _ = rd.Close() }()
	att, err := rd.NetworkInterfaces().ListByInstanceIDs(e.ctx, []string{instanceID})
	require.NoError(t, err)
	require.Len(t, att, 2)
	assert.NotEqual(t, att[0].Index, att[1].Index, "разные слоты, lost-update нет")
	slots := map[int32]bool{att[0].Index: true, att[1].Index: true}
	assert.True(t, slots[0] && slots[1], "слоты {0,1}, got %v", slots)
}

// TestNICAttach_IdempotentReplay [S2-02/A analog] — повтор attach того же NIC на тот
// же инстанс → OK, ровно одна привязка, слот сохранён.
func TestNICAttach_IdempotentReplay(t *testing.T) {
	e := newNICAttachEnv(t)
	projectID := "prj-replay"
	netID := e.makeNetwork(t, projectID)
	subnetID := e.makeZonalSubnet(t, projectID, netID, "zone-a", "10.0.0.0/24")
	nic := e.makeFreeNIC(t, projectID, subnetID, "0e:00:00:00:0d:01")
	const instanceID = "epdinstd000000001"

	p := kacho.AttachNICParams{NICID: nic, InstanceID: instanceID, InstanceName: "vm", InstanceZoneID: "zone-a", ProjectID: projectID, Index: kacho.AutoIndex}
	r1, err := e.attach(p)
	require.NoError(t, err)
	r2, err := e.attach(p) // replay
	require.NoError(t, err, "идемпотентный replay → OK")
	assert.Equal(t, r1.UsedByID, r2.UsedByID)

	rd, err := e.repo.Reader(e.ctx)
	require.NoError(t, err)
	defer func() { _ = rd.Close() }()
	att, err := rd.NetworkInterfaces().ListByInstanceIDs(e.ctx, []string{instanceID})
	require.NoError(t, err)
	require.Len(t, att, 1, "ровно одна привязка после replay")
}

// TestNICDetach_Idempotent [S4-06/A6] — detach → OK; повтор detach (уже отвязан) →
// OK no-op без ошибки.
func TestNICDetach_Idempotent(t *testing.T) {
	e := newNICAttachEnv(t)
	projectID := "prj-detach"
	netID := e.makeNetwork(t, projectID)
	subnetID := e.makeZonalSubnet(t, projectID, netID, "zone-a", "10.0.0.0/24")
	nic := e.makeFreeNIC(t, projectID, subnetID, "0e:00:00:00:0e:01")
	const instanceID = "epdinste000000001"

	_, err := e.attach(kacho.AttachNICParams{NICID: nic, InstanceID: instanceID, InstanceName: "vm", InstanceZoneID: "zone-a", ProjectID: projectID, Index: kacho.AutoIndex})
	require.NoError(t, err)

	detach := func() (*kacho.NetworkInterfaceRecord, error) {
		w := e.writer(t)
		rec, derr := w.NetworkInterfaces().DetachFromInstance(e.ctx, nic, instanceID)
		if derr != nil {
			w.Abort()
			return nil, derr
		}
		return rec, w.Commit()
	}

	r1, err := detach()
	require.NoError(t, err)
	assert.Equal(t, "", r1.UsedByID, "detach очищает used_by_id")

	r2, err := detach() // повтор — идемпотентно
	require.NoError(t, err, "повторный detach → OK no-op")
	assert.Equal(t, "", r2.UsedByID)
}

// colExists — есть ли колонка used_by_index на network_interfaces.
func usedByIndexColExists(t *testing.T, ctx context.Context, db *sql.DB) bool {
	t.Helper()
	var n int
	err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM information_schema.columns
		  WHERE table_schema='kacho_vpc' AND table_name='network_interfaces' AND column_name='used_by_index'`).Scan(&n)
	require.NoError(t, err)
	return n > 0
}

// TestMigration0014_UsedByIndex_UpDownRoundtrip [step 2] — реальный goose up/down:
// UpTo 14 → колонка used_by_index + partial UNIQUE ni_used_by_index_uniq присутствуют;
// DownTo 13 → сняты; UpTo 14 снова → идемпотентно восстановлены.
func TestMigration0014_UsedByIndex_UpDownRoundtrip(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	pgc, err := postgres.Run(ctx, "postgres:16-alpine",
		postgres.WithDatabase("kacho_vpc_test"),
		postgres.WithUsername("vpc"), postgres.WithPassword("vpc"),
		postgres.BasicWaitStrategies())
	require.NoError(t, err)
	t.Cleanup(func() { _ = pgc.Terminate(ctx) })
	dsn, err := pgc.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	db, err := sql.Open("pgx", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	goose.SetBaseFS(migrations.FS)
	require.NoError(t, goose.SetDialect("postgres"))

	require.NoError(t, goose.UpTo(db, ".", 14))
	assert.True(t, usedByIndexColExists(t, ctx, db), "после UpTo 14 колонка used_by_index есть")
	var idxCount int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT count(*) FROM pg_indexes WHERE schemaname='kacho_vpc' AND indexname='ni_used_by_index_uniq'`).Scan(&idxCount))
	assert.Equal(t, 1, idxCount, "partial UNIQUE ni_used_by_index_uniq создан")

	require.NoError(t, goose.DownTo(db, ".", 13), "goose down 14→13")
	assert.False(t, usedByIndexColExists(t, ctx, db), "после DownTo 13 колонка used_by_index снята")

	require.NoError(t, goose.UpTo(db, ".", 14), "goose up 13→14 повторно")
	assert.True(t, usedByIndexColExists(t, ctx, db), "идемпотентно восстановлена")
}
