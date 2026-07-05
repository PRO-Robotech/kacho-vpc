// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package repo_test

// Referrer-tracking `owned`-флаг + обобщенный Address.Delete-guard (back-reference
// VIP-адреса на корневой ресурс, напр. network load balancer):
//   - SetReference с owned=true/false персистит и читается назад (GetReference,
//     ReferencesForAddresses);
//   - Address.Delete при референсе типа network_load_balancer блокируется
//     обобщенным сообщением (шаблон общий с NIC);
//   - публичное чтение (Get) отдает referrer с name + owned.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	coredb "github.com/PRO-Robotech/kacho-corelib/db"
	"github.com/PRO-Robotech/kacho-corelib/ids"
	addrapp "github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/api/address"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
	kachopg "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/pg"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/repomock"
)

// newExtVIPAddr — external IPv4 Address с explicit-адресом (IPAM-пул не
// задействован, Insert без FK на subnet/pool).
func newExtVIPAddr(projectID, name, ip string) *domain.Address {
	return &domain.Address{
		ID:        ids.NewID(ids.PrefixAddress),
		ProjectID: projectID,
		Name:      domain.RcNameVPC(name),
		Type:      domain.AddressTypeExternal,
		IpVersion: domain.IpVersionIPv4,
		ExternalIpv4: &domain.ExternalIpv4Spec{
			Address: ip,
			ZoneID:  "zone-a",
		},
	}
}

// TestIntegration_AddressReference_OwnedRoundTrip: owned=true/false персистит и
// читается назад через GetReference и batch ReferencesForAddresses.
func TestIntegration_AddressReference_OwnedRoundTrip(t *testing.T) {
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

	ownedAddr := newExtVIPAddr("prj-A", "addr-owned", "203.0.113.71")
	linkedAddr := newExtVIPAddr("prj-A", "addr-linked", "203.0.113.72")
	require.NoError(t, legacyWithTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		if _, e := w.Addresses().Insert(ctx, ownedAddr); e != nil {
			return e
		}
		_, e := w.Addresses().Insert(ctx, linkedAddr)
		return e
	}))

	require.NoError(t, legacyWithTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		if _, e := w.Addresses().SetReference(ctx, &domain.AddressReference{
			AddressID:    ownedAddr.ID,
			ReferrerType: "network_load_balancer",
			ReferrerID:   "nlb00000000owned1",
			ReferrerName: "lb-owned",
			Owned:        true,
		}); e != nil {
			return e
		}
		_, e := w.Addresses().SetReference(ctx, &domain.AddressReference{
			AddressID:    linkedAddr.ID,
			ReferrerType: "network_load_balancer",
			ReferrerID:   "nlb0000000linked1",
			ReferrerName: "lb-linked",
			Owned:        false,
		})
		return e
	}))

	rd, err := r.Reader(ctx)
	require.NoError(t, err)
	refOwned, err := rd.Addresses().GetReference(ctx, ownedAddr.ID)
	require.NoError(t, err)
	refLinked, err := rd.Addresses().GetReference(ctx, linkedAddr.ID)
	require.NoError(t, err)
	batch, err := rd.Addresses().ReferencesForAddresses(ctx, []string{ownedAddr.ID, linkedAddr.ID})
	require.NoError(t, err)
	require.NoError(t, rd.Close())

	assert.True(t, refOwned.Owned, "owned=true должен персистить и читаться назад")
	assert.Equal(t, "lb-owned", refOwned.ReferrerName)
	assert.False(t, refLinked.Owned, "owned=false должен персистить")

	require.NotNil(t, batch[ownedAddr.ID])
	assert.True(t, batch[ownedAddr.ID].Owned, "batch-чтение несет owned=true")
	require.NotNil(t, batch[linkedAddr.ID])
	assert.False(t, batch[linkedAddr.ID].Owned, "batch-чтение несет owned=false")
}

