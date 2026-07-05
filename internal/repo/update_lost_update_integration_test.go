// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package repo_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	coredb "github.com/PRO-Robotech/kacho-corelib/db"
	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
	kachopg "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/pg"
)

// Lost-update на generic PATCH Update.
//
// doUpdate каждого ресурса (routetable/network/subnet/securitygroup/gateway)
// делает read-modify-write: Get → applyMask → unconditional `UPDATE ... SET
// <все mutable-колонки> WHERE id=$1`. Repo.Update пишет ВЕСЬ набор mutable
// полей, не только заявленные в mask. Под READ COMMITTED две конкурентные
// Update с DISJOINT масками (A пишет name, B пишет description) читают один
// snapshot и каждая затирает un-masked поле другой → second-writer-wins
// (lost-update) — нарушение инварианта целостности данных.
//
// Фикс: Get внутри writer-TX заменен на GetForUpdate (`SELECT ... FOR UPDATE`).
// Row-lock сериализует доступ: второй GetForUpdate блокируется до commit
// первого, затем читает уже обновленный row и применяет свою маску поверх → оба
// поля сохраняются.
//
// Choreography (детерминированно ловит lost-update):
//  1. TX-A берет row-lock через GetForUpdate (читает [name0, desc0]).
//  2. Горутина TX-B: GetForUpdate → set description="descB" → Update → commit.
//     Под FOR UPDATE TX-B блокируется на своем GetForUpdate до commit TX-A.
//     На голом Get TX-B прочитала бы [name0, desc0] сразу и записала бы
//     [name0, descB]; затем TX-A записала бы [nameA, desc0] → descB потерян.
//  3. Пауза, чтобы TX-B гарантированно дошла до GetForUpdate (и заблокировалась).
//  4. TX-A: set name="nameA" → Update → commit (освобождает lock).
//  5. join TX-B (теперь читает [nameA, desc0], ставит description="descB",
//     пишет [nameA, descB]).
//  6. Итог обязан быть [nameA, descB] — оба disjoint-поля сохранены.

func TestIntegration_RouteTable_ConcurrentDisjointUpdate_NoLostUpdate(t *testing.T) {
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

	netID := ids.NewID(ids.PrefixNetwork)
	rtID := ids.NewID(ids.PrefixRouteTable)
	require.NoError(t, legacyWithTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		if _, e := w.Networks().Insert(ctx, &domain.Network{ID: netID, ProjectID: "f-rt", Name: domain.RcNameVPC("n-rt")}); e != nil {
			return e
		}
		_, e := w.RouteTables().Insert(ctx, &domain.RouteTable{
			ID: rtID, ProjectID: "f-rt", Name: domain.RcNameVPC("name0"),
			Description: domain.RcDescription("desc0"), NetworkID: netID,
		})
		return e
	}))

	// TX-A: держим row-lock (read-modify-write, маска = {name}).
	wa, err := r.Writer(ctx)
	require.NoError(t, err)
	recA, err := wa.RouteTables().GetForUpdate(ctx, rtID)
	require.NoError(t, err)

	// TX-B в горутине: read-modify-write, маска = {description}.
	bDone := make(chan error, 1)
	go func() {
		wb, err := r.Writer(ctx)
		if err != nil {
			bDone <- err
			return
		}
		defer wb.Abort()
		recB, err := wb.RouteTables().GetForUpdate(ctx, rtID) // блокируется до commit TX-A
		if err != nil {
			bDone <- err
			return
		}
		recB.Description = domain.RcDescription("descB") // disjoint-поле B
		if _, err := wb.RouteTables().Update(ctx, &recB.RouteTable); err != nil {
			bDone <- err
			return
		}
		bDone <- wb.Commit()
	}()

	// Дать TX-B дойти до GetForUpdate и заблокироваться.
	time.Sleep(300 * time.Millisecond)

	// TX-A: ставит name → Update → commit (освобождает lock).
	recA.Name = domain.RcNameVPC("nameA") // disjoint-поле A
	_, err = wa.RouteTables().Update(ctx, &recA.RouteTable)
	require.NoError(t, err)
	require.NoError(t, wa.Commit())

	require.NoError(t, <-bDone)

	rd, err := r.Reader(ctx)
	require.NoError(t, err)
	defer func() { _ = rd.Close() }()
	got, err := rd.RouteTables().Get(ctx, rtID)
	require.NoError(t, err)
	require.Equal(t, domain.RcNameVPC("nameA"), got.Name,
		"name set by TX-A must persist (no lost-update)")
	require.Equal(t, domain.RcDescription("descB"), got.Description,
		"description set by TX-B must persist (no lost-update)")
}

