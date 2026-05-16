package repo_test

import (
	"context"
	"database/sql"
	"strings"
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
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
)

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

	// KAC-94 (миграция 0034): схема `public` → `kacho_vpc`. Production-DSN
	// получает search_path через config.baseDSN(); тесты строят DSN из
	// testcontainers напрямую, поэтому добавляем то же значение здесь —
	// иначе любой query из pgxpool после migrations не найдёт таблицы.
	return appendSearchPathOptions(dsn)
}

// appendSearchPathOptions добавляет libpq `options=-c search_path=kacho_vpc,public`
// в DSN (если ещё не задан). См. config.baseDSN — production-эквивалент.
func appendSearchPathOptions(dsn string) string {
	const optionsParam = "options=-c%20search_path%3Dkacho_vpc%2Cpublic"
	if strings.Contains(dsn, "options=") || strings.Contains(dsn, "options%3D") {
		return dsn
	}
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	return dsn + sep + optionsParam
}

func TestIntegration_NetworkRepo_CRUD(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx := context.Background()
	dsn := setupTestDB(t)

	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	r := repo.NewNetworkRepo(pool)

	// Insert. Wave 2 pilot (KAC-99/KAC-94): domain.Network больше не несёт
	// CreatedAt (DB-managed) и принимает newtypes для Name/Description/Labels.
	n := &domain.Network{
		ID:          ids.NewUID(),
		FolderID:    "folder-1",
		Name:        domain.RcNameVPC("test-network"),
		Description: domain.RcDescription("test"),
		Labels:      domain.LabelsFromMap(map[string]string{"env": "test"}),
	}
	created, err := r.Insert(ctx, n)
	require.NoError(t, err)
	assert.Equal(t, n.ID, created.ID)
	assert.Equal(t, domain.RcNameVPC("test-network"), created.Name)
	val, _ := created.Labels.Get("env")
	assert.Equal(t, domain.LabelVal("test"), val)

	// Get
	got, err := r.Get(ctx, n.ID)
	require.NoError(t, err)
	assert.Equal(t, n.ID, got.ID)

	// List
	nets, nextToken, err := r.List(ctx, repo.NetworkFilter{FolderID: "folder-1"}, repo.Pagination{})
	require.NoError(t, err)
	assert.Len(t, nets, 1)
	assert.Empty(t, nextToken)

	// Update — Update принимает domain.Network (без CreatedAt). Берём embedded
	// из repo-entity, меняем нужные поля.
	got.Name = domain.RcNameVPC("updated-network")
	got.Description = domain.RcDescription("updated")
	updated, err := r.Update(ctx, &got.Network)
	require.NoError(t, err)
	assert.Equal(t, domain.RcNameVPC("updated-network"), updated.Name)

	// Delete
	err = r.Delete(ctx, n.ID)
	require.NoError(t, err)

	// Get after delete — должна вернуть ErrNotFound
	_, err = r.Get(ctx, n.ID)
	assert.ErrorIs(t, err, repo.ErrNotFound)
}

// TestIntegration_NetworkRepo_VPNIDAllocation — удалён в KAC-79/KAC-36
// (post-kube-ovn: vpn_id allocation больше не выполняется kacho-vpc, kube-ovn
// управляет underlay сам).

func TestIntegration_SubnetRepo_CidrBlocks(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx := context.Background()
	dsn := setupTestDB(t)

	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	nr := repo.NewNetworkRepo(pool)
	sr := repo.NewSubnetRepo(pool)

	// Сначала создаём network (FK constraint). CreatedAt больше не в domain
	// (KAC-99/KAC-94), DB-managed via repo.
	net := &domain.Network{
		ID:       ids.NewUID(),
		FolderID: "folder-1",
		Name:     domain.RcNameVPC("net-for-subnet"),
	}
	_, err = nr.Insert(ctx, net)
	require.NoError(t, err)

	// Insert subnet с несколькими CIDRs
	sub := &domain.Subnet{
		ID:           ids.NewUID(),
		FolderID:     "folder-1",
		Name:         domain.RcNameVPC("test-subnet"),
		NetworkID:    net.ID,
		ZoneID:       "ru-central1-a",
		V4CidrBlocks: []string{"10.0.0.0/24", "10.1.0.0/24"},
	}
	created, err := sr.Insert(ctx, sub)
	require.NoError(t, err)
	assert.Equal(t, []string{"10.0.0.0/24", "10.1.0.0/24"}, created.V4CidrBlocks)

	// Get
	got, err := sr.Get(ctx, sub.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{"10.0.0.0/24", "10.1.0.0/24"}, got.V4CidrBlocks)
	assert.Empty(t, got.V6CidrBlocks)
}

