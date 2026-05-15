package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// Happy-path Update сценарии для всех ресурсов — покрывают doUpdate /
// validateXxxUpdate / applyXxxMask paths. Сценарии из Postman master collection
// (NET-UP-NAME, SUB-UP-NAME, etc.).

// ---- Subnet.Update happy-path → triger doUpdate / validateSubnetUpdate / applySubnetMask ----

func TestSubnetService_Update_NameOnly(t *testing.T) {
	or := newMockOpsRepo()
	nr := newMockNetworkRepo()
	net := makeNetwork(nr)
	sr := newMockSubnetRepo()
	svc := NewSubnetService(sr, nr, newMockFolderClient(true), or, nil)

	createOp, _ := svc.Create(context.Background(), CreateSubnetReq{
		FolderID: "f1", Name: "sub1", NetworkID: net.ID, ZoneID: "ru-central1-a",
		V4CidrBlocks: []string{"10.0.0.0/24"},
	})
	awaitOpDone(t, or, createOp.ID)

	subs, _, _ := svc.List(context.Background(), SubnetFilter{FolderID: "f1"}, Pagination{})
	require.Len(t, subs, 1)

	updOp, err := svc.Update(context.Background(), UpdateSubnetReq{
		SubnetID:   subs[0].ID,
		Name:       "sub1-upd",
		UpdateMask: []string{"name"},
	})
	require.NoError(t, err)
	saved := awaitOpDone(t, or, updOp.ID)
	require.Nil(t, saved.Error)

	got, _ := svc.Get(context.Background(), subs[0].ID)
	assert.Equal(t, "sub1-upd", string(got.Name))
}

func TestSubnetService_Update_FullPATCH(t *testing.T) {
	or := newMockOpsRepo()
	nr := newMockNetworkRepo()
	net := makeNetwork(nr)
	sr := newMockSubnetRepo()
	svc := NewSubnetService(sr, nr, newMockFolderClient(true), or, nil)

	createOp, _ := svc.Create(context.Background(), CreateSubnetReq{
		FolderID: "f1", Name: "sub1", NetworkID: net.ID, ZoneID: "ru-central1-a",
		V4CidrBlocks: []string{"10.0.0.0/24"},
	})
	awaitOpDone(t, or, createOp.ID)

	subs, _, _ := svc.List(context.Background(), SubnetFilter{FolderID: "f1"}, Pagination{})
	updOp, err := svc.Update(context.Background(), UpdateSubnetReq{
		SubnetID:    subs[0].ID,
		Name:        "sub-new",
		Description: "new desc",
		Labels:      map[string]string{"env": "prod"},
		// V4CidrBlocks указан в req, но НЕ в mask — должен быть silently ignored.
		V4CidrBlocks: []string{"99.99.99.99/24"},
		// Empty mask → full PATCH applies mutable fields only.
	})
	require.NoError(t, err)
	saved := awaitOpDone(t, or, updOp.ID)
	require.Nil(t, saved.Error)

	got, _ := svc.Get(context.Background(), subs[0].ID)
	assert.Equal(t, "sub-new", string(got.Name))
	assert.Equal(t, "new desc", string(got.Description))
	// Immutable v4_cidr_blocks НЕ обновился.
	assert.Equal(t, []string{"10.0.0.0/24"}, got.V4CidrBlocks)
}

func TestSubnetService_Update_BadName(t *testing.T) {
	or := newMockOpsRepo()
	nr := newMockNetworkRepo()
	net := makeNetwork(nr)
	sr := newMockSubnetRepo()
	svc := NewSubnetService(sr, nr, newMockFolderClient(true), or, nil)

	createOp, _ := svc.Create(context.Background(), CreateSubnetReq{
		FolderID: "f1", Name: "sub1", NetworkID: net.ID, ZoneID: "ru-central1-a",
		V4CidrBlocks: []string{"10.0.0.0/24"},
	})
	awaitOpDone(t, or, createOp.ID)

	subs, _, _ := svc.List(context.Background(), SubnetFilter{FolderID: "f1"}, Pagination{})
	_, err := svc.Update(context.Background(), UpdateSubnetReq{
		SubnetID:   subs[0].ID,
		Name:       "1bad",
		UpdateMask: []string{"name"},
	})
	require.Error(t, err)
}

func TestSubnetService_Update_UnknownMask(t *testing.T) {
	or := newMockOpsRepo()
	svc := NewSubnetService(newMockSubnetRepo(), newMockNetworkRepo(), newMockFolderClient(true), or, nil)
	_, err := svc.Update(context.Background(), UpdateSubnetReq{
		SubnetID:   "sub-X",
		UpdateMask: []string{"unknown_field"},
	})
	require.Error(t, err)
}

// ---- RouteTable.Update — moved to internal/apps/kacho/api/routetable/usecase_test.go (Wave 3b) ----

// ---- Address.Update — moved to internal/apps/kacho/api/address/usecase_test.go (Wave 3, KAC-94) ----
// TestAddressService_Update_FullPATCH покрыт TestHandler_FullFlow +
// TestUpdateUseCase_DeletionProtection в новом пакете.

// ---- SecurityGroup.Update happy-path → triger doUpdate / applyMask ----

