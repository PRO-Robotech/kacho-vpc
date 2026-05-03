package service_test

import (
	"context"
	"io/fs"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	testcontainers "github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	coredb "github.com/PRO-Robotech/kacho-corelib/db"
	"github.com/PRO-Robotech/kacho-corelib/operations"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/migrations"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	"github.com/PRO-Robotech/kacho-vpc/internal/service"
)

// noopFolderClient всегда возвращает Exists=true.
type noopFolderClient struct{}

func (n *noopFolderClient) Exists(_ context.Context, _ string) (bool, error) {
	return true, nil
}

// ---- DB Setup ----

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
		if trimmed == "-- +goose StatementBegin" || trimmed == "-- +goose StatementEnd" {
			continue
		}
		if inUp {
			result = append(result, line)
		}
	}
	return strings.Join(result, "\n")
}

func setupServices(t *testing.T, pool *pgxpool.Pool) (
	*service.NetworkService,
	*service.SubnetService,
	*service.SecurityGroupService,
	*service.RouteTableService,
	*service.AddressService,
) {
	t.Helper()
	opsRepo := operations.NewRepo(pool, "public")
	fc := &noopFolderClient{}

	networkRepo := repo.NewNetworkRepo(pool)
	subnetRepo := repo.NewSubnetRepo(pool)
	sgRepo := repo.NewSecurityGroupRepo(pool)
	rtRepo := repo.NewRouteTableRepo(pool)
	addrRepo := repo.NewAddressRepo(pool)

	networkSvc := service.NewNetworkService(networkRepo, opsRepo, fc)
	subnetSvc := service.NewSubnetService(subnetRepo, networkRepo, opsRepo, fc)
	sgSvc := service.NewSecurityGroupService(sgRepo, networkRepo, opsRepo, fc)
	rtSvc := service.NewRouteTableService(rtRepo, networkRepo, opsRepo, fc)
	addrSvc := service.NewAddressService(addrRepo, opsRepo, fc)

	return networkSvc, subnetSvc, sgSvc, rtSvc, addrSvc
}

const (
	testFolderID = "10000000-0000-0000-0000-000000000001"
)

// waitForOp ждёт завершения операции, периодически опрашивая opsRepo.
func waitForOp(t *testing.T, pool *pgxpool.Pool, opID string) *operations.Operation {
	t.Helper()
	opsRepo := operations.NewRepo(pool, "public")
	ctx := context.Background()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		op, err := opsRepo.Get(ctx, opID)
		require.NoError(t, err)
		if op.Done {
			return op
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("operation did not complete in time")
	return nil
}

// B1: Создание Network через async Operations.
func TestIntegration_B1_NetworkCreate(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test skipped in short mode")
	}
	pool := setupTestDB(t)
	networkSvc, _, _, _, _ := setupServices(t, pool)
	ctx := context.Background()

	op, err := networkSvc.Create(ctx, testFolderID, "test-network", "desc", nil)
	require.NoError(t, err)
	assert.NotEmpty(t, op.ID)

	// Ждём завершения операции
	finalOp := waitForOp(t, pool, op.ID)
	assert.True(t, finalOp.Done)
	assert.Nil(t, finalOp.Error, "operation should succeed")
	assert.NotNil(t, finalOp.Response)

	// Проверяем, что Network создана в БД
	networkRepo := repo.NewNetworkRepo(pool)
	opsRepo := operations.NewRepo(pool, "public")
	netSvc := service.NewNetworkService(networkRepo, opsRepo, &noopFolderClient{})

	// Список из folder
	nets, _, err := netSvc.List(ctx, service.ListFilter{FolderID: testFolderID})
	require.NoError(t, err)
	assert.Len(t, nets, 1)
	assert.Equal(t, "test-network", nets[0].Name)
}

