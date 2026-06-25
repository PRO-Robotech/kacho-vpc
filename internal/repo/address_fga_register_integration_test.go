// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package repo_test

// Регистрация owner-tuple в FGA для Address должна нести labels на двух путях:
// (a) Create эмитит register-intent с labels + parent_project_id (иначе
// resource_mirror в kacho-iam без labels и label-селектор не матчит даже
// свежесозданный Address); (b) Update при смене labels переэмитит
// register-intent с обновленными labels. Не-label Update нового intent не
// порождает.
//
// Берем external IPv4 с explicit-адресом — IPAM-пул не задействован
// (pools=nil), Insert + register-emit идут в одной writer-tx.

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coredb "github.com/PRO-Robotech/kacho-corelib/db"
	addrapp "github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/api/address"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/cqrsadapter"
	kachopg "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/pg"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/repomock"
)

// singleAddressID возвращает единственный id address в проекте.
func singleAddressID(ctx context.Context, t *testing.T, pool *pgxpool.Pool, projectID string) string {
	t.Helper()
	var id string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT id FROM kacho_vpc.addresses WHERE project_id = $1`, projectID).Scan(&id))
	return id
}

// TestAddressRepo_T32Create01_CreateEmitsLabels_UpdateRevokes проверяет оба пути:
// Create обязан эмитить labels + parent_project_id; Update со сменой labels —
// переэмитить; не-label Update — без лишнего intent.
func TestAddressRepo_T32Create01_CreateEmitsLabels_UpdateRevokes(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	dsn := setupTestDB(t)
	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	r := kachopg.New(pool, nil)
	t.Cleanup(r.Close)
	or := repomock.NewOpsRepo()
	pc := &repomock.ProjectClient{OK: true}
	subnetAdapter := cqrsadapter.NewSubnet(r)

	createUC := addrapp.NewCreateAddressUseCase(r, subnetAdapter, pc, or, nil)
	updateUC := addrapp.NewUpdateAddressUseCase(r, or)

	// --- Create external IPv4 Address с labels (explicit-адрес, без IPAM) ---
	op, err := createUC.Execute(ctx, addrapp.CreateInput{
		ProjectID: "prj-A",
		Name:      "addr-okun",
		Labels:    map[string]string{"addr": "okun"},
		ExternalSpec: &addrapp.ExternalAddrSpec{
			Address: "203.0.113.10",
			ZoneID:  "zone-a",
		},
	})
	require.NoError(t, err)
	awaitOp(t, or, op.ID)

	addrID := singleAddressID(ctx, t, pool, "prj-A")

	createRegs := registerPayloads(ctx, t, pool, addrID)
	require.Len(t, createRegs, 1, "exactly one register intent on Create")
	assert.Equal(t, map[string]string{"addr": "okun"}, createRegs[0].Labels,
		"BUG (a): Address Create MUST emit labels, not a bare tuple — else selector never matches")
	assert.Equal(t, "prj-A", createRegs[0].ParentProjectID, "parent_project_id = Address project_id")
	assert.Equal(t, "vpc_address:"+addrID, createRegs[0].Tuple.Object)

	// --- Update labels (okun → sudak) ---
	upOp, err := updateUC.Execute(ctx, addrapp.UpdateInput{
		AddressID:  addrID,
		Labels:     map[string]string{"addr": "sudak"},
		UpdateMask: []string{"labels"},
	})
	require.NoError(t, err)
	awaitOp(t, or, upOp.ID)

	updateRegs := registerPayloads(ctx, t, pool, addrID)
	require.Len(t, updateRegs, 2, "BUG (b): Address Update(labels) MUST re-emit a register intent")
	assert.Equal(t, map[string]string{"addr": "sudak"}, updateRegs[1].Labels,
		"the Update register intent carries the refreshed labels (revoke old selector)")
	assert.Equal(t, "prj-A", updateRegs[1].ParentProjectID)
	require.False(t, updateRegs[1].SourceVersion.IsZero(), "source_version проставлен на Update intent")

	// --- Не-label Update → без лишнего register-intent ---
	nlOp, err := updateUC.Execute(ctx, addrapp.UpdateInput{
		AddressID:   addrID,
		Description: "renamed",
		UpdateMask:  []string{"description"},
	})
	require.NoError(t, err)
	awaitOp(t, or, nlOp.ID)
	require.Len(t, registerPayloads(ctx, t, pool, addrID), 2,
		"non-label Update → no new register intent (G-2)")
}

// TestAddressRepo_T32FullPatch01_EmptyMaskEmits — пустой update_mask
// (full-object PATCH) обязан переэмитить register-intent.
func TestAddressRepo_T32FullPatch01_EmptyMaskEmits(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	dsn := setupTestDB(t)
	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	r := kachopg.New(pool, nil)
	t.Cleanup(r.Close)
	or := repomock.NewOpsRepo()
	pc := &repomock.ProjectClient{OK: true}
	subnetAdapter := cqrsadapter.NewSubnet(r)
	createUC := addrapp.NewCreateAddressUseCase(r, subnetAdapter, pc, or, nil)
	updateUC := addrapp.NewUpdateAddressUseCase(r, or)

	op, err := createUC.Execute(ctx, addrapp.CreateInput{
		ProjectID: "prj-A",
		Name:      "addr-fp",
		Labels:    map[string]string{"addr": "treska"},
		ExternalSpec: &addrapp.ExternalAddrSpec{
			Address: "203.0.113.20",
			ZoneID:  "zone-a",
		},
	})
	require.NoError(t, err)
	awaitOp(t, or, op.ID)
	addrID := singleAddressID(ctx, t, pool, "prj-A")

	// Full-PATCH (пустой update_mask): labels обнуляются → emit с пустыми labels.
	upOp, err := updateUC.Execute(ctx, addrapp.UpdateInput{AddressID: addrID})
	require.NoError(t, err)
	awaitOp(t, or, upOp.ID)

	regs := registerPayloads(ctx, t, pool, addrID)
	require.Len(t, regs, 2, "пустой mask = full PATCH ⇒ labelsInMask=true ⇒ emit")
	assert.Empty(t, regs[1].Labels, "full-PATCH обнулил labels → intent с пустыми labels")
}
