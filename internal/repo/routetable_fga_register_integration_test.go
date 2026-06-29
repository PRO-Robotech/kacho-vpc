// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package repo_test

// Регистрация owner-tuple в FGA для RouteTable должна нести labels на двух путях:
// (a) Create эмитит register-intent с labels + parent_project_id (иначе
// resource_mirror в kacho-iam без labels и label-селектор не матчит даже
// свежесозданную RouteTable — granted-юзер видит 0 в List и 403 на detail);
// (b) Update при смене labels переэмитит register-intent с обновленными labels
// (revoke старого селектора). Не-label Update нового intent не порождает.
//
// testcontainers-интеграция гоняет реальные use-cases RouteTable (Create +
// Update) против Postgres 16 и проверяет payload'ы в fga_register_outbox.

import (
	"context"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coredb "github.com/PRO-Robotech/kacho-corelib/db"
	rtapp "github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/api/routetable"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	kachopg "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/pg"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/repomock"
)

// singleRTID возвращает единственный id route_table в проекте.
func singleRTID(ctx context.Context, t *testing.T, pool *pgxpool.Pool, projectID string) string {
	t.Helper()
	var id string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT id FROM kacho_vpc.route_tables WHERE project_id = $1`, projectID).Scan(&id))
	return id
}

// TestRouteTableRepo_T32Create01_CreateEmitsLabels_UpdateRevokes проверяет оба
// пути end-to-end: (a) Create обязан эмитить labels + parent_project_id, (b)
// Update со сменой labels обязан переэмитить register-intent с обновленными
// labels, (c) не-label Update нового intent не порождает.
func TestRouteTableRepo_T32Create01_CreateEmitsLabels_UpdateRevokes(t *testing.T) {
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

	createUC := rtapp.NewCreateRouteTableUseCase(r, pc, or)
	updateUC := rtapp.NewUpdateRouteTableUseCase(r, or)

	netID := insertNetworkRow(ctx, t, r, "prj-A", "net-for-rt")

	// --- Create RouteTable с labels ---
	op, err := createUC.Execute(ctx, domain.RouteTable{
		ProjectID: "prj-A",
		NetworkID: netID,
		Name:      domain.RcNameVPC("rt-okun"),
		Labels:    domain.LabelsFromMap(map[string]string{"rt": "okun"}),
	})
	require.NoError(t, err)
	awaitOp(t, or, op.ID)

	rtID := singleRTID(ctx, t, pool, "prj-A")

	createRegs := registerPayloads(ctx, t, pool, rtID)
	require.Len(t, createRegs, 1, "exactly one register intent on Create")
	assert.Equal(t, map[string]string{"rt": "okun"}, createRegs[0].Labels,
		"BUG (a): RouteTable Create MUST emit labels, not a bare tuple — else selector never matches")
	assert.Equal(t, "prj-A", createRegs[0].ParentProjectID, "parent_project_id = RouteTable project_id")
	assert.Equal(t, "vpc_route_table:"+rtID, createRegs[0].Tuple.Object)

	// --- Update labels (okun → sudak) → revoke старого селектора ---
	upOp, err := updateUC.Execute(ctx, rtapp.UpdateInput{
		RouteTableID: rtID,
		RouteTable:   domain.RouteTable{Labels: domain.LabelsFromMap(map[string]string{"rt": "sudak"})},
		UpdateMask:   []string{"labels"},
	})
	require.NoError(t, err)
	awaitOp(t, or, upOp.ID)

	updateRegs := registerPayloads(ctx, t, pool, rtID)
	require.Len(t, updateRegs, 2, "BUG (b): RouteTable Update(labels) MUST re-emit a register intent")
	assert.Equal(t, map[string]string{"rt": "sudak"}, updateRegs[1].Labels,
		"the Update register intent carries the refreshed labels (revoke old selector)")
	assert.Equal(t, "prj-A", updateRegs[1].ParentProjectID)
	require.False(t, updateRegs[1].SourceVersion.IsZero(), "source_version проставлен на Update intent")

	// --- Не-label Update → без лишнего register-intent ---
	nlOp, err := updateUC.Execute(ctx, rtapp.UpdateInput{
		RouteTableID: rtID,
		RouteTable:   domain.RouteTable{Description: domain.RcDescription("renamed")},
		UpdateMask:   []string{"description"},
	})
	require.NoError(t, err)
	awaitOp(t, or, nlOp.ID)
	require.Len(t, registerPayloads(ctx, t, pool, rtID), 2,
		"non-label Update → no new register intent (G-2)")
}

// TestRouteTableRepo_T32FullPatch01_EmptyMaskEmits — пустой update_mask
// (full-object PATCH) обязан переэмитить register-intent (labelsInMask=true).
func TestRouteTableRepo_T32FullPatch01_EmptyMaskEmits(t *testing.T) {
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
	createUC := rtapp.NewCreateRouteTableUseCase(r, pc, or)
	updateUC := rtapp.NewUpdateRouteTableUseCase(r, or)

	netID := insertNetworkRow(ctx, t, r, "prj-A", "net-for-rt-fp")
	op, err := createUC.Execute(ctx, domain.RouteTable{
		ProjectID: "prj-A", NetworkID: netID, Name: domain.RcNameVPC("rt-fp"),
		Labels: domain.LabelsFromMap(map[string]string{"rt": "treska"}),
	})
	require.NoError(t, err)
	awaitOp(t, or, op.ID)
	rtID := singleRTID(ctx, t, pool, "prj-A")

	// Full-PATCH (пустой update_mask), labels обнуляются → emit с пустыми labels.
	upOp, err := updateUC.Execute(ctx, rtapp.UpdateInput{RouteTableID: rtID, RouteTable: domain.RouteTable{}})
	require.NoError(t, err)
	awaitOp(t, or, upOp.ID)

	regs := registerPayloads(ctx, t, pool, rtID)
	require.Len(t, regs, 2, "пустой mask = full PATCH ⇒ labelsInMask=true ⇒ emit")
	assert.Empty(t, regs[1].Labels, "full-PATCH обнулил labels → intent с пустыми labels")
}

// TestRouteTableRepo_T32Conc01_ConcurrentLabelFlip_LastSourceWins запускает ≥2
// goroutines, конкурентно флипающих label ({rt:treska} ↔ {}), затем делает ОДИН
// финальный сериализованный Update к известному label. Каждый Update едет в своей
// writer-tx (row-lock от GetForUpdate сериализует RMW) и эмитит register intent
// атомарно с закоммиченной строкой, поэтому per-tx строка и ее intent никогда не
// расходятся. IAM применяет last-source-wins по source_version. Покрывает
// concurrent-race для consumer-side эмита register intent.
func TestRouteTableRepo_T32Conc01_ConcurrentLabelFlip_LastSourceWins(t *testing.T) {
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
	createUC := rtapp.NewCreateRouteTableUseCase(r, pc, or)
	updateUC := rtapp.NewUpdateRouteTableUseCase(r, or)

	netID := insertNetworkRow(ctx, t, r, "prj-A", "net-for-rt-conc")
	op, err := createUC.Execute(ctx, domain.RouteTable{
		ProjectID: "prj-A", NetworkID: netID, Name: domain.RcNameVPC("rt-conc"),
		Labels: domain.LabelsFromMap(map[string]string{"rt": "treska"}),
	})
	require.NoError(t, err)
	awaitOp(t, or, op.ID)
	rtID := singleRTID(ctx, t, pool, "prj-A")

	const n = 6
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			labels := map[string]string{"rt": "treska"}
			if i%2 == 1 {
				labels = map[string]string{}
			}
			upOp, uerr := updateUC.Execute(ctx, rtapp.UpdateInput{
				RouteTableID: rtID,
				RouteTable:   domain.RouteTable{Labels: domain.LabelsFromMap(labels)},
				UpdateMask:   []string{"labels"},
			})
			if uerr != nil {
				return
			}
			repomock.AwaitOpDone(t, or, upOp.ID)
		}(i)
	}
	wg.Wait()

	// Финальный детерминированный Update — сериализован после шторма — задает winner.
	finOp, err := updateUC.Execute(ctx, rtapp.UpdateInput{
		RouteTableID: rtID,
		RouteTable:   domain.RouteTable{Labels: domain.LabelsFromMap(map[string]string{"rt": "okun"})},
		UpdateMask:   []string{"labels"},
	})
	require.NoError(t, err)
	finSaved := repomock.AwaitOpDone(t, or, finOp.ID)
	require.True(t, finSaved.Done)
	require.Nil(t, finSaved.Error)

	// Финальное состояние строки.
	rd, err := r.Reader(ctx)
	require.NoError(t, err)
	got, err := rd.RouteTables().Get(ctx, rtID)
	require.NoError(t, rd.Close())
	require.NoError(t, err)
	v, ok := got.Labels.Get("rt")
	require.True(t, ok, "финальная строка несет детерминированный winner-label")
	require.Equal(t, "okun", string(v))

	// Каждый register intent — одно из реально записанных состояний label
	// ({rt:treska} | {} | {rt:okun}), без torn/garbage payload.
	regs := registerPayloads(ctx, t, pool, rtID)
	require.GreaterOrEqual(t, len(regs), 2, "intent'ы Create + шторм + финальный Update")
	for _, p := range regs {
		switch lv, has := p.Labels["rt"]; {
		case !has:
			assert.Empty(t, p.Labels, "intent с очищенным label несет пустую map")
		default:
			assert.Contains(t, []string{"treska", "okun"}, lv, "label intent'а — реально записанное значение")
		}
	}

	// Intent с максимальным source_version зеркалит финальную строку
	// (сходимость last-source-wins — stale-membership не зависает).
	maxIdx := 0
	for i := 1; i < len(regs); i++ {
		if regs[i].SourceVersion.After(regs[maxIdx].SourceVersion) {
			maxIdx = i
		}
	}
	assert.Equal(t, "okun", regs[maxIdx].Labels["rt"],
		"intent с max source_version зеркалит финальную строку (mirror сходится, last-source-wins)")
}
