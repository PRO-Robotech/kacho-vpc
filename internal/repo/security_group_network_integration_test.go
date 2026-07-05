// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package repo_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	coredb "github.com/PRO-Robotech/kacho-corelib/db"
	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-corelib/operations"
	sgapp "github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/api/securitygroup"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/cqrsadapter"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
	kachopg "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/pg"
)

// Интеграционные тесты use-case-уровня против реального Postgres
// (testcontainers): network_id у SecurityGroup обязателен и immutable, а
// SG→SG-правила допустимы только в пределах одной сети. Гоняют
// CreateSecurityGroupUseCase / UpdateRulesUseCase / UpdateRuleUseCase /
// UpdateSecurityGroupUseCase end-to-end (sync-валидация + async Operation worker).

// sgNetProjectClient — заглушка: проект всегда существует, чтобы единственным
// источником отказа оставалась проверяемая в тесте бизнес-логика.
type sgNetProjectClient struct{}

func (sgNetProjectClient) Exists(context.Context, string) (bool, error) { return true, nil }

// sgNetFixture собирает wired use-cases и repo-хэндлы для одной тестовой БД.
type sgNetFixture struct {
	ctx     context.Context
	r       kacho.Repository
	opsRepo operations.Repo

	create      *sgapp.CreateSecurityGroupUseCase
	update      *sgapp.UpdateSecurityGroupUseCase
	updateRules *sgapp.UpdateRulesUseCase
	updateRule  *sgapp.UpdateRuleUseCase
}

func newSGNetFixture(t *testing.T) *sgNetFixture {
	t.Helper()
	ctx := context.Background()
	dsn := setupTestDB(t)

	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	r := kachopg.New(pool, nil)
	t.Cleanup(func() { r.Close() })

	opsRepo := operations.NewRepo(pool, "kacho_vpc")
	netReader := cqrsadapter.NewNetwork(r)
	sgReader := cqrsadapter.NewSecurityGroup(r)
	pc := sgNetProjectClient{}

	return &sgNetFixture{
		ctx:         ctx,
		r:           r,
		opsRepo:     opsRepo,
		create:      sgapp.NewCreateSecurityGroupUseCase(r, netReader, pc, opsRepo).WithSGReader(sgReader),
		update:      sgapp.NewUpdateSecurityGroupUseCase(r, opsRepo),
		updateRules: sgapp.NewUpdateRulesUseCase(r, opsRepo, sgReader),
		updateRule:  sgapp.NewUpdateRuleUseCase(r, opsRepo, sgReader),
	}
}

// seedNetwork вставляет Network напрямую через writer-TX.
func (f *sgNetFixture) seedNetwork(t *testing.T, projectID, name string) string {
	t.Helper()
	id := ids.NewID(ids.PrefixNetwork)
	require.NoError(t, legacyWithTx(t, f.ctx, f.r, func(w kacho.RepositoryWriter) error {
		_, e := w.Networks().Insert(f.ctx, &domain.Network{ID: id, ProjectID: projectID, Name: domain.RcNameVPC(name)})
		return e
	}))
	return id
}

// seedSG вставляет SecurityGroup напрямую (минуя use-case), привязанный к
// заданной сети — для подготовки target-SG.
func (f *sgNetFixture) seedSG(t *testing.T, projectID, networkID, name string) string {
	t.Helper()
	id := ids.NewID(ids.PrefixSecurityGroup)
	require.NoError(t, legacyWithTx(t, f.ctx, f.r, func(w kacho.RepositoryWriter) error {
		_, e := w.SecurityGroups().Insert(f.ctx, &domain.SecurityGroup{
			ID:        id,
			ProjectID: projectID,
			NetworkID: networkID,
		})
		return e
	}))
	return id
}

func (f *sgNetFixture) getSG(t *testing.T, id string) *kacho.SecurityGroupRecord {
	t.Helper()
	rd, err := f.r.Reader(f.ctx)
	require.NoError(t, err)
	rec, gerr := rd.SecurityGroups().Get(f.ctx, id)
	require.NoError(t, rd.Close())
	require.NoError(t, gerr)
	return rec
}