// B5: Удаление Network с зависимостями → Operation с ошибкой FAILED_PRECONDITION.
func TestIntegration_B5_NetworkDeleteWithDeps(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test skipped in short mode")
	}
	pool := setupTestDB(t)
	networkSvc, subnetSvc, _, _, _ := setupServices(t, pool)
	ctx := context.Background()

	folderID := "10000000-0000-0000-0000-000000000002"
	createOp, err := networkSvc.Create(ctx, folderID, "net-with-deps", "", nil)
	require.NoError(t, err)

	netOp := waitForOp(t, pool, createOp.ID)
	require.True(t, netOp.Done)
	require.Nil(t, netOp.Error)

	// Получаем созданную сеть
	nets, _, err := service.NewNetworkService(repo.NewNetworkRepo(pool), operations.NewRepo(pool, "public"), &noopFolderClient{}).
		List(ctx, service.ListFilter{FolderID: folderID})
	require.NoError(t, err)
	require.Len(t, nets, 1)
	netID := nets[0].ID

	// Создаём Subnet для зависимости
	snOp, err := subnetSvc.Create(ctx, folderID, netID, "ru-central1-a", "10.0.0.0/24", "dep-subnet", "", nil)
	require.NoError(t, err)
	snFinalOp := waitForOp(t, pool, snOp.ID)
	require.True(t, snFinalOp.Done)
	require.Nil(t, snFinalOp.Error)

	// Пытаемся удалить Network
	delOp, err := networkSvc.Delete(ctx, netID)
	require.NoError(t, err)
	delFinalOp := waitForOp(t, pool, delOp.ID)
	assert.True(t, delFinalOp.Done)
	assert.NotNil(t, delFinalOp.Error, "should fail with FAILED_PRECONDITION")
	assert.Equal(t, int32(codes.FailedPrecondition), delFinalOp.Error.Code)
}

// C1: Создание Subnet с валидным CIDR.
func TestIntegration_C1_SubnetCreate(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test skipped in short mode")
	}
	pool := setupTestDB(t)
	networkSvc, subnetSvc, _, _, _ := setupServices(t, pool)
	ctx := context.Background()

	folderID := "10000000-0000-0000-0000-000000000003"
	netOp, err := networkSvc.Create(ctx, folderID, "parent-net", "", nil)
	require.NoError(t, err)
	waitForOp(t, pool, netOp.ID)

	nets, _, err := service.NewNetworkService(repo.NewNetworkRepo(pool), operations.NewRepo(pool, "public"), &noopFolderClient{}).
		List(ctx, service.ListFilter{FolderID: folderID})
	require.NoError(t, err)
	require.Len(t, nets, 1)
	netID := nets[0].ID

	snOp, err := subnetSvc.Create(ctx, folderID, netID, "ru-central1-a", "192.168.0.0/24", "my-subnet", "", nil)
	require.NoError(t, err)
	snFinalOp := waitForOp(t, pool, snOp.ID)
	assert.True(t, snFinalOp.Done)
	assert.Nil(t, snFinalOp.Error)

	// Проверяем что Subnet создана
	subnetRepo := repo.NewSubnetRepo(pool)
	opsRepo := operations.NewRepo(pool, "public")
	snSvc := service.NewSubnetService(subnetRepo, repo.NewNetworkRepo(pool), opsRepo, &noopFolderClient{})
	subnets, _, err := snSvc.List(ctx, service.ListFilter{FolderID: folderID})
	require.NoError(t, err)
	require.Len(t, subnets, 1)
	assert.Equal(t, "192.168.0.0/24", subnets[0].CIDRBlock)
}

// C_CIDR: Subnet с некорректным CIDR → INVALID_ARGUMENT немедленно.
func TestIntegration_C_InvalidCIDR(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test skipped in short mode")
	}
	pool := setupTestDB(t)
	_, subnetSvc, _, _, _ := setupServices(t, pool)
	ctx := context.Background()

	_, err := subnetSvc.Create(ctx, testFolderID, "net-id", "ru-central1-a",
		"192.168.0.1/24", // host bits set
		"bad-subnet", "", nil)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

