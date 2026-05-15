package repo_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	coredb "github.com/PRO-Robotech/kacho-corelib/db"
	"github.com/PRO-Robotech/kacho-corelib/ids"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
)

// KAC-56: DB-level auto-association (PL/pgSQL triggers, миграция 0019).
//
// Проверяем 4 поведения:
//  1. AFTER INSERT ON route_tables → existing Subnet'ы с NULL route_table_id
//     получают route_table_id = NEW.id; Subnet с explicit route_table_id
//     не перетирается.
//  2. BEFORE INSERT ON subnets → Subnet, создаваемый в сети с RT, получает
//     route_table_id = самой ранней RT (auto-pick); если RT нет — NULL.
//  3. FK subnets.route_table_id → route_tables(id) ON DELETE SET NULL:
//     RT.Delete обнуляет route_table_id в зависимых Subnet'ах.
//  4. AFTER UPDATE OF route_table_id ON subnets → outbox-эмит Subnet.UPDATED
//     с payload.auto_association=true.
//
// Тестируем напрямую через repo (без service-слоя) — это DB-level гарантия.

func TestIntegration_VPC_AutoAssociation_RT_AutoAssoc_Subnets(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	dsn := setupTestDB(t)
	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	netRepo := repo.NewNetworkRepo(pool)
	subnetRepo := repo.NewSubnetRepo(pool)
	rtRepo := repo.NewRouteTableRepo(pool)

	_ = time.Now().UTC().Truncate(time.Microsecond) // CreatedAt — DB-managed (KAC-94).
	net := &domain.Network{
		ID: ids.NewID(ids.PrefixNetwork), FolderID: "f-assoc-a", Name: domain.RcNameVPC("net-assoc-a"),
	}
	_, err = netRepo.Insert(ctx, net)
	require.NoError(t, err)

	// Подсеть #1 — без route_table_id (auto-pick должен оставить NULL, так как
	// RT ещё нет; trigger (2) кэндидатов не находит).
	subA := &domain.Subnet{
		ID: ids.NewID(ids.PrefixSubnet), FolderID: "f-assoc-a", Name: domain.RcNameVPC("sub-assoc-a"), NetworkID: net.ID, ZoneID: "ru-central1-a",
		V4CidrBlocks: []string{"10.71.0.0/24"},
	}
	_, err = subnetRepo.Insert(ctx, subA)
	require.NoError(t, err)
	subAGot, err := subnetRepo.Get(ctx, subA.ID)
	require.NoError(t, err)
	require.Empty(t, subAGot.RouteTableID, "auto-pick: нет RT в сети — должен остаться NULL/'' ")

	// Подсеть #2 — с explicit route_table_id (укажем тот id, что заведём ниже
	// для RT-explicit-protection); сначала создадим эту RT.
	rtExplicit := &domain.RouteTable{
		ID: ids.NewID(ids.PrefixRouteTable), FolderID: "f-assoc-a", Name: domain.RcNameVPC("rt-explicit"), NetworkID: net.ID,
	}
	_, err = rtRepo.Insert(ctx, rtExplicit)
	require.NoError(t, err)

	// После INSERT RT-1 — trigger (1) обновит subA на rtExplicit.id.
	subAAfter, err := subnetRepo.Get(ctx, subA.ID)
	require.NoError(t, err)
	require.Equal(t, rtExplicit.ID, subAAfter.RouteTableID,
		"AFTER INSERT trigger должен auto-assoc subA на новую RT")

	// Subnet с explicit RT — создаём новую RT-2 и Subnet-B, у которого
	// route_table_id уже задан = rtExplicit.id (а не RT-2).
	rt2 := &domain.RouteTable{
		ID: ids.NewID(ids.PrefixRouteTable), FolderID: "f-assoc-a", Name: domain.RcNameVPC("rt-explicit-2"), NetworkID: net.ID,
	}
	_, err = rtRepo.Insert(ctx, rt2)
	require.NoError(t, err)

	// subA по-прежнему привязан к rtExplicit (RT-2 не перетирает — route_table_id уже NOT NULL).
	subAAfter2, err := subnetRepo.Get(ctx, subA.ID)
	require.NoError(t, err)
	require.Equal(t, rtExplicit.ID, subAAfter2.RouteTableID,
		"existing route_table_id не должен перетираться при INSERT новой RT")
}