// awaitOp поллит opsRepo.Get до done=true. Дедлайн 10s (тот же щедрый бюджет, что
// и у остальных integration-хелперов: под загруженным CI-раннером in-process
// operations.Run-goroutine может не успеть за 3s → ложный red).
func (f *sgNetFixture) awaitOp(t *testing.T, opID string) *operations.Operation {
	t.Helper()
	var op *operations.Operation
	require.Eventually(t, func() bool {
		var err error
		op, err = f.opsRepo.Get(f.ctx, opID)
		require.NoError(t, err)
		return op.Done
	}, 10*time.Second, 20*time.Millisecond, "operation %s did not complete within deadline", opID)
	return op
}

// assertFieldViolation проверяет, что ошибка — gRPC InvalidArgument с заданным
// сообщением и записью BadRequest.field_violations, у которой field == wantField.
func assertFieldViolation(t *testing.T, err error, wantMsg, wantField string) {
	t.Helper()
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok, "expected a gRPC status error, got %v", err)
	assert.Equal(t, codes.InvalidArgument, st.Code(), "code")
	assert.Equal(t, wantMsg, st.Message(), "message")
	found := false
	for _, d := range st.Details() {
		if br, ok := d.(*errdetails.BadRequest); ok {
			for _, fv := range br.GetFieldViolations() {
				if fv.GetField() == wantField {
					found = true
				}
			}
		}
	}
	assert.True(t, found, "expected BadRequest.field_violations with field=%q in %v", wantField, st.Details())
}

func ingressSGTargetRule(targetSGID string) domain.SecurityGroupRule {
	return domain.SecurityGroupRule{
		Direction:       domain.SecurityGroupRuleDirectionIngress,
		FromPort:        -1,
		ToPort:          -1,
		SecurityGroupID: targetSGID,
	}
}

// ---------------------------------------------------------------------------
// network_id обязателен на Create
// ---------------------------------------------------------------------------

