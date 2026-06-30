// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package pg_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coredb "github.com/PRO-Robotech/kacho-corelib/db"
	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
	kachopg "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/pg"
)

// Integration-тесты (testcontainers Postgres, concurrent goroutines) финальных
// DB-гейтов anycast-аллокации, которые НЕ ловятся unit-тестом:
//   GWT-31 — глобально-уникальный addresses_anycast_host_uniq: два конкурентных
//            auto-alloc из пула с одним свободным адресом → один host выдан, не дважды;
//   GWT-34 — used_by-CAS (SetReference) на свободном anycast-Address: два конкурентных
//            BYO-привязки от разных referrer → ровно один winner, single-owner.

// newAnycastAddress — anycast-Address (id-prefix adr) с anycast-spec
// {network_id, pool_id, address}. host="" → ещё не аллоцирован (пустой
// anycast->>'address' → в partial expression-индексе addresses_anycast_host_uniq
// не участвует). used — system-managed флаг занятости.
func newAnycastAddress(project, name, networkID, poolID, host string, used bool) *domain.Address {
	return &domain.Address{
		ID:          ids.NewID(ids.PrefixAddress),
		ProjectID:   project,
		Name:        domain.RcNameVPC(name),
		Description: domain.RcDescription(""),
		Labels:      domain.LabelsFromMap(nil),
		Type:        domain.AddressTypeInternal,
		IpVersion:   domain.IpVersionIPv4,
		Used:        used,
		Anycast: &domain.AnycastSpec{
			NetworkID:     networkID,
			AnycastPoolID: poolID,
			Address:       host,
		},
	}
}

// TestIntegration_Anycast_ConcurrentHostAlloc_OneWins — GWT-31: два конкурентных
// auto-alloc из пула 100.64.201.0/31 (единственный usable host 100.64.201.1).
// Каждая goroutine воспроизводит use-case-флоу аллокации: Insert anycast-Address
// (без host) → SetAnycast(host). Финальный DB-гейт — addresses_anycast_host_uniq:
// ровно один INSERT host'а проходит, второй получает unique-violation
// (ErrAlreadyExists, маппится use-case'ом в generic "could not allocate anycast
// address"). Один и тот же host НЕ выдан дважды. Race не ловится unit-тестом.
func TestIntegration_Anycast_ConcurrentHostAlloc_OneWins(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	pgPool, err := coredb.NewPool(ctx, setupTestDB(t))
	require.NoError(t, err)
	defer pgPool.Close()
	r := kachopg.New(pgPool, nil)
	defer r.Close()

	netID := seedNetwork(t, ctx, r, "prj-31", "net-31")
	poolID := seedAnycastPool(t, ctx, r, "prj-31", "aap-31", []string{"100.64.201.0/31"})
	require.NoError(t, anycastTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		return w.AnycastAddressPools().AttachNetwork(ctx, poolID, netID, []string{"100.64.201.0/31"})
	}))

	// /31 → единственный аллоцируемый host 100.64.201.1 — обе goroutine
	// детерминированно борются за него.
	const host = "100.64.201.1"

	var (
		wg       sync.WaitGroup
		ok       atomic.Int32
		conflict atomic.Int32
		other    = make(chan error, 2)
		start    = make(chan struct{})
	)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			addr := newAnycastAddress("prj-31", "anycast-31", netID, poolID, "", true)
			aerr := anycastTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
				if _, e := w.Addresses().Insert(ctx, addr); e != nil {
					return e
				}
				// SetAnycast ставит host; глобально-уникальный expression-индекс
				// addresses_anycast_host_uniq (anycast->>'address', 23505) отбивает
				// уже выданный IP.
				_, e := w.Addresses().SetAnycast(ctx, addr.ID, &domain.AnycastSpec{
					NetworkID:     netID,
					AnycastPoolID: poolID,
					Address:       host,
				})
				return e
			})
			switch {
			case aerr == nil:
				ok.Add(1)
			case errors.Is(aerr, repo.ErrAlreadyExists):
				conflict.Add(1)
			default:
				other <- aerr
			}
		}()
	}
	close(start)
	wg.Wait()
	close(other)
	for e := range other {
		t.Fatalf("unexpected error in concurrent anycast host alloc: %v", e)
	}

	assert.Equal(t, int32(1), ok.Load(), "ровно один auto-alloc получает адрес")
	assert.Equal(t, int32(1), conflict.Load(), "второй получает unique-violation (generic could-not-allocate)")

	// Один host НЕ выдан дважды: ровно одна addresses-строка с этим
	// anycast->>'address' (проигравшая writer-TX откатана целиком — её
	// Address-строка тоже снята).
	var hosts int
	require.NoError(t, pgPool.QueryRow(ctx,
		"SELECT count(*) FROM addresses WHERE (anycast ->> 'address') = $1", host).Scan(&hosts))
	assert.Equal(t, 1, hosts, "один и тот же anycast host выдан ровно один раз")
}

