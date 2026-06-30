// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package pg_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coredb "github.com/PRO-Robotech/kacho-corelib/db"
	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
	kachopg "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/pg"
)

// Integration-тесты (testcontainers Postgres) DB-уровневого инварианта
// AnycastAddressPool — единая child-таблица network_cidr_claims с
// EXCLUDE gist (network_id WITH =, cidr_range WITH &&). Покрывают то, что НЕ
// ловится unit-тестом: атомарность претензий сети в обе стороны (subnet поверх
// пула И пул поверх subnet'а) под реальной конкуренцией, per-network изоляцию,
// per-block регистрацию claim'ов и cleanup на detach/Delete.
//
// Покрывает acceptance-сценарии под-фазы 7.0:
//   GWT-17 — attach регистрирует claim ПО КАЖДОМУ блоку пула (+ idempotent re-attach, GWT-18);
//   GWT-21 — Subnet.Create поверх pool-claim → FailedPrecondition, subnet не создан;
//   GWT-22 — pool :attachNetwork поверх subnet-claim → FailedPrecondition, pivot не создан;
//   GWT-23 — КЛЮЧЕВОЙ: конкурентно Subnet.Create vs :attachNetwork на одну сеть → ровно один winner;
//   GWT-24 — одинаковый CIDR в РАЗНЫХ сетях → обе attach проходят (scope = network_id);
//   GWT-25 — detach снимает claim + pivot, освобождённый диапазон занимаем заново;
//   GWT-26 — CountAllocationsInNetwork (DB-факт за detach-guard) + idempotent no-op detach;
//   GWT-38 — Subnet:addCidrBlocks вторичным блоком поверх pool-claim → FailedPrecondition,
//            блок не добавлен, subnet-claim не записан (вся writer-TX откат), pool-claim цел.

// anycastTx — writer-TX обёртка: Commit на успехе, Abort на ошибке fn. Возвращает
// ошибку репозитория (FailedPrecondition при 23P01 EXCLUDE → claim-overlap).
func anycastTx(t *testing.T, ctx context.Context, r *kachopg.Repository, fn func(kacho.RepositoryWriter) error) error {
	t.Helper()
	w, err := r.Writer(ctx)
	require.NoError(t, err)
	if err := fn(w); err != nil {
		w.Abort()
		return err
	}
	return w.Commit()
}

// newAnycastPool — INTERNAL IPv4 anycast-пул со статусом ACTIVE и переданными
// блоками (источник истины — child anycast_address_pool_cidrs, материализуется в
// Insert). is_default=false (tenant-пул).
func newAnycastPool(project, name string, blocks []string) *domain.AnycastAddressPool {
	return &domain.AnycastAddressPool{
		ID:          ids.NewID(ids.PrefixAnycastPool),
		ProjectID:   project,
		Name:        domain.RcNameVPC(name),
		Description: domain.RcDescription(""),
		Labels:      domain.LabelsFromMap(nil),
		Scope:       domain.AnycastScopeInternal,
		IPVersion:   domain.IpVersionIPv4,
		CIDRBlocks:  blocks,
		IsDefault:   false,
		Status:      domain.AnycastPoolStatusActive,
	}
}

// seedAnycastPool — INSERT пула (+ материализация блоков) в отдельной committed-TX.
func seedAnycastPool(t *testing.T, ctx context.Context, r *kachopg.Repository, project, name string, blocks []string) string {
	t.Helper()
	p := newAnycastPool(project, name, blocks)
	require.NoError(t, anycastTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		_, e := w.AnycastAddressPools().Insert(ctx, p)
		return e
	}))
	return p.ID
}

// seedNetwork — только parent Network (без подсети), committed.
func seedNetwork(t *testing.T, ctx context.Context, r *kachopg.Repository, project, name string) string {
	t.Helper()
	net := newNetwork(project, name)
	require.NoError(t, anycastTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		_, e := w.Networks().Insert(ctx, net)
		return e
	}))
	return net.ID
}