func TestIntegration_SGNet_CreateWithoutNetwork_InvalidArgument(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	f := newSGNetFixture(t)

	_, err := f.create.Execute(f.ctx, domain.SecurityGroup{
		ProjectID: "P",
		Name:      domain.RcNameVPC("sg-1"),
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Equal(t, "network_id required", st.Message())

	// Строка SG не создана.
	rd, err := f.r.Reader(f.ctx)
	require.NoError(t, err)
	sgs, _, lerr := rd.SecurityGroups().List(f.ctx, kacho.SecurityGroupFilter{ProjectID: "P"}, kacho.Pagination{})
	require.NoError(t, rd.Close())
	require.NoError(t, lerr)
	assert.Empty(t, sgs)
}

func TestIntegration_SGNet_CreateWithValidNetwork_OK(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	f := newSGNetFixture(t)
	netA := f.seedNetwork(t, "P", "net-A")

	op, err := f.create.Execute(f.ctx, domain.SecurityGroup{
		ProjectID: "P",
		NetworkID: netA,
		Name:      domain.RcNameVPC("sg-2"),
	})
	require.NoError(t, err)
	require.NotEmpty(t, op.ID)
	require.False(t, op.Done)

	done := f.awaitOp(t, op.ID)
	require.Nil(t, done.Error, "operation should not error: %v", done.Error)

	rd, err := f.r.Reader(f.ctx)
	require.NoError(t, err)
	sgs, _, lerr := rd.SecurityGroups().List(f.ctx, kacho.SecurityGroupFilter{ProjectID: "P", Name: "sg-2"}, kacho.Pagination{})
	require.NoError(t, rd.Close())
	require.NoError(t, lerr)
	require.Len(t, sgs, 1)
	assert.Equal(t, netA, sgs[0].NetworkID)
}

func TestIntegration_SGNet_CreateWithMissingNetwork_NotFound(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	f := newSGNetFixture(t)
	missing := "enp00000000000000000"

	_, err := f.create.Execute(f.ctx, domain.SecurityGroup{
		ProjectID: "P",
		NetworkID: missing,
		Name:      domain.RcNameVPC("sg-3"),
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
	assert.Equal(t, "Network "+missing+" not found", st.Message())
}

// ---------------------------------------------------------------------------
// SG→SG-правила в пределах одной сети
// ---------------------------------------------------------------------------

func TestIntegration_SGNet_CreateCrossNetworkRule_InvalidArgument(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	f := newSGNetFixture(t)
	netA := f.seedNetwork(t, "P", "net-A")
	netB := f.seedNetwork(t, "P", "net-B")
	sgB := f.seedSG(t, "P", netB, "sg-target-B")

	_, err := f.create.Execute(f.ctx, domain.SecurityGroup{
		ProjectID: "P",
		NetworkID: netA,
		Name:      domain.RcNameVPC("sg-7"),
		Rules:     []domain.SecurityGroupRule{ingressSGTargetRule(sgB)},
	})
	assertFieldViolation(t, err,
		"security group rule can only reference a security group in the same network",
		"rule_specs[0].security_group_id")

	rd, err := f.r.Reader(f.ctx)
	require.NoError(t, err)
	sgs, _, lerr := rd.SecurityGroups().List(f.ctx, kacho.SecurityGroupFilter{ProjectID: "P", Name: "sg-7"}, kacho.Pagination{})
	require.NoError(t, rd.Close())
	require.NoError(t, lerr)
	assert.Empty(t, sgs, "cross-network SG-target rule must abort Create")
}

// Правило на target-SG из той же сети принимается (через Create).
func TestIntegration_SGNet_CreateSameNetworkRule_OK(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	f := newSGNetFixture(t)
	netA := f.seedNetwork(t, "P", "net-A")
	sgA := f.seedSG(t, "P", netA, "sg-target-A")

	op, err := f.create.Execute(f.ctx, domain.SecurityGroup{
		ProjectID: "P",
		NetworkID: netA,
		Name:      domain.RcNameVPC("sg-8"),
		Rules:     []domain.SecurityGroupRule{ingressSGTargetRule(sgA)},
	})
	require.NoError(t, err)
	done := f.awaitOp(t, op.ID)
	require.Nil(t, done.Error, "same-network SG-target rule must be accepted: %v", done.Error)
}

func TestIntegration_SGNet_UpdateRulesCrossNetwork_InvalidArgument(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	f := newSGNetFixture(t)
	netA := f.seedNetwork(t, "P", "net-A")
	netB := f.seedNetwork(t, "P", "net-B")
	sg8 := f.seedSG(t, "P", netA, "sg-8")
	sgB := f.seedSG(t, "P", netB, "sg-target-B")

	_, err := f.updateRules.Execute(f.ctx, sgapp.UpdateRulesInput{
		SecurityGroupID:   sg8,
		AdditionRuleSpecs: []domain.SecurityGroupRule{ingressSGTargetRule(sgB)},
	})
	assertFieldViolation(t, err,
		"security group rule can only reference a security group in the same network",
		"addition_rule_specs[0].security_group_id")

	// Набор правил не изменился.
	rec := f.getSG(t, sg8)
	assert.Empty(t, rec.Rules)
}

// Позитивное зеркало: правило в той же сети через UpdateRules → OK.
func TestIntegration_SGNet_UpdateRulesSameNetwork_OK(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	f := newSGNetFixture(t)
	netA := f.seedNetwork(t, "P", "net-A")
	sg8 := f.seedSG(t, "P", netA, "sg-8")
	sgA := f.seedSG(t, "P", netA, "sg-target-A")

	op, err := f.updateRules.Execute(f.ctx, sgapp.UpdateRulesInput{
		SecurityGroupID:   sg8,
		AdditionRuleSpecs: []domain.SecurityGroupRule{ingressSGTargetRule(sgA)},
	})
	require.NoError(t, err)
	done := f.awaitOp(t, op.ID)
	require.Nil(t, done.Error, "same-network rule via UpdateRules must be accepted: %v", done.Error)

	rec := f.getSG(t, sg8)
	require.Len(t, rec.Rules, 1)
	assert.Equal(t, sgA, rec.Rules[0].SecurityGroupID)
}

// UpdateSecurityGroupRuleRequest несет только description/labels — публичный RPC
// не может сменить SG-target правила (поля security_group_id в нем нет).
// UpdateRuleUseCase все равно валидирует same-network как defense-in-depth:
// редактирование (description/labels) правила с cross-network SG-target
// отклоняется с cross-network InvalidArgument и полем `security_group_id`. Такое
// правило сидируем напрямую (минуя валидацию Create), чтобы проверить guard.
func TestIntegration_SGNet_UpdateRuleCrossNetworkTarget_InvalidArgument(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	f := newSGNetFixture(t)
	netA := f.seedNetwork(t, "P", "net-A")
	netB := f.seedNetwork(t, "P", "net-B")
	sgB := f.seedSG(t, "P", netB, "sg-target-B")

	// Сидируем sg-8 в net-A с правилом, держащим cross-network SG-target. Id
	// правила обязан быть well-formed resource id (в проде его выдает ids.NewID;
	// use-case отвергает malformed rule id еще до same-network guard).
	sg8 := ids.NewID(ids.PrefixSecurityGroup)
	ruleID := ids.NewID(ids.PrefixSecurityGroup)
	require.NoError(t, legacyWithTx(t, f.ctx, f.r, func(w kacho.RepositoryWriter) error {
		_, e := w.SecurityGroups().Insert(f.ctx, &domain.SecurityGroup{
			ID: sg8, ProjectID: "P", NetworkID: netA,
			Rules: []domain.SecurityGroupRule{{
				ID: ruleID, Direction: domain.SecurityGroupRuleDirectionIngress,
				FromPort: -1, ToPort: -1, SecurityGroupID: sgB, // cross-network target
			}},
		})
		return e
	}))

	// Правка description должна быть отклонена, т.к. правило держит cross-network
	// target (same-network инвариант защищен на UpdateRule).
	_, err := f.updateRule.Execute(f.ctx, sgapp.UpdateRuleInput{
		SecurityGroupID: sg8,
		RuleID:          ruleID,
		Description:     "renamed",
		UpdateMask:      []string{"description"},
	})
	assertFieldViolation(t, err,
		"security group rule can only reference a security group in the same network",
		"security_group_id")
}

// Позитивное зеркало: правка правила, чей SG-target в той же сети → OK.
func TestIntegration_SGNet_UpdateRuleSameNetworkTarget_OK(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	f := newSGNetFixture(t)
	netA := f.seedNetwork(t, "P", "net-A")
	sgA := f.seedSG(t, "P", netA, "sg-target-A")

	sg8 := ids.NewID(ids.PrefixSecurityGroup)
	ruleID := ids.NewID(ids.PrefixSecurityGroup)
	require.NoError(t, legacyWithTx(t, f.ctx, f.r, func(w kacho.RepositoryWriter) error {
		_, e := w.SecurityGroups().Insert(f.ctx, &domain.SecurityGroup{
			ID: sg8, ProjectID: "P", NetworkID: netA,
			Rules: []domain.SecurityGroupRule{{
				ID: ruleID, Direction: domain.SecurityGroupRuleDirectionIngress,
				FromPort: -1, ToPort: -1, SecurityGroupID: sgA, // same-network target
			}},
		})
		return e
	}))

	op, err := f.updateRule.Execute(f.ctx, sgapp.UpdateRuleInput{
		SecurityGroupID: sg8,
		RuleID:          ruleID,
		Description:     "renamed",
		UpdateMask:      []string{"description"},
	})
	require.NoError(t, err)
	done := f.awaitOp(t, op.ID)
	require.Nil(t, done.Error, "editing a same-network SG-target rule must succeed: %v", done.Error)
}

func TestIntegration_SGNet_UpdateRulesTargetNotFound_InvalidArgument(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	f := newSGNetFixture(t)
	netA := f.seedNetwork(t, "P", "net-A")
	sg8 := f.seedSG(t, "P", netA, "sg-8")
	missing := "enp11111111111111111"

	_, err := f.updateRules.Execute(f.ctx, sgapp.UpdateRulesInput{
		SecurityGroupID:   sg8,
		AdditionRuleSpecs: []domain.SecurityGroupRule{ingressSGTargetRule(missing)},
	})
	assertFieldViolation(t, err,
		"security group rule references a non-existent security group",
		"addition_rule_specs[0].security_group_id")
}

// Добавление SG-target-правила, пока target-SG удаляют: исход детерминирован на
// service-слое. Либо правило добавлено до удаления (висящая ссылка грациозно
// переживается на чтении), либо удаление выигрывает первым и UpdateRules видит
// отсутствующий target → InvalidArgument. Никогда не panic / INTERNAL-leak.
func TestIntegration_SGNet_ConcurrentTargetDelete(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	f := newSGNetFixture(t)
	netA := f.seedNetwork(t, "P", "net-A")

	const iterations = 20
	for i := 0; i < iterations; i++ {
		sg8 := f.seedSG(t, "P", netA, "")
		sgA := f.seedSG(t, "P", netA, "")

		var wg sync.WaitGroup
		var updErr error
		var op *operations.Operation
		start := make(chan struct{})
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			var e error
			op, e = f.updateRules.Execute(f.ctx, sgapp.UpdateRulesInput{
				SecurityGroupID:   sg8,
				AdditionRuleSpecs: []domain.SecurityGroupRule{ingressSGTargetRule(sgA)},
			})
			updErr = e
		}()
		go func() {
			defer wg.Done()
			<-start
			_ = legacyWithTx(t, f.ctx, f.r, func(w kacho.RepositoryWriter) error {
				return w.SecurityGroups().Delete(f.ctx, sgA)
			})
		}()
		close(start)
		wg.Wait()

		if updErr != nil {
			// Проиграли гонку удалению → InvalidArgument (target исчез).
			st, ok := status.FromError(updErr)
			require.True(t, ok, "iter %d: expected gRPC status, got %v", i, updErr)
			assert.Equal(t, codes.InvalidArgument, st.Code(), "iter %d", i)
			assert.NotEqual(t, codes.Internal, st.Code(), "iter %d: never INTERNAL leak", i)
		} else {
			// Sync прошел → гоним worker; он не должен panic / leak'нуть INTERNAL.
			require.NotNil(t, op)
			done := f.awaitOp(t, op.ID)
			if done.Error != nil {
				assert.NotEqual(t, int32(codes.Internal), done.Error.GetCode(), "iter %d: worker must not return INTERNAL", i)
			} else {
				// Правило записано; target-SG может висеть — чтение обязано пройти грациозно.
				rec := f.getSG(t, sg8)
				_ = rec // отсутствие паники = pass
			}
		}

		// cleanup
		_ = legacyWithTx(t, f.ctx, f.r, func(w kacho.RepositoryWriter) error {
			_ = w.SecurityGroups().Delete(f.ctx, sg8)
			_ = w.SecurityGroups().Delete(f.ctx, sgA)
			return nil
		})
	}
}

// CIDR- и predefined-правила не подпадают под same-network-валидацию.
func TestIntegration_SGNet_CidrAndPredefinedRulesUnaffected(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	f := newSGNetFixture(t)
	netA := f.seedNetwork(t, "P", "net-A")
	sg2 := f.seedSG(t, "P", netA, "sg-2")

	op, err := f.updateRules.Execute(f.ctx, sgapp.UpdateRulesInput{
		SecurityGroupID: sg2,
		AdditionRuleSpecs: []domain.SecurityGroupRule{
			{Direction: domain.SecurityGroupRuleDirectionIngress, FromPort: -1, ToPort: -1, V4CidrBlocks: []string{"10.0.0.0/24"}},
			{Direction: domain.SecurityGroupRuleDirectionEgress, FromPort: -1, ToPort: -1, PredefinedTarget: "self_security_group"},
		},
	})
	require.NoError(t, err)
	done := f.awaitOp(t, op.ID)
	require.Nil(t, done.Error, "CIDR/predefined rules must be unaffected: %v", done.Error)
}

// ---------------------------------------------------------------------------
// network_id immutable на Update
// ---------------------------------------------------------------------------

func TestIntegration_SGNet_UpdateMaskNetwork_InvalidArgument(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	f := newSGNetFixture(t)
	netA := f.seedNetwork(t, "P", "net-A")
	netB := f.seedNetwork(t, "P", "net-B")
	sg2 := f.seedSG(t, "P", netA, "sg-2")

	_, err := f.update.Execute(f.ctx, sgapp.UpdateInput{
		SecurityGroupID: sg2,
		SecurityGroup:   domain.SecurityGroup{NetworkID: netB},
		UpdateMask:      []string{"network_id"},
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())

	rec := f.getSG(t, sg2)
	assert.Equal(t, netA, rec.NetworkID, "network_id must be unchanged")
}
