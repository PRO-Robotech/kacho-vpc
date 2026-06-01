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

// KAC-243 — SecurityGroup network_id mandatory+immutable + SG→SG rules
// same-network. Use-case-level integration tests against a real Postgres
// (testcontainers) driving CreateSecurityGroupUseCase / UpdateRulesUseCase /
// UpdateRuleUseCase / UpdateSecurityGroupUseCase / MoveSecurityGroupUseCase end
// to end (sync validation + async Operation worker).
//
// Mirrors acceptance `sub-phase-securitygroup-network-mandatory-and-same-network-rules`
// scenarios SG-NET-01..14,19 (backend). Migration (group E) is a separate task —
// NOT covered here.

// sgNetProjectClient — folder always exists; cross-project Move destination
// existence is satisfied so the guard (D5) is the only thing that can reject.
type sgNetProjectClient struct{}

func (sgNetProjectClient) Exists(context.Context, string) (bool, error) { return true, nil }

// sgNetFixture bundles the wired use-cases + repo handles for one test DB.
type sgNetFixture struct {
	ctx     context.Context
	r       kacho.Repository
	opsRepo operations.Repo

	create      *sgapp.CreateSecurityGroupUseCase
	update      *sgapp.UpdateSecurityGroupUseCase
	updateRules *sgapp.UpdateRulesUseCase
	updateRule  *sgapp.UpdateRuleUseCase
	move        *sgapp.MoveSecurityGroupUseCase
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
		move:        sgapp.NewMoveSecurityGroupUseCase(r, pc, opsRepo),
	}
}

// seedNetwork inserts a Network directly via the writer-TX.
func (f *sgNetFixture) seedNetwork(t *testing.T, projectID, name string) string {
	t.Helper()
	id := ids.NewID(ids.PrefixNetwork)
	require.NoError(t, legacyWithTx(t, f.ctx, f.r, func(w kacho.RepositoryWriter) error {
		_, e := w.Networks().Insert(f.ctx, &domain.Network{ID: id, ProjectID: projectID, Name: domain.RcNameVPC(name)})
		return e
	}))
	return id
}

