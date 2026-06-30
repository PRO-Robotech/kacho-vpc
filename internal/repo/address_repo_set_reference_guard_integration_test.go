// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package repo_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coredb "github.com/PRO-Robotech/kacho-corelib/db"
	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/helpers"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
	kachopg "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/pg"
)

// SetReferenceGuarded — BYO ownership/family-guard вложен в CAS WHERE-условие
// первого UPDATE (ownership/family — immutable-колонки Address), поэтому проверка
// атомарна с used_by-CAS (без TOCTOU read-then-update). Чужой проект/семейство
// (либо адрес не существует) под guard'ом → helpers.ErrGuardMismatch; пустые
// expect_* → проверка пропускается (back-compat → ErrNotFound на отсутствующий).
func TestIntegration_AddressRepo_SetReferenceGuarded(t *testing.T) {
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

	seed := func(name, projectID, ip string, family domain.IpVersion) string {
		addr := &domain.Address{
			ID:        ids.NewUID(),
			ProjectID: projectID,
			Name:      domain.RcNameVPC(name),
			Type:      domain.AddressTypeExternal,
			IpVersion: family,
			ExternalIpv4: &domain.ExternalIpv4Spec{
				Address: ip,
				ZoneID:  "zone-a",
			},
		}
		require.NoError(t, legacyWithTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
			_, e := w.Addresses().Insert(ctx, addr)
			return e
		}))
		return addr.ID
	}

	// happy: совпадающие project/family → bind проходит, used=true.
	okID := seed("byo-guard-ok", "prj-A", "203.0.113.10", domain.IpVersionIPv4)
	require.NoError(t, legacyWithTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		_, e := w.Addresses().SetReferenceGuarded(ctx, &domain.AddressReference{
			AddressID: okID, ReferrerType: "nlb_load_balancer", ReferrerID: "lb000000000000001",
		}, "prj-A", domain.IpVersionIPv4, false)
		return e
	}))
	rd, err := r.Reader(ctx)
	require.NoError(t, err)
	got, err := rd.Addresses().Get(ctx, okID)
	require.NoError(t, rd.Close())
	require.NoError(t, err)
	assert.True(t, got.Used, "happy guard → used=true")

	// ownership guard: чужой проект → ErrGuardMismatch, used не выставлен.
	foreignID := seed("byo-guard-foreign", "prj-B", "203.0.113.11", domain.IpVersionIPv4)
	err = legacyWithTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		_, e := w.Addresses().SetReferenceGuarded(ctx, &domain.AddressReference{
			AddressID: foreignID, ReferrerType: "nlb_load_balancer", ReferrerID: "lb000000000000001",
		}, "prj-A", domain.IpVersionIPv4, false)
		return e
	})
	require.ErrorIs(t, err, helpers.ErrGuardMismatch, "чужой проект → ErrGuardMismatch")
	rd2, err := r.Reader(ctx)
	require.NoError(t, err)
	gotF, err := rd2.Addresses().Get(ctx, foreignID)
	require.NoError(t, rd2.Close())
	require.NoError(t, err)
	assert.False(t, gotF.Used, "mismatch → used НЕ выставлен (атомарный CAS откатил)")

	// family guard: другое семейство → ErrGuardMismatch.
	famID := seed("byo-guard-fam", "prj-A", "203.0.113.12", domain.IpVersionIPv4)
	err = legacyWithTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		_, e := w.Addresses().SetReferenceGuarded(ctx, &domain.AddressReference{
			AddressID: famID, ReferrerType: "nlb_load_balancer", ReferrerID: "lb000000000000001",
		}, "prj-A", domain.IpVersionIPv6, false)
		return e
	})
	require.ErrorIs(t, err, helpers.ErrGuardMismatch, "чужое семейство → ErrGuardMismatch")

	// несуществующий адрес под guard'ом → ErrGuardMismatch (анти-oracle).
	err = legacyWithTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		_, e := w.Addresses().SetReferenceGuarded(ctx, &domain.AddressReference{
			AddressID: "e9bnonexistent00001", ReferrerType: "nlb_load_balancer", ReferrerID: "lb000000000000001",
		}, "prj-A", domain.IpVersionIPv4, false)
		return e
	})
	require.ErrorIs(t, err, helpers.ErrGuardMismatch, "несуществующий под guard'ом → ErrGuardMismatch (не NotFound)")

	// без guard'а несуществующий адрес → ErrNotFound (back-compat).
	err = legacyWithTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		_, e := w.Addresses().SetReferenceGuarded(ctx, &domain.AddressReference{
			AddressID: "e9bnonexistent00002", ReferrerType: "compute_instance", ReferrerID: "x",
		}, "", domain.IpVersionUnspecified, false)
		return e
	})
	require.ErrorIs(t, err, helpers.ErrNotFound, "без guard'а несуществующий → ErrNotFound")

	// used_by-CAS под совпадающим guard'ом: второй referrer → ErrFailedPrecondition.
	casID := seed("byo-guard-cas", "prj-A", "203.0.113.13", domain.IpVersionIPv4)
	require.NoError(t, legacyWithTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		_, e := w.Addresses().SetReferenceGuarded(ctx, &domain.AddressReference{
			AddressID: casID, ReferrerType: "nlb_load_balancer", ReferrerID: "lb000000000000001",
		}, "prj-A", domain.IpVersionIPv4, false)
		return e
	}))
	err = legacyWithTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		_, e := w.Addresses().SetReferenceGuarded(ctx, &domain.AddressReference{
			AddressID: casID, ReferrerType: "nlb_load_balancer", ReferrerID: "lb000000000000002",
		}, "prj-A", domain.IpVersionIPv4, false)
		return e
	})
	require.ErrorIs(t, err, helpers.ErrFailedPrecondition,
		"занятый адрес от чужого referrer под guard'ом → ErrFailedPrecondition")
}