// D3: SecurityGroup rules full-replace.
func TestIntegration_D3_SGRulesFullReplace(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test skipped in short mode")
	}
	pool := setupTestDB(t)
	networkSvc, _, sgSvc, _, _ := setupServices(t, pool)
	ctx := context.Background()

	folderID := "10000000-0000-0000-0000-000000000004"
	netOp, err := networkSvc.Create(ctx, folderID, "sg-net", "", nil)
	require.NoError(t, err)
	waitForOp(t, pool, netOp.ID)

	nets, _, err := service.NewNetworkService(repo.NewNetworkRepo(pool), operations.NewRepo(pool, "public"), &noopFolderClient{}).
		List(ctx, service.ListFilter{FolderID: folderID})
	require.NoError(t, err)
	netID := nets[0].ID

	// Создаём SG с 2 правилами
	rules1 := []domain.SecurityGroupRule{
		{Direction: "INGRESS", Protocol: "TCP", PortRangeMin: 80, PortRangeMax: 80},
		{Direction: "EGRESS", Protocol: "ICMP"},
	}
	sgCreateOp, err := sgSvc.Create(ctx, folderID, netID, "sg-test", "", nil, rules1)
	require.NoError(t, err)
	sgFinalOp := waitForOp(t, pool, sgCreateOp.ID)
	require.True(t, sgFinalOp.Done)
	require.Nil(t, sgFinalOp.Error)

	// Читаем созданную SG
	sgRepo2 := repo.NewSecurityGroupRepo(pool)
	opsRepo2 := operations.NewRepo(pool, "public")
	sgSvc2 := service.NewSecurityGroupService(sgRepo2, repo.NewNetworkRepo(pool), opsRepo2, &noopFolderClient{})
	sgs, _, err := sgSvc2.List(ctx, service.ListFilter{FolderID: folderID})
	require.NoError(t, err)
	require.Len(t, sgs, 1)
	assert.Len(t, sgs[0].Rules, 2, "должно быть 2 правила после создания")
	for _, r := range sgs[0].Rules {
		assert.NotEmpty(t, r.ID, "каждое правило должно иметь server-assigned ID")
	}
	sgID := sgs[0].ID
	sgRV := sgs[0].ResourceVersion
	oldRuleIDs := []string{sgs[0].Rules[0].ID, sgs[0].Rules[1].ID}

	// Update с 1 правилом — full-replace
	rules2 := []domain.SecurityGroupRule{
		{Direction: "EGRESS", Protocol: "UDP"},
	}
	updateOp, err := sgSvc2.Update(ctx, sgID, sgRV, "sg-test", "", nil, rules2, nil)
	require.NoError(t, err)
	updateFinalOp := waitForOp(t, pool, updateOp.ID)
	require.True(t, updateFinalOp.Done)
	require.Nil(t, updateFinalOp.Error)

	// Проверяем что rules заменены
	sgsAfter, _, err := sgSvc2.List(ctx, service.ListFilter{FolderID: folderID})
	require.NoError(t, err)
	require.Len(t, sgsAfter[0].Rules, 1, "должно быть 1 правило после full-replace")
	assert.NotContains(t, oldRuleIDs, sgsAfter[0].Rules[0].ID, "ID правила должен быть новым")
}

// F1: Создание Address с автоматическим IP из 203.0.113.0/24.
func TestIntegration_F1_AddressAllocatesIP(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test skipped in short mode")
	}
	pool := setupTestDB(t)
	_, _, _, _, addrSvc := setupServices(t, pool)
	ctx := context.Background()

	folderID := "10000000-0000-0000-0000-000000000005"
	createOp, err := addrSvc.Create(ctx, folderID, "my-ip", "desc", nil, "ADDRESS_TYPE_EXTERNAL", "ru-central1-a")
	require.NoError(t, err)
	finalOp := waitForOp(t, pool, createOp.ID)
	require.True(t, finalOp.Done)
	require.Nil(t, finalOp.Error)

	// Читаем созданный адрес
	addrRepo := repo.NewAddressRepo(pool)
	opsRepo := operations.NewRepo(pool, "public")
	addrSvc2 := service.NewAddressService(addrRepo, opsRepo, &noopFolderClient{})
	addrs, _, err := addrSvc2.List(ctx, service.ListFilter{FolderID: folderID})
	require.NoError(t, err)
	require.Len(t, addrs, 1)
	assert.True(t, strings.HasPrefix(addrs[0].AllocatedIPv4, "203.0.113."),
		"IP должен быть из 203.0.113.0/24, получен: "+addrs[0].AllocatedIPv4)
	assert.Equal(t, domain.AddressStatusReserved, addrs[0].Status)
}

// F_OCC: Update с неверным resource_version → Operation с ошибкой ABORTED.
func TestIntegration_F_OCC(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test skipped in short mode")
	}
	pool := setupTestDB(t)
	networkSvc, _, _, _, _ := setupServices(t, pool)
	ctx := context.Background()

	folderID := "10000000-0000-0000-0000-000000000006"
	createOp, err := networkSvc.Create(ctx, folderID, "occ-net", "", nil)
	require.NoError(t, err)
	waitForOp(t, pool, createOp.ID)

	nets, _, err := service.NewNetworkService(repo.NewNetworkRepo(pool), operations.NewRepo(pool, "public"), &noopFolderClient{}).
		List(ctx, service.ListFilter{FolderID: folderID})
	require.NoError(t, err)
	netID := nets[0].ID

	// Обновляем с неверным resource_version
	updateOp, err := networkSvc.Update(ctx, netID, "wrong-rv", "occ-net-updated", "", nil, nil)
	require.NoError(t, err) // синхронная часть OK

	updateFinalOp := waitForOp(t, pool, updateOp.ID)
	assert.True(t, updateFinalOp.Done)
	assert.NotNil(t, updateFinalOp.Error, "должна быть ошибка ABORTED")
	assert.Equal(t, int32(codes.Aborted), updateFinalOp.Error.Code)
}
