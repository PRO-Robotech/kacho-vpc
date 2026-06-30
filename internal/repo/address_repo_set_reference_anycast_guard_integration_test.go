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

// SetReferenceGuarded c expectAnycast=true обязан привязываться ТОЛЬКО к anycast-
// адресу (колонка anycast jsonb ≠ null). Закрывает дыру: внешний публичный
// (non-anycast) Address проходил guard по project/family и ошибочно привязывался
// как VIP к INTERNAL-LB. Проверка вложена в CAS WHERE (immutable-признак anycast) —
// без TOCTOU. Несовпадение → ErrGuardMismatch (use-case → generic InvalidArgument,
// анти-oracle), used не выставлен.
func TestIntegration_AddressRepo_SetReferenceGuarded_Anycast(t *testing.T) {
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

	// Внешний публичный (non-anycast) Address: anycast IS NULL.
	seedExternal := func(name, ip string) string {
		addr := &domain.Address{
			ID:        ids.NewUID(),
			ProjectID: "prj-byo",
			Name:      domain.RcNameVPC(name),
			Type:      domain.AddressTypeExternal,
			IpVersion: domain.IpVersionIPv4,
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
	// Anycast-адрес: anycast IS NOT NULL (network/pool опущены → generated FK-колонки
	// NULL, FK не требуется; для guard важна лишь непустота колонки anycast).
	seedAnycast := func(name, host string) string {
		addr := &domain.Address{
			ID:        ids.NewID(ids.PrefixAddress),
			ProjectID: "prj-byo",
			Name:      domain.RcNameVPC(name),
			Type:      domain.AddressTypeInternal,
			IpVersion: domain.IpVersionIPv4,
			Anycast:   &domain.AnycastSpec{Address: host},
		}
		require.NoError(t, legacyWithTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
			_, e := w.Addresses().Insert(ctx, addr)
			return e
		}))
		return addr.ID
	}

	// Не-anycast адрес под expectAnycast=true → ErrGuardMismatch; used НЕ выставлен.
	extID := seedExternal("byo-ext", "203.0.113.40")
	err = legacyWithTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		_, e := w.Addresses().SetReferenceGuarded(ctx, &domain.AddressReference{
			AddressID: extID, ReferrerType: "nlb_load_balancer", ReferrerID: "lb000000000000001",
		}, "prj-byo", domain.IpVersionIPv4, true)
		return e
	})
	require.ErrorIs(t, err, helpers.ErrGuardMismatch,
		"non-anycast Address под expectAnycast=true → ErrGuardMismatch")
	rd, err := r.Reader(ctx)
	require.NoError(t, err)
	gotExt, err := rd.Addresses().Get(ctx, extID)
	require.NoError(t, rd.Close())
	require.NoError(t, err)
	assert.False(t, gotExt.Used, "mismatch → used НЕ выставлен (атомарный CAS откатил)")

	// Anycast-адрес под expectAnycast=true → bind проходит, used=true.
	anyID := seedAnycast("byo-anycast", "100.64.99.1")
	require.NoError(t, legacyWithTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		_, e := w.Addresses().SetReferenceGuarded(ctx, &domain.AddressReference{
			AddressID: anyID, ReferrerType: "nlb_load_balancer", ReferrerID: "lb000000000000001",
		}, "prj-byo", domain.IpVersionIPv4, true)
		return e
	}))
	rd2, err := r.Reader(ctx)
	require.NoError(t, err)
	gotAny, err := rd2.Addresses().Get(ctx, anyID)
	require.NoError(t, rd2.Close())
	require.NoError(t, err)
	assert.True(t, gotAny.Used, "anycast Address под expectAnycast=true → bind проходит, used=true")

	// expectAnycast=false (back-compat) — non-anycast адрес привязывается как раньше.
	extID2 := seedExternal("byo-ext-2", "203.0.113.41")
	require.NoError(t, legacyWithTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		_, e := w.Addresses().SetReferenceGuarded(ctx, &domain.AddressReference{
			AddressID: extID2, ReferrerType: "compute_instance", ReferrerID: "epdvm0000000000001",
		}, "prj-byo", domain.IpVersionIPv4, false)
		return e
	}), "expectAnycast=false → non-anycast bind проходит (back-compat)")
}