// countClaims — число строк network_cidr_claims по сети с заданным kind.
func countClaims(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, networkID, kind string) int {
	t.Helper()
	var n int
	require.NoError(t, pgPool.QueryRow(ctx,
		"SELECT count(*) FROM network_cidr_claims WHERE network_id = $1 AND kind = $2", networkID, kind).Scan(&n))
	return n
}

// TestIntegration_Anycast_AttachRegistersClaimPerBlock — GWT-17/18: attach
// регистрирует claim ПО КАЖДОМУ блоку пула; повторный attach идемпотентен.
func TestIntegration_Anycast_AttachRegistersClaimPerBlock(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	pgPool, err := coredb.NewPool(ctx, setupTestDB(t))
	require.NoError(t, err)
	defer pgPool.Close()
	r := kachopg.New(pgPool, nil)
	defer r.Close()

	netID := seedNetwork(t, ctx, r, "prj-17", "net-17")
	// Мульти-блок пул (within-pool блоки disjoint) → доказываем per-block claim.
	blocks := []string{"100.64.12.0/22", "100.64.20.0/22"}
	poolID := seedAnycastPool(t, ctx, r, "prj-17", "aap-17", blocks)

	require.NoError(t, anycastTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		return w.AnycastAddressPools().AttachNetwork(ctx, poolID, netID, blocks)
	}))

	// Pivot-строка одна.
	var pivots int
	require.NoError(t, pgPool.QueryRow(ctx,
		"SELECT count(*) FROM anycast_pool_network_attachments WHERE pool_id = $1 AND network_id = $2",
		poolID, netID).Scan(&pivots))
	assert.Equal(t, 1, pivots, "ровно одна pivot-строка пул↔сеть")

	// Claim ПО КАЖДОМУ блоку пула.
	var claims int
	require.NoError(t, pgPool.QueryRow(ctx,
		"SELECT count(*) FROM network_cidr_claims WHERE network_id = $1 AND kind = 'anycast_pool' AND ref_id = $2",
		netID, poolID).Scan(&claims))
	assert.Equal(t, len(blocks), claims, "одна claim-строка на каждый блок пула")
	for _, b := range blocks {
		var c int
		require.NoError(t, pgPool.QueryRow(ctx,
			"SELECT count(*) FROM network_cidr_claims WHERE network_id = $1 AND kind = 'anycast_pool' AND ref_id = $2 AND cidr_range = $3::cidr",
			netID, poolID, b).Scan(&c))
		assert.Equalf(t, 1, c, "claim по блоку %s присутствует", b)
	}

	// Idempotent re-attach (GWT-18): без дублей и без ALREADY_EXISTS.
	require.NoError(t, anycastTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		return w.AnycastAddressPools().AttachNetwork(ctx, poolID, netID, blocks)
	}), "повторный attach — no-op")
	require.NoError(t, pgPool.QueryRow(ctx,
		"SELECT count(*) FROM network_cidr_claims WHERE network_id = $1 AND kind = 'anycast_pool' AND ref_id = $2",
		netID, poolID).Scan(&claims))
	assert.Equal(t, len(blocks), claims, "re-attach не плодит claim-строки")
}

