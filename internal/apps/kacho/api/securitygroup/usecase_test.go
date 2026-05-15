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
	"github.com/PRO-Robotech/kacho-vpc/internal/ports/portmock"
)

// Тесты SecurityGroup use-case'ов и handler'а. Wave 3 (KAC-94): сюда переехали
// прежние тесты `internal/handler/coverage_test.go::TestSecurityGroupHandler_*`
// и `coverage2_test.go::TestSecurityGroupHandler_*` (теперь — против
// `*securitygroup.Handler`).
//
// Mock-port'ы — переиспользуем `internal/ports/portmock` (уже реализует
// `internal/ports.SecurityGroupRepo`, который ⊇ нашему локальному
// SecurityGroupRepo).

// ---- builders ----

func makeHandler(
	t *testing.T,
	sgr *portmock.SecurityGroupRepo,
	nr *portmock.NetworkRepo,
	or *portmock.OpsRepo,
	fc *portmock.FolderClient,
) *Handler {
	t.Helper()
	create := NewCreateSecurityGroupUseCase(sgr, nr, fc, or)
	update := NewUpdateSecurityGroupUseCase(sgr, or)
	updateRules := NewUpdateRulesUseCase(sgr, or)
	updateRule := NewUpdateRuleUseCase(sgr, or)
	deleteUC := NewDeleteSecurityGroupUseCase(sgr, or)
	move := NewMoveSecurityGroupUseCase(sgr, fc, or)
	get := NewGetSecurityGroupUseCase(sgr)
	list := NewListSecurityGroupsUseCase(sgr)
	listOps := NewListOperationsUseCase(sgr, or)
	return NewHandler(create, update, updateRules, updateRule, deleteUC, move, get, list, listOps)
}

// minimalHandler — wiring с дефолтными mock'ами; folder=true.
func minimalHandler(t *testing.T) (*Handler, *portmock.OpsRepo, *portmock.SecurityGroupRepo) {
	t.Helper()
	sgr := portmock.NewSecurityGroupRepo()
	nr := portmock.NewNetworkRepo()
	or := portmock.NewOpsRepo()
	fc := &portmock.FolderClient{OK: true}
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
	resp, err := h.List(context.Background(), &vpcv1.ListSecurityGroupsRequest{FolderId: "f1"})
	require.NoError(t, err)
	assert.Empty(t, resp.SecurityGroups)
}

func TestHandler_Create_Validates(t *testing.T) {
	// folder_id отсутствует → InvalidArgument.
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
	sgr := portmock.NewSecurityGroupRepo()
	or := portmock.NewOpsRepo()
	uc := NewCreateSecurityGroupUseCase(sgr, portmock.NewNetworkRepo(), &portmock.FolderClient{OK: true}, or)

	// folder_id required.
	_, err := uc.Execute(context.Background(), CreateInput{SecurityGroup: domain.SecurityGroup{Name: "test"}})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())

	// invalid name (digit start, NameVPC permissive но цифра в начале — нет).
	_, err = uc.Execute(context.Background(), CreateInput{SecurityGroup: domain.SecurityGroup{
		FolderID: "f1",
		Name:     domain.RcNameVPC("1bad"),
	}})
	require.Error(t, err)
	st, _ = status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestCreateUseCase_FolderNotFound(t *testing.T) {
	sgr := portmock.NewSecurityGroupRepo()
	or := portmock.NewOpsRepo()
	uc := NewCreateSecurityGroupUseCase(sgr, portmock.NewNetworkRepo(), &portmock.FolderClient{OK: false}, or)

	_, err := uc.Execute(context.Background(), CreateInput{SecurityGroup: domain.SecurityGroup{
		FolderID: "f1",
		Name:     domain.RcNameVPC("sg1"),
	}})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestCreateUseCase_OK_FolderLevel(t *testing.T) {
	// network_id пустой → folder-level / unbound SG.
	sgr := portmock.NewSecurityGroupRepo()
	or := portmock.NewOpsRepo()
	uc := NewCreateSecurityGroupUseCase(sgr, portmock.NewNetworkRepo(), &portmock.FolderClient{OK: true}, or)

	op, err := uc.Execute(context.Background(), CreateInput{SecurityGroup: domain.SecurityGroup{
		FolderID:    "f1",
		Name:        domain.RcNameVPC("sg1"),
		Description: domain.RcDescription("desc"),
	}})
	require.NoError(t, err)
	require.NotEmpty(t, op.ID)

	saved := portmock.AwaitOpDone(t, or, op.ID)
	assert.True(t, saved.Done)
	assert.Nil(t, saved.Error)
}

func TestDeleteUseCase_InvalidArg(t *testing.T) {
	uc := NewDeleteSecurityGroupUseCase(portmock.NewSecurityGroupRepo(), portmock.NewOpsRepo())
	_, err := uc.Execute(context.Background(), "")
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestMoveUseCase_Validates(t *testing.T) {
	uc := NewMoveSecurityGroupUseCase(portmock.NewSecurityGroupRepo(), &portmock.FolderClient{OK: true}, portmock.NewOpsRepo())
	_, err := uc.Execute(context.Background(), "", "f2")
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	_, err = uc.Execute(context.Background(), ids.NewID(ids.PrefixSecurityGroup), "")
	st, _ = status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestListUseCase_RequiresFolder(t *testing.T) {
	uc := NewListSecurityGroupsUseCase(portmock.NewSecurityGroupRepo())
	_, _, err := uc.Execute(context.Background(), SecurityGroupFilter{}, Pagination{})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestUpdateRulesUseCase_InvalidArg(t *testing.T) {
	uc := NewUpdateRulesUseCase(portmock.NewSecurityGroupRepo(), portmock.NewOpsRepo())
	// security_group_id required (resource-id validation).
	_, err := uc.Execute(context.Background(), UpdateRulesInput{SecurityGroupID: "bad"})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestUpdateRuleUseCase_InvalidArg(t *testing.T) {
	uc := NewUpdateRuleUseCase(portmock.NewSecurityGroupRepo(), portmock.NewOpsRepo())
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
	uc := NewGetSecurityGroupUseCase(portmock.NewSecurityGroupRepo())
	_, err := uc.Execute(context.Background(), "bad-id")
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

// ---- helpers — pure converter functions ----

func TestSGToProto_Fields(t *testing.T) {
	rec := &domain.SecurityGroupRecord{
		SecurityGroup: domain.SecurityGroup{
			ID:                "sg-1",
			FolderID:          "f1",
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
