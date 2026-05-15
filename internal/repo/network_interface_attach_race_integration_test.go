package repo_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	coredb "github.com/PRO-Robotech/kacho-corelib/db"
	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/ports"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
)

// KAC-52 — NIC attach race. До этой правки ports.AttachToInstance делал
// software TOCTOU (Get → check used_by_id=="" → unconditional UPDATE) и при
// concurrent Attach к одному NIC второй writer молча перезаписывал ownership
// (инцидент 2026-05-14: два Compute.Instance.Create указали один
// existing_network_interface_id → два pod-а на одной NIC → Kube-OVN отказал
// в IP-allocation для второго pod-а).
//
// Защита: repo.SetUsedBy в attach-режиме делает атомарный single-statement
// CAS — `UPDATE … WHERE id=$1 AND (used_by_id = ” OR used_by_id = $new)`,
// 0 rows из RETURNING → ports.ErrFailedPrecondition. Single-statement
// UPDATE на одной row защищён row-level lock-ом Postgres: параллельный
// writer ждёт commit-а первого, видит обновлённый row, CAS не matches.
// Никакого UNIQUE-индекса не нужно — миграция 0016 пыталась добавить такой
// backstop, но семантически запретила multi-NIC instance; откачена в 0017.
//
// Этот тест запускает N goroutines, каждая пытается attach один и тот же NIC к
// своему instance_id. Инвариант: **ровно одна** транзакция успешна, остальные
// получают ErrFailedPrecondition (CAS не matches → 0 RETURNING-rows). В БД
// used_by_id равен победителю.
func TestIntegration_NICRepo_AttachRace(t *testing.T) {
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
	nicRepo := repo.NewNetworkInterfaceRepo(pool)

	net := &domain.Network{
		ID: ids.NewID(ids.PrefixNetwork), FolderID: "folder-attach-race", Name: domain.RcNameVPC("net-attach-race"),
	}
	_, err = netRepo.Insert(ctx, net)
	require.NoError(t, err)

	sub := &domain.Subnet{
		ID: ids.NewID(ids.PrefixSubnet), FolderID: "folder-attach-race", Name: domain.RcNameVPC("sub-attach-race"), NetworkID: net.ID, ZoneID: "ru-central1-a",
		V4CidrBlocks: []string{"10.30.0.0/24"},
	}
	_, err = subnetRepo.Insert(ctx, sub)
	require.NoError(t, err)

	nic := &domain.NetworkInterface{
		ID: ids.NewID(ids.PrefixSubnet), FolderID: "folder-attach-race",
		Name: domain.RcNameVPC("nic-race"), SubnetID: sub.ID, MAC: "0e:11:22:33:44:55",
		Status: domain.NIStatusAvailable,
	}
	_, err = nicRepo.Insert(ctx, nic)
	require.NoError(t, err)

	// N конкурирующих attach: каждая goroutine пытается стать owner-ом NIC.
	const N = 16

	var (
		wg          sync.WaitGroup
		successes   atomic.Int32
		conflicts   atomic.Int32
		muWinner    sync.Mutex
		winnerOwner string
	)

	start := make(chan struct{})
	for i := 0; i < N; i++ {
		instanceID := "inst-" + ids.NewID(ids.PrefixSubnet)
		wg.Add(1)
		go func(owner string) {
			defer wg.Done()
			<-start
			res, err := nicRepo.SetUsedBy(ctx, nic.ID, "compute_instance", owner, "", domain.NIStatusActive)
			switch {
			case err == nil:
				require.Equalf(t, owner, res.UsedByID,
					"победитель должен видеть СВОЙ owner в RETURNING; вместо %q got %q",
					owner, res.UsedByID)
				muWinner.Lock()
				winnerOwner = owner
				muWinner.Unlock()
				successes.Add(1)
			case errors.Is(err, ports.ErrFailedPrecondition):
				conflicts.Add(1)
			default:
				require.Fail(t, "unexpected error", "owner=%s err=%v", owner, err)
			}
		}(instanceID)
	}
	close(start)
	wg.Wait()

	require.Equal(t, int32(1), successes.Load(), "ровно один attach должен выиграть гонку")
	require.Equal(t, int32(N-1), conflicts.Load(), "остальные attach получают ErrFailedPrecondition")

	// В БД ownership принадлежит победителю.
	got, err := nicRepo.Get(ctx, nic.ID)
	require.NoError(t, err)
	require.NotEmpty(t, winnerOwner)
	require.Equal(t, winnerOwner, got.UsedByID, "DB state должен равняться owner-у победителя")
	require.Equal(t, "compute_instance", got.UsedByType)
	require.Equal(t, domain.NIStatusActive, got.Status)
}

