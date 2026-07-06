// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package repo_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	coredb "github.com/PRO-Robotech/kacho-corelib/db"
	"github.com/PRO-Robotech/kacho-corelib/ids"

	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/api/networkinterface"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/helpers"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
	kachopg "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/pg"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/repomock"
)

// TestIntegration_NetworkInterface_DeleteVsConcurrentAttach_NoOrphanedAddress —
// регрессия TOCTOU между NIC.Delete и конкурентным NIC.Update (project-rule #10).
//
// Раньше DeleteNetworkInterfaceUseCase.doDelete читал NIC через голый `Get`
// (plain SELECT, без FOR UPDATE), снимал snapshot address-set'а и чистил
// ClearReference ровно этот snapshot. Update же сериализует read-modify-write
// через `GetForUpdate` (row-lock). Раз Delete НЕ брал тот же lock, конкурентный
// Update мог доаттачить адрес МЕЖДУ snapshot'ом Delete и его DML — новый адрес не
// попадал в snapshot, ClearReference его пропускал, а NIC-строка удалялась → адрес
// оставался used=true с address_references-строкой, ссылающейся на удалённый NIC
// (навсегда «кирпич»: не освободить, не переаттачить). Фикс — Delete тоже берёт
// `GetForUpdate`, и две операции сериализуются на row-lock'е.
//
// Choreography (детерминированная, без sleep'ов):
//  1. wHold берёт FOR UPDATE на NIC (эмулирует Update, уже захвативший row-lock).
//  2. Стартуем реальный Delete use-case; его worker упирается в lock:
//     — с фиксом: блокируется сразу на GetForUpdate;
//     — без фикса: Get проходит (plain SELECT не ждёт), worker чистит stale
//     snapshot {A} и блокируется уже на DELETE самой NIC-строки.
//     В обоих случаях появляется НЕ-granted lock → waitForLockWaiter отпускает.
//  3. wHold доаттачивает адрес B к NIC (SetReference + UpdateMeta) и commit'ит.
//  4. — Без фикса: Delete до-DELETE'ит NIC по stale-плану; B остаётся
//     used=true, referrer=удалённый NIC → ОРФАН (тест краснеет).
//     — С фиксом: GetForUpdate отпускает, worker перечитывает свежий {A,B},
//     чистит оба и удаляет NIC → орфана нет.
func TestIntegration_NetworkInterface_DeleteVsConcurrentAttach_NoOrphanedAddress(t *testing.T) {
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

	const projectID = "f-nic-toctou"
	netID := ids.NewID(ids.PrefixNetwork)
	subID := ids.NewID(ids.PrefixSubnet)
	nicID := ids.NewID(ids.PrefixNetworkInterface)
	addrV4 := ids.NewID(ids.PrefixAddress) // A — уже привязан к NIC
	addrV6 := ids.NewID(ids.PrefixAddress) // B — доаттачивается конкурентно

	require.NoError(t, legacyWithTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		if _, e := w.Networks().Insert(ctx, &domain.Network{ID: netID, ProjectID: projectID, Name: domain.RcNameVPC("n-toctou")}); e != nil {
			return e
		}
		if _, e := w.Subnets().Insert(ctx, &domain.Subnet{
			ID: subID, ProjectID: projectID, Name: domain.RcNameVPC("s-toctou"),
			NetworkID: netID, PlacementType: domain.PlacementZonal, ZoneID: "zone-a", V4CidrBlocks: []string{"10.20.0.0/24"},
		}); e != nil {
			return e
		}
		if _, e := w.Addresses().Insert(ctx, &domain.Address{
			ID: addrV4, ProjectID: projectID, Name: domain.RcNameVPC("a-v4"),
			Type: domain.AddressTypeExternal, IpVersion: domain.IpVersionIPv4,
			ExternalIpv4: &domain.ExternalIpv4Spec{Address: "198.51.100.10", ZoneID: "zone-a"},
		}); e != nil {
			return e
		}
		if _, e := w.Addresses().Insert(ctx, &domain.Address{
			ID: addrV6, ProjectID: projectID, Name: domain.RcNameVPC("b-v6"),
			Type: domain.AddressTypeExternal, IpVersion: domain.IpVersionIPv6,
			ExternalIpv6: &domain.ExternalIpv6Spec{Address: "2001:db8::10", ZoneID: "zone-a"},
		}); e != nil {
			return e
		}
		if _, e := w.NetworkInterfaces().Insert(ctx, &domain.NetworkInterface{
			ID: nicID, ProjectID: projectID, Name: domain.RcNameVPC("nic-toctou"),
			SubnetID: subID, MAC: "0e:aa:bb:cc:dd:01", Status: domain.NIStatusAvailable,
			V4AddressIDs: []string{addrV4},
		}); e != nil {
			return e
		}
		// A помечаем used с referrer=NIC (как после Create/attach).
		_, e := w.Addresses().SetReference(ctx, &domain.AddressReference{
			AddressID: addrV4, ReferrerType: "vpc_network_interface", ReferrerID: nicID, ReferrerName: "nic-toctou",
		})
		return e
	}))

	// (1) Захватываем row-lock на NIC отдельной writer-TX (эмуляция Update).
	wHold, err := r.Writer(ctx)
	require.NoError(t, err)
	defer wHold.Abort()
	recHold, err := wHold.NetworkInterfaces().GetForUpdate(ctx, nicID)
	require.NoError(t, err)

	// (2) Стартуем реальный Delete use-case.
	or := repomock.NewOpsRepo()
	deleteUC := networkinterface.NewDeleteNetworkInterfaceUseCase(r, or)
	op, err := deleteUC.Execute(ctx, nicID)
	require.NoError(t, err)

	// Дождаться, пока Delete-worker реально упрётся в lock (детерминированно).
	waitForLockWaiter(t, ctx, pool)

	// (3) Конкурентный Update доаттачивает B к NIC и коммитит (освобождая lock).
	_, err = wHold.Addresses().SetReference(ctx, &domain.AddressReference{
		AddressID: addrV6, ReferrerType: "vpc_network_interface", ReferrerID: nicID, ReferrerName: "nic-toctou",
	})
	require.NoError(t, err)
	recHold.V6AddressIDs = []string{addrV6}
	_, err = wHold.NetworkInterfaces().UpdateMeta(ctx, &recHold.NetworkInterface)
	require.NoError(t, err)
	require.NoError(t, wHold.Commit())

	// (4) Delete завершается; проверяем инвариант — адрес B НЕ осиротел.
	saved := repomock.AwaitOpDone(t, or, op.ID)
	require.Nil(t, saved.Error, "Delete operation must succeed")

	rd, err := r.Reader(ctx)
	require.NoError(t, err)
	defer func() { _ = rd.Close() }()

	gotB, err := rd.Addresses().Get(ctx, addrV6)
	require.NoError(t, err)
	require.False(t, gotB.Used,
		"address B must be freed after NIC delete — a plain (unlocked) Get in Delete leaves it used=true referencing the deleted NIC (TOCTOU orphan, project-rule #10)")
	_, refErr := rd.Addresses().GetReference(ctx, addrV6)
	require.ErrorIs(t, refErr, helpers.ErrNotFound,
		"no dangling address_references row must point at the deleted NIC")
}