// TestIntegration_Anycast_SubnetOverPool_Rejected — GWT-21: Subnet.Create поверх
// зарегистрированного pool-claim → FailedPrecondition; subnet не создан.
func TestIntegration_Anycast_SubnetOverPool_Rejected(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	pgPool, err := coredb.NewPool(ctx, setupTestDB(t))
	require.NoError(t, err)
	defer pgPool.Close()
	r := kachopg.New(pgPool, nil)
	defer r.Close()

	netID := seedNetwork(t, ctx, r, "prj-21", "net-21")
	poolID := seedAnycastPool(t, ctx, r, "prj-21", "aap-21", []string{"100.64.12.0/22"})
	require.NoError(t, anycastTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		return w.AnycastAddressPools().AttachNetwork(ctx, poolID, netID, []string{"100.64.12.0/22"})
	}))

	// Subnet с блоком 100.64.12.0/24 ⊂ пула → claim-конфликт.
	sub := newSubnet("prj-21", "sub-21", netID, "zone-a", []string{"100.64.12.0/24"})
	err = anycastTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		_, e := w.Subnets().Insert(ctx, sub)
		return e
	})
	require.Error(t, err, "subnet поверх pool-claim должен быть отклонён")
	assert.True(t, errors.Is(err, repo.ErrFailedPrecondition), "want ErrFailedPrecondition, got %v", err)
	assert.Contains(t, err.Error(), "network CIDR claims can not overlap")

	// Subnet не создан (writer-TX откатана целиком).
	rd, err := r.Reader(ctx)
	require.NoError(t, err)
	defer func() { _ = rd.Close() }()
	_, gerr := rd.Subnets().Get(ctx, sub.ID)
	assert.True(t, errors.Is(gerr, repo.ErrNotFound), "subnet не должен существовать, got %v", gerr)
	assert.Equal(t, 0, countClaims(t, ctx, pgPool, netID, "subnet"), "subnet-claim не записан")
}

// TestIntegration_Anycast_PoolOverSubnet_Rejected — GWT-22: :attachNetwork поверх
// существующего subnet-claim → FailedPrecondition; pivot не создан.
func TestIntegration_Anycast_PoolOverSubnet_Rejected(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	pgPool, err := coredb.NewPool(ctx, setupTestDB(t))
	require.NoError(t, err)
	defer pgPool.Close()
	r := kachopg.New(pgPool, nil)
	defer r.Close()

	// Сеть с подсетью 100.64.12.0/24 → subnet-claim зарегистрирован.
	netID, _ := seedNetworkSubnet(t, ctx, r, "prj-22", "net-22", "sub-22", []string{"100.64.12.0/24"})
	poolID := seedAnycastPool(t, ctx, r, "prj-22", "aap-22", []string{"100.64.12.0/22"})

	err = anycastTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		return w.AnycastAddressPools().AttachNetwork(ctx, poolID, netID, []string{"100.64.12.0/22"})
	})
	require.Error(t, err, "attach пула поверх subnet-claim должен быть отклонён")
	assert.True(t, errors.Is(err, repo.ErrFailedPrecondition), "want ErrFailedPrecondition, got %v", err)
	assert.Contains(t, err.Error(), "network CIDR claims can not overlap")

	// Pivot не создан (вся writer-TX, включая INSERT pivot, откатана).
	var pivots int
	require.NoError(t, pgPool.QueryRow(ctx,
		"SELECT count(*) FROM anycast_pool_network_attachments WHERE pool_id = $1", poolID).Scan(&pivots))
	assert.Equal(t, 0, pivots, "pivot-строка не должна быть создана при отклонённом attach")
	assert.Equal(t, 0, countClaims(t, ctx, pgPool, netID, "anycast_pool"), "anycast-claim не записан")
}

