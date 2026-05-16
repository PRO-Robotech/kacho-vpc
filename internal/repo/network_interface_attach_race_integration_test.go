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
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/helpers"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
	kachopg "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/pg"
)

// KAC-52 — NIC attach race. До этой правки AttachToInstance делал
// software TOCTOU (Get → check used_by_id=="" → unconditional UPDATE) и при
// concurrent Attach к одному NIC второй writer молча перезаписывал ownership
// (инцидент 2026-05-14).
//
// Защита: AttachToInstance в attach-режиме делает атомарный single-statement
// CAS — `UPDATE … WHERE id=$1 AND (used_by_id = ” OR used_by_id = $new)`,
// 0 rows из RETURNING → helpers.ErrFailedPrecondition.
//
// Этот тест запускает N goroutines, каждая пытается attach один и тот же NIC к
// своему instance_id. Инвариант: **ровно одна** транзакция успешна, остальные
// получают helpers.ErrFailedPrecondition.
//
// KAC-94 A.7 sub-PR 5/6: переписан на CQRS Writer.AttachToInstance.
func TestIntegration_NICRepo_AttachRace(t *testing.T) {
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
		ID: ids.NewID(ids.PrefixNetwork), FolderID: "folder-attach-race", Name: domain.RcNameVPC("net-attach-race"),
	}
	require.NoError(t, withTx(t, func(w kacho.RepositoryWriter) error {
		_, e := w.Networks().Insert(ctx, net)
		return e
	}))

	sub := &domain.Subnet{
		ID: ids.NewID(ids.PrefixSubnet), FolderID: "folder-attach-race", Name: domain.RcNameVPC("sub-attach-race"), NetworkID: net.ID, ZoneID: "ru-central1-a",
		V4CidrBlocks: []string{"10.30.0.0/24"},
	}
	require.NoError(t, withTx(t, func(w kacho.RepositoryWriter) error {
		_, e := w.Subnets().Insert(ctx, sub)
		return e
	}))

	nic := &domain.NetworkInterface{
		ID: ids.NewID(ids.PrefixSubnet), FolderID: "folder-attach-race",
		Name: domain.RcNameVPC("nic-race"), SubnetID: sub.ID, MAC: "0e:11:22:33:44:55",
		Status: domain.NIStatusAvailable,
	}
	require.NoError(t, withTx(t, func(w kacho.RepositoryWriter) error {
		_, e := w.NetworkInterfaces().Insert(ctx, nic)
		return e
	}))

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
			err := withTx(t, func(w kacho.RepositoryWriter) error {
				res, e := w.NetworkInterfaces().AttachToInstance(ctx, nic.ID, "compute_instance", owner, "")
				if e == nil {
					require.Equalf(t, owner, res.UsedByID,
						"победитель должен видеть СВОЙ owner в RETURNING; вместо %q got %q",
						owner, res.UsedByID)
				}
				return e
			})
			switch {
			case err == nil:
				muWinner.Lock()
				winnerOwner = owner
				muWinner.Unlock()
				successes.Add(1)
			case errors.Is(err, helpers.ErrFailedPrecondition):
				conflicts.Add(1)
			default:
				require.Fail(t, "unexpected error", "owner=%s err=%v", owner, err)
			}
		}(instanceID)
	}
	close(start)
	wg.Wait()

	require.Equal(t, int32(1), successes.Load(), "ровно один attach должен выиграть гонку")
	require.Equal(t, int32(N-1), conflicts.Load(), "остальные attach получают helpers.ErrFailedPrecondition")

	// В БД ownership принадлежит победителю.
	rd, err := r.Reader(ctx)
	require.NoError(t, err)
	got, err := rd.NetworkInterfaces().Get(ctx, nic.ID)
	require.NoError(t, rd.Close())
	require.NoError(t, err)
	require.NotEmpty(t, winnerOwner)
	require.Equal(t, winnerOwner, got.UsedByID, "DB state должен равняться owner-у победителя")
	require.Equal(t, "compute_instance", got.UsedByType)
	require.Equal(t, domain.NIStatusActive, got.Status)
}