// TestIntegration_AddressReference_OwnedUpdatedByReattach: idempotent re-set того
// же referrer'а обновляет owned (false → true).
func TestIntegration_AddressReference_OwnedUpdatedByReattach(t *testing.T) {
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

	addr := newExtVIPAddr("prj-A", "addr-reattach", "203.0.113.74")
	require.NoError(t, legacyWithTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		_, e := w.Addresses().Insert(ctx, addr)
		return e
	}))
	ref := &domain.AddressReference{
		AddressID:    addr.ID,
		ReferrerType: "network_load_balancer",
		ReferrerID:   "nlb0000reattach01",
		ReferrerName: "lb-x",
		Owned:        false,
	}
	// Результат SetReference захватываем наружу — assert вне tx-callback (FailNow
	// внутри callback оставил бы pgx-tx незакрытой).
	var out1, out2 *domain.AddressReference
	require.NoError(t, legacyWithTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		var e error
		out1, e = w.Addresses().SetReference(ctx, ref)
		return e
	}))
	require.NotNil(t, out1)
	assert.False(t, out1.Owned, "первичный set: owned=false")

	ref.Owned = true
	require.NoError(t, legacyWithTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		var e error
		out2, e = w.Addresses().SetReference(ctx, ref)
		return e
	}))
	require.NotNil(t, out2)
	assert.True(t, out2.Owned, "re-set того же referrer'а обновляет owned")

	rd, err := r.Reader(ctx)
	require.NoError(t, err)
	got, err := rd.Addresses().GetReference(ctx, addr.ID)
	require.NoError(t, rd.Close())
	require.NoError(t, err)
	assert.True(t, got.Owned)
}

// TestIntegration_Address_Delete_InUseByLoadBalancer_GeneralizedMessage: Delete
// адреса при референсе network_load_balancer блокируется обобщенным сообщением
// (real DB read referrer'а в sync-guard, до создания Operation).
func TestIntegration_Address_Delete_InUseByLoadBalancer_GeneralizedMessage(t *testing.T) {
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
	or := repomock.NewOpsRepo()

	addr := newExtVIPAddr("prj-A", "addr-vip", "203.0.113.73")
	require.NoError(t, legacyWithTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		_, e := w.Addresses().Insert(ctx, addr)
		return e
	}))
	require.NoError(t, legacyWithTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		_, e := w.Addresses().SetReference(ctx, &domain.AddressReference{
			AddressID:    addr.ID,
			ReferrerType: "network_load_balancer",
			ReferrerID:   "nlb00000000000001",
			ReferrerName: "lb-name",
			Owned:        true,
		})
		return e
	}))

	_, err = addrapp.NewDeleteAddressUseCase(r, or).Execute(ctx, addr.ID)
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
	assert.Equal(t,
		"address "+addr.ID+" is in use by network_load_balancer lb-name; detach it before deleting the address",
		st.Message())
}

// TestIntegration_Address_Get_UsedByReferrerNameAndOwned: публичный Get отдает
// referrer с name + owned (DB → record через loadUsedBy/ReferencesForAddresses).
func TestIntegration_Address_Get_UsedByReferrerNameAndOwned(t *testing.T) {
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

	addr := newExtVIPAddr("prj-A", "addr-getref", "203.0.113.75")
	require.NoError(t, legacyWithTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		_, e := w.Addresses().Insert(ctx, addr)
		return e
	}))
	require.NoError(t, legacyWithTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		_, e := w.Addresses().SetReference(ctx, &domain.AddressReference{
			AddressID:    addr.ID,
			ReferrerType: "network_load_balancer",
			ReferrerID:   "nlb0000000getref1",
			ReferrerName: "lb-getref",
			Owned:        true,
		})
		return e
	}))

	rec, err := addrapp.NewGetAddressUseCase(r, nil).Execute(ctx, "", addr.ID)
	require.NoError(t, err)
	require.Len(t, rec.UsedBy, 1)
	assert.Equal(t, "network_load_balancer", rec.UsedBy[0].ReferrerType)
	assert.Equal(t, "lb-getref", rec.UsedBy[0].ReferrerName)
	assert.True(t, rec.UsedBy[0].Owned)
}