func TestSecurityGroupService_Update_NameOnly(t *testing.T) {
	or := newMockOpsRepo()
	nr := newMockNetworkRepo()
	net := makeNetwork(nr)
	svc := NewSecurityGroupService(newMockSGRepo(), nr, newMockFolderClient(true), or)

	createOp, _ := svc.Create(context.Background(), CreateSecurityGroupReq{
		FolderID: "f1", Name: "sg", NetworkID: net.ID,
	})
	awaitOpDone(t, or, createOp.ID)

	sgs, _, _ := svc.List(context.Background(), SecurityGroupFilter{FolderID: "f1"}, Pagination{})
	require.Len(t, sgs, 1)

	updOp, err := svc.Update(context.Background(), UpdateSecurityGroupReq{
		SecurityGroupID: sgs[0].ID,
		Name:            "sg-upd",
		UpdateMask:      []string{"name"},
	})
	require.NoError(t, err)
	saved := awaitOpDone(t, or, updOp.ID)
	require.Nil(t, saved.Error)
}

func TestSecurityGroupService_Update_FullPATCH(t *testing.T) {
	or := newMockOpsRepo()
	nr := newMockNetworkRepo()
	net := makeNetwork(nr)
	svc := NewSecurityGroupService(newMockSGRepo(), nr, newMockFolderClient(true), or)

	createOp, _ := svc.Create(context.Background(), CreateSecurityGroupReq{
		FolderID: "f1", Name: "sg", NetworkID: net.ID,
	})
	awaitOpDone(t, or, createOp.ID)

	sgs, _, _ := svc.List(context.Background(), SecurityGroupFilter{FolderID: "f1"}, Pagination{})
	updOp, err := svc.Update(context.Background(), UpdateSecurityGroupReq{
		SecurityGroupID: sgs[0].ID,
		Name:            "sg-upd",
		Description:     "updated",
		Labels:          map[string]string{"env": "test"},
	})
	require.NoError(t, err)
	saved := awaitOpDone(t, or, updOp.ID)
	require.Nil(t, saved.Error)
}

func TestSecurityGroupService_Update_BadName(t *testing.T) {
	or := newMockOpsRepo()
	svc := NewSecurityGroupService(newMockSGRepo(), newMockNetworkRepo(), newMockFolderClient(true), or)
	_, err := svc.Update(context.Background(), UpdateSecurityGroupReq{
		SecurityGroupID: "sg-X",
		Name:            "1bad",
		UpdateMask:      []string{"name"},
	})
	require.Error(t, err)
}

// ---- Gateway.Update / PrivateEndpoint.Update — moved to internal/apps/kacho/api/{gateway,privateendpoint}/usecase_test.go (Wave 3b) ----

// ---- SG validateSGRule edge cases ----

func TestValidateSGRule_BadDirection(t *testing.T) {
	err := validateSGRule("rule[0]", domain.SecurityGroupRule{Direction: "BIDI"})
	require.Error(t, err)
}

func TestValidateSGRule_BadCIDR(t *testing.T) {
	err := validateSGRule("rule[0]", domain.SecurityGroupRule{
		Direction:    "INGRESS",
		V4CidrBlocks: []string{"not-a-cidr"},
	})
	require.Error(t, err)
}

func TestValidateSGRule_HappyPath(t *testing.T) {
	err := validateSGRule("rule[0]", domain.SecurityGroupRule{
		Direction:    "INGRESS",
		V4CidrBlocks: []string{"10.0.0.0/24"},
	})
	require.NoError(t, err)
}

// ---- pure converter functions in service ----

func TestDomainSGToProto_Conversion(t *testing.T) {
	rec := &domain.SecurityGroupRecord{
		SecurityGroup: domain.SecurityGroup{
			ID:                "sg-1",
			FolderID:          "f",
			NetworkID:         "n",
			Name:              domain.RcNameVPC("sg"),
			Description:       domain.RcDescription("d"),
			Labels:            domain.LabelsFromMap(map[string]string{"k": "v"}),
			Status:            domain.SecurityGroupStatusActive,
			DefaultForNetwork: true,
			Rules: []domain.SecurityGroupRule{
				{ID: "r1", Direction: domain.SecurityGroupRuleDirectionIngress, FromPort: 22, ToPort: 22},
			},
		},
	}
	any, err := marshalSecurityGroupRecord(rec)
	require.NoError(t, err)
	var p vpcv1.SecurityGroup
	require.NoError(t, any.UnmarshalTo(&p))
	assert.Equal(t, "sg-1", p.Id)
	assert.True(t, p.DefaultForNetwork)
	require.Len(t, p.Rules, 1)
}

// TestDomainPrivateEndpointToProto_Conversion — moved to internal/apps/kacho/api/privateendpoint/usecase_test.go (Wave 3b).

func TestAssignRuleIDs_GeneratesIDs(t *testing.T) {
	rules := assignRuleIDs([]domain.SecurityGroupRule{
		{Direction: domain.SecurityGroupRuleDirectionIngress},
		{Direction: domain.SecurityGroupRuleDirectionEgress},
	})
	require.Len(t, rules, 2)
	assert.NotEmpty(t, rules[0].ID)
	assert.NotEmpty(t, rules[1].ID)
	assert.NotEqual(t, rules[0].ID, rules[1].ID)
}
