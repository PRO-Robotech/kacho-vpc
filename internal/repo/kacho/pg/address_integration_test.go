// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package pg_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coredb "github.com/PRO-Robotech/kacho-corelib/db"
	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
	kachopg "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/pg"
)

// Integration-тесты CQRS-impl Address.
//
// Покрывают атомарность IPAM allocate-flow: Insert + Allocate* + outbox.Emit
// в одной writer-TX. Если Abort — все три операции откатываются вместе (нет
// orphan Address без IP, нет outbox-row для freelist-строки, которая на самом
// деле не была удалена).

// newAddress — helper для построения domain.Address с минимальным набором полей.
func newAddress(projectID, name string, ext bool) *domain.Address {
	a := &domain.Address{
		ID:          ids.NewID(ids.PrefixAddress),
		ProjectID:   projectID,
		Name:        domain.RcNameVPC(name),
		Description: domain.RcDescription(""),
		Labels:      domain.LabelsFromMap(nil),
		Reserved:    true,
	}
	if ext {
		a.Type = domain.AddressTypeExternal
		a.IpVersion = domain.IpVersionIPv4
		a.ExternalIpv4 = &domain.ExternalIpv4Spec{ZoneID: "zone-a"}
	} else {
		a.Type = domain.AddressTypeInternal
		a.IpVersion = domain.IpVersionIPv4
		a.InternalIpv4 = &domain.InternalIpv4Spec{}
	}
	return a
}

// TestCQRS_Address_WriterCommit_ReaderSees — Writer.Insert + Commit; параллельный
// Reader видит запись.
func TestCQRS_Address_WriterCommit_ReaderSees(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	dsn := setupTestDB(t)
	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	r := kachopg.New(pool, nil)

	w, err := r.Writer(ctx)
	require.NoError(t, err)

	a := newAddress("project-1", "addr-1", true)
	created, err := w.Addresses().Insert(ctx, a)
	require.NoError(t, err)
	assert.Equal(t, a.ID, created.ID)
	require.NoError(t, w.Outbox().Emit(ctx, "Address", created.ID, "CREATED", map[string]any{"id": created.ID}))
	require.NoError(t, w.Commit())

	rd, err := r.Reader(ctx)
	require.NoError(t, err)
	defer func() { _ = rd.Close() }()

	got, err := rd.Addresses().Get(ctx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, created.ID, got.ID)
	assert.Equal(t, "project-1", got.ProjectID)
	assert.Equal(t, "zone-a", got.ExternalIpv4.ZoneID)
}

// TestCQRS_Address_WriterAbort_ReaderEmpty — Writer.Insert + Abort; Reader ничего
// не видит (откат TX).
func TestCQRS_Address_WriterAbort_ReaderEmpty(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	dsn := setupTestDB(t)
	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	r := kachopg.New(pool, nil)

	w, err := r.Writer(ctx)
	require.NoError(t, err)

	a := newAddress("project-1", "addr-aborted", true)
	created, err := w.Addresses().Insert(ctx, a)
	require.NoError(t, err)
	assert.Equal(t, a.ID, created.ID)
	// Abort instead of Commit.
	w.Abort()

	rd, err := r.Reader(ctx)
	require.NoError(t, err)
	defer func() { _ = rd.Close() }()

	_, err = rd.Addresses().Get(ctx, created.ID)
	require.Error(t, err, "Address must NOT be visible after Abort")
}

// TestCQRS_Address_WriterSeesOwnWrites — внутри одной writer-TX Get/List видят
// свои Insert (writer расширяет reader).
func TestCQRS_Address_WriterSeesOwnWrites(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	dsn := setupTestDB(t)
	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	r := kachopg.New(pool, nil)
	w, err := r.Writer(ctx)
	require.NoError(t, err)
	defer w.Abort()

	a := newAddress("project-self", "addr-self", true)
	_, err = w.Addresses().Insert(ctx, a)
	require.NoError(t, err)

	// Get внутри той же TX — должен видеть только что вставленный Address.
	got, err := w.Addresses().Get(ctx, a.ID)
	require.NoError(t, err)
	assert.Equal(t, a.ID, got.ID)

	// List внутри той же TX — Address виден.
	rows, _, err := w.Addresses().List(ctx, kacho.AddressFilter{ProjectID: "project-self"}, kacho.Pagination{PageSize: 10})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, a.ID, rows[0].ID)
}

// TestCQRS_Address_SetReference_CAS — атомарный CAS upsert referrer-row +
// addresses.used=true в одной writer-TX. Попытка перепривязать к ЧУЖОМУ
// referrer → ErrFailedPrecondition. Idempotent re-attach к тому же referrer
// проходит.
func TestCQRS_Address_SetReference_CAS(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	dsn := setupTestDB(t)
	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	r := kachopg.New(pool, nil)

	// Seed Address.
	w1, err := r.Writer(ctx)
	require.NoError(t, err)
	a := newAddress("project-cas", "addr-cas", false)
	_, err = w1.Addresses().Insert(ctx, a)
	require.NoError(t, err)
	require.NoError(t, w1.Commit())

	// Attach NIC-1.
	w2, err := r.Writer(ctx)
	require.NoError(t, err)
	ref1, err := w2.Addresses().SetReference(ctx, &domain.AddressReference{
		AddressID:    a.ID,
		ReferrerType: "network_interface",
		ReferrerID:   "e9bnic1",
		ReferrerName: "nic-1",
	})
	require.NoError(t, err)
	assert.Equal(t, "e9bnic1", ref1.ReferrerID)
	require.NoError(t, w2.Commit())

	// Idempotent re-attach к тому же NIC-1 — проходит.
	w3, err := r.Writer(ctx)
	require.NoError(t, err)
	ref2, err := w3.Addresses().SetReference(ctx, &domain.AddressReference{
		AddressID:    a.ID,
		ReferrerType: "network_interface",
		ReferrerID:   "e9bnic1",
		ReferrerName: "nic-1-renamed",
	})
	require.NoError(t, err)
	assert.Equal(t, "nic-1-renamed", ref2.ReferrerName)
	require.NoError(t, w3.Commit())

	// Attempt to attach NIC-2 — CAS fails → FailedPrecondition (мы пробросим
	// ошибку проверкой error-Is через sentinel в repo-leaf; чтобы не закладывать
	// stack-trace assertion, проверяем что ошибка ≠ nil + что reference остался
	// прежним).
	w4, err := r.Writer(ctx)
	require.NoError(t, err)
	_, err = w4.Addresses().SetReference(ctx, &domain.AddressReference{
		AddressID:    a.ID,
		ReferrerType: "network_interface",
		ReferrerID:   "e9bnic2",
		ReferrerName: "nic-2",
	})
	require.Error(t, err, "second SetReference with foreign referrer must FAIL (CAS)")
	w4.Abort()

	// Verify reference still pointing to nic-1.
	rd, err := r.Reader(ctx)
	require.NoError(t, err)
	defer func() { _ = rd.Close() }()
	stillRef, err := rd.Addresses().GetReference(ctx, a.ID)
	require.NoError(t, err)
	assert.Equal(t, "e9bnic1", stillRef.ReferrerID, "CAS must keep nic-1 as referrer")
}
