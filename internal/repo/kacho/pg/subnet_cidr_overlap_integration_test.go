// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package pg_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coredb "github.com/PRO-Robotech/kacho-corelib/db"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	kachocore "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
	kachopg "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/pg"
)

// DB-level non-overlap для ВСЕХ CIDR-блоков подсетей в пределах одной Network
// (нормализованная child-таблица subnet_cidr_blocks + EXCLUDE gist). Baseline
// EXCLUDE subnets_no_overlap_v4/v6 покрывает лишь primary-блок (массив[1]);
// вторичные блоки (добавляемые SetCidrBlocks через AddCidrBlocks) под него не
// попадают. Эти тесты доказывают, что вторичные блоки тоже под DB-инвариантом.
//
// Покрывает acceptance-сценарии:
//   02/03/11 — secondary пересекает primary/secondary чужой подсети → FailedPrecondition;
//   01       — disjoint secondary → OK (нет ложных пересечений);
//   09       — concurrent add на ДВЕ разные подсети одной сети → ровно один успех;
//   12b      — идентичный блок в РАЗНОЙ сети → OK (scope = network_id);
//   14       — Remove освобождает диапазон → другая подсеть его занимает;
//   16       — Delete подсети снимает ее блоки (FK ON DELETE CASCADE).

// addSecondaryCidr воспроизводит writer-TX-флоу AddCidrBlocks на repo-уровне:
// GetForUpdate (row-lock) → merge → SetCidrBlocks → Commit. Возвращает ошибку
// репозитория (FailedPrecondition при пересечении на DB-EXCLUDE).
func addSecondaryCidr(t *testing.T, ctx context.Context, r kachocore.Repository, subID string, v4add, v6add []string) error {
	t.Helper()
	w, err := r.Writer(ctx)
	require.NoError(t, err)
	defer w.Abort()

	sub, err := w.Subnets().GetForUpdate(ctx, subID)
	if err != nil {
		return err
	}
	mergedV4 := append(append([]string{}, sub.V4CidrBlocks...), v4add...)
	mergedV6 := append(append([]string{}, sub.V6CidrBlocks...), v6add...)
	if _, err := w.Subnets().SetCidrBlocks(ctx, subID, mergedV4, mergedV6); err != nil {
		return err
	}
	return w.Commit()
}

// seedNetworkSubnet — parent Network + одна подсеть с переданным primary v4-блоком.
func seedNetworkSubnet(t *testing.T, ctx context.Context, r kachocore.Repository, project, netName, subName string, v4 []string) (netID, subID string) {
	t.Helper()
	wn, err := r.Writer(ctx)
	require.NoError(t, err)
	net := newNetwork(project, netName)
	_, err = wn.Networks().Insert(ctx, net)
	require.NoError(t, err)
	require.NoError(t, wn.Commit())

	ws, err := r.Writer(ctx)
	require.NoError(t, err)
	sub := newSubnet(project, subName, net.ID, "zone-a", v4)
	created, err := ws.Subnets().Insert(ctx, sub)
	require.NoError(t, err)
	require.NoError(t, ws.Commit())
	return net.ID, created.ID
}

// TestIntegration_SecondaryCidrOverlap_CrossSubnet_PrimaryHit — вторичный блок
// sub-1 пересекается с PRIMARY-блоком sub-2 той же сети → FailedPrecondition
// (acceptance 02). RED на baseline: primary sub-1 не меняется при append,
// отдельной DB-защиты на вторичный блок нет → пересечение проходит.
func TestIntegration_SecondaryCidrOverlap_CrossSubnet_PrimaryHit(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	pool, err := coredb.NewPool(ctx, setupTestDB(t))
	require.NoError(t, err)
	defer pool.Close()
	r := kachopg.New(pool, nil)
	defer r.Close()

	netID, sub1 := seedNetworkSubnet(t, ctx, r, "proj-sec-02", "net-sec-02", "sub-1", []string{"10.0.0.0/24"})
	// sub-2 в той же сети с primary 10.0.9.0/24.
	ws, err := r.Writer(ctx)
	require.NoError(t, err)
	_, err = ws.Subnets().Insert(ctx, newSubnet("proj-sec-02", "sub-2", netID, "zone-a", []string{"10.0.9.0/24"}))
	require.NoError(t, err)
	require.NoError(t, ws.Commit())

	// secondary 10.0.9.128/25 у sub-1 пересекается с primary sub-2.
	err = addSecondaryCidr(t, ctx, r, sub1, []string{"10.0.9.128/25"}, nil)
	require.Error(t, err, "secondary-vs-primary overlap must be rejected on DB-level")
	assert.True(t, errors.Is(err, repo.ErrFailedPrecondition), "want ErrFailedPrecondition, got %v", err)
	assert.Contains(t, err.Error(), "Subnet CIDRs can not overlap")

	// sub-1 не изменен — secondary не добавлен.
	rd, err := r.Reader(ctx)
	require.NoError(t, err)
	defer func() { _ = rd.Close() }()
	got, err := rd.Subnets().Get(ctx, sub1)
	require.NoError(t, err)
	assert.Equal(t, []string{"10.0.0.0/24"}, got.V4CidrBlocks)
}

