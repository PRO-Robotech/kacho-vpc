package repo_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	coredb "github.com/PRO-Robotech/kacho-corelib/db"
	"github.com/PRO-Robotech/kacho-corelib/ids"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
	kachopg "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/pg"
)

// KAC-56: DB-level auto-association (PL/pgSQL triggers, миграция 0019).
//
// Проверяем 4 поведения:
//  1. AFTER INSERT ON route_tables → existing Subnet'ы с NULL route_table_id
//     получают route_table_id = NEW.id; Subnet с explicit route_table_id
//     не перетирается.
//  2. BEFORE INSERT ON subnets → Subnet, создаваемый в сети с RT, получает
//     route_table_id = самой ранней RT (auto-pick); если RT нет — NULL.
//  3. FK subnets.route_table_id → route_tables(id) ON DELETE SET NULL.
//  4. AFTER UPDATE OF route_table_id ON subnets → outbox-эмит Subnet.UPDATED
//     с payload.auto_association=true.
//
// Тестируем напрямую через repo (без service-слоя) — это DB-level гарантия.
//
// KAC-94 A.7 sub-PR 5/6: переписан на CQRS Writer.

func setupAssocRepo(t *testing.T) (kacho.Repository, func()) {
	ctx := context.Background()
	dsn := setupTestDB(t)
	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	r := kachopg.New(pool, nil)
	return r, func() {
		r.Close()
		pool.Close()
	}
}

func TestIntegration_VPC_AutoAssociation_RT_AutoAssoc_Subnets(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	r, cleanup := setupAssocRepo(t)
	defer cleanup()

	withTx := func(t *testing.T, fn func(kacho.RepositoryWriter) error) error {
		t.Helper()
		w, err := r.Writer(ctx)
		require.NoError(t, err)
		if err := fn(w); err != nil {
			w.Abort()
			return err
		}
		return w.Commit()
	}

	net := &domain.Network{
		ID: ids.NewID(ids.PrefixNetwork), ProjectID: "f-assoc-a", Name: domain.RcNameVPC("net-assoc-a"),
	}
	require.NoError(t, withTx(t, func(w kacho.RepositoryWriter) error {
		_, e := w.Networks().Insert(ctx, net)
		return e
	}))

	subA := &domain.Subnet{
		ID: ids.NewID(ids.PrefixSubnet), ProjectID: "f-assoc-a", Name: domain.RcNameVPC("sub-assoc-a"), NetworkID: net.ID, ZoneID: "ru-central1-a",
		V4CidrBlocks: []string{"10.71.0.0/24"},
	}
	require.NoError(t, withTx(t, func(w kacho.RepositoryWriter) error {
		_, e := w.Subnets().Insert(ctx, subA)
		return e
	}))

	subnetGet := func(id string) *kacho.SubnetRecord {
		rd, err := r.Reader(ctx)
		require.NoError(t, err)
		defer rd.Close()
		got, err := rd.Subnets().Get(ctx, id)
		require.NoError(t, err)
		return got
	}

	require.Empty(t, subnetGet(subA.ID).RouteTableID, "auto-pick: нет RT в сети — должен остаться NULL/'' ")

	// Создаём RT — AFTER INSERT trigger обновит subA на rtExplicit.id.
	rtExplicit := &domain.RouteTable{
		ID: ids.NewID(ids.PrefixRouteTable), ProjectID: "f-assoc-a", Name: domain.RcNameVPC("rt-explicit"), NetworkID: net.ID,
	}
	require.NoError(t, withTx(t, func(w kacho.RepositoryWriter) error {
		_, e := w.RouteTables().Insert(ctx, rtExplicit)
		return e
	}))

	require.Equal(t, rtExplicit.ID, subnetGet(subA.ID).RouteTableID,
		"AFTER INSERT trigger должен auto-assoc subA на новую RT")

	// Новая RT-2 не должна перетирать subA's route_table_id.
	rt2 := &domain.RouteTable{
		ID: ids.NewID(ids.PrefixRouteTable), ProjectID: "f-assoc-a", Name: domain.RcNameVPC("rt-explicit-2"), NetworkID: net.ID,
	}
	require.NoError(t, withTx(t, func(w kacho.RepositoryWriter) error {
		_, e := w.RouteTables().Insert(ctx, rt2)
		return e
	}))

	require.Equal(t, rtExplicit.ID, subnetGet(subA.ID).RouteTableID,
		"existing route_table_id не должен перетираться при INSERT новой RT")
}

