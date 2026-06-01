package securitygroup

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/kachomock"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/repomock"
)

// Тесты SecurityGroup use-case'ов и handler'а. Wave 3 (KAC-94): сюда переехали
// прежние тесты `internal/handler/coverage_test.go::TestSecurityGroupHandler_*`
// и `coverage2_test.go::TestSecurityGroupHandler_*` (теперь — против
// `*securitygroup.Handler`).
//
// Mock-port'ы — переиспользуем `internal/repo/repomock` (уже реализует
// `internal/repo.SecurityGroupRepoIface`, который ⊇ нашему локальному
// SecurityGroupRepo).

// ---- builders ----

// sgReaderMock — SecurityGroupReader adapter поверх kachomock.Repository (KAC-243
// §C): резолвит SG-record через committed reader-snapshot мока.
type sgReaderMock struct{ repo *kachomock.Repository }

func newSGReaderMock(r *kachomock.Repository) *sgReaderMock { return &sgReaderMock{repo: r} }

func (m *sgReaderMock) Get(ctx context.Context, id string) (*kacho.SecurityGroupRecord, error) {
	rd, err := m.repo.Reader(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rd.Close() }()
	return rd.SecurityGroups().Get(ctx, id)
}

func makeHandler(
	t *testing.T,
	sgr *kachomock.Repository,
	nr *repomock.NetworkRepo,
	or *repomock.OpsRepo,
	fc *repomock.ProjectClient,
) *Handler {
	t.Helper()
	sgReader := newSGReaderMock(sgr)
	create := NewCreateSecurityGroupUseCase(sgr, nr, fc, or).WithSGReader(sgReader)
	update := NewUpdateSecurityGroupUseCase(sgr, or)
	updateRules := NewUpdateRulesUseCase(sgr, or, sgReader)
	updateRule := NewUpdateRuleUseCase(sgr, or, sgReader)
	deleteUC := NewDeleteSecurityGroupUseCase(sgr, or)
	move := NewMoveSecurityGroupUseCase(sgr, fc, or)
	get := NewGetSecurityGroupUseCase(sgr)
	list := NewListSecurityGroupsUseCase(sgr, nil)
	listOps := NewListOperationsUseCase(sgr, or)
	return NewHandler(create, update, updateRules, updateRule, deleteUC, move, get, list, listOps)
}

// minimalHandler — wiring с дефолтными mock'ами; folder=true.
func minimalHandler(t *testing.T) (*Handler, *repomock.OpsRepo, *kachomock.Repository) {
	t.Helper()
	sgr := kachomock.NewRepository()
	nr := repomock.NewNetworkRepo()
	or := repomock.NewOpsRepo()
	fc := &repomock.ProjectClient{OK: true}
	return makeHandler(t, sgr, nr, or, fc), or, sgr
}

// ---- Handler — sync paths ----