func TestIntegration_VPC_AutoAssociation_Subnet_AutoPick_RT(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	dsn := setupTestDB(t)
	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	netRepo := repo.NewNetworkRepo(pool)
	subnetRepo := repo.NewSubnetRepo(pool)
	rtRepo := repo.NewRouteTableRepo(pool)

	_ = time.Now().UTC().Truncate(time.Microsecond) // CreatedAt — DB-managed (KAC-94).
	net := &domain.Network{
		ID: ids.NewID(ids.PrefixNetwork), FolderID: "f-assoc-b", Name: domain.RcNameVPC("net-assoc-b"),
	}
	_, err = netRepo.Insert(ctx, net)
	require.NoError(t, err)

	// Сначала RT, потом Subnet — BEFORE INSERT trigger должен auto-pick RT.
	rtEarly := &domain.RouteTable{
		ID: ids.NewID(ids.PrefixRouteTable), FolderID: "f-assoc-b", Name: domain.RcNameVPC("rt-early"), NetworkID: net.ID,
	}
	_, err = rtRepo.Insert(ctx, rtEarly)
	require.NoError(t, err)

	rtLate := &domain.RouteTable{
		ID: ids.NewID(ids.PrefixRouteTable), FolderID: "f-assoc-b", Name: domain.RcNameVPC("rt-late"), NetworkID: net.ID,
	}
	_, err = rtRepo.Insert(ctx, rtLate)
	require.NoError(t, err)

	sub := &domain.Subnet{
		ID: ids.NewID(ids.PrefixSubnet), FolderID: "f-assoc-b", Name: domain.RcNameVPC("sub-autopick"), NetworkID: net.ID, ZoneID: "ru-central1-a",
		V4CidrBlocks: []string{"10.72.0.0/24"},
		// route_table_id не задан — trigger должен подставить rtEarly (по created_at ASC).
	}
	_, err = subnetRepo.Insert(ctx, sub)
	require.NoError(t, err)
	subGot, err := subnetRepo.Get(ctx, sub.ID)
	require.NoError(t, err)
	require.Equal(t, rtEarly.ID, subGot.RouteTableID,
		"auto-pick должен выбрать самую раннюю RT (created_at ASC)")

	// Subnet с explicit route_table_id=rtLate — trigger не должен перетереть.
	subExplicit := &domain.Subnet{
		ID: ids.NewID(ids.PrefixSubnet), FolderID: "f-assoc-b", Name: domain.RcNameVPC("sub-explicit-late"), NetworkID: net.ID, ZoneID: "ru-central1-a",
		V4CidrBlocks: []string{"10.73.0.0/24"}, RouteTableID: rtLate.ID,
	}
	_, err = subnetRepo.Insert(ctx, subExplicit)
	require.NoError(t, err)
	subExplicitGot, err := subnetRepo.Get(ctx, subExplicit.ID)
	require.NoError(t, err)
	require.Equal(t, rtLate.ID, subExplicitGot.RouteTableID,
		"explicit route_table_id не должен перетираться auto-pick'ом")
}