// KAC-52 — Re-attach к тому же owner должен быть idempotent (CAS условие
// `used_by_id = ” OR used_by_id = $new` allows re-attach к самому себе).
func TestIntegration_NICRepo_AttachIdempotent(t *testing.T) {
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
	nicRepo := repo.NewNetworkInterfaceRepo(pool)

	net := &domain.Network{
		ID: ids.NewID(ids.PrefixNetwork), FolderID: "folder-idempotent", Name: domain.RcNameVPC("net-idempotent"),
	}
	_, err = netRepo.Insert(ctx, net)
	require.NoError(t, err)
	sub := &domain.Subnet{
		ID: ids.NewID(ids.PrefixSubnet), FolderID: "folder-idempotent", Name: domain.RcNameVPC("sub-idempotent"), NetworkID: net.ID, ZoneID: "ru-central1-a",
		V4CidrBlocks: []string{"10.31.0.0/24"},
	}
	_, err = subnetRepo.Insert(ctx, sub)
	require.NoError(t, err)
	nic := &domain.NetworkInterface{
		ID: ids.NewID(ids.PrefixSubnet), FolderID: "folder-idempotent",
		Name: domain.RcNameVPC("nic-idempotent"), SubnetID: sub.ID, MAC: "0e:11:22:33:44:66",
		Status: domain.NIStatusAvailable,
	}
	_, err = nicRepo.Insert(ctx, nic)
	require.NoError(t, err)

	owner := "inst-same"
	_, err = nicRepo.SetUsedBy(ctx, nic.ID, "compute_instance", owner, "", domain.NIStatusActive)
	require.NoError(t, err, "первый attach должен пройти")

	_, err = nicRepo.SetUsedBy(ctx, nic.ID, "compute_instance", owner, "", domain.NIStatusActive)
	require.NoError(t, err, "повторный attach с тем же owner должен быть idempotent")

	// Другой owner должен fail-ить.
	_, err = nicRepo.SetUsedBy(ctx, nic.ID, "compute_instance", "inst-other", "", domain.NIStatusActive)
	require.Error(t, err)
	require.True(t, errors.Is(err, ports.ErrFailedPrecondition),
		"attach к занятому NIC чужим owner-ом → ErrFailedPrecondition, got %v", err)
}

// KAC-52 — Detach идемпотент: повторный detach уже-свободного NIC — no-op
// без error. Это сохраняет существующее поведение для admin-tooling и worker'ов,
// которые могут гонять Detach.
func TestIntegration_NICRepo_DetachIdempotent(t *testing.T) {
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
	nicRepo := repo.NewNetworkInterfaceRepo(pool)

	net := &domain.Network{
		ID: ids.NewID(ids.PrefixNetwork), FolderID: "folder-detach", Name: domain.RcNameVPC("net-detach"),
	}
	_, err = netRepo.Insert(ctx, net)
	require.NoError(t, err)
	sub := &domain.Subnet{
		ID: ids.NewID(ids.PrefixSubnet), FolderID: "folder-detach", Name: domain.RcNameVPC("sub-detach"), NetworkID: net.ID, ZoneID: "ru-central1-a",
		V4CidrBlocks: []string{"10.32.0.0/24"},
	}
	_, err = subnetRepo.Insert(ctx, sub)
	require.NoError(t, err)
	nic := &domain.NetworkInterface{
		ID: ids.NewID(ids.PrefixSubnet), FolderID: "folder-detach",
		Name: domain.RcNameVPC("nic-detach"), SubnetID: sub.ID, MAC: "0e:11:22:33:44:77",
		Status: domain.NIStatusAvailable,
	}
	_, err = nicRepo.Insert(ctx, nic)
	require.NoError(t, err)

	// Attach → Detach → Detach (idempotent).
	_, err = nicRepo.SetUsedBy(ctx, nic.ID, "compute_instance", "inst-x", "", domain.NIStatusActive)
	require.NoError(t, err)
	_, err = nicRepo.SetUsedBy(ctx, nic.ID, "", "", "", domain.NIStatusAvailable)
	require.NoError(t, err, "первый detach должен пройти")
	res, err := nicRepo.SetUsedBy(ctx, nic.ID, "", "", "", domain.NIStatusAvailable)
	require.NoError(t, err, "повторный detach уже-свободного NIC — no-op без error")
	require.Empty(t, res.UsedByID)
	require.Empty(t, res.UsedByType)
}
