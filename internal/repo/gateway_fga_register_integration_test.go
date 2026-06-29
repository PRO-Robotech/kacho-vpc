// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package repo_test

// Регистрация owner-tuple в FGA для Gateway должна нести labels на двух путях:
// (a) Create эмитит register-intent с labels + parent_project_id (иначе
// resource_mirror в kacho-iam без labels и label-селектор не матчит даже
// свежесозданный Gateway); (b) Update при смене labels переэмитит
// register-intent с обновленными labels. Не-label Update нового intent не
// порождает.

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coredb "github.com/PRO-Robotech/kacho-corelib/db"
	gwapp "github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/api/gateway"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	kachopg "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/pg"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/repomock"
)

// singleGatewayID возвращает единственный id gateway в проекте.
func singleGatewayID(ctx context.Context, t *testing.T, pool *pgxpool.Pool, projectID string) string {
	t.Helper()
	var id string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT id FROM kacho_vpc.gateways WHERE project_id = $1`, projectID).Scan(&id))
	return id
}

// TestGatewayRepo_T32Create01_CreateEmitsLabels_UpdateRevokes проверяет оба пути:
// Create обязан эмитить labels + parent_project_id; Update со сменой labels —
// переэмитить; не-label Update — без лишнего intent.
func TestGatewayRepo_T32Create01_CreateEmitsLabels_UpdateRevokes(t *testing.T) {
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

	createUC := gwapp.NewCreateGatewayUseCase(r, pc, or)
	updateUC := gwapp.NewUpdateGatewayUseCase(r, or)

	// --- Create Gateway с labels ---
	op, err := createUC.Execute(ctx, domain.Gateway{
		ProjectID:   "prj-A",
		Name:        domain.RcNameVPC("gw-okun"),
		GatewayType: domain.GatewayTypeSharedEgress,
		Labels:      domain.LabelsFromMap(map[string]string{"gw": "okun"}),
	})
	require.NoError(t, err)
	awaitOp(t, or, op.ID)

	gwID := singleGatewayID(ctx, t, pool, "prj-A")

	createRegs := registerPayloads(ctx, t, pool, gwID)
	require.Len(t, createRegs, 1, "exactly one register intent on Create")
	assert.Equal(t, map[string]string{"gw": "okun"}, createRegs[0].Labels,
		"BUG (a): Gateway Create MUST emit labels, not a bare tuple — else selector never matches")
	assert.Equal(t, "prj-A", createRegs[0].ParentProjectID, "parent_project_id = Gateway project_id")
	assert.Equal(t, "vpc_gateway:"+gwID, createRegs[0].Tuple.Object)

	// --- Update labels (okun → sudak) ---
	upOp, err := updateUC.Execute(ctx, gwapp.UpdateInput{
		GatewayID:  gwID,
		Gateway:    domain.Gateway{Labels: domain.LabelsFromMap(map[string]string{"gw": "sudak"})},
		UpdateMask: []string{"labels"},
	})
	require.NoError(t, err)
	awaitOp(t, or, upOp.ID)

	updateRegs := registerPayloads(ctx, t, pool, gwID)
	require.Len(t, updateRegs, 2, "BUG (b): Gateway Update(labels) MUST re-emit a register intent")
	assert.Equal(t, map[string]string{"gw": "sudak"}, updateRegs[1].Labels,
		"the Update register intent carries the refreshed labels (revoke old selector)")
	assert.Equal(t, "prj-A", updateRegs[1].ParentProjectID)
	require.False(t, updateRegs[1].SourceVersion.IsZero(), "source_version проставлен на Update intent")

	// --- Не-label Update → без лишнего register-intent ---
	nlOp, err := updateUC.Execute(ctx, gwapp.UpdateInput{
		GatewayID:  gwID,
		Gateway:    domain.Gateway{Description: domain.RcDescription("renamed")},
		UpdateMask: []string{"description"},
	})
	require.NoError(t, err)
	awaitOp(t, or, nlOp.ID)
	require.Len(t, registerPayloads(ctx, t, pool, gwID), 2,
		"non-label Update → no new register intent (G-2)")
}

// TestGatewayRepo_T32FullPatch01_EmptyMaskEmits — пустой update_mask
// (full-object PATCH) обязан переэмитить register-intent.
func TestGatewayRepo_T32FullPatch01_EmptyMaskEmits(t *testing.T) {
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
	createUC := gwapp.NewCreateGatewayUseCase(r, pc, or)
	updateUC := gwapp.NewUpdateGatewayUseCase(r, or)

	op, err := createUC.Execute(ctx, domain.Gateway{
		ProjectID: "prj-A", Name: domain.RcNameVPC("gw-fp"),
		GatewayType: domain.GatewayTypeSharedEgress,
		Labels:      domain.LabelsFromMap(map[string]string{"gw": "treska"}),
	})
	require.NoError(t, err)
	awaitOp(t, or, op.ID)
	gwID := singleGatewayID(ctx, t, pool, "prj-A")

	upOp, err := updateUC.Execute(ctx, gwapp.UpdateInput{GatewayID: gwID, Gateway: domain.Gateway{}})
	require.NoError(t, err)
	awaitOp(t, or, upOp.ID)

	regs := registerPayloads(ctx, t, pool, gwID)
	require.Len(t, regs, 2, "пустой mask = full PATCH ⇒ labelsInMask=true ⇒ emit")
	assert.Empty(t, regs[1].Labels, "full-PATCH обнулил labels → intent с пустыми labels")
}