func TestIntegration_VPC_AutoAssociation_RT_Delete_FK_SetNull(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	dsn := setupTestDB(t)
	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	netRepo := repo.NewNetworkRepo(pool)
	subnetRepo := repo.NewSubnetRepo(pool)
	rtRepo := repo.NewRouteTableRepo(pool)

	_ = time.Now().UTC().Truncate(time.Microsecond) // CreatedAt — DB-managed (KAC-94).
	net := &domain.Network{
		ID: ids.NewID(ids.PrefixNetwork), FolderID: "f-assoc-c", Name: domain.RcNameVPC("net-assoc-c"),
	}
	_, err = netRepo.Insert(ctx, net)
	require.NoError(t, err)

	rt := &domain.RouteTable{
		ID: ids.NewID(ids.PrefixRouteTable), FolderID: "f-assoc-c", Name: domain.RcNameVPC("rt-tobedeleted"), NetworkID: net.ID,
	}
	_, err = rtRepo.Insert(ctx, rt)
	require.NoError(t, err)

	sub := &domain.Subnet{
		ID: ids.NewID(ids.PrefixSubnet), FolderID: "f-assoc-c", Name: domain.RcNameVPC("sub-fk-setnull"), NetworkID: net.ID, ZoneID: "ru-central1-a",
		V4CidrBlocks: []string{"10.74.0.0/24"},
		// route_table_id не задан — auto-pick подставит rt.ID.
	}
	_, err = subnetRepo.Insert(ctx, sub)
	require.NoError(t, err)
	subBefore, err := subnetRepo.Get(ctx, sub.ID)
	require.NoError(t, err)
	require.Equal(t, rt.ID, subBefore.RouteTableID, "auto-pick precondition")

	// Удаляем RT — FK ON DELETE SET NULL обнулит subnet.route_table_id.
	require.NoError(t, rtRepo.Delete(ctx, rt.ID))

	subAfter, err := subnetRepo.Get(ctx, sub.ID)
	require.NoError(t, err)
	require.Empty(t, subAfter.RouteTableID,
		"FK ON DELETE SET NULL: subnet.route_table_id должен обнулиться после RT.Delete")
}

func TestIntegration_VPC_AutoAssociation_OutboxEmit_OnTriggeredUpdate(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	dsn := setupTestDB(t)
	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	netRepo := repo.NewNetworkRepo(pool)
	subnetRepo := repo.NewSubnetRepo(pool)
	rtRepo := repo.NewRouteTableRepo(pool)

	_ = time.Now().UTC().Truncate(time.Microsecond) // CreatedAt — DB-managed (KAC-94).
	net := &domain.Network{
		ID: ids.NewID(ids.PrefixNetwork), FolderID: "f-assoc-d", Name: domain.RcNameVPC("net-assoc-d"),
	}
	_, err = netRepo.Insert(ctx, net)
	require.NoError(t, err)

	sub := &domain.Subnet{
		ID: ids.NewID(ids.PrefixSubnet), FolderID: "f-assoc-d", Name: domain.RcNameVPC("sub-outbox"), NetworkID: net.ID, ZoneID: "ru-central1-a",
		V4CidrBlocks: []string{"10.75.0.0/24"},
	}
	_, err = subnetRepo.Insert(ctx, sub)
	require.NoError(t, err)

	// snapshot outbox seq до создания RT
	var seqBefore int64
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT COALESCE(MAX(sequence_no), 0) FROM vpc_outbox`).Scan(&seqBefore))

	// Создаём RT — trigger (1) обновит subnet, trigger (5) запишет outbox.
	rt := &domain.RouteTable{
		ID: ids.NewID(ids.PrefixRouteTable), FolderID: "f-assoc-d", Name: domain.RcNameVPC("rt-outbox"), NetworkID: net.ID,
	}
	_, err = rtRepo.Insert(ctx, rt)
	require.NoError(t, err)

	// Проверяем что в outbox есть Subnet.UPDATED с auto_association=true.
	var (
		kind, resID, evtType string
		payload              map[string]any
	)
	row := pool.QueryRow(ctx, `
		SELECT resource_kind, resource_id, event_type, payload
		  FROM vpc_outbox
		 WHERE sequence_no > $1
		   AND resource_kind = 'Subnet'
		   AND resource_id = $2
		   AND event_type = 'UPDATED'
		 ORDER BY sequence_no DESC
		 LIMIT 1`, seqBefore, sub.ID)
	require.NoError(t, scanOutboxRow(row, &kind, &resID, &evtType, &payload))
	require.Equal(t, "Subnet", kind)
	require.Equal(t, sub.ID, resID)
	require.Equal(t, "UPDATED", evtType)
	require.Equal(t, rt.ID, payload["route_table_id"])
	require.Equal(t, true, payload["auto_association"],
		"triggered emit ставит auto_association=true маркер")
}

// scanOutboxRow — общий helper для тестов: распаковывает row в kind/id/evtType/payload.
func scanOutboxRow(row interface {
	Scan(dest ...any) error
}, kind, resID, evtType *string, payload *map[string]any) error {
	var payloadJSON []byte
	if err := row.Scan(kind, resID, evtType, &payloadJSON); err != nil {
		return err
	}
	return json.Unmarshal(payloadJSON, payload)
}
