// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package pg_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	coredb "github.com/PRO-Robotech/kacho-corelib/db"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	kachopg "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/pg"
)

// `addresses.used` — system-managed флаг, выставляется ТОЛЬКО referrer-методами
// (SetReference / MarkEphemeralInUse / ClearReference) при attach/detach NIC.
// Публичный Address.Update (name/description/labels/reserved/deletion_protection)
// НЕ должен трогать `used` — иначе read-modify-write поверх устаревшего снимка
// затирает конкурентный NIC-attach (used=true → used=false), и адрес,
// фактически занятый интерфейсом, становится «свободным» (его можно удалить →
// dangling reference / FK violation).
//
// Контракт: Update сохраняет текущее значение `used`, кем бы оно ни было
// выставлено между чтением и записью use-case'а.
func TestCQRS_Address_Update_DoesNotClobberUsed(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	dsn := setupTestDB(t)
	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	r := kachopg.New(pool, nil)

	// 1. Insert external address (used=false).
	w1, err := r.Writer(ctx)
	require.NoError(t, err)
	a := newAddress("project-used", "addr-used", true)
	created, err := w1.Addresses().Insert(ctx, a)
	require.NoError(t, err)
	require.False(t, created.Used)
	require.NoError(t, w1.Commit())

	// 2. NIC attach: SetReference выставляет used=true (committed).
	w2, err := r.Writer(ctx)
	require.NoError(t, err)
	_, err = w2.Addresses().SetReference(ctx, &domain.AddressReference{
		AddressID:    created.ID,
		ReferrerType: "compute_instance",
		ReferrerID:   "epdinstance0001",
		ReferrerName: "vm-x",
	})
	require.NoError(t, err)
	require.NoError(t, w2.Commit())

	// 3. Address.Update read-modify-write поверх СНИМКА, где used=false (как было
	//    бы, если use-case прочитал адрес ДО attach). Симулируем: домен-объект с
	//    Used=false, меняем description.
	w3, err := r.Writer(ctx)
	require.NoError(t, err)
	stale := created // снимок «до attach» — used=false
	stale.Description = "edited"
	stale.Used = false // явно: use-case никогда не кладет used в mask
	_, err = w3.Addresses().Update(ctx, &stale.Address)
	require.NoError(t, err)
	require.NoError(t, w3.Commit())

	// 4. used обязан остаться true — attach не затерт.
	rd, err := r.Reader(ctx)
	require.NoError(t, err)
	defer func() { _ = rd.Close() }()
	got, err := rd.Addresses().Get(ctx, created.ID)
	require.NoError(t, err)
	require.True(t, got.Used, "Address.Update must NOT clobber system-managed used flag")
	require.Equal(t, "edited", string(got.Description))
}