// assertGetForUpdateLocks — общий помощник: проверяет, что repo-метод
// GetForUpdate действительно берет row-lock (`SELECT ... FOR UPDATE`).
// Choreography: TX-A держит lock; goroutine TX-B вызывает GetForUpdate на том же
// row — она ОБЯЗАНА блокироваться до commit TX-A. Если бы GetForUpdate был
// обычным SELECT (без FOR UPDATE) — путь сериализации не подключен и B вернулась
// бы немедленно (тест красный). Это легкая проверка «serialization path wired»
// для ресурсов, у которых полный concurrent-disjoint-тест не дублируется
// (subnet / securitygroup / gateway).
func assertGetForUpdateLocks(
	t *testing.T,
	r kacho.Repository,
	lockA func(w kacho.RepositoryWriter) error,
	lockB func(w kacho.RepositoryWriter) error,
) {
	t.Helper()
	ctx := context.Background()

	wa, err := r.Writer(ctx)
	require.NoError(t, err)
	require.NoError(t, lockA(wa)) // TX-A берет row-lock

	bAcquired := make(chan struct{})
	bDone := make(chan error, 1)
	go func() {
		wb, err := r.Writer(ctx)
		if err != nil {
			bDone <- err
			return
		}
		defer wb.Abort()
		if err := lockB(wb); err != nil { // блокируется до commit TX-A
			bDone <- err
			return
		}
		close(bAcquired)
		bDone <- nil
	}()

	// TX-B обязана быть заблокирована, пока TX-A держит lock.
	select {
	case <-bAcquired:
		t.Fatal("concurrent GetForUpdate acquired the row without waiting for TX-A commit — FOR UPDATE row-lock not wired")
	case <-time.After(300 * time.Millisecond):
		// ожидаемо: B все еще ждет lock.
	}

	require.NoError(t, wa.Commit()) // освобождаем lock

	select {
	case <-bAcquired: // B прошла GetForUpdate после commit TX-A
	case <-time.After(3 * time.Second):
		t.Fatal("concurrent GetForUpdate did not unblock after TX-A commit")
	}
	require.NoError(t, <-bDone)
}

// Subnet / SecurityGroup / Gateway: облегченная проверка, что путь сериализации
// (GetForUpdate с FOR UPDATE row-lock) подключен в repo. Полный
// concurrent-disjoint-тест без потери поля — на RouteTable + Network выше; здесь
// убеждаемся, что row-lock реально берется и для остальных трех.

func TestIntegration_Subnet_GetForUpdate_TakesRowLock(t *testing.T) {
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

	netID := ids.NewID(ids.PrefixNetwork)
	subID := ids.NewID(ids.PrefixSubnet)
	require.NoError(t, legacyWithTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		if _, e := w.Networks().Insert(ctx, &domain.Network{ID: netID, ProjectID: "f-su", Name: domain.RcNameVPC("n-su")}); e != nil {
			return e
		}
		_, e := w.Subnets().Insert(ctx, &domain.Subnet{
			ID: subID, ProjectID: "f-su", Name: domain.RcNameVPC("s-su"),
			NetworkID: netID, PlacementType: domain.PlacementZonal, ZoneID: "zone-a", V4CidrBlocks: []string{"10.9.0.0/24"},
		})
		return e
	}))

	assertGetForUpdateLocks(t, r,
		func(w kacho.RepositoryWriter) error { _, e := w.Subnets().GetForUpdate(ctx, subID); return e },
		func(w kacho.RepositoryWriter) error { _, e := w.Subnets().GetForUpdate(ctx, subID); return e },
	)
}