func TestHandler_Get_InvalidArg(t *testing.T) {
	h, _, _ := minimalHandler(t)
	_, err := h.Get(context.Background(), &vpcv1.GetSecurityGroupRequest{SecurityGroupId: ""})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestHandler_Get_NotFound(t *testing.T) {
	h, _, _ := minimalHandler(t)
	_, err := h.Get(context.Background(), &vpcv1.GetSecurityGroupRequest{SecurityGroupId: ids.NewID(ids.PrefixSecurityGroup)})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestHandler_List_Empty(t *testing.T) {
	h, _, _ := minimalHandler(t)
	resp, err := h.List(context.Background(), &vpcv1.ListSecurityGroupsRequest{ProjectId: "f1"})
	require.NoError(t, err)
	assert.Empty(t, resp.SecurityGroups)
}

func TestHandler_Create_Validates(t *testing.T) {
	// project_id отсутствует → InvalidArgument.
	h, _, _ := minimalHandler(t)
	_, err := h.Create(context.Background(), &vpcv1.CreateSecurityGroupRequest{Name: "sg"})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestHandler_Update_InvalidArg(t *testing.T) {
	h, _, _ := minimalHandler(t)
	_, err := h.Update(context.Background(), &vpcv1.UpdateSecurityGroupRequest{SecurityGroupId: ""})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestHandler_UpdateRules_InvalidArg(t *testing.T) {
	h, _, _ := minimalHandler(t)
	_, err := h.UpdateRules(context.Background(), &vpcv1.UpdateSecurityGroupRulesRequest{SecurityGroupId: ""})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestHandler_UpdateRule_InvalidArg(t *testing.T) {
	h, _, _ := minimalHandler(t)
	_, err := h.UpdateRule(context.Background(), &vpcv1.UpdateSecurityGroupRuleRequest{SecurityGroupId: ""})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestHandler_Delete_InvalidArg(t *testing.T) {
	h, _, _ := minimalHandler(t)
	_, err := h.Delete(context.Background(), &vpcv1.DeleteSecurityGroupRequest{SecurityGroupId: ""})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestHandler_Move_RequiresID(t *testing.T) {
	h, _, _ := minimalHandler(t)
	_, err := h.Move(context.Background(), &vpcv1.MoveSecurityGroupRequest{SecurityGroupId: ""})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestHandler_ListOperations_RequiresID(t *testing.T) {
	h, _, _ := minimalHandler(t)
	_, err := h.ListOperations(context.Background(), &vpcv1.ListSecurityGroupOperationsRequest{SecurityGroupId: ""})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

// ---- use-case-level (без handler'а) ----

func TestCreateUseCase_ValidationError(t *testing.T) {
	sgr := kachomock.NewRepository()
	or := repomock.NewOpsRepo()
	nr := repomock.NewNetworkRepo()
	uc := NewCreateSecurityGroupUseCase(sgr, nr, &repomock.ProjectClient{OK: true}, or)

	// project_id required.
	_, err := uc.Execute(context.Background(), domain.SecurityGroup{Name: "test"})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())

	// network_id required (KAC-243 §A): пустой network_id → sync InvalidArgument.
	_, err = uc.Execute(context.Background(), domain.SecurityGroup{ProjectID: "f1", Name: "test"})
	require.Error(t, err)
	st, _ = status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Equal(t, "network_id required", st.Message())

	// invalid name (digit start, NameVPC permissive но цифра в начале — нет).
	// Seed a valid network so we get past the network_id required/existence gate.
	netID := ids.NewID(ids.PrefixNetwork)
	_, _ = nr.Insert(context.Background(), &domain.Network{ID: netID, ProjectID: "f1", Name: domain.RcNameVPC("net")})
	_, err = uc.Execute(context.Background(), domain.SecurityGroup{
		ProjectID: "f1",
		NetworkID: netID,
		Name:      domain.RcNameVPC("1bad"),
	})
	require.Error(t, err)
	st, _ = status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

// TestCreateUseCase_FolderNotFound — KAC-94 / skill evgeniy I.4: sync
// folder.Exists precheck удалён (race-prone). Verbatim-YC NotFound теперь
// возвращается через `operation.error` из async `doCreate`, не через
// sync-status. Поэтому: Execute → не ошибка; AwaitOpDone → Operation.Done=true
// с Error.Code == NotFound.
func TestCreateUseCase_FolderNotFound(t *testing.T) {
	sgr := kachomock.NewRepository()
	or := repomock.NewOpsRepo()
	nr := repomock.NewNetworkRepo()
	netID := ids.NewID(ids.PrefixNetwork)
	_, _ = nr.Insert(context.Background(), &domain.Network{ID: netID, ProjectID: "f1", Name: domain.RcNameVPC("net")})
	uc := NewCreateSecurityGroupUseCase(sgr, nr, &repomock.ProjectClient{OK: false}, or)

	op, err := uc.Execute(context.Background(), domain.SecurityGroup{
		ProjectID: "f1",
		NetworkID: netID,
		Name:      domain.RcNameVPC("sg1"),
	})
	require.NoError(t, err)
	require.NotEmpty(t, op.ID)

	saved := repomock.AwaitOpDone(t, or, op.ID)
	require.True(t, saved.Done)
	require.NotNil(t, saved.Error, "operation should fail in worker — folder missing")
	assert.Equal(t, int32(codes.NotFound), saved.Error.Code)
}

// TestCreateUseCase_OK — KAC-243 §A: network_id обязателен; happy-path Create
// с валидной существующей Network.
func TestCreateUseCase_OK(t *testing.T) {
	sgr := kachomock.NewRepository()
	or := repomock.NewOpsRepo()
	nr := repomock.NewNetworkRepo()
	netID := ids.NewID(ids.PrefixNetwork)
	_, _ = nr.Insert(context.Background(), &domain.Network{ID: netID, ProjectID: "f1", Name: domain.RcNameVPC("net")})
	uc := NewCreateSecurityGroupUseCase(sgr, nr, &repomock.ProjectClient{OK: true}, or)

	op, err := uc.Execute(context.Background(), domain.SecurityGroup{
		ProjectID:   "f1",
		NetworkID:   netID,
		Name:        domain.RcNameVPC("sg1"),
		Description: domain.RcDescription("desc"),
	})
	require.NoError(t, err)
	require.NotEmpty(t, op.ID)

	saved := repomock.AwaitOpDone(t, or, op.ID)
	assert.True(t, saved.Done)
	assert.Nil(t, saved.Error)
}

func TestDeleteUseCase_InvalidArg(t *testing.T) {
	uc := NewDeleteSecurityGroupUseCase(kachomock.NewRepository(), repomock.NewOpsRepo())
	_, err := uc.Execute(context.Background(), "")
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestMoveUseCase_Validates(t *testing.T) {
	uc := NewMoveSecurityGroupUseCase(kachomock.NewRepository(), &repomock.ProjectClient{OK: true}, repomock.NewOpsRepo())
	_, err := uc.Execute(context.Background(), "", "f2")
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	_, err = uc.Execute(context.Background(), ids.NewID(ids.PrefixSecurityGroup), "")
	st, _ = status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestListUseCase_RequiresFolder(t *testing.T) {
	uc := NewListSecurityGroupsUseCase(kachomock.NewRepository(), nil)
	_, _, err := uc.Execute(context.Background(), "", SecurityGroupFilter{}, Pagination{})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestUpdateRulesUseCase_InvalidArg(t *testing.T) {
	uc := NewUpdateRulesUseCase(kachomock.NewRepository(), repomock.NewOpsRepo(), nil)
	// security_group_id required (resource-id validation).
	_, err := uc.Execute(context.Background(), UpdateRulesInput{SecurityGroupID: "bad"})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

// ---- KAC-243 same-network SG-target rule validation (fast, mock-based) ----

// seedMockSG inserts a SecurityGroup into the kachomock via a committed writer-TX.
func seedMockSG(t *testing.T, sgr *kachomock.Repository, projectID, networkID, name string) string {
	t.Helper()
	id := ids.NewID(ids.PrefixSecurityGroup)
	w, err := sgr.Writer(context.Background())
	require.NoError(t, err)
	_, err = w.SecurityGroups().Insert(context.Background(), &domain.SecurityGroup{
		ID: id, ProjectID: projectID, NetworkID: networkID, Status: domain.SecurityGroupStatusActive,
		Name: domain.RcNameVPC(name),
	})
	require.NoError(t, err)
	require.NoError(t, w.Commit())
	return id
}

func sgTargetRule(targetSGID string) domain.SecurityGroupRule {
	return domain.SecurityGroupRule{
		Direction: domain.SecurityGroupRuleDirectionIngress, FromPort: -1, ToPort: -1,
		SecurityGroupID: targetSGID,
	}
}

// SG-NET-07: Create with cross-network SG-target rule → sync InvalidArgument.
func TestCreateUseCase_CrossNetworkRule_InvalidArgument(t *testing.T) {
	sgr := kachomock.NewRepository()
	or := repomock.NewOpsRepo()
	nr := repomock.NewNetworkRepo()
	netA := ids.NewID(ids.PrefixNetwork)
	netB := ids.NewID(ids.PrefixNetwork)
	_, _ = nr.Insert(context.Background(), &domain.Network{ID: netA, ProjectID: "P", Name: "net-A"})
	_, _ = nr.Insert(context.Background(), &domain.Network{ID: netB, ProjectID: "P", Name: "net-B"})
	sgB := seedMockSG(t, sgr, "P", netB, "sg-target-B")

	uc := NewCreateSecurityGroupUseCase(sgr, nr, &repomock.ProjectClient{OK: true}, or).WithSGReader(newSGReaderMock(sgr))
	_, err := uc.Execute(context.Background(), domain.SecurityGroup{
		ProjectID: "P", NetworkID: netA, Name: domain.RcNameVPC("sg-7"),
		Rules: []domain.SecurityGroupRule{sgTargetRule(sgB)},
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Equal(t, "security group rule can only reference a security group in the same network", st.Message())
}

// SG-NET-08: Create with same-network SG-target rule → OK (no sync error).
func TestCreateUseCase_SameNetworkRule_OK(t *testing.T) {
	sgr := kachomock.NewRepository()
	or := repomock.NewOpsRepo()
	nr := repomock.NewNetworkRepo()
	netA := ids.NewID(ids.PrefixNetwork)
	_, _ = nr.Insert(context.Background(), &domain.Network{ID: netA, ProjectID: "P", Name: "net-A"})
	sgA := seedMockSG(t, sgr, "P", netA, "sg-target-A")

	uc := NewCreateSecurityGroupUseCase(sgr, nr, &repomock.ProjectClient{OK: true}, or).WithSGReader(newSGReaderMock(sgr))
	op, err := uc.Execute(context.Background(), domain.SecurityGroup{
		ProjectID: "P", NetworkID: netA, Name: domain.RcNameVPC("sg-8"),
		Rules: []domain.SecurityGroupRule{sgTargetRule(sgA)},
	})
	require.NoError(t, err)
	saved := repomock.AwaitOpDone(t, or, op.ID)
	assert.True(t, saved.Done)
	assert.Nil(t, saved.Error)
}

// SG-NET-11: Create with non-existent SG-target → sync InvalidArgument.
func TestCreateUseCase_TargetNotFound_InvalidArgument(t *testing.T) {
	sgr := kachomock.NewRepository()
	or := repomock.NewOpsRepo()
	nr := repomock.NewNetworkRepo()
	netA := ids.NewID(ids.PrefixNetwork)
	_, _ = nr.Insert(context.Background(), &domain.Network{ID: netA, ProjectID: "P", Name: "net-A"})

	uc := NewCreateSecurityGroupUseCase(sgr, nr, &repomock.ProjectClient{OK: true}, or).WithSGReader(newSGReaderMock(sgr))
	_, err := uc.Execute(context.Background(), domain.SecurityGroup{
		ProjectID: "P", NetworkID: netA, Name: domain.RcNameVPC("sg-x"),
		Rules: []domain.SecurityGroupRule{sgTargetRule("enp11111111111111111")},
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Equal(t, "security group rule references a non-existent security group", st.Message())
}

// SG-NET-09: UpdateRules with cross-network SG-target → sync InvalidArgument.
func TestUpdateRulesUseCase_CrossNetworkRule_InvalidArgument(t *testing.T) {
	sgr := kachomock.NewRepository()
	or := repomock.NewOpsRepo()
	netA := ids.NewID(ids.PrefixNetwork)
	netB := ids.NewID(ids.PrefixNetwork)
	sg8 := seedMockSG(t, sgr, "P", netA, "sg-8")
	sgB := seedMockSG(t, sgr, "P", netB, "sg-target-B")

	uc := NewUpdateRulesUseCase(sgr, or, newSGReaderMock(sgr))
	_, err := uc.Execute(context.Background(), UpdateRulesInput{
		SecurityGroupID:   sg8,
		AdditionRuleSpecs: []domain.SecurityGroupRule{sgTargetRule(sgB)},
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Equal(t, "security group rule can only reference a security group in the same network", st.Message())
}

// SG-NET-19: Move of network-bound SG → sync FailedPrecondition.
func TestMoveUseCase_NetworkBound_FailedPrecondition(t *testing.T) {
	sgr := kachomock.NewRepository()
	or := repomock.NewOpsRepo()
	netA := ids.NewID(ids.PrefixNetwork)
	sg19 := seedMockSG(t, sgr, "P", netA, "sg-19")

	uc := NewMoveSecurityGroupUseCase(sgr, &repomock.ProjectClient{OK: true}, or)
	_, err := uc.Execute(context.Background(), sg19, "Q")
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
	assert.Equal(t, "security group cannot be moved between projects while bound to a network", st.Message())
}

func TestUpdateRuleUseCase_InvalidArg(t *testing.T) {
	uc := NewUpdateRuleUseCase(kachomock.NewRepository(), repomock.NewOpsRepo(), nil)
	_, err := uc.Execute(context.Background(), UpdateRuleInput{SecurityGroupID: "bad"})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())

	// SG-id ok, rule-id пустой → InvalidArgument.
	_, err = uc.Execute(context.Background(), UpdateRuleInput{SecurityGroupID: ids.NewID(ids.PrefixSecurityGroup), RuleID: ""})
	require.Error(t, err)
	st, _ = status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestGetUseCase_InvalidArg(t *testing.T) {
	uc := NewGetSecurityGroupUseCase(kachomock.NewRepository())
	_, err := uc.Execute(context.Background(), "bad-id")
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

// ---- helpers — pure converter functions ----

func TestSGToProto_Fields(t *testing.T) {
	rec := &kacho.SecurityGroupRecord{
		SecurityGroup: domain.SecurityGroup{
			ID:                "sg-1",
			ProjectID:         "f1",
			NetworkID:         "net-1",
			Name:              domain.RcNameVPC("sg"),
			Description:       domain.RcDescription("desc"),
			Labels:            domain.LabelsFromMap(map[string]string{"k": "v"}),
			Status:            domain.SecurityGroupStatusActive,
			DefaultForNetwork: false,
			Rules: []domain.SecurityGroupRule{
				{
					ID:           "r1",
					Direction:    domain.SecurityGroupRuleDirectionIngress,
					Description:  domain.RcDescription("in"),
					ProtocolName: "tcp",
					FromPort:     22, ToPort: 22,
					V4CidrBlocks: []string{"10.0.0.0/24"},
				},
				{
					ID:             "r2",
					Direction:      domain.SecurityGroupRuleDirectionEgress,
					ProtocolNumber: 17,
				},
			},
		},
	}
	p, err := securityGroupToPb(rec)
	require.NoError(t, err)
	assert.Equal(t, "sg-1", p.Id)
	assert.Equal(t, vpcv1.SecurityGroup_ACTIVE, p.Status)
	assert.Len(t, p.Rules, 2)
	assert.Equal(t, vpcv1.SecurityGroupRule_INGRESS, p.Rules[0].Direction)
	require.NotNil(t, p.Rules[0].Ports)
	assert.Equal(t, int64(22), p.Rules[0].Ports.FromPort)
	assert.Nil(t, p.Rules[1].Ports, "ports nil for any")
}

func TestRuleSpecFromProto_Fields(t *testing.T) {
	rs := &vpcv1.SecurityGroupRuleSpec{
		Description: "desc",
		Labels:      map[string]string{"k": "v"},
		Direction:   vpcv1.SecurityGroupRule_INGRESS,
		Ports:       &vpcv1.PortRange{FromPort: 80, ToPort: 80},
		Protocol:    &vpcv1.SecurityGroupRuleSpec_ProtocolName{ProtocolName: "tcp"},
		Target: &vpcv1.SecurityGroupRuleSpec_CidrBlocks{
			CidrBlocks: &vpcv1.CidrBlocks{V4CidrBlocks: []string{"0.0.0.0/0"}},
		},
	}
	r := ruleSpecFromProto(rs)
	assert.Equal(t, domain.SecurityGroupRuleDirectionIngress, r.Direction)
	assert.Equal(t, int64(80), r.FromPort)
	assert.Equal(t, "tcp", r.ProtocolName)
	assert.Equal(t, []string{"0.0.0.0/0"}, r.V4CidrBlocks)
}

func TestRuleSpecFromProto_ProtocolNumber(t *testing.T) {
	rs := &vpcv1.SecurityGroupRuleSpec{
		Direction: vpcv1.SecurityGroupRule_EGRESS,
		Protocol:  &vpcv1.SecurityGroupRuleSpec_ProtocolNumber{ProtocolNumber: 17},
		Target:    &vpcv1.SecurityGroupRuleSpec_SecurityGroupId{SecurityGroupId: "sg-2"},
	}
	r := ruleSpecFromProto(rs)
	assert.Equal(t, domain.SecurityGroupRuleDirectionEgress, r.Direction)
	assert.Equal(t, int64(17), r.ProtocolNumber)
	assert.Equal(t, "sg-2", r.SecurityGroupID)
}

func TestRuleSpecFromProto_Predefined(t *testing.T) {
	rs := &vpcv1.SecurityGroupRuleSpec{
		Direction: vpcv1.SecurityGroupRule_INGRESS,
		Target:    &vpcv1.SecurityGroupRuleSpec_PredefinedTarget{PredefinedTarget: "self_security_group"},
	}
	r := ruleSpecFromProto(rs)
	assert.Equal(t, "self_security_group", r.PredefinedTarget)
}
