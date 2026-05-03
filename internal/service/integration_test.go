package service_test

import (
	"context"
	"io/fs"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	"github.com/testcontainers/testcontainers-go"

	coredb "github.com/PRO-Robotech/kacho-corelib/db"
	"github.com/PRO-Robotech/kacho-corelib/outbox"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/migrations"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	"github.com/PRO-Robotech/kacho-vpc/internal/service"
)

// noopFolderClient всегда возвращает Exists=true (для тестов без resource-manager).
type noopFolderClient struct{}

func (n *noopFolderClient) Exists(_ context.Context, _ string) (bool, error) {
	return true, nil
}

// ---------- DB Setup ----------

func applyMigrationsDirect(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()

	files, err := fs.ReadDir(migrations.FS, ".")
	require.NoError(t, err)

	conn, err := pool.Acquire(ctx)
	require.NoError(t, err)
	defer conn.Release()

	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".sql") {
			continue
		}
		data, readErr := migrations.FS.ReadFile(f.Name())
		require.NoError(t, readErr)

		sql := extractUpSection(string(data))
		if sql == "" {
			continue
		}

		_, execErr := conn.Exec(ctx, sql)
		require.NoError(t, execErr, "migration %s failed", f.Name())
	}
}

func extractUpSection(content string) string {
	lines := strings.Split(content, "\n")
	var inUp bool
	var result []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "-- +goose Up" {
			inUp = true
			continue
		}
		if trimmed == "-- +goose Down" {
			break
		}
		// Пропускаем goose StatementBegin/End аннотации
		if trimmed == "-- +goose StatementBegin" || trimmed == "-- +goose StatementEnd" {
			continue
		}
		if inUp {
			result = append(result, line)
		}
	}
	return strings.Join(result, "\n")
}

func setupTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()

	ctx := context.Background()

	pg, err := tcpostgres.Run(ctx,
		"postgres:16",
		tcpostgres.WithDatabase("testdb"),
		tcpostgres.WithUsername("testuser"),
		tcpostgres.WithPassword("testpass"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
		),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = pg.Terminate(ctx) })

	connStr, err := pg.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	pool, err := coredb.NewPool(ctx, connStr)
	require.NoError(t, err)
	t.Cleanup(func() { pool.Close() })

	applyMigrationsDirect(t, pool)

	return pool
}

func setupServices(t *testing.T, pool *pgxpool.Pool) (
	*service.NetworkService,
	*service.SubnetService,
	*service.SecurityGroupService,
	*service.RouteTableService,
	*service.AddressService,
) {
	t.Helper()
	transactor := coredb.NewTransactor(pool)
	outboxWriter := outbox.NewWriter("kacho_vpc")
	fc := &noopFolderClient{}

	networkRepo := repo.NewNetworkRepo(pool, transactor, outboxWriter)
	subnetRepo := repo.NewSubnetRepo(pool, transactor, outboxWriter)
	sgRepo := repo.NewSecurityGroupRepo(pool, transactor, outboxWriter)
	rtRepo := repo.NewRouteTableRepo(pool, transactor, outboxWriter)
	addrRepo := repo.NewAddressRepo(pool, transactor, outboxWriter)

	networkSvc := service.NewNetworkService(networkRepo, fc)
	subnetSvc := service.NewSubnetService(subnetRepo, networkRepo, fc)
	sgSvc := service.NewSecurityGroupService(sgRepo, networkRepo, fc)
	rtSvc := service.NewRouteTableService(rtRepo, networkRepo, fc)
	addrSvc := service.NewAddressService(addrRepo, fc)

	return networkSvc, subnetSvc, sgSvc, rtSvc, addrSvc
}

// ---------- Integration Tests ----------

const (
	testFolderID  = "10000000-0000-0000-0000-000000000001"
	testCloudID   = "20000000-0000-0000-0000-000000000001"
	testOrgID     = "30000000-0000-0000-0000-000000000001"
)

// B1: Создание Network через реальный Postgres.
func TestIntegration_B1_NetworkCreate(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test skipped in short mode")
	}
	pool := setupTestDB(t)
	networkSvc, _, _, _, _ := setupServices(t, pool)

	net, err := networkSvc.Upsert(context.Background(), &domain.Network{
		Name:           "test-network",
		FolderID:       testFolderID,
		CloudID:        testCloudID,
		OrganizationID: testOrgID,
	})
	require.NoError(t, err)
	assert.NotEmpty(t, net.UID)
	assert.Equal(t, "test-network", net.Name)
	assert.Equal(t, "ACTIVE", net.State)
	assert.Greater(t, net.ResourceVersion, int64(0))
}

// B5: Удаление Network с зависимостями → FAILED_PRECONDITION.
func TestIntegration_B5_NetworkDeleteWithDeps(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test skipped in short mode")
	}
	pool := setupTestDB(t)
	networkSvc, subnetSvc, _, _, _ := setupServices(t, pool)
	ctx := context.Background()

	net, err := networkSvc.Upsert(ctx, &domain.Network{
		Name: "net-with-deps", FolderID: testFolderID,
		CloudID: testCloudID, OrganizationID: testOrgID,
	})
	require.NoError(t, err)

	_, err = subnetSvc.Upsert(ctx, &domain.Subnet{
		Name:      "subnet-dep",
		FolderID:  testFolderID,
		NetworkID: net.UID,
		CIDRBlock: "10.0.0.0/24",
	})
	require.NoError(t, err)

	err = networkSvc.Delete(ctx, net.UID)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
}

