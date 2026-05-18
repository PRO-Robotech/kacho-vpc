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

// KAC-88 / G1 of KAC-84 audit — address_references.SetReference race.
//
// Защита (parity c NIC SetUsedBy): SetReference (и MarkEphemeralInUse)
// делают атомарный single-statement CAS — `INSERT … ON CONFLICT (address_id)
// DO UPDATE … WHERE address_references.referrer_id = EXCLUDED.referrer_id`.
// 0 rows из RETURNING (через pgx.ErrNoRows) → helpers.ErrFailedPrecondition.
//
// KAC-94 A.7 sub-PR 5/6: переписан на CQRS Writer.
func TestIntegration_AddressRepo_SetReferenceRace(t *testing.T) {
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

	addr := &domain.Address{
		ID:        ids.NewID(ids.PrefixAddress),
		ProjectID: "folder-setref-race",
		Name:      domain.RcNameVPC("addr-setref-race"),
		Type:      domain.AddressTypeExternal,
		IpVersion: domain.IpVersionIPv4,
		ExternalIpv4: &domain.ExternalIpv4Spec{
			Address: "203.0.113.88",
			ZoneID:  "ru-central1-a",
		},
	}
	require.NoError(t, withTx(t, func(w kacho.RepositoryWriter) error {
		_, e := w.Addresses().Insert(ctx, addr)
		return e
	}))

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
		referrerID := "e9bnic" + ids.NewID(ids.PrefixSubnet)
		wg.Add(1)
		go func(rid string) {
			defer wg.Done()
			<-start
			var ref *domain.AddressReference
			err := withTx(t, func(w kacho.RepositoryWriter) error {
				var e error
				ref, e = w.Addresses().SetReference(ctx, &domain.AddressReference{
					AddressID:    addr.ID,
					ReferrerType: "network_interface",
					ReferrerID:   rid,
					ReferrerName: "nic-" + rid[:6],
				})
				return e
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
			case errors.Is(err, helpers.ErrFailedPrecondition):
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
		"остальные N-1 SetReference должны получить helpers.ErrFailedPrecondition (got %d conflicts)", conflict.Load())

	require.NotEmpty(t, winner, "должен быть зафиксирован победитель")

	rd, err := r.Reader(ctx)
	require.NoError(t, err)
	gotRef, err := rd.Addresses().GetReference(ctx, addr.ID)
	require.NoError(t, err)
	gotAddr, err := rd.Addresses().Get(ctx, addr.ID)
	require.NoError(t, rd.Close())
	require.NoError(t, err)
	require.Equal(t, winner, gotRef.ReferrerID,
		"DB state должен равняться referrer-у победителя")
	require.True(t, gotAddr.Used, "address.used должен быть true после winning SetReference")
}

// KAC-88 — Re-attach к тому же referrer должен быть idempotent.
func TestIntegration_AddressRepo_SetReferenceIdempotent(t *testing.T) {
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

	addr := &domain.Address{
		ID:        ids.NewID(ids.PrefixAddress),
		ProjectID: "folder-setref-idempotent",
		Name:      domain.RcNameVPC("addr-setref-idempotent"),
		Type:      domain.AddressTypeExternal,
		IpVersion: domain.IpVersionIPv4,
		ExternalIpv4: &domain.ExternalIpv4Spec{
			Address: "203.0.113.89",
			ZoneID:  "ru-central1-a",
		},
	}
	require.NoError(t, withTx(t, func(w kacho.RepositoryWriter) error {
		_, e := w.Addresses().Insert(ctx, addr)
		return e
	}))

	referrer := "e9bsame00000000001"
	var ref *domain.AddressReference
	require.NoError(t, withTx(t, func(w kacho.RepositoryWriter) error {
		var e error
		ref, e = w.Addresses().SetReference(ctx, &domain.AddressReference{
			AddressID:    addr.ID,
			ReferrerType: "network_interface",
			ReferrerID:   referrer,
			ReferrerName: "nic-same",
		})
		return e
	}), "первый SetReference должен пройти")
	require.Equal(t, referrer, ref.ReferrerID)

	// Idempotent re-set с тем же referrer.
	require.NoError(t, withTx(t, func(w kacho.RepositoryWriter) error {
		var e error
		ref, e = w.Addresses().SetReference(ctx, &domain.AddressReference{
			AddressID:    addr.ID,
			ReferrerType: "network_interface",
			ReferrerID:   referrer,
			ReferrerName: "nic-same-renamed",
		})
		return e
	}), "повторный SetReference с тем же referrer — idempotent")
	require.Equal(t, referrer, ref.ReferrerID)
	require.Equal(t, "nic-same-renamed", ref.ReferrerName)

	// Другой referrer должен fail-ить.
	err = withTx(t, func(w kacho.RepositoryWriter) error {
		_, e := w.Addresses().SetReference(ctx, &domain.AddressReference{
			AddressID:    addr.ID,
			ReferrerType: "network_interface",
			ReferrerID:   "e9bother0000000001",
			ReferrerName: "nic-other",
		})
		return e
	})
	require.Error(t, err)
	require.True(t, errors.Is(err, helpers.ErrFailedPrecondition),
		"SetReference к занятому address от чужого referrer-а → helpers.ErrFailedPrecondition, got %v", err)

	// После ClearReference адрес снова свободен.
	require.NoError(t, withTx(t, func(w kacho.RepositoryWriter) error {
		return w.Addresses().ClearReference(ctx, addr.ID)
	}))
	require.NoError(t, withTx(t, func(w kacho.RepositoryWriter) error {
		_, e := w.Addresses().SetReference(ctx, &domain.AddressReference{
			AddressID:    addr.ID,
			ReferrerType: "network_interface",
			ReferrerID:   "e9bafter0000000001",
			ReferrerName: "nic-after-clear",
		})
		return e
	}), "после ClearReference новый referrer должен проходить")
}