func TestIntegration_VPC_AutoAssociation_Subnet_AutoPick_RT(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	r, cleanup := setupAssocRepo(t)
	defer cleanup()

	withTx := func(t *testing.T, fn func(kacho.RepositoryWriter) error) error {
		t.Helper()
		w, err := r.Writer(ctx)
		require.NoError(t, err)
		if err := fn(w); err != nil {
			w.Abort()
			return err
		}
		return w.Commit()
	}

	net := &domain.Network{
		ID: ids.NewID(ids.PrefixNetwork), ProjectID: "f-assoc-b", Name: domain.RcNameVPC("net-assoc-b"),
	}
	require.NoError(t, withTx(t, func(w kacho.RepositoryWriter) error {
		_, e := w.Networks().Insert(ctx, net)
		return e
	}))

	rtEarly := &domain.RouteTable{
		ID: ids.NewID(ids.PrefixRouteTable), ProjectID: "f-assoc-b", Name: domain.RcNameVPC("rt-early"), NetworkID: net.ID,
	}
	require.NoError(t, withTx(t, func(w kacho.RepositoryWriter) error {
		_, e := w.RouteTables().Insert(ctx, rtEarly)
		return e
	}))

	rtLate := &domain.RouteTable{
		ID: ids.NewID(ids.PrefixRouteTable), ProjectID: "f-assoc-b", Name: domain.RcNameVPC("rt-late"), NetworkID: net.ID,
	}
	require.NoError(t, withTx(t, func(w kacho.RepositoryWriter) error {
		_, e := w.RouteTables().Insert(ctx, rtLate)
		return e
	}))

	sub := &domain.Subnet{
		ID: ids.NewID(ids.PrefixSubnet), ProjectID: "f-assoc-b", Name: domain.RcNameVPC("sub-autopick"), NetworkID: net.ID, ZoneID: "ru-central1-a",
		V4CidrBlocks: []string{"10.72.0.0/24"},
	}
	require.NoError(t, withTx(t, func(w kacho.RepositoryWriter) error {
		_, e := w.Subnets().Insert(ctx, sub)
		return e
	}))

	rd, err := r.Reader(ctx)
	require.NoError(t, err)
	subGot, err := rd.Subnets().Get(ctx, sub.ID)
	require.NoError(t, rd.Close())
	require.NoError(t, err)
	require.Equal(t, rtEarly.ID, subGot.RouteTableID,
		"auto-pick должен выбрать самую раннюю RT (created_at ASC)")

	subExplicit := &domain.Subnet{
		ID: ids.NewID(ids.PrefixSubnet), ProjectID: "f-assoc-b", Name: domain.RcNameVPC("sub-explicit-late"), NetworkID: net.ID, ZoneID: "ru-central1-a",
		V4CidrBlocks: []string{"10.73.0.0/24"}, RouteTableID: rtLate.ID,
	}
	require.NoError(t, withTx(t, func(w kacho.RepositoryWriter) error {
		_, e := w.Subnets().Insert(ctx, subExplicit)
		return e
	}))

	rd2, err := r.Reader(ctx)
	require.NoError(t, err)
	subExplicitGot, err := rd2.Subnets().Get(ctx, subExplicit.ID)
	require.NoError(t, rd2.Close())
	require.NoError(t, err)
	require.Equal(t, rtLate.ID, subExplicitGot.RouteTableID,
		"explicit route_table_id не должен перетираться auto-pick'ом")
}