// TestIntegration_Anycast_AddCidrBlocks_SecondaryOverPool_Rejected — GWT-38:
// Subnet:addCidrBlocks вторичным блоком поверх pool-claim → FailedPrecondition;
// блок не добавлен; subnet-claim не записан (вся writer-TX откат); pool-claim цел.
func TestIntegration_Anycast_AddCidrBlocks_SecondaryOverPool_Rejected(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	pgPool, err := coredb.NewPool(ctx, setupTestDB(t))
	require.NoError(t, err)
	defer pgPool.Close()
	r := kachopg.New(pgPool, nil)
	defer r.Close()

	netID := seedNetwork(t, ctx, r, "prj-38", "net-38")
	poolID := seedAnycastPool(t, ctx, r, "prj-38", "aap-38", []string{"100.64.12.0/22"})
	require.NoError(t, anycastTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		return w.AnycastAddressPools().AttachNetwork(ctx, poolID, netID, []string{"100.64.12.0/22"})
	}))
	// Подсеть с primary-блоком ВНЕ пула (конфликта нет).
	_, subID := seedNetworkSubnetInExisting(t, ctx, r, "prj-38", netID, "sub-38", []string{"100.65.0.0/24"})

	// Вторичный блок 100.64.12.128/25 пересекается с пулом 100.64.12.0/22.
	err = addSecondaryCidr(t, ctx, r, subID, []string{"100.64.12.128/25"}, nil)
	require.Error(t, err, "secondary поверх pool-claim должен быть отклонён")
	assert.True(t, errors.Is(err, repo.ErrFailedPrecondition), "want ErrFailedPrecondition, got %v", err)
	assert.Contains(t, err.Error(), "network CIDR claims can not overlap")

	// Вторичный блок не добавлен — у подсети только primary.
	rd, err := r.Reader(ctx)
	require.NoError(t, err)
	defer func() { _ = rd.Close() }()
	gotSub, err := rd.Subnets().Get(ctx, subID)
	require.NoError(t, err)
	assert.Equal(t, []string{"100.65.0.0/24"}, gotSub.V4CidrBlocks, "secondary не добавлен")

	// Claim-строка вторичного блока НЕ записана (вся :addCidrBlocks-TX откатана,
	// включая DELETE+re-INSERT subnet-claim'ов).
	var secClaims int
	require.NoError(t, pgPool.QueryRow(ctx,
		"SELECT count(*) FROM network_cidr_claims WHERE network_id = $1 AND kind = 'subnet' AND cidr_range = '100.64.12.128/25'::cidr",
		netID).Scan(&secClaims))
	assert.Equal(t, 0, secClaims, "subnet-claim вторичного блока не записан")
	// Primary subnet-claim уцелел (откат вернул прежний набор).
	var primClaims int
	require.NoError(t, pgPool.QueryRow(ctx,
		"SELECT count(*) FROM network_cidr_claims WHERE network_id = $1 AND kind = 'subnet' AND ref_id = $2 AND cidr_range = '100.65.0.0/24'::cidr",
		netID, subID).Scan(&primClaims))
	assert.Equal(t, 1, primClaims, "primary subnet-claim уцелел")

	// Pool-claim не изменён.
	var poolClaim int
	require.NoError(t, pgPool.QueryRow(ctx,
		"SELECT count(*) FROM network_cidr_claims WHERE network_id = $1 AND kind = 'anycast_pool' AND ref_id = $2 AND cidr_range = '100.64.12.0/22'::cidr",
		netID, poolID).Scan(&poolClaim))
	assert.Equal(t, 1, poolClaim, "pool-claim сохранён неизменным")
}

