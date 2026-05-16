package repo_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	coredb "github.com/PRO-Robotech/kacho-corelib/db"
	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
)

// KAC-88 / G1 of KAC-84 audit — address_references.SetReference race.
//
// До этой правки AddressRepo.SetReference делал `INSERT ... ON CONFLICT
// (address_id) DO UPDATE` без CAS-условия. Любой конкурирующий референсер
// молча перетирал существующего referrer'а:
//
//	worker A: SELECT addr.used=false → guard pass → SetReference(referrer=NIC_A)  → row written
//	worker B: SELECT addr.used=false → guard pass → SetReference(referrer=NIC_B)  → row overwritten
//
// Это точный parity-case с инцидентом 2026-05-14 (KAC-52, NIC-attach race):
// два независимых ресурса (NIC_A и NIC_B) считают address-X своим, но
// address_references.referrer_id указывает только на одного из них.
//
// Защита (parity c NIC SetUsedBy): repo.SetReference (и MarkEphemeralInUse)
// делают атомарный single-statement CAS — `INSERT … ON CONFLICT (address_id)
// DO UPDATE … WHERE address_references.referrer_id = EXCLUDED.referrer_id`.
// 0 rows из RETURNING (через pgx.ErrNoRows) → repo.ErrFailedPrecondition.
// Single-statement upsert на одной row защищён row-level lock-ом Postgres:
// конкурентный writer ждёт commit-а первого, видит уже заполнённую row,
// CAS не matches → 0 rows.
//
// Этот тест запускает N goroutines, каждая пытается SetReference на один и тот
// же address со своим referrer_id. Инвариант: **ровно одна** транзакция
// успешна, остальные получают repo.ErrFailedPrecondition. В БД referrer_id равен
// победителю.
func TestIntegration_AddressRepo_SetReferenceRace(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx := context.Background()
	dsn := setupTestDB(t)
	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	ar := repo.NewAddressRepo(pool)

	_ = time.Now().UTC().Truncate(time.Microsecond) // CreatedAt — DB-managed, см. KAC-94 Wave 2.
	addr := &domain.Address{
		ID:        ids.NewID(ids.PrefixAddress),
		FolderID:  "folder-setref-race",
		Name:      domain.RcNameVPC("addr-setref-race"),
		Type:      domain.AddressTypeExternal,
		IpVersion: domain.IpVersionIPv4,
		ExternalIpv4: &domain.ExternalIpv4Spec{
			Address: "203.0.113.88",
			ZoneID:  "ru-central1-a",
		},
	}
	_, err = ar.Insert(ctx, addr)
	require.NoError(t, err)

	const N = 5

	var (
		wg       sync.WaitGroup
		ok       atomic.Int32
		conflict atomic.Int32
		muWin    sync.Mutex
		winner   string
	)

	start := make(chan struct{})
	for i := 0; i < N; i++ {
		// Каждой goroutine — свой уникальный referrer_id.
		referrerID := "e9bnic" + ids.NewID(ids.PrefixSubnet)
		wg.Add(1)
		go func(rid string) {
			defer wg.Done()
			<-start
			ref, err := ar.SetReference(ctx, &domain.AddressReference{
				AddressID:    addr.ID,
				ReferrerType: "network_interface",
				ReferrerID:   rid,
				ReferrerName: "nic-" + rid[:6],
			})
			switch {
			case err == nil:
				require.Equalf(t, rid, ref.ReferrerID,
					"победитель должен видеть СВОЙ referrer в RETURNING; вместо %q got %q",
					rid, ref.ReferrerID)
				muWin.Lock()
				winner = rid
				muWin.Unlock()
				ok.Add(1)
			case errors.Is(err, repo.ErrFailedPrecondition):
				conflict.Add(1)
			default:
				require.Failf(t, "unexpected error", "referrer=%s err=%v", rid, err)
			}
		}(referrerID)
	}
	close(start)
	wg.Wait()

	require.Equal(t, int32(1), ok.Load(),
		"ровно один SetReference должен выиграть гонку (got %d successes)", ok.Load())
	require.Equal(t, int32(N-1), conflict.Load(),
		"остальные N-1 SetReference должны получить repo.ErrFailedPrecondition (got %d conflicts)", conflict.Load())

	// В БД referrer_id принадлежит победителю + address.used = true.
	require.NotEmpty(t, winner, "должен быть зафиксирован победитель")

	gotRef, err := ar.GetReference(ctx, addr.ID)
	require.NoError(t, err)
	require.Equal(t, winner, gotRef.ReferrerID,
		"DB state должен равняться referrer-у победителя")

	gotAddr, err := ar.Get(ctx, addr.ID)
	require.NoError(t, err)
	require.True(t, gotAddr.Used, "address.used должен быть true после winning SetReference")
}

