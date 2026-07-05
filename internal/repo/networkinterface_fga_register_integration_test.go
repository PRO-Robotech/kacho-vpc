// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package repo_test

// Регистрация owner-tuple в FGA для NetworkInterface должна нести labels на двух
// путях: (a) Create эмитит register-intent с labels + parent_project_id (иначе
// resource_mirror в kacho-iam без labels и label-селектор не матчит даже
// свежесозданный NIC); (b) Update при смене labels переэмитит register-intent с
// обновленными labels. Не-label Update нового intent не порождает.
//
// NIC требует parent-Subnet (FK), создаем сеть + подсеть напрямую через writer.

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coredb "github.com/PRO-Robotech/kacho-corelib/db"
	"github.com/PRO-Robotech/kacho-corelib/ids"
	niapp "github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/api/networkinterface"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/fgaregister"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
	kachopg "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/pg"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/repomock"
)

// insertSubnetRow вставляет сеть + подсеть напрямую через writer (FK-precondition
// для Create NIC), возвращает subnet id.
func insertSubnetRow(ctx context.Context, t *testing.T, r kacho.Repository, projectID, name string) string {
	t.Helper()
	netID := insertNetworkRow(ctx, t, r, projectID, name+"-net")
	subID := ids.NewID(ids.PrefixSubnet)
	require.NoError(t, legacyWithTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		_, e := w.Subnets().Insert(ctx, &domain.Subnet{
			ID:            subID,
			ProjectID:     projectID,
			Name:          domain.RcNameVPC(name + "-sub"),
			NetworkID:     netID,
			PlacementType: domain.PlacementZonal,
			ZoneID:        "zone-a",
			V4CidrBlocks:  []string{"10.0.0.0/24"},
		})
		return e
	}))
	return subID
}