// TestIntegration_Anycast_ConcurrentSubnetVsAttach_OneWins — GWT-23 (КЛЮЧЕВОЙ):
// конкурентно Subnet.Create и pool :attachNetwork на ОДНУ сеть с пересекающимися
// CIDR. Ровно одна операция проходит, другая → 23P01 → FailedPrecondition. В
// network_cidr_claims по сети нет двух пересекающихся строк (инвариант энфорсится
// атомарно на DB-уровне в обе стороны — НЕ software TOCTOU). Race не ловится
// unit-тестом.
func TestIntegration_Anycast_ConcurrentSubnetVsAttach_OneWins(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	pgPool, err := coredb.NewPool(ctx, setupTestDB(t))
	require.NoError(t, err)
	defer pgPool.Close()
	r := kachopg.New(pgPool, nil)
	defer r.Close()

	// Сеть без претензий; пул НЕ приаттачен (обе операции борются за первую claim).
	netID := seedNetwork(t, ctx, r, "prj-23", "net-23")
	poolID := seedAnycastPool(t, ctx, r, "prj-23", "aap-23", []string{"100.64.12.0/22"})
	// Subnet-блок 100.64.12.128/25 ⊂ пула 100.64.12.0/22 → claim'ы пересекаются.
	sub := newSubnet("prj-23", "sub-23", netID, "zone-a", []string{"100.64.12.128/25"})

	var (
		wg    sync.WaitGroup
		errs  [2]error
		start = make(chan struct{})
	)
	wg.Add(2)
	// (a) Subnet.Create.
	go func() {
		defer wg.Done()
		<-start
		errs[0] = anycastTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
			_, e := w.Subnets().Insert(ctx, sub)
			return e
		})
	}()
	// (b) pool :attachNetwork.
	go func() {
		defer wg.Done()
		<-start
		errs[1] = anycastTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
			return w.AnycastAddressPools().AttachNetwork(ctx, poolID, netID, []string{"100.64.12.0/22"})
		})
	}()
	close(start)
	wg.Wait()

	okCount := 0
	for _, e := range errs {
		if e == nil {
			okCount++
			continue
		}
		assert.True(t, errors.Is(e, repo.ErrFailedPrecondition), "проигравшая операция → ErrFailedPrecondition, got %v", e)
		assert.Contains(t, e.Error(), "network CIDR claims can not overlap")
	}
	assert.Equal(t, 1, okCount, "ровно одна из конкурентных пересекающихся операций должна выиграть")

	// Сеть стартовала пустой → после гонки ровно одна claim-строка (winner'а):
	// два пересекающихся claim'а физически невозможны (EXCLUDE).
	var total int
	require.NoError(t, pgPool.QueryRow(ctx,
		"SELECT count(*) FROM network_cidr_claims WHERE network_id = $1", netID).Scan(&total))
	assert.Equal(t, 1, total, "ровно одна претензия в сети — двух пересекающихся строк нет")
}

// TestIntegration_Anycast_CrossNetworkIsolation_OK — GWT-24: одинаковый CIDR в
// РАЗНЫХ сетях — обе attach проходят (claim scope = network_id, per-network
// изоляция; пересечение в разных сетях не считается конфликтом).
func TestIntegration_Anycast_CrossNetworkIsolation_OK(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	pgPool, err := coredb.NewPool(ctx, setupTestDB(t))
	require.NoError(t, err)
	defer pgPool.Close()
	r := kachopg.New(pgPool, nil)
	defer r.Close()

	block := []string{"100.64.12.0/22"}
	net1 := seedNetwork(t, ctx, r, "prj-24", "net-24-1")
	net2 := seedNetwork(t, ctx, r, "prj-24", "net-24-2")
	pool1 := seedAnycastPool(t, ctx, r, "prj-24", "aap-24-1", block)
	pool2 := seedAnycastPool(t, ctx, r, "prj-24", "aap-24-2", block)

	require.NoError(t, anycastTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		return w.AnycastAddressPools().AttachNetwork(ctx, pool1, net1, block)
	}), "attach в net-1 проходит")
	require.NoError(t, anycastTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		return w.AnycastAddressPools().AttachNetwork(ctx, pool2, net2, block)
	}), "тот же CIDR в net-2 проходит (per-network изоляция)")

	assert.Equal(t, 1, countClaims(t, ctx, pgPool, net1, "anycast_pool"))
	assert.Equal(t, 1, countClaims(t, ctx, pgPool, net2, "anycast_pool"))
}