// TestIntegration_Anycast_UsedByCAS_OneWins — GWT-34: на свободный anycast-Address
// (used_by пуст) два конкурентных BYO-привязки от РАЗНЫХ referrer. used_by-CAS
// (SetReference: INSERT … ON CONFLICT (address_id) DO UPDATE … WHERE referrer_id =
// EXCLUDED.referrer_id, 0 rows → ErrFailedPrecondition) → ровно один CAS проходит;
// второй получает ErrFailedPrecondition (use-case addressref маппит в "address is
// already in use"). Адрес привязан ровно к одному owner'у. Race не ловится unit-тестом.
func TestIntegration_Anycast_UsedByCAS_OneWins(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	pgPool, err := coredb.NewPool(ctx, setupTestDB(t))
	require.NoError(t, err)
	defer pgPool.Close()
	r := kachopg.New(pgPool, nil)
	defer r.Close()

	netID := seedNetwork(t, ctx, r, "prj-34", "net-34")
	poolID := seedAnycastPool(t, ctx, r, "prj-34", "aap-34", []string{"100.64.34.0/24"})
	// Свободный anycast-Address (used=false, referrer'а нет).
	addr := newAnycastAddress("prj-34", "anycast-34-byo", netID, poolID, "100.64.34.7", false)
	require.NoError(t, anycastTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		_, e := w.Addresses().Insert(ctx, addr)
		return e
	}))

	var (
		wg       sync.WaitGroup
		ok       atomic.Int32
		conflict atomic.Int32
		muWin    sync.Mutex
		winner   string
		other    = make(chan error, 2)
		start    = make(chan struct{})
	)
	// Два разных referrer (две LB).
	referrers := [2]string{"lb-34-a", "lb-34-b"}
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(rid string) {
			defer wg.Done()
			<-start
			var ref *domain.AddressReference
			cerr := anycastTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
				var e error
				ref, e = w.Addresses().SetReference(ctx, &domain.AddressReference{
					AddressID:    addr.ID,
					ReferrerType: "nlb_load_balancer",
					ReferrerID:   rid,
					ReferrerName: rid,
				})
				return e
			})
			switch {
			case cerr == nil:
				muWin.Lock()
				winner = ref.ReferrerID
				muWin.Unlock()
				ok.Add(1)
			case errors.Is(cerr, repo.ErrFailedPrecondition):
				conflict.Add(1)
			default:
				other <- cerr
			}
		}(referrers[i])
	}
	close(start)
	wg.Wait()
	close(other)
	for e := range other {
		t.Fatalf("unexpected error in concurrent used_by CAS: %v", e)
	}

	assert.Equal(t, int32(1), ok.Load(), "ровно один CAS привязки проходит")
	assert.Equal(t, int32(1), conflict.Load(), "второй → ErrFailedPrecondition (address is already in use)")
	require.NotEmpty(t, winner)

	// Адрес привязан ровно к одному owner'у — winner'у; used=true.
	rd, err := r.Reader(ctx)
	require.NoError(t, err)
	gotRef, err := rd.Addresses().GetReference(ctx, addr.ID)
	require.NoError(t, err)
	gotAddr, err := rd.Addresses().Get(ctx, addr.ID)
	require.NoError(t, rd.Close())
	require.NoError(t, err)
	assert.Equal(t, winner, gotRef.ReferrerID, "DB-state = referrer победителя (single-owner)")
	assert.True(t, gotAddr.Used, "address.used=true после winning CAS")

	var refs int
	require.NoError(t, pgPool.QueryRow(ctx,
		"SELECT count(*) FROM address_references WHERE address_id = $1", addr.ID).Scan(&refs))
	assert.Equal(t, 1, refs, "адрес не привязан к двум LB одновременно")
}