// KAC-52 — Re-attach к тому же owner должен быть idempotent.
func TestIntegration_NICRepo_AttachIdempotent(t *testing.T) {
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

	net := &domain.Network{ID: ids.NewID(ids.PrefixNetwork), FolderID: "folder-idempotent", Name: domain.RcNameVPC("net-idempotent")}
	require.NoError(t, withTx(t, func(w kacho.RepositoryWriter) error {
		_, e := w.Networks().Insert(ctx, net)
		return e
	}))
	sub := &domain.Subnet{
		ID: ids.NewID(ids.PrefixSubnet), FolderID: "folder-idempotent", Name: domain.RcNameVPC("sub-idempotent"), NetworkID: net.ID, ZoneID: "ru-central1-a",
		V4CidrBlocks: []string{"10.31.0.0/24"},
	}
	require.NoError(t, withTx(t, func(w kacho.RepositoryWriter) error {
		_, e := w.Subnets().Insert(ctx, sub)
		return e
	}))
	nic := &domain.NetworkInterface{
		ID: ids.NewID(ids.PrefixSubnet), FolderID: "folder-idempotent",
		Name: domain.RcNameVPC("nic-idempotent"), SubnetID: sub.ID, MAC: "0e:11:22:33:44:66",
		Status: domain.NIStatusAvailable,
	}
	require.NoError(t, withTx(t, func(w kacho.RepositoryWriter) error {
		_, e := w.NetworkInterfaces().Insert(ctx, nic)
		return e
	}))

	owner := "inst-same"
	require.NoError(t, withTx(t, func(w kacho.RepositoryWriter) error {
		_, e := w.NetworkInterfaces().AttachToInstance(ctx, nic.ID, "compute_instance", owner, "")
		return e
	}), "первый attach должен пройти")

	require.NoError(t, withTx(t, func(w kacho.RepositoryWriter) error {
		_, e := w.NetworkInterfaces().AttachToInstance(ctx, nic.ID, "compute_instance", owner, "")
		return e
	}), "повторный attach с тем же owner должен быть idempotent")

	// Другой owner должен fail-ить.
	err = withTx(t, func(w kacho.RepositoryWriter) error {
		_, e := w.NetworkInterfaces().AttachToInstance(ctx, nic.ID, "compute_instance", "inst-other", "")
		return e
	})
	require.Error(t, err)
	require.True(t, errors.Is(err, helpers.ErrFailedPrecondition),
		"attach к занятому NIC чужим owner-ом → helpers.ErrFailedPrecondition, got %v", err)
}

// KAC-52 — Detach идемпотент: повторный detach уже-свободного NIC — no-op без error.
func TestIntegration_NICRepo_DetachIdempotent(t *testing.T) {
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

	net := &domain.Network{ID: ids.NewID(ids.PrefixNetwork), FolderID: "folder-detach", Name: domain.RcNameVPC("net-detach")}
	require.NoError(t, withTx(t, func(w kacho.RepositoryWriter) error {
		_, e := w.Networks().Insert(ctx, net)
		return e
	}))
	sub := &domain.Subnet{
		ID: ids.NewID(ids.PrefixSubnet), FolderID: "folder-detach", Name: domain.RcNameVPC("sub-detach"), NetworkID: net.ID, ZoneID: "ru-central1-a",
		V4CidrBlocks: []string{"10.32.0.0/24"},
	}
	require.NoError(t, withTx(t, func(w kacho.RepositoryWriter) error {
		_, e := w.Subnets().Insert(ctx, sub)
		return e
	}))
	nic := &domain.NetworkInterface{
		ID: ids.NewID(ids.PrefixSubnet), FolderID: "folder-detach",
		Name: domain.RcNameVPC("nic-detach"), SubnetID: sub.ID, MAC: "0e:11:22:33:44:77",
		Status: domain.NIStatusAvailable,
	}
	require.NoError(t, withTx(t, func(w kacho.RepositoryWriter) error {
		_, e := w.NetworkInterfaces().Insert(ctx, nic)
		return e
	}))

	// Attach → Detach → Detach (idempotent).
	require.NoError(t, withTx(t, func(w kacho.RepositoryWriter) error {
		_, e := w.NetworkInterfaces().AttachToInstance(ctx, nic.ID, "compute_instance", "inst-x", "")
		return e
	}))
	require.NoError(t, withTx(t, func(w kacho.RepositoryWriter) error {
		_, e := w.NetworkInterfaces().DetachFromInstance(ctx, nic.ID)
		return e
	}), "первый detach должен пройти")
	var detached *kacho.NetworkInterfaceRecord
	require.NoError(t, withTx(t, func(w kacho.RepositoryWriter) error {
		res, e := w.NetworkInterfaces().DetachFromInstance(ctx, nic.ID)
		if e == nil {
			detached = res
		}
		return e
	}), "повторный detach уже-свободного NIC — no-op без error")
	require.NotNil(t, detached)
	require.Empty(t, detached.UsedByID)
	require.Empty(t, detached.UsedByType)
}