func TestIntegration_AddressRepo_ExternalAndInternal(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx := context.Background()
	dsn := setupTestDB(t)

	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	ar := repo.NewAddressRepo(pool)

	// External address
	extAddr := &domain.Address{
		ID:        ids.NewUID(),
		FolderID:  "folder-1",
		Name:      domain.RcNameVPC("my-external-ip"),
		Type:      domain.AddressTypeExternal,
		IpVersion: domain.IpVersionIPv4,
		Reserved:  true,
		ExternalIpv4: &domain.ExternalIpv4Spec{
			Address: "203.0.113.10",
			ZoneID:  "ru-central1-a",
		},
	}
	created, err := ar.Insert(ctx, extAddr)
	require.NoError(t, err)
	require.NotNil(t, created.ExternalIpv4)
	assert.Equal(t, "203.0.113.10", created.ExternalIpv4.Address)
	assert.Nil(t, created.InternalIpv4)

	// ExistsIP
	exists, err := ar.ExistsIP(ctx, "203.0.113.10")
	require.NoError(t, err)
	assert.True(t, exists)

	exists, err = ar.ExistsIP(ctx, "203.0.113.99")
	require.NoError(t, err)
	assert.False(t, exists)

	// Internal address — addresses.internal_subnet_id has an FK to subnets,
	// so the referenced subnet (and its network) must exist first.
	net := &domain.Network{
		ID:       ids.NewUID(),
		FolderID: "folder-1",
		Name:     domain.RcNameVPC("net-for-internal-addr"),
	}
	_, err = repo.NewNetworkRepo(pool).Insert(ctx, net)
	require.NoError(t, err)
	sub := &domain.Subnet{
		ID:           ids.NewUID(),
		FolderID:     "folder-1",
		Name:         domain.RcNameVPC("sub-for-internal-addr"),
		NetworkID:    net.ID,
		ZoneID:       "ru-central1-a",
		V4CidrBlocks: []string{"10.0.0.0/24"},
	}
	_, err = repo.NewSubnetRepo(pool).Insert(ctx, sub)
	require.NoError(t, err)

	intAddr := &domain.Address{
		ID:        ids.NewUID(),
		FolderID:  "folder-1",
		Type:      domain.AddressTypeInternal,
		IpVersion: domain.IpVersionIPv4,
		InternalIpv4: &domain.InternalIpv4Spec{
			Address:  "10.0.0.5",
			SubnetID: sub.ID,
		},
	}
	created2, err := ar.Insert(ctx, intAddr)
	require.NoError(t, err)
	require.NotNil(t, created2.InternalIpv4)
	assert.Equal(t, "10.0.0.5", created2.InternalIpv4.Address)
}