// KAC-88 — Re-attach к тому же referrer должен быть idempotent (CAS условие
// `referrer_id = EXCLUDED.referrer_id` matches → row пишется так же, RETURNING
// возвращает row → service видит no error). Зеркало
// TestIntegration_NICRepo_AttachIdempotent.
func TestIntegration_AddressRepo_SetReferenceIdempotent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx := context.Background()
	dsn := setupTestDB(t)
	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	ar := repo.NewAddressRepo(pool)

	_ = time.Now().UTC().Truncate(time.Microsecond) // CreatedAt — DB-managed.
	addr := &domain.Address{
		ID:        ids.NewID(ids.PrefixAddress),
		FolderID:  "folder-setref-idempotent",
		Name:      domain.RcNameVPC("addr-setref-idempotent"),
		Type:      domain.AddressTypeExternal,
		IpVersion: domain.IpVersionIPv4,
		ExternalIpv4: &domain.ExternalIpv4Spec{
			Address: "203.0.113.89",
			ZoneID:  "ru-central1-a",
		},
	}
	_, err = ar.Insert(ctx, addr)
	require.NoError(t, err)

	referrer := "e9bsame00000000001"
	ref, err := ar.SetReference(ctx, &domain.AddressReference{
		AddressID:    addr.ID,
		ReferrerType: "network_interface",
		ReferrerID:   referrer,
		ReferrerName: "nic-same",
	})
	require.NoError(t, err, "первый SetReference должен пройти")
	require.Equal(t, referrer, ref.ReferrerID)

	// Idempotent re-set с тем же referrer (CAS matches → row пишется, no error).
	// Допустимо обновить referrer_name на том же referrer_id.
	ref, err = ar.SetReference(ctx, &domain.AddressReference{
		AddressID:    addr.ID,
		ReferrerType: "network_interface",
		ReferrerID:   referrer,
		ReferrerName: "nic-same-renamed",
	})
	require.NoError(t, err, "повторный SetReference с тем же referrer — idempotent")
	require.Equal(t, referrer, ref.ReferrerID)
	require.Equal(t, "nic-same-renamed", ref.ReferrerName)

	// Другой referrer должен fail-ить (parity c NIC-attach к чужому owner-у).
	_, err = ar.SetReference(ctx, &domain.AddressReference{
		AddressID:    addr.ID,
		ReferrerType: "network_interface",
		ReferrerID:   "e9bother0000000001",
		ReferrerName: "nic-other",
	})
	require.Error(t, err)
	require.True(t, errors.Is(err, repo.ErrFailedPrecondition),
		"SetReference к занятому address от чужого referrer-а → repo.ErrFailedPrecondition, got %v", err)

	// После ClearReference адрес снова свободен — новый referrer проходит.
	require.NoError(t, ar.ClearReference(ctx, addr.ID))
	_, err = ar.SetReference(ctx, &domain.AddressReference{
		AddressID:    addr.ID,
		ReferrerType: "network_interface",
		ReferrerID:   "e9bafter0000000001",
		ReferrerName: "nic-after-clear",
	})
	require.NoError(t, err, "после ClearReference новый referrer должен проходить")
}