func TestIntegration_SecurityGroup_GetForUpdate_TakesRowLock(t *testing.T) {
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

	netID := ids.NewID(ids.PrefixNetwork)
	sgID := ids.NewID(ids.PrefixSecurityGroup)
	require.NoError(t, legacyWithTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		if _, e := w.Networks().Insert(ctx, &domain.Network{ID: netID, ProjectID: "f-sg", Name: domain.RcNameVPC("n-sg")}); e != nil {
			return e
		}
		_, e := w.SecurityGroups().Insert(ctx, &domain.SecurityGroup{
			ID: sgID, ProjectID: "f-sg", NetworkID: netID, Name: domain.RcNameVPC("sg0"),
		})
		return e
	}))

	assertGetForUpdateLocks(t, r,
		func(w kacho.RepositoryWriter) error { _, e := w.SecurityGroups().GetForUpdate(ctx, sgID); return e },
		func(w kacho.RepositoryWriter) error { _, e := w.SecurityGroups().GetForUpdate(ctx, sgID); return e },
	)
}

func TestIntegration_Gateway_GetForUpdate_TakesRowLock(t *testing.T) {
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

	gwID := ids.NewID(ids.PrefixGateway)
	require.NoError(t, legacyWithTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		_, e := w.Gateways().Insert(ctx, &domain.Gateway{
			ID: gwID, ProjectID: "f-gw", Name: domain.RcNameVPC("gw0"),
			GatewayType: domain.GatewayTypeSharedEgress,
		})
		return e
	}))

	assertGetForUpdateLocks(t, r,
		func(w kacho.RepositoryWriter) error { _, e := w.Gateways().GetForUpdate(ctx, gwID); return e },
		func(w kacho.RepositoryWriter) error { _, e := w.Gateways().GetForUpdate(ctx, gwID); return e },
	)
}

func TestIntegration_Network_ConcurrentDisjointUpdate_NoLostUpdate(t *testing.T) {
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

	netID := ids.NewID(ids.PrefixNetwork)
	require.NoError(t, legacyWithTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		_, e := w.Networks().Insert(ctx, &domain.Network{
			ID: netID, ProjectID: "f-net", Name: domain.RcNameVPC("name0"),
			Description: domain.RcDescription("desc0"),
		})
		return e
	}))

	wa, err := r.Writer(ctx)
	require.NoError(t, err)
	recA, err := wa.Networks().GetForUpdate(ctx, netID)
	require.NoError(t, err)

	bDone := make(chan error, 1)
	go func() {
		wb, err := r.Writer(ctx)
		if err != nil {
			bDone <- err
			return
		}
		defer wb.Abort()
		recB, err := wb.Networks().GetForUpdate(ctx, netID)
		if err != nil {
			bDone <- err
			return
		}
		recB.Description = domain.RcDescription("descB")
		if _, err := wb.Networks().Update(ctx, &recB.Network); err != nil {
			bDone <- err
			return
		}
		bDone <- wb.Commit()
	}()

	time.Sleep(300 * time.Millisecond)

	recA.Name = domain.RcNameVPC("nameA")
	_, err = wa.Networks().Update(ctx, &recA.Network)
	require.NoError(t, err)
	require.NoError(t, wa.Commit())

	require.NoError(t, <-bDone)

	rd, err := r.Reader(ctx)
	require.NoError(t, err)
	defer func() { _ = rd.Close() }()
	got, err := rd.Networks().Get(ctx, netID)
	require.NoError(t, err)
	require.Equal(t, domain.RcNameVPC("nameA"), got.Name,
		"name set by TX-A must persist (no lost-update)")
	require.Equal(t, domain.RcDescription("descB"), got.Description,
		"description set by TX-B must persist (no lost-update)")
}