func TestIntegration_AddressRepo_References(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx := context.Background()
	dsn := setupTestDB(t)

	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	ar := repo.NewAddressRepo(pool)

	addr := &domain.Address{
		ID:        ids.NewUID(),
		FolderID:  "folder-1",
		Name:      domain.RcNameVPC("ref-tracked-ip"),
		Type:      domain.AddressTypeExternal,
		IpVersion: domain.IpVersionIPv4,
		ExternalIpv4: &domain.ExternalIpv4Spec{
			Address: "203.0.113.77",
			ZoneID:  "ru-central1-a",
		},
	}
	created, err := ar.Insert(ctx, addr)
	require.NoError(t, err)
	assert.False(t, created.Used)

	// No reference yet → NotFound.
	_, err = ar.GetReference(ctx, addr.ID)
	require.ErrorIs(t, err, repo.ErrNotFound)

	// SetReference → upsert + used=true.
	ref, err := ar.SetReference(ctx, &domain.AddressReference{
		AddressID:    addr.ID,
		ReferrerType: "compute_instance",
		ReferrerID:   "epdinstance00000001",
		ReferrerName: "vm-1",
	})
	require.NoError(t, err)
	assert.Equal(t, "compute_instance", ref.ReferrerType)
	assert.Equal(t, "epdinstance00000001", ref.ReferrerID)
	assert.Equal(t, "vm-1", ref.ReferrerName)
	assert.False(t, ref.AttachedAt.IsZero())

	got, err := ar.Get(ctx, addr.ID)
	require.NoError(t, err)
	assert.True(t, got.Used)

	// KAC-88: re-set с ДРУГИМ referrer → repo.ErrFailedPrecondition (CAS-guard).
	// До KAC-88 это был silent overwrite — parity-case с инцидентом KAC-52
	// (NIC-attach race). Теперь нужно явно ClearReference перед сменой owner-а.
	_, err = ar.SetReference(ctx, &domain.AddressReference{
		AddressID:    addr.ID,
		ReferrerType: "compute_instance",
		ReferrerID:   "epdinstance00000002",
	})
	require.ErrorIs(t, err, repo.ErrFailedPrecondition,
		"SetReference к занятому address от чужого referrer → repo.ErrFailedPrecondition")

	// Idempotent re-set с ТЕМ ЖЕ referrer (CAS matches) — допустимо обновить
	// referrer_name; address.used остаётся true; addr_references row не меняется.
	ref, err = ar.SetReference(ctx, &domain.AddressReference{
		AddressID:    addr.ID,
		ReferrerType: "compute_instance",
		ReferrerID:   "epdinstance00000001",
		ReferrerName: "vm-1-renamed",
	})
	require.NoError(t, err)
	assert.Equal(t, "epdinstance00000001", ref.ReferrerID)
	assert.Equal(t, "vm-1-renamed", ref.ReferrerName)

	// GetReference returns the (still-original-id) referrer.
	ref, err = ar.GetReference(ctx, addr.ID)
	require.NoError(t, err)
	assert.Equal(t, "epdinstance00000001", ref.ReferrerID)
	assert.Equal(t, "vm-1-renamed", ref.ReferrerName)

	// Batch lookup.
	refs, err := ar.ReferencesForAddresses(ctx, []string{addr.ID, "e9bnonexistent00001"})
	require.NoError(t, err)
	require.Len(t, refs, 1)
	assert.Equal(t, "epdinstance00000001", refs[addr.ID].ReferrerID)

	// ClearReference → used=false, no referrer.
	require.NoError(t, ar.ClearReference(ctx, addr.ID))
	got, err = ar.Get(ctx, addr.ID)
	require.NoError(t, err)
	assert.False(t, got.Used)
	_, err = ar.GetReference(ctx, addr.ID)
	require.ErrorIs(t, err, repo.ErrNotFound)

	// ClearReference again → no-op (still no error, address still exists).
	require.NoError(t, ar.ClearReference(ctx, addr.ID))

	// SetReference on a non-existent address → NotFound.
	_, err = ar.SetReference(ctx, &domain.AddressReference{
		AddressID: "e9bnonexistent00001", ReferrerType: "compute_instance", ReferrerID: "x",
	})
	require.ErrorIs(t, err, repo.ErrNotFound)

	// FK CASCADE: deleting the address removes the reference row too. Set a
	// referrer, then delete the address, then ensure GetReference → NotFound.
	_, err = ar.SetReference(ctx, &domain.AddressReference{
		AddressID: addr.ID, ReferrerType: "compute_instance", ReferrerID: "epdinstance00000003",
	})
	require.NoError(t, err)
	require.NoError(t, ar.Delete(ctx, addr.ID))
	_, err = ar.GetReference(ctx, addr.ID)
	require.ErrorIs(t, err, repo.ErrNotFound)
}

func TestIntegration_RouteTableRepo_StaticRoutes(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx := context.Background()
	dsn := setupTestDB(t)

	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	nr := repo.NewNetworkRepo(pool)
	rtr := repo.NewRouteTableRepo(pool)

	net := &domain.Network{
		ID:       ids.NewUID(),
		FolderID: "folder-1",
		Name:     domain.RcNameVPC("net-for-rt"),
	}
	_, err = nr.Insert(ctx, net)
	require.NoError(t, err)

	rt := &domain.RouteTable{
		ID:        ids.NewUID(),
		FolderID:  "folder-1",
		Name:      domain.RcNameVPC("test-rt"),
		NetworkID: net.ID,
		StaticRoutes: []domain.StaticRoute{
			{DestinationPrefix: "0.0.0.0/0", NextHopAddress: "192.168.0.1"},
			{DestinationPrefix: "10.0.0.0/8", NextHopAddress: "192.168.0.254"},
		},
	}
	created, err := rtr.Insert(ctx, rt)
	require.NoError(t, err)
	require.Len(t, created.StaticRoutes, 2)
	assert.Equal(t, "0.0.0.0/0", created.StaticRoutes[0].DestinationPrefix)

	// Update static routes
	created.StaticRoutes = []domain.StaticRoute{
		{DestinationPrefix: "0.0.0.0/0", NextHopAddress: "10.10.10.1"},
	}
	updated, err := rtr.Update(ctx, &created.RouteTable)
	require.NoError(t, err)
	require.Len(t, updated.StaticRoutes, 1)
	assert.Equal(t, "10.10.10.1", updated.StaticRoutes[0].NextHopAddress)
}