// singleNICID возвращает единственный id network_interface в проекте.
func singleNICID(ctx context.Context, t *testing.T, pool *pgxpool.Pool, projectID string) string {
	t.Helper()
	var id string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT id FROM kacho_vpc.network_interfaces WHERE project_id = $1`, projectID).Scan(&id))
	return id
}

// TestNetworkInterfaceRepo_T32Create01_CreateEmitsLabels_UpdateRevokes проверяет
// оба пути: Create обязан эмитить labels + parent_project_id; Update со сменой
// labels — переэмитить; не-label Update — без лишнего intent.
func TestNetworkInterfaceRepo_T32Create01_CreateEmitsLabels_UpdateRevokes(t *testing.T) {
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

	createUC := niapp.NewCreateNetworkInterfaceUseCase(r, pc, or)
	updateUC := niapp.NewUpdateNetworkInterfaceUseCase(r, or)

	subID := insertSubnetRow(ctx, t, r, "prj-A", "nic")

	// --- Create NIC с labels ---
	op, err := createUC.Execute(ctx, niapp.CreateInput{
		NetworkInterface: domain.NetworkInterface{
			ProjectID: "prj-A",
			Name:      domain.RcNameVPC("nic-okun"),
			SubnetID:  subID,
			Labels:    domain.LabelsFromMap(map[string]string{"nic": "okun"}),
		},
	})
	require.NoError(t, err)
	awaitOp(t, or, op.ID)

	nicID := singleNICID(ctx, t, pool, "prj-A")

	createRegs := registerPayloads(ctx, t, pool, nicID)
	require.Len(t, createRegs, 1, "exactly one register intent on Create")
	assert.Equal(t, map[string]string{"nic": "okun"}, createRegs[0].Labels,
		"BUG (a): NIC Create MUST emit labels, not a bare tuple — else selector never matches")
	assert.Equal(t, "prj-A", createRegs[0].ParentProjectID, "parent_project_id = NIC project_id")
	assert.Equal(t, "vpc_network_interface:"+nicID, createRegs[0].Tuple.Object)

	// --- Update labels (okun → sudak) ---
	upOp, err := updateUC.Execute(ctx, niapp.UpdateInput{
		NetworkInterfaceID: nicID,
		NetworkInterface:   domain.NetworkInterface{Labels: domain.LabelsFromMap(map[string]string{"nic": "sudak"})},
		UpdateMask:         []string{"labels"},
	})
	require.NoError(t, err)
	awaitOp(t, or, upOp.ID)

	updateRegs := registerPayloads(ctx, t, pool, nicID)
	require.Len(t, updateRegs, 2, "BUG (b): NIC Update(labels) MUST re-emit a register intent")
	assert.Equal(t, map[string]string{"nic": "sudak"}, updateRegs[1].Labels,
		"the Update register intent carries the refreshed labels (revoke old selector)")
	assert.Equal(t, "prj-A", updateRegs[1].ParentProjectID)
	require.False(t, updateRegs[1].SourceVersion.IsZero(), "source_version проставлен на Update intent")

	// --- Не-label Update → без лишнего register-intent ---
	nlOp, err := updateUC.Execute(ctx, niapp.UpdateInput{
		NetworkInterfaceID: nicID,
		NetworkInterface:   domain.NetworkInterface{Description: domain.RcDescription("renamed")},
		UpdateMask:         []string{"description"},
	})
	require.NoError(t, err)
	awaitOp(t, or, nlOp.ID)
	require.Len(t, registerPayloads(ctx, t, pool, nicID), 2,
		"non-label Update → no new register intent (G-2)")
}

// TestNetworkInterfaceRepo_T32FullPatch01_EmptyMaskEmits — пустой update_mask
// (full-object PATCH) обязан переэмитить register-intent.
func TestNetworkInterfaceRepo_T32FullPatch01_EmptyMaskEmits(t *testing.T) {
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
	createUC := niapp.NewCreateNetworkInterfaceUseCase(r, pc, or)
	updateUC := niapp.NewUpdateNetworkInterfaceUseCase(r, or)

	subID := insertSubnetRow(ctx, t, r, "prj-A", "nic-fp")
	op, err := createUC.Execute(ctx, niapp.CreateInput{
		NetworkInterface: domain.NetworkInterface{
			ProjectID: "prj-A",
			Name:      domain.RcNameVPC("nic-fp"),
			SubnetID:  subID,
			Labels:    domain.LabelsFromMap(map[string]string{"nic": "treska"}),
		},
	})
	require.NoError(t, err)
	awaitOp(t, or, op.ID)
	nicID := singleNICID(ctx, t, pool, "prj-A")

	// Full-PATCH (пустой update_mask): labels обнуляются → emit с пустыми labels.
	upOp, err := updateUC.Execute(ctx, niapp.UpdateInput{NetworkInterfaceID: nicID, NetworkInterface: domain.NetworkInterface{}})
	require.NoError(t, err)
	awaitOp(t, or, upOp.ID)

	regs := registerPayloads(ctx, t, pool, nicID)
	require.Len(t, regs, 2, "пустой mask = full PATCH ⇒ labelsInMask=true ⇒ emit")
	assert.Empty(t, regs[1].Labels, "full-PATCH обнулил labels → intent с пустыми labels")
}

// TestNetworkInterfaceRepo_T32Atom01_RollbackNoIntent проверяет атомарность на
// уровне writer-tx: tx, аборченная после UPDATE NIC + register emit, не коммитит
// НИЧЕГО — ни изменение строки, ни register intent. Это доказывает, что intent
// едет в ТОЙ ЖЕ tx (без dual-write): провалившийся Update не оставит orphan
// intent, а успешный — не потеряет его.
func TestNetworkInterfaceRepo_T32Atom01_RollbackNoIntent(t *testing.T) {
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
	createUC := niapp.NewCreateNetworkInterfaceUseCase(r, pc, or)

	subID := insertSubnetRow(ctx, t, r, "prj-A", "nic-atom")
	op, err := createUC.Execute(ctx, niapp.CreateInput{
		NetworkInterface: domain.NetworkInterface{
			ProjectID: "prj-A", Name: domain.RcNameVPC("nic-atom"), SubnetID: subID,
			Labels: domain.LabelsFromMap(map[string]string{"nic": "treska"}),
		},
	})
	require.NoError(t, err)
	awaitOp(t, or, op.ID)
	nicID := singleNICID(ctx, t, pool, "prj-A")

	before := registerPayloads(ctx, t, pool, nicID)
	require.Len(t, before, 1)

	// Повторяем writer-tx из networkinterface/update.go (UpdateMeta + register
	// emit), но делаем Abort вместо Commit — оба изменения должны откатиться вместе.
	w, err := r.Writer(ctx)
	require.NoError(t, err)
	rec, err := w.NetworkInterfaces().Get(ctx, nicID)
	require.NoError(t, err)
	rec.NetworkInterface.Labels = domain.LabelsFromMap(nil) // очищаем labels
	_, err = w.NetworkInterfaces().UpdateMeta(ctx, &rec.NetworkInterface)
	require.NoError(t, err)
	require.NoError(t, w.FGARegister().EmitRegister(ctx, fgaregister.RegisterItems(
		fgaregister.ProjectHierarchyItem("prj-A", "vpc_network_interface", nicID, nil),
	)))
	w.Abort() // имитируем сбой до commit

	// Ни UPDATE, ни register intent не пережили abort.
	after := registerPayloads(ctx, t, pool, nicID)
	require.Len(t, after, 1, "аборченная writer-tx НЕ оставляет нового register intent")

	rd, err := r.Reader(ctx)
	require.NoError(t, err)
	got, err := rd.NetworkInterfaces().Get(ctx, nicID)
	require.NoError(t, rd.Close())
	require.NoError(t, err)
	v, ok := got.Labels.Get("nic")
	require.True(t, ok, "смена label откатилась вместе с аборченной tx")
	assert.Equal(t, "treska", string(v))
}