// C1: Создание Subnet с валидным network_id.
func TestIntegration_C1_SubnetCreate(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test skipped in short mode")
	}
	pool := setupTestDB(t)
	networkSvc, subnetSvc, _, _, _ := setupServices(t, pool)
	ctx := context.Background()

	folderID2 := "10000000-0000-0000-0000-000000000002"

	net, err := networkSvc.Upsert(ctx, &domain.Network{
		Name: "parent-net", FolderID: folderID2,
		CloudID: testCloudID, OrganizationID: testOrgID,
	})
	require.NoError(t, err)

	subnet, err := subnetSvc.Upsert(ctx, &domain.Subnet{
		Name:      "my-subnet",
		FolderID:  folderID2,
		NetworkID: net.UID,
		CIDRBlock: "192.168.0.0/24",
	})
	require.NoError(t, err)
	assert.NotEmpty(t, subnet.UID)
	assert.Equal(t, "ACTIVE", subnet.State)
	assert.Equal(t, net.UID, subnet.NetworkID)
}

// D3: SG Upsert — full-replace правил с новыми server-assigned UUIDs.
func TestIntegration_D3_SGRulesFullReplace(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test skipped in short mode")
	}
	pool := setupTestDB(t)
	networkSvc, _, sgSvc, _, _ := setupServices(t, pool)
	ctx := context.Background()

	folderID := "00000000-0000-0000-0000-000000000003"
	net, err := networkSvc.Upsert(ctx, &domain.Network{
		Name: "sg-net", FolderID: folderID,
		CloudID: testCloudID, OrganizationID: testOrgID,
	})
	require.NoError(t, err)

	// Первый upsert с 2 правилами
	sg1, err := sgSvc.Upsert(ctx, &domain.SecurityGroup{
		Name:      "sg-test",
		FolderID:  folderID,
		NetworkID: net.UID,
		Rules: []domain.SecurityGroupRule{
			{Direction: "INGRESS", Protocol: "TCP", PortRangeMin: 80, PortRangeMax: 80},
			{Direction: "EGRESS", Protocol: "ANY"},
		},
	})
	require.NoError(t, err)
	assert.Len(t, sg1.Rules, 2)
	// server-assigned IDs
	for _, r := range sg1.Rules {
		assert.NotEmpty(t, r.ID)
	}
	oldRuleIDs := []string{sg1.Rules[0].ID, sg1.Rules[1].ID}

	// Второй upsert с 1 правилом — full-replace
	sg2, err := sgSvc.Upsert(ctx, &domain.SecurityGroup{
		Name:      "sg-test",
		FolderID:  folderID,
		NetworkID: net.UID,
		Rules: []domain.SecurityGroupRule{
			{Direction: "INGRESS", Protocol: "UDP"},
		},
	})
	require.NoError(t, err)
	assert.Len(t, sg2.Rules, 1, "должно быть только 1 правило после full-replace")
	// Новый ID должен быть отличен от старых
	assert.NotContains(t, oldRuleIDs, sg2.Rules[0].ID)
}

// F1: Создание Address с автоматическим IP из 203.0.113.0/24.
func TestIntegration_F1_AddressAllocatesIP(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test skipped in short mode")
	}
	pool := setupTestDB(t)
	_, _, _, _, addrSvc := setupServices(t, pool)
	ctx := context.Background()

	addr, err := addrSvc.Upsert(ctx, &domain.Address{
		Name:           "my-ip",
		FolderID:       "00000000-0000-0000-0000-000000000004",
		CloudID:        testCloudID,
		OrganizationID: testOrgID,
	})
	require.NoError(t, err)
	assert.Equal(t, "RESERVED", addr.State)
	assert.Equal(t, "EXTERNAL", addr.AddressType)
	assert.True(t, strings.HasPrefix(addr.AllocatedIPv4, "203.0.113."), "IP must be from 203.0.113.0/24, got: "+addr.AllocatedIPv4)
}

// G1: NetworkExists возвращает true для существующей сети.
func TestIntegration_G1_NetworkExists(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test skipped in short mode")
	}
	pool := setupTestDB(t)
	networkSvc, _, _, _, _ := setupServices(t, pool)
	ctx := context.Background()

	net, err := networkSvc.Upsert(ctx, &domain.Network{
		Name:           "exists-net",
		FolderID:       "00000000-0000-0000-0000-000000000005",
		CloudID:        testCloudID,
		OrganizationID: testOrgID,
	})
	require.NoError(t, err)

	found, err := networkSvc.GetByUID(ctx, net.UID)
	require.NoError(t, err)
	assert.NotNil(t, found)
	assert.Equal(t, net.UID, found.UID)
}

// H4: FolderClient недоступен → ошибка при создании сети.
func TestIntegration_H4_FolderClientUnavailable(t *testing.T) {
	netRepo := new(MockNetworkRepo)

	// FolderClient, который возвращает ошибку
	failFC := &failFolderClient{}
	svc := service.NewNetworkService(netRepo, failFC)

	_, err := svc.Upsert(context.Background(), &domain.Network{
		Name:     "net",
		FolderID: "00000000-0000-0000-0000-000000000006",
	})

	require.Error(t, err)
	// Ошибка должна прокинуться из FolderClient
}

// failFolderClient имитирует недоступный resource-manager.
type failFolderClient struct{}

func (f *failFolderClient) Exists(_ context.Context, _ string) (bool, error) {
	return false, status.Error(codes.Unavailable, "connection refused")
}