func TestIntegration_VPC_AutoAssociation_RT_Delete_FK_SetNull(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	r, cleanup := setupAssocRepo(t)
	defer cleanup()

	withTx := func(t *testing.T, fn func(kacho.RepositoryWriter) error) error {
		t.Helper()
		w, err := r.Writer(ctx)
		require.NoError(t, err)
		if err := fn(w); err != nil {
			w.Abort()
			return err
		}
		return w.Commit()
	}

	net := &domain.Network{
		ID: ids.NewID(ids.PrefixNetwork), ProjectID: "f-assoc-c", Name: domain.RcNameVPC("net-assoc-c"),
	}
	require.NoError(t, withTx(t, func(w kacho.RepositoryWriter) error {
		_, e := w.Networks().Insert(ctx, net)
		return e
	}))

	rt := &domain.RouteTable{
		ID: ids.NewID(ids.PrefixRouteTable), ProjectID: "f-assoc-c", Name: domain.RcNameVPC("rt-tobedeleted"), NetworkID: net.ID,
	}
	require.NoError(t, withTx(t, func(w kacho.RepositoryWriter) error {
		_, e := w.RouteTables().Insert(ctx, rt)
		return e
	}))

	sub := &domain.Subnet{
		ID: ids.NewID(ids.PrefixSubnet), ProjectID: "f-assoc-c", Name: domain.RcNameVPC("sub-fk-setnull"), NetworkID: net.ID, ZoneID: "ru-central1-a",
		V4CidrBlocks: []string{"10.74.0.0/24"},
	}
	require.NoError(t, withTx(t, func(w kacho.RepositoryWriter) error {
		_, e := w.Subnets().Insert(ctx, sub)
		return e
	}))

	rd, err := r.Reader(ctx)
	require.NoError(t, err)
	subBefore, err := rd.Subnets().Get(ctx, sub.ID)
	require.NoError(t, rd.Close())
	require.NoError(t, err)
	require.Equal(t, rt.ID, subBefore.RouteTableID, "auto-pick precondition")

	// Удаляем RT — FK ON DELETE SET NULL обнулит subnet.route_table_id.
	require.NoError(t, withTx(t, func(w kacho.RepositoryWriter) error {
		return w.RouteTables().Delete(ctx, rt.ID)
	}))

	rd2, err := r.Reader(ctx)
	require.NoError(t, err)
	subAfter, err := rd2.Subnets().Get(ctx, sub.ID)
	require.NoError(t, rd2.Close())
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
	r := kachopg.New(pool, nil)
	defer r.Close()

	withTx := func(t *testing.T, fn func(kacho.RepositoryWriter) error) error {
		t.Helper()
		w, err := r.Writer(ctx)
		require.NoError(t, err)
		if err := fn(w); err != nil {
			w.Abort()
			return err
		}
		return w.Commit()
	}

	net := &domain.Network{
		ID: ids.NewID(ids.PrefixNetwork), ProjectID: "f-assoc-d", Name: domain.RcNameVPC("net-assoc-d"),
	}
	require.NoError(t, withTx(t, func(w kacho.RepositoryWriter) error {
		_, e := w.Networks().Insert(ctx, net)
		return e
	}))

	sub := &domain.Subnet{
		ID: ids.NewID(ids.PrefixSubnet), ProjectID: "f-assoc-d", Name: domain.RcNameVPC("sub-outbox"), NetworkID: net.ID, ZoneID: "ru-central1-a",
		V4CidrBlocks: []string{"10.75.0.0/24"},
	}
	require.NoError(t, withTx(t, func(w kacho.RepositoryWriter) error {
		_, e := w.Subnets().Insert(ctx, sub)
		return e
	}))

	// snapshot outbox seq до создания RT.
	var seqBefore int64
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT COALESCE(MAX(sequence_no), 0) FROM vpc_outbox`).Scan(&seqBefore))

	rt := &domain.RouteTable{
		ID: ids.NewID(ids.PrefixRouteTable), ProjectID: "f-assoc-d", Name: domain.RcNameVPC("rt-outbox"), NetworkID: net.ID,
	}
	require.NoError(t, withTx(t, func(w kacho.RepositoryWriter) error {
		_, e := w.RouteTables().Insert(ctx, rt)
		return e
	}))

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

func scanOutboxRow(row interface {
	Scan(dest ...any) error
}, kind, resID, evtType *string, payload *map[string]any) error {
	var payloadJSON []byte
	if err := row.Scan(kind, resID, evtType, &payloadJSON); err != nil {
		return err
	}
	return json.Unmarshal(payloadJSON, payload)
}