// TestIntegration_SecondaryCidrOverlap_CrossSubnet_SecondaryHit — вторичный блок
// sub-1 пересекается с ВТОРИЧНЫМ блоком sub-2 (полное покрытие пар
// secondary×secondary, acceptance 03).
func TestIntegration_SecondaryCidrOverlap_CrossSubnet_SecondaryHit(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	pool, err := coredb.NewPool(ctx, setupTestDB(t))
	require.NoError(t, err)
	defer pool.Close()
	r := kachopg.New(pool, nil)
	defer r.Close()

	netID, sub1 := seedNetworkSubnet(t, ctx, r, "proj-sec-03", "net-sec-03", "sub-1", []string{"10.0.0.0/24"})
	ws, err := r.Writer(ctx)
	require.NoError(t, err)
	sub2created, err := ws.Subnets().Insert(ctx, newSubnet("proj-sec-03", "sub-2", netID, "zone-a", []string{"10.0.9.0/24"}))
	require.NoError(t, err)
	require.NoError(t, ws.Commit())

	// sub-2 получает вторичный 10.0.20.0/24.
	require.NoError(t, addSecondaryCidr(t, ctx, r, sub2created.ID, []string{"10.0.20.0/24"}, nil))

	// sub-1 пытается взять тот же 10.0.20.0/24 → пересечение secondary×secondary.
	err = addSecondaryCidr(t, ctx, r, sub1, []string{"10.0.20.0/24"}, nil)
	require.Error(t, err, "secondary-vs-secondary overlap must be rejected")
	assert.True(t, errors.Is(err, repo.ErrFailedPrecondition), "want ErrFailedPrecondition, got %v", err)
	assert.Contains(t, err.Error(), "Subnet CIDRs can not overlap")
}

// TestIntegration_SecondaryCidrOverlap_Disjoint_OK — непересекающийся вторичный
// блок добавляется успешно (acceptance 01); guard против ложных пересечений.
func TestIntegration_SecondaryCidrOverlap_Disjoint_OK(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	pool, err := coredb.NewPool(ctx, setupTestDB(t))
	require.NoError(t, err)
	defer pool.Close()
	r := kachopg.New(pool, nil)
	defer r.Close()

	_, sub1 := seedNetworkSubnet(t, ctx, r, "proj-sec-01", "net-sec-01", "sub-1", []string{"10.0.0.0/24"})

	require.NoError(t, addSecondaryCidr(t, ctx, r, sub1, []string{"10.0.1.0/24"}, nil),
		"disjoint secondary block must succeed")

	rd, err := r.Reader(ctx)
	require.NoError(t, err)
	defer func() { _ = rd.Close() }()
	got, err := rd.Subnets().Get(ctx, sub1)
	require.NoError(t, err)
	assert.Equal(t, []string{"10.0.0.0/24", "10.0.1.0/24"}, got.V4CidrBlocks, "order preserved, primary unchanged")
}