// seedSG inserts a SecurityGroup directly (bypassing the use-case) bound to the
// given network — used to set up target SGs.
func (f *sgNetFixture) seedSG(t *testing.T, projectID, networkID, name string) string {
	t.Helper()
	id := ids.NewID(ids.PrefixSecurityGroup)
	require.NoError(t, legacyWithTx(t, f.ctx, f.r, func(w kacho.RepositoryWriter) error {
		_, e := w.SecurityGroups().Insert(f.ctx, &domain.SecurityGroup{
			ID:        id,
			ProjectID: projectID,
			NetworkID: networkID,
			Status:    domain.SecurityGroupStatusActive,
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

// awaitOp polls opsRepo.Get until done=true (or a 3s deadline).
func (f *sgNetFixture) awaitOp(t *testing.T, opID string) *operations.Operation {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		op, err := f.opsRepo.Get(f.ctx, opID)
		require.NoError(t, err)
		if op.Done {
			return op
		}
		if time.Now().After(deadline) {
			t.Fatalf("operation %s did not complete within deadline", opID)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// assertFieldViolation asserts the error is gRPC InvalidArgument with the given
// message and a BadRequest.field_violations entry whose field == wantField.
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
// Group A — network_id mandatory on Create
// ---------------------------------------------------------------------------

// SG-NET-01-NEG-CREATE-NO-NETWORK
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

	// No SG row created.
	rd, err := f.r.Reader(f.ctx)
	require.NoError(t, err)
	sgs, _, lerr := rd.SecurityGroups().List(f.ctx, kacho.SecurityGroupFilter{ProjectID: "P"}, kacho.Pagination{})
	require.NoError(t, rd.Close())
	require.NoError(t, lerr)
	assert.Empty(t, sgs)
}

// SG-NET-02-CREATE-OK
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
	assert.Equal(t, domain.SecurityGroupStatusActive, sgs[0].Status)
}

// SG-NET-03-NEG-NETWORK-NOTFOUND
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
// Group C — SG→SG rules same-network
// ---------------------------------------------------------------------------

// SG-NET-07-NEG-RULE-CROSS-NETWORK-CREATE
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

// SG-NET-08-RULE-SAME-NETWORK-OK (via Create)
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

// SG-NET-09-NEG-RULE-CROSS-NETWORK-UPDATERULES
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

	// Rule set unchanged.
	rec := f.getSG(t, sg8)
	assert.Empty(t, rec.Rules)
}

// SG-NET-09 positive mirror: same-network rule via UpdateRules → OK.
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

// SG-NET-10-NEG-RULE-CROSS-NETWORK-UPDATERULE
//
// Proto note: UpdateSecurityGroupRuleRequest carries only description/labels —
// the public RPC cannot change a rule's SG-target (no security_group_id field;
// proto change out of scope of this task). UpdateRuleUseCase therefore validates
// same-network as DEFENSE: editing (description/labels) a rule that holds a
// cross-network SG-target is rejected with the cross-network InvalidArgument and
// field `security_group_id`. We seed such a rule directly (bypassing Create's
// validation) to exercise the guard.
func TestIntegration_SGNet_UpdateRuleCrossNetworkTarget_InvalidArgument(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	f := newSGNetFixture(t)
	netA := f.seedNetwork(t, "P", "net-A")
	netB := f.seedNetwork(t, "P", "net-B")
	sgB := f.seedSG(t, "P", netB, "sg-target-B")

	// Seed sg-8 in net-A with a rule r1 holding a cross-network SG-target.
	sg8 := ids.NewID(ids.PrefixSecurityGroup)
	require.NoError(t, legacyWithTx(t, f.ctx, f.r, func(w kacho.RepositoryWriter) error {
		_, e := w.SecurityGroups().Insert(f.ctx, &domain.SecurityGroup{
			ID: sg8, ProjectID: "P", NetworkID: netA, Status: domain.SecurityGroupStatusActive,
			Rules: []domain.SecurityGroupRule{{
				ID: "r1", Direction: domain.SecurityGroupRuleDirectionIngress,
				FromPort: -1, ToPort: -1, SecurityGroupID: sgB, // cross-network target
			}},
		})
		return e
	}))

	// Editing r1's description must be rejected because r1 holds a cross-network
	// target (same-network invariant defended at UpdateRule).
	_, err := f.updateRule.Execute(f.ctx, sgapp.UpdateRuleInput{
		SecurityGroupID: sg8,
		RuleID:          "r1",
		Description:     "renamed",
		UpdateMask:      []string{"description"},
	})
	assertFieldViolation(t, err,
		"security group rule can only reference a security group in the same network",
		"security_group_id")
}

// SG-NET-10 positive mirror: editing a rule whose SG-target is same-network → OK.
func TestIntegration_SGNet_UpdateRuleSameNetworkTarget_OK(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	f := newSGNetFixture(t)
	netA := f.seedNetwork(t, "P", "net-A")
	sgA := f.seedSG(t, "P", netA, "sg-target-A")

	sg8 := ids.NewID(ids.PrefixSecurityGroup)
	require.NoError(t, legacyWithTx(t, f.ctx, f.r, func(w kacho.RepositoryWriter) error {
		_, e := w.SecurityGroups().Insert(f.ctx, &domain.SecurityGroup{
			ID: sg8, ProjectID: "P", NetworkID: netA, Status: domain.SecurityGroupStatusActive,
			Rules: []domain.SecurityGroupRule{{
				ID: "r1", Direction: domain.SecurityGroupRuleDirectionIngress,
				FromPort: -1, ToPort: -1, SecurityGroupID: sgA, // same-network target
			}},
		})
		return e
	}))

	op, err := f.updateRule.Execute(f.ctx, sgapp.UpdateRuleInput{
		SecurityGroupID: sg8,
		RuleID:          "r1",
		Description:     "renamed",
		UpdateMask:      []string{"description"},
	})
	require.NoError(t, err)
	done := f.awaitOp(t, op.ID)
	require.Nil(t, done.Error, "editing a same-network SG-target rule must succeed: %v", done.Error)
}

// SG-NET-11-NEG-RULE-TARGET-NOTFOUND
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

// SG-NET-12-CONCURRENT-TARGET-DELETE — adding an SG-target rule while the target
// SG is being deleted: deterministic per D3 (service-layer). Either the rule is
// added before the delete (graceful dangling-ref survived on read) OR the delete
// wins first and UpdateRules sees the target missing → InvalidArgument. Never a
// panic / INTERNAL leak.
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
			// Lost the race against delete → InvalidArgument (target gone).
			st, ok := status.FromError(updErr)
			require.True(t, ok, "iter %d: expected gRPC status, got %v", i, updErr)
			assert.Equal(t, codes.InvalidArgument, st.Code(), "iter %d", i)
			assert.NotEqual(t, codes.Internal, st.Code(), "iter %d: never INTERNAL leak", i)
		} else {
			// Sync passed → drive worker; it must not panic / leak INTERNAL.
			require.NotNil(t, op)
			done := f.awaitOp(t, op.ID)
			if done.Error != nil {
				assert.NotEqual(t, int32(codes.Internal), done.Error.GetCode(), "iter %d: worker must not return INTERNAL", i)
			} else {
				// Rule landed; the target SG may now be dangling — must read gracefully.
				rec := f.getSG(t, sg8)
				_ = rec // no panic == pass
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

// SG-NET-13-CIDR-RULE-UNAFFECTED — CIDR / predefined rules are not subject to
// same-network validation.
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
// Group B — network_id immutable on Update
// ---------------------------------------------------------------------------

// SG-NET-04-NEG-UPDATE-MASK-NETWORK
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

// ---------------------------------------------------------------------------
// Group G — Move guarded under network-bound SG
// ---------------------------------------------------------------------------

// SG-NET-19-NEG-MOVE-FORBIDDEN
func TestIntegration_SGNet_MoveNetworkBound_FailedPrecondition(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	f := newSGNetFixture(t)
	netA := f.seedNetwork(t, "P", "net-A")
	sg19 := f.seedSG(t, "P", netA, "sg-19")

	_, err := f.move.Execute(f.ctx, sg19, "Q")
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
	assert.Equal(t, "security group cannot be moved between projects while bound to a network", st.Message())

	rec := f.getSG(t, sg19)
	assert.Equal(t, "P", rec.ProjectID, "SG must be unchanged")
	assert.Equal(t, netA, rec.NetworkID)
}