// TestIntegration_Anycast_Detach_RemovesClaimAndPivot — GWT-25: detach снимает
// claim + pivot; ранее конфликтовавший Subnet.Create в сети снова проходит.
func TestIntegration_Anycast_Detach_RemovesClaimAndPivot(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	pgPool, err := coredb.NewPool(ctx, setupTestDB(t))
	require.NoError(t, err)
	defer pgPool.Close()
	r := kachopg.New(pgPool, nil)
	defer r.Close()

	netID := seedNetwork(t, ctx, r, "prj-25", "net-25")
	poolID := seedAnycastPool(t, ctx, r, "prj-25", "aap-25", []string{"100.64.12.0/22"})
	require.NoError(t, anycastTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		return w.AnycastAddressPools().AttachNetwork(ctx, poolID, netID, []string{"100.64.12.0/22"})
	}))

	// Пока пул приаттачен — пересекающийся Subnet.Create отбивается.
	conflict := newSubnet("prj-25", "sub-25-pre", netID, "zone-a", []string{"100.64.12.0/24"})
	require.Error(t, anycastTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		_, e := w.Subnets().Insert(ctx, conflict)
		return e
	}), "до detach диапазон занят пулом")

	require.NoError(t, anycastTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		return w.AnycastAddressPools().DetachNetwork(ctx, poolID, netID)
	}))

	// Pivot и claim сняты.
	var pivots int
	require.NoError(t, pgPool.QueryRow(ctx,
		"SELECT count(*) FROM anycast_pool_network_attachments WHERE pool_id = $1 AND network_id = $2",
		poolID, netID).Scan(&pivots))
	assert.Equal(t, 0, pivots, "pivot снят на detach")
	assert.Equal(t, 0, countClaims(t, ctx, pgPool, netID, "anycast_pool"), "claim снят на detach")

	// Освобождённый диапазон занимаем Subnet'ом.
	freed := newSubnet("prj-25", "sub-25-post", netID, "zone-a", []string{"100.64.12.0/24"})
	require.NoError(t, anycastTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		_, e := w.Subnets().Insert(ctx, freed)
		return e
	}), "после detach ранее конфликтовавший Subnet.Create проходит")
}

// TestIntegration_Anycast_DetachGuard_CountAllocations — GWT-26: DB-факт за
// detach-guard'ом — CountAllocationsInNetwork > 0 при живой anycast-аллокации
// пула в сети (use-case маппит это в FailedPrecondition "anycast address pool has
// allocated addresses in network"); idempotent no-op detach непривязанного пула.
func TestIntegration_Anycast_DetachGuard_CountAllocations(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	pgPool, err := coredb.NewPool(ctx, setupTestDB(t))
	require.NoError(t, err)
	defer pgPool.Close()
	r := kachopg.New(pgPool, nil)
	defer r.Close()

	netID := seedNetwork(t, ctx, r, "prj-26", "net-26")
	poolID := seedAnycastPool(t, ctx, r, "prj-26", "aap-26", []string{"100.64.12.0/22"})
	require.NoError(t, anycastTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		return w.AnycastAddressPools().AttachNetwork(ctx, poolID, netID, []string{"100.64.12.0/22"})
	}))

	// Нет аллокаций — счётчик 0.
	require.NoError(t, anycastTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		n, e := w.AnycastAddressPools().CountAllocationsInNetwork(ctx, poolID, netID)
		assert.Equal(t, int64(0), n, "до аллокации счётчик 0")
		return e
	}))

	// Живой anycast-Address пула в сети.
	addr := newAnycastAddress("prj-26", "anycast-26", netID, poolID, "100.64.12.5", true)
	require.NoError(t, anycastTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		_, e := w.Addresses().Insert(ctx, addr)
		return e
	}))

	require.NoError(t, anycastTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		n, e := w.AnycastAddressPools().CountAllocationsInNetwork(ctx, poolID, netID)
		assert.Equal(t, int64(1), n, "живая аллокация → счётчик 1 (за этим detach отвергается)")
		return e
	}))

	// Idempotent no-op detach пула, который к сети НЕ приаттачен.
	otherNet := seedNetwork(t, ctx, r, "prj-26", "net-26-other")
	require.NoError(t, anycastTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		return w.AnycastAddressPools().DetachNetwork(ctx, poolID, otherNet)
	}), "detach непривязанного пула — no-op без ошибки")
}
