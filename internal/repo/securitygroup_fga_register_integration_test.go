// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package repo_test

// Регистрация owner-tuple в FGA для SecurityGroup должна нести labels на двух
// путях: (a) Create эмитит register-intent с labels (иначе resource_mirror без
// labels и label-селектор не матчит даже свежесозданный SG); (b) Update при смене
// labels переэмитит register-intent с обновленными labels (revoke старого
// селектора). Не-label Update нового intent не порождает.
//
// testcontainers-интеграция гоняет реальные use-cases SecurityGroup
// (Create + Update) против Postgres 16 и проверяет payload'ы в
// fga_register_outbox.

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coredb "github.com/PRO-Robotech/kacho-corelib/db"
	"github.com/PRO-Robotech/kacho-corelib/ids"
	sgapp "github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/api/securitygroup"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/fgaregister"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/cqrsadapter"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
	kachopg "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/pg"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/repomock"
)

// insertNetworkRow вставляет голый Network напрямую через writer (без use-case),
// чтобы выполнить FK-precondition Create SG (сеть существует).
func insertNetworkRow(ctx context.Context, t *testing.T, r kacho.Repository, projectID, name string) string {
	t.Helper()
	netID := ids.NewID(ids.PrefixNetwork)
	require.NoError(t, legacyWithTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		_, e := w.Networks().Insert(ctx, &domain.Network{
			ID:        netID,
			ProjectID: projectID,
			Name:      domain.RcNameVPC(name),
		})
		return e
	}))
	return netID
}

// sgRegisterPayloadsByID возвращает register-payload'ы по id security-group.
func sgRegisterPayloadsByID(ctx context.Context, t *testing.T, pool *pgxpool.Pool, sgID string) []fgaregister.Payload {
	t.Helper()
	rows, err := pool.Query(ctx,
		`SELECT payload FROM kacho_vpc.fga_register_outbox
		   WHERE resource_id = $1 AND event_type = 'fga.register' ORDER BY id ASC`, sgID)
	require.NoError(t, err)
	defer rows.Close()
	var out []fgaregister.Payload
	for rows.Next() {
		var raw []byte
		require.NoError(t, rows.Scan(&raw))
		var p fgaregister.Payload
		require.NoError(t, json.Unmarshal(raw, &p))
		out = append(out, p)
	}
	require.NoError(t, rows.Err())
	return out
}

// TestSecurityGroupRepo_T31Revoke02_CreateEmitsLabels_UpdateRevokes проверяет оба
// пути end-to-end: (a) Create обязан эмитить labels, (b) Update со сменой labels
// обязан переэмитить register-intent с обновленными labels.
func TestSecurityGroupRepo_T31Revoke02_CreateEmitsLabels_UpdateRevokes(t *testing.T) {
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
	netAdapter := cqrsadapter.NewNetwork(r)
	sgAdapter := cqrsadapter.NewSecurityGroup(r)

	createUC := sgapp.NewCreateSecurityGroupUseCase(r, netAdapter, pc, or).WithSGReader(sgAdapter)
	updateUC := sgapp.NewUpdateSecurityGroupUseCase(r, or).WithSGReader(sgAdapter)

	netID := insertNetworkRow(ctx, t, r, "prj-A", "net-for-sg")

	// --- Create SG с labels ---
	op, err := createUC.Execute(ctx, domain.SecurityGroup{
		ProjectID: "prj-A",
		NetworkID: netID,
		Name:      domain.RcNameVPC("sg-okun"),
		Labels:    domain.LabelsFromMap(map[string]string{"sg": "okun"}),
	})
	require.NoError(t, err)
	awaitOp(t, or, op.ID)

	// Резолвим id созданного SG из БД.
	sgID := singleSGID(ctx, t, pool, "prj-A")

	createRegs := sgRegisterPayloadsByID(ctx, t, pool, sgID)
	require.Len(t, createRegs, 1, "exactly one register intent on Create")
	assert.Equal(t, map[string]string{"sg": "okun"}, createRegs[0].Labels,
		"BUG #113 (a): SG Create MUST emit labels, not a bare tuple — else selector never matches")
	assert.Equal(t, "prj-A", createRegs[0].ParentProjectID, "parent_project_id = SG project_id")

	// --- Update labels SG (okun → sudak) ---
	upOp, err := updateUC.Execute(ctx, sgapp.UpdateInput{
		SecurityGroupID: sgID,
		SecurityGroup:   domain.SecurityGroup{Labels: domain.LabelsFromMap(map[string]string{"sg": "sudak"})},
		UpdateMask:      []string{"labels"},
	})
	require.NoError(t, err)
	awaitOp(t, or, upOp.ID)

	updateRegs := sgRegisterPayloadsByID(ctx, t, pool, sgID)
	require.Len(t, updateRegs, 2, "BUG #113 (b): SG Update(labels) MUST re-emit a register intent")
	assert.Equal(t, map[string]string{"sg": "sudak"}, updateRegs[1].Labels,
		"the Update register intent carries the refreshed labels (revoke old selector)")
	assert.Equal(t, "prj-A", updateRegs[1].ParentProjectID)

	// --- Не-label Update → без лишнего register-intent ---
	nlOp, err := updateUC.Execute(ctx, sgapp.UpdateInput{
		SecurityGroupID: sgID,
		SecurityGroup:   domain.SecurityGroup{Description: domain.RcDescription("renamed")},
		UpdateMask:      []string{"description"},
	})
	require.NoError(t, err)
	awaitOp(t, or, nlOp.ID)
	require.Len(t, sgRegisterPayloadsByID(ctx, t, pool, sgID), 2,
		"non-label Update → no new register intent (G-2)")
}

// awaitOp ждет завершения операции и проверяет успех.
func awaitOp(t *testing.T, or *repomock.OpsRepo, opID string) {
	t.Helper()
	saved := repomock.AwaitOpDone(t, or, opID)
	require.True(t, saved.Done)
	require.Nil(t, saved.Error, "operation must succeed")
}

// singleSGID возвращает единственный id security_group в проекте.
func singleSGID(ctx context.Context, t *testing.T, pool *pgxpool.Pool, projectID string) string {
	t.Helper()
	var id string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT id FROM kacho_vpc.security_groups WHERE project_id = $1`, projectID).Scan(&id))
	return id
}
