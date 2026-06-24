// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package pg_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	"errors"

	coredb "github.com/PRO-Robotech/kacho-corelib/db"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/shared/serviceerr"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	kachopg "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/pg"
	"google.golang.org/grpc/status"
)

// DeleteGuarded — атомарный backstop Address.Delete: между sync-проверкой
// «in use / protected» и worker-DELETE состояние адреса могло измениться
// (NIC-attach → used=true, или включен deletion_protection). Безусловный DELETE
// каскадил бы address_references (ON DELETE CASCADE) и молча отцеплял NIC.
// DeleteGuarded удаляет ТОЛЬКО если used=false и deletion_protection=false.

// Используемый адрес (used=true) НЕ удаляется → FailedPrecondition, строка цела.
func TestCQRS_Address_DeleteGuarded_RefusesInUse(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	dsn := setupTestDB(t)
	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()
	r := kachopg.New(pool, nil)

	// Insert external address, затем attach NIC (used=true) — все committed.
	w1, err := r.Writer(ctx)
	require.NoError(t, err)
	a := newAddress("project-del", "addr-inuse", true)
	created, err := w1.Addresses().Insert(ctx, a)
	require.NoError(t, err)
	_, err = w1.Addresses().SetReference(ctx, &domain.AddressReference{
		AddressID: created.ID, ReferrerType: "compute_instance", ReferrerID: "epdinst00000001", ReferrerName: "vm",
	})
	require.NoError(t, err)
	require.NoError(t, w1.Commit())

	// DeleteGuarded должен отказать (used=true) и НЕ удалить строку/reference.
	w2, err := r.Writer(ctx)
	require.NoError(t, err)
	_, derr := w2.Addresses().DeleteGuarded(ctx, created.ID)
	require.Error(t, derr)
	require.True(t, errors.Is(derr, repo.ErrFailedPrecondition))
	require.Equal(t, codes.FailedPrecondition, status.Code(serviceerr.MapRepoErr(derr)))
	require.NoError(t, w2.Commit())

	rd, err := r.Reader(ctx)
	require.NoError(t, err)
	defer func() { _ = rd.Close() }()
	got, err := rd.Addresses().Get(ctx, created.ID)
	require.NoError(t, err, "in-use address must survive a guarded delete")
	require.True(t, got.Used)
}

// deletion_protection=true → FailedPrecondition, строка цела.
func TestCQRS_Address_DeleteGuarded_RefusesProtected(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	dsn := setupTestDB(t)
	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()
	r := kachopg.New(pool, nil)

	w1, err := r.Writer(ctx)
	require.NoError(t, err)
	a := newAddress("project-del", "addr-prot", true)
	a.DeletionProtection = true
	created, err := w1.Addresses().Insert(ctx, a)
	require.NoError(t, err)
	require.NoError(t, w1.Commit())

	w2, err := r.Writer(ctx)
	require.NoError(t, err)
	_, derr := w2.Addresses().DeleteGuarded(ctx, created.ID)
	require.Error(t, derr)
	require.True(t, errors.Is(derr, repo.ErrFailedPrecondition))
	require.NoError(t, w2.Commit())

	rd, err := r.Reader(ctx)
	require.NoError(t, err)
	defer func() { _ = rd.Close() }()
	_, err = rd.Addresses().Get(ctx, created.ID)
	require.NoError(t, err, "protected address must survive a guarded delete")
}

// Свободный адрес удаляется и возвращает свой record (для return-to-freelist).
func TestCQRS_Address_DeleteGuarded_DeletesFree(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	dsn := setupTestDB(t)
	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()
	r := kachopg.New(pool, nil)

	w1, err := r.Writer(ctx)
	require.NoError(t, err)
	a := newAddress("project-del", "addr-free", true)
	created, err := w1.Addresses().Insert(ctx, a)
	require.NoError(t, err)
	require.NoError(t, w1.Commit())

	w2, err := r.Writer(ctx)
	require.NoError(t, err)
	deleted, derr := w2.Addresses().DeleteGuarded(ctx, created.ID)
	require.NoError(t, derr)
	require.Equal(t, created.ID, deleted.ID)
	require.NoError(t, w2.Commit())

	rd, err := r.Reader(ctx)
	require.NoError(t, err)
	defer func() { _ = rd.Close() }()
	_, err = rd.Addresses().Get(ctx, created.ID)
	require.True(t, errors.Is(err, repo.ErrNotFound))
}