// TestIntegration_SecondaryCidrOverlap_V6_Rejected — вторичный IPv6-блок,
// пересекающийся с IPv6-блоком другой подсети → FailedPrecondition (acceptance 11).
func TestIntegration_SecondaryCidrOverlap_V6_Rejected(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	pool, err := coredb.NewPool(ctx, setupTestDB(t))
	require.NoError(t, err)
	defer pool.Close()
	r := kachopg.New(pool, nil)
	defer r.Close()

	// sub-6a с v6 primary 2001:db8:1::/48.
	wn, err := r.Writer(ctx)
	require.NoError(t, err)
	net := newNetwork("proj-sec-11", "net-sec-11")
	_, err = wn.Networks().Insert(ctx, net)
	require.NoError(t, err)
	require.NoError(t, wn.Commit())

	sub6a := newSubnet("proj-sec-11", "sub-6a", net.ID, "zone-a", nil)
	sub6a.V6CidrBlocks = []string{"2001:db8:1::/48"}
	sub6b := newSubnet("proj-sec-11", "sub-6b", net.ID, "zone-a", nil)
	sub6b.V6CidrBlocks = []string{"2001:db8:2::/48"}
	ws, err := r.Writer(ctx)
	require.NoError(t, err)
	_, err = ws.Subnets().Insert(ctx, sub6a)
	require.NoError(t, err)
	created6b, err := ws.Subnets().Insert(ctx, sub6b)
	require.NoError(t, err)
	require.NoError(t, ws.Commit())

	// sub-6b добавляет 2001:db8:1:abcd::/64 (входит в 2001:db8:1::/48 sub-6a).
	err = addSecondaryCidr(t, ctx, r, created6b.ID, nil, []string{"2001:db8:1:abcd::/64"})
	require.Error(t, err, "secondary v6 overlap must be rejected")
	assert.True(t, errors.Is(err, repo.ErrFailedPrecondition), "want ErrFailedPrecondition, got %v", err)
	assert.Contains(t, err.Error(), "Subnet CIDRs can not overlap")
}

// TestIntegration_SecondaryCidrOverlap_CrossNetwork_OK — идентичный блок в РАЗНОЙ
// сети не конфликтует (scope = network_id, acceptance 12b).
func TestIntegration_SecondaryCidrOverlap_CrossNetwork_OK(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	pool, err := coredb.NewPool(ctx, setupTestDB(t))
	require.NoError(t, err)
	defer pool.Close()
	r := kachopg.New(pool, nil)
	defer r.Close()

	_, _ = seedNetworkSubnet(t, ctx, r, "proj-sec-12", "net-A-12", "sub-1", []string{"10.0.0.0/24"})
	_, subB1 := seedNetworkSubnet(t, ctx, r, "proj-sec-12", "net-B-12", "sub-b1", []string{"192.168.0.0/24"})

	// sub-B1 (net-B) берет 10.0.0.0/24 — занят sub-1, но в ДРУГОЙ сети → OK.
	require.NoError(t, addSecondaryCidr(t, ctx, r, subB1, []string{"10.0.0.0/24"}, nil),
		"identical block in a different network must be allowed")
}

// TestIntegration_SecondaryCidrOverlap_ConcurrentTwoSubnets_OneWins — две
// goroutine добавляют один и тот же блок к РАЗНЫМ подсетям одной сети; ровно
// одна проходит, другая получает FailedPrecondition (acceptance 09). GetForUpdate
// сериализует только ту же подсеть — cross-subnet защищает лишь network-scoped
// EXCLUDE. Центральный P1-сценарий: race не ловится unit-тестом.
func TestIntegration_SecondaryCidrOverlap_ConcurrentTwoSubnets_OneWins(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	pool, err := coredb.NewPool(ctx, setupTestDB(t))
	require.NoError(t, err)
	defer pool.Close()
	r := kachopg.New(pool, nil)
	defer r.Close()

	netID, sub1 := seedNetworkSubnet(t, ctx, r, "proj-sec-09", "net-sec-09", "sub-1", []string{"10.0.0.0/24"})
	ws, err := r.Writer(ctx)
	require.NoError(t, err)
	sub2, err := ws.Subnets().Insert(ctx, newSubnet("proj-sec-09", "sub-2", netID, "zone-a", []string{"10.1.0.0/24"}))
	require.NoError(t, err)
	require.NoError(t, ws.Commit())

	var (
		wg   sync.WaitGroup
		errs [2]error
	)
	targets := [2]string{sub1, sub2.ID}
	wg.Add(2)
	for i := 0; i < 2; i++ {
		go func(idx int) {
			defer wg.Done()
			errs[idx] = addSecondaryCidr(t, ctx, r, targets[idx], []string{"10.50.0.0/24"}, nil)
		}(i)
	}
	wg.Wait()

	okCount := 0
	for _, e := range errs {
		if e == nil {
			okCount++
			continue
		}
		assert.True(t, errors.Is(e, repo.ErrFailedPrecondition), "loser must get ErrFailedPrecondition, got %v", e)
		assert.Contains(t, e.Error(), "Subnet CIDRs can not overlap")
	}
	assert.Equal(t, 1, okCount, "exactly one concurrent overlapping secondary add must win")

	// Блок 10.50.0.0/24 присутствует ровно у одной подсети сети.
	var cnt int
	require.NoError(t, pool.QueryRow(ctx,
		"SELECT count(*) FROM subnet_cidr_blocks WHERE network_id = $1 AND block = '10.50.0.0/24'::cidr", netID,
	).Scan(&cnt))
	assert.Equal(t, 1, cnt, "range belongs to exactly one subnet of the network")
}

// TestIntegration_SecondaryCidrOverlap_RemoveFreesRange — снятие вторичного блока
// освобождает диапазон для другой подсети той же сети (acceptance 14).
func TestIntegration_SecondaryCidrOverlap_RemoveFreesRange(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	pool, err := coredb.NewPool(ctx, setupTestDB(t))
	require.NoError(t, err)
	defer pool.Close()
	r := kachopg.New(pool, nil)
	defer r.Close()

	netID, sub1 := seedNetworkSubnet(t, ctx, r, "proj-sec-14", "net-sec-14", "sub-1", []string{"10.0.0.0/24"})
	require.NoError(t, addSecondaryCidr(t, ctx, r, sub1, []string{"10.0.5.0/24"}, nil))
	_, sub2 := seedNetworkSubnetInExisting(t, ctx, r, "proj-sec-14", netID, "sub-2", []string{"10.1.0.0/24"})

	// Перед remove: sub-2 не может взять 10.0.5.0/24 — занят sub-1.
	require.Error(t, addSecondaryCidr(t, ctx, r, sub2, []string{"10.0.5.0/24"}, nil),
		"pre-remove the range is taken by sub-1")

	// Remove 10.0.5.0/24 с sub-1: SetCidrBlocks тем же путем (remaining-набор).
	w, err := r.Writer(ctx)
	require.NoError(t, err)
	_, err = w.Subnets().SetCidrBlocks(ctx, sub1, []string{"10.0.0.0/24"}, nil)
	require.NoError(t, err)
	require.NoError(t, w.Commit())

	// Теперь sub-2 успешно занимает освобожденный 10.0.5.0/24.
	require.NoError(t, addSecondaryCidr(t, ctx, r, sub2, []string{"10.0.5.0/24"}, nil),
		"removed range must become reusable by another subnet")
}

// TestIntegration_SecondaryCidrOverlap_DeleteFreesRange — Delete подсети снимает
// ее блоки из инварианта (FK ON DELETE CASCADE), диапазон занимаем заново
// (acceptance 16).
func TestIntegration_SecondaryCidrOverlap_DeleteFreesRange(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	pool, err := coredb.NewPool(ctx, setupTestDB(t))
	require.NoError(t, err)
	defer pool.Close()
	r := kachopg.New(pool, nil)
	defer r.Close()

	netID, sub8 := seedNetworkSubnet(t, ctx, r, "proj-sec-16", "net-sec-16", "sub-8", []string{"10.0.0.0/24"})
	require.NoError(t, addSecondaryCidr(t, ctx, r, sub8, []string{"10.0.7.0/24"}, nil))

	// Delete sub-8 (без Address/NIC — удаляема).
	w, err := r.Writer(ctx)
	require.NoError(t, err)
	require.NoError(t, w.Subnets().Delete(ctx, sub8))
	require.NoError(t, w.Commit())

	// child-строки sub-8 сняты каскадом.
	var cnt int
	require.NoError(t, pool.QueryRow(ctx,
		"SELECT count(*) FROM subnet_cidr_blocks WHERE network_id = $1", netID).Scan(&cnt))
	assert.Equal(t, 0, cnt, "delete cascades subnet_cidr_blocks rows")

	// Новая подсеть берет оба освобожденных диапазона.
	_, sub9 := seedNetworkSubnetInExisting(t, ctx, r, "proj-sec-16", netID, "sub-9", []string{"10.0.0.0/24"})
	require.NoError(t, addSecondaryCidr(t, ctx, r, sub9, []string{"10.0.7.0/24"}, nil),
		"freed ranges reusable after subnet delete")
}

// seedNetworkSubnetInExisting — подсеть в УЖЕ существующей сети.
func seedNetworkSubnetInExisting(t *testing.T, ctx context.Context, r kachocore.Repository, project, netID, subName string, v4 []string) (string, string) {
	t.Helper()
	ws, err := r.Writer(ctx)
	require.NoError(t, err)
	created, err := ws.Subnets().Insert(ctx, newSubnet(project, subName, netID, "zone-a", v4))
	require.NoError(t, err)
	require.NoError(t, ws.Commit())
	return netID, created.ID
}
