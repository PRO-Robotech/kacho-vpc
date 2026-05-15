package handler

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	fieldmaskpb "google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	pepb "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1/privatelink"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/protoconv"
	svc "github.com/PRO-Robotech/kacho-vpc/internal/service"
)

// Этот файл — happy-path и error-path handler-тесты для остальных методов
// (Move/AddCidrBlocks/RemoveCidrBlocks/Relocate/ListUsedAddresses/UpdateRules/
// UpdateRule/PrivateEndpoint*). Fake-реализации port-ов — в
// `internal/ports/portmock` (shim — в mock_test.go).

// ---- Subnet handler — Move / AddCidrBlocks / RemoveCidrBlocks / Relocate / ListUsedAddresses ----

func TestSubnetHandler_Move_Validates(t *testing.T) {
	sr := newMockSubnetRepoForSvc()
	or := newMockOpsRepo()
	h := NewSubnetHandler(svc.NewSubnetService(sr, newMockNetworkRepo(), newMockFolderClient(true), or, nil))
	_, err := h.Move(context.Background(), &vpcv1.MoveSubnetRequest{SubnetId: ""})
	st, _ := grpcstatus.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestSubnetHandler_AddCidrBlocks_Validates(t *testing.T) {
	sr := newMockSubnetRepoForSvc()
	or := newMockOpsRepo()
	h := NewSubnetHandler(svc.NewSubnetService(sr, newMockNetworkRepo(), newMockFolderClient(true), or, nil))
	_, err := h.AddCidrBlocks(context.Background(), &vpcv1.AddSubnetCidrBlocksRequest{SubnetId: ""})
	st, _ := grpcstatus.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestSubnetHandler_RemoveCidrBlocks_Validates(t *testing.T) {
	sr := newMockSubnetRepoForSvc()
	or := newMockOpsRepo()
	h := NewSubnetHandler(svc.NewSubnetService(sr, newMockNetworkRepo(), newMockFolderClient(true), or, nil))
	_, err := h.RemoveCidrBlocks(context.Background(), &vpcv1.RemoveSubnetCidrBlocksRequest{SubnetId: ""})
	st, _ := grpcstatus.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestSubnetHandler_Relocate_Validates(t *testing.T) {
	sr := newMockSubnetRepoForSvc()
	or := newMockOpsRepo()
	h := NewSubnetHandler(svc.NewSubnetService(sr, newMockNetworkRepo(), newMockFolderClient(true), or, nil))
	_, err := h.Relocate(context.Background(), &vpcv1.RelocateSubnetRequest{SubnetId: ""})
	st, _ := grpcstatus.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestSubnetHandler_ListUsedAddresses_Validates(t *testing.T) {
	sr := newMockSubnetRepoForSvc()
	or := newMockOpsRepo()
	h := NewSubnetHandler(svc.NewSubnetService(sr, newMockNetworkRepo(), newMockFolderClient(true), or, nil))
	_, err := h.ListUsedAddresses(context.Background(), &vpcv1.ListUsedAddressesRequest{SubnetId: ""})
	st, _ := grpcstatus.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

// ---- Address handler — additional ----

func TestAddressHandler_Move_Validates(t *testing.T) {
	addrSvc, _ := makeAddressService()
	h := NewAddressHandler(addrSvc, nil)
	_, err := h.Move(context.Background(), &vpcv1.MoveAddressRequest{AddressId: ""})
	st, _ := grpcstatus.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestAddressHandler_GetByValue_Empty(t *testing.T) {
	addrSvc, _ := makeAddressService()
	h := NewAddressHandler(addrSvc, nil)
	_, err := h.GetByValue(context.Background(), &vpcv1.GetAddressByValueRequest{})
	require.Error(t, err)
}

func TestAddressHandler_ListBySubnet_NotFound(t *testing.T) {
	addrSvc, _ := makeAddressService()
	h := NewAddressHandler(addrSvc, nil)
	_, err := h.ListBySubnet(context.Background(), &vpcv1.ListAddressesBySubnetRequest{SubnetId: ids.NewID(ids.PrefixSubnet)})
	st, _ := grpcstatus.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

// ---- RouteTable handler — Move ----

func TestRouteTableHandler_Move_Validates(t *testing.T) {
	rtr := newMockRouteTableRepoForSvc()
	or := newMockOpsRepo()
	h := NewRouteTableHandler(svc.NewRouteTableService(rtr, newMockNetworkRepo(), newMockFolderClient(true), or))
	_, err := h.Move(context.Background(), &vpcv1.MoveRouteTableRequest{RouteTableId: ""})
	st, _ := grpcstatus.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

// ---- SecurityGroup handler — UpdateRules / UpdateRule / Move ----

func TestSecurityGroupHandler_UpdateRules_Path(t *testing.T) {
	sgSvc, _ := makeSGService(t)
	h := NewSecurityGroupHandler(sgSvc)
	_, err := h.UpdateRules(context.Background(), &vpcv1.UpdateSecurityGroupRulesRequest{SecurityGroupId: ""})
	st, _ := grpcstatus.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestSecurityGroupHandler_UpdateRule_Path(t *testing.T) {
	sgSvc, _ := makeSGService(t)
	h := NewSecurityGroupHandler(sgSvc)
	_, err := h.UpdateRule(context.Background(), &vpcv1.UpdateSecurityGroupRuleRequest{SecurityGroupId: ""})
	st, _ := grpcstatus.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestSecurityGroupHandler_Move_RequiresID(t *testing.T) {
	sgSvc, _ := makeSGService(t)
	h := NewSecurityGroupHandler(sgSvc)
	_, err := h.Move(context.Background(), &vpcv1.MoveSecurityGroupRequest{SecurityGroupId: ""})
	st, _ := grpcstatus.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestSGToProto_Fields(t *testing.T) {
	sg := &domain.SecurityGroup{
		ID:                "sg-1",
		FolderID:          "f1",
		NetworkID:         "net-1",
		Name:              "sg",
		Description:       "desc",
		Labels:            map[string]string{"k": "v"},
		Status:            "ACTIVE",
		DefaultForNetwork: false,
		Rules: []domain.SecurityGroupRule{
			{
				ID: "r1", Direction: "INGRESS", Description: "in",
				ProtocolName: "tcp",
				FromPort:     22, ToPort: 22,
				V4CidrBlocks: []string{"10.0.0.0/24"},
			},
			{
				ID: "r2", Direction: "EGRESS",
				ProtocolNumber: 17,
				// Ports nil (any) — round-trip.
			},
		},
	}
	p := protoconv.SecurityGroup(sg)
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
	assert.Equal(t, "INGRESS", r.Direction)
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
	assert.Equal(t, "EGRESS", r.Direction)
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

// ---- PrivateEndpoint handler — full coverage ----

func makePEService(t *testing.T) *svc.PrivateEndpointService {
	t.Helper()
	or := newMockOpsRepo()
	return svc.NewPrivateEndpointService(newMockPERepoForSvc(), newMockFolderClient(true), newMockNetworkRepo(), newMockSubnetRepoForSvc(), or)
}

func TestPrivateEndpointHandler_Get_InvalidArg(t *testing.T) {
	h := NewPrivateEndpointHandler(makePEService(t))
	_, err := h.Get(context.Background(), &pepb.GetPrivateEndpointRequest{PrivateEndpointId: ""})
	st, _ := grpcstatus.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestPrivateEndpointHandler_Get_NotFound(t *testing.T) {
	h := NewPrivateEndpointHandler(makePEService(t))
	_, err := h.Get(context.Background(), &pepb.GetPrivateEndpointRequest{PrivateEndpointId: ids.NewID(ids.PrefixPrivateEndpoint)})
	st, _ := grpcstatus.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestPrivateEndpointHandler_List_Empty(t *testing.T) {
	h := NewPrivateEndpointHandler(makePEService(t))
	resp, err := h.List(context.Background(), &pepb.ListPrivateEndpointsRequest{
		Container: &pepb.ListPrivateEndpointsRequest_FolderId{FolderId: "f1"},
	})
	require.NoError(t, err)
	assert.Empty(t, resp.PrivateEndpoints)
}

func TestPrivateEndpointHandler_Create_Validates(t *testing.T) {
	h := NewPrivateEndpointHandler(makePEService(t))
	_, err := h.Create(context.Background(), &pepb.CreatePrivateEndpointRequest{Name: "pe"})
	st, _ := grpcstatus.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestPrivateEndpointHandler_Update_RequiresID(t *testing.T) {
	h := NewPrivateEndpointHandler(makePEService(t))
	_, err := h.Update(context.Background(), &pepb.UpdatePrivateEndpointRequest{PrivateEndpointId: ""})
	st, _ := grpcstatus.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestPrivateEndpointHandler_Delete_InvalidArg(t *testing.T) {
	h := NewPrivateEndpointHandler(makePEService(t))
	_, err := h.Delete(context.Background(), &pepb.DeletePrivateEndpointRequest{PrivateEndpointId: ""})
	st, _ := grpcstatus.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestPrivateEndpointHandler_ListOperations_RequiresID(t *testing.T) {
	h := NewPrivateEndpointHandler(makePEService(t))
	_, err := h.ListOperations(context.Background(), &pepb.ListPrivateEndpointOperationsRequest{PrivateEndpointId: ""})
	st, _ := grpcstatus.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestPrivateEndpointToProto_Fields(t *testing.T) {
	p := &domain.PrivateEndpoint{
		ID:          "pe-1",
		FolderID:    "f1",
		Name:        "pe",
		Description: "desc",
		Labels:      map[string]string{"env": "test"},
		NetworkID:   "net-1",
		SubnetID:    "sub-1",
		IPAddress:   "10.0.0.5",
		AddressID:   "adr-1",
		ServiceType: "object_storage",
		Status:      "AVAILABLE",
		DnsOptions:  map[string]any{"private_dns_records_enabled": true},
	}
	out := protoconv.PrivateEndpoint(p)
	assert.Equal(t, "pe-1", out.Id)
	assert.Equal(t, pepb.PrivateEndpoint_AVAILABLE, out.Status)
	require.NotNil(t, out.Address)
	assert.Equal(t, "10.0.0.5", out.Address.Address)
	require.NotNil(t, out.DnsOptions)
	assert.True(t, out.DnsOptions.PrivateDnsRecordsEnabled)
	assert.NotNil(t, out.GetObjectStorage())
}

func TestPrivateEndpointToProto_StatusMap(t *testing.T) {
	for status, expected := range map[string]pepb.PrivateEndpoint_Status{
		"PENDING":   pepb.PrivateEndpoint_PENDING,
		"AVAILABLE": pepb.PrivateEndpoint_AVAILABLE,
		"DELETING":  pepb.PrivateEndpoint_DELETING,
		"unknown":   pepb.PrivateEndpoint_STATUS_UNSPECIFIED,
	} {
		out := protoconv.PrivateEndpoint(&domain.PrivateEndpoint{Status: status})
		assert.Equal(t, expected, out.Status, "status=%s", status)
	}
}

// ---- pure converter functions ----

// ---- Network handler — happy path Update / List* / ListOperations ----

func TestNetworkHandler_Update_Happy(t *testing.T) {
	nr := newMockNetworkRepo()
	or := newMockOpsRepo()
	networkSvc := svc.NewNetworkService(nr, newMockSubnetRepoForSvc(), newMockRouteTableRepoForSvc(), nil, newMockFolderClient(true), or, nil)
	h := NewNetworkHandler(networkSvc)

	createOp, err := h.Create(context.Background(), &vpcv1.CreateNetworkRequest{FolderId: "f1", Name: "n"})
	require.NoError(t, err)
	awaitOpDone(t, or, createOp.Id)

	resp, _ := h.List(context.Background(), &vpcv1.ListNetworksRequest{FolderId: "f1"})
	require.Len(t, resp.Networks, 1)
	netID := resp.Networks[0].Id

	// Update with mask
	updOp, err := h.Update(context.Background(), &vpcv1.UpdateNetworkRequest{
		NetworkId: netID, Name: "n-upd",
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"name"}},
	})
	require.NoError(t, err)
	awaitOpDone(t, or, updOp.Id)
	got, _ := h.Get(context.Background(), &vpcv1.GetNetworkRequest{NetworkId: netID})
	assert.Equal(t, "n-upd", got.Name)

	// ListSubnets / ListRouteTables / ListSecurityGroups happy
	_, err = h.ListSubnets(context.Background(), &vpcv1.ListNetworkSubnetsRequest{NetworkId: netID})
	require.NoError(t, err)
	_, err = h.ListRouteTables(context.Background(), &vpcv1.ListNetworkRouteTablesRequest{NetworkId: netID})
	require.NoError(t, err)
	_, err = h.ListOperations(context.Background(), &vpcv1.ListNetworkOperationsRequest{NetworkId: netID})
	require.NoError(t, err)

	// Move (worker validates)
	moveOp, err := h.Move(context.Background(), &vpcv1.MoveNetworkRequest{NetworkId: netID, DestinationFolderId: ids.NewID(ids.PrefixFolder)})
	require.NoError(t, err)
	awaitOpDone(t, or, moveOp.Id)

	// Delete
	delOp, err := h.Delete(context.Background(), &vpcv1.DeleteNetworkRequest{NetworkId: netID})
	require.NoError(t, err)
	awaitOpDone(t, or, delOp.Id)
}

// ---- Subnet handler — full happy-path ----

func TestSubnetHandler_FullFlow(t *testing.T) {
	sr := newMockSubnetRepoForSvc()
	nr := newMockNetworkRepo()
	or := newMockOpsRepo()

	netID := ids.NewID(ids.PrefixNetwork)
	_, _ = nr.Insert(context.Background(), &domain.Network{ID: netID, FolderID: "f1", Name: "net"})

	subnetSvc := svc.NewSubnetService(sr, nr, newMockFolderClient(true), or, nil)
	h := NewSubnetHandler(subnetSvc)

	createOp, err := h.Create(context.Background(), &vpcv1.CreateSubnetRequest{
		FolderId: "f1", Name: "sub", NetworkId: netID,
		ZoneId:       "ru-central1-a",
		V4CidrBlocks: []string{"10.0.0.0/24"},
	})
	require.NoError(t, err)
	awaitOpDone(t, or, createOp.Id)

	resp, _ := h.List(context.Background(), &vpcv1.ListSubnetsRequest{FolderId: "f1"})
	require.Len(t, resp.Subnets, 1)
	subID := resp.Subnets[0].Id

	got, err := h.Get(context.Background(), &vpcv1.GetSubnetRequest{SubnetId: subID})
	require.NoError(t, err)
	assert.Equal(t, subID, got.Id)

	updOp, err := h.Update(context.Background(), &vpcv1.UpdateSubnetRequest{
		SubnetId:   subID,
		Name:       "sub-upd",
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"name"}},
	})
	require.NoError(t, err)
	awaitOpDone(t, or, updOp.Id)

	_, err = h.ListOperations(context.Background(), &vpcv1.ListSubnetOperationsRequest{SubnetId: subID})
	require.NoError(t, err)

	moveOp, err := h.Move(context.Background(), &vpcv1.MoveSubnetRequest{SubnetId: subID, DestinationFolderId: ids.NewID(ids.PrefixFolder)})
	require.NoError(t, err)
	awaitOpDone(t, or, moveOp.Id)

	delOp, err := h.Delete(context.Background(), &vpcv1.DeleteSubnetRequest{SubnetId: subID})
	require.NoError(t, err)
	awaitOpDone(t, or, delOp.Id)
}

// ---- RouteTable handler — full happy-path ----

func TestRouteTableHandler_FullFlow(t *testing.T) {
	rtr := newMockRouteTableRepoForSvc()
	nr := newMockNetworkRepo()
	or := newMockOpsRepo()

	netID := ids.NewID(ids.PrefixNetwork)
	_, _ = nr.Insert(context.Background(), &domain.Network{ID: netID, FolderID: "f1", Name: "net"})

	rtSvc := svc.NewRouteTableService(rtr, nr, newMockFolderClient(true), or)
	h := NewRouteTableHandler(rtSvc)

	createOp, err := h.Create(context.Background(), &vpcv1.CreateRouteTableRequest{
		FolderId: "f1", Name: "rt", NetworkId: netID,
	})
	require.NoError(t, err)
	awaitOpDone(t, or, createOp.Id)

	resp, _ := h.List(context.Background(), &vpcv1.ListRouteTablesRequest{FolderId: "f1"})
	require.Len(t, resp.RouteTables, 1)
	rtID := resp.RouteTables[0].Id

	_, err = h.Get(context.Background(), &vpcv1.GetRouteTableRequest{RouteTableId: rtID})
	require.NoError(t, err)

	updOp, err := h.Update(context.Background(), &vpcv1.UpdateRouteTableRequest{
		RouteTableId: rtID, Name: "rt-upd",
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"name"}},
	})
	require.NoError(t, err)
	awaitOpDone(t, or, updOp.Id)

	_, err = h.ListOperations(context.Background(), &vpcv1.ListRouteTableOperationsRequest{RouteTableId: rtID})
	require.NoError(t, err)

	moveOp, err := h.Move(context.Background(), &vpcv1.MoveRouteTableRequest{RouteTableId: rtID, DestinationFolderId: ids.NewID(ids.PrefixFolder)})
	require.NoError(t, err)
	awaitOpDone(t, or, moveOp.Id)

	delOp, err := h.Delete(context.Background(), &vpcv1.DeleteRouteTableRequest{RouteTableId: rtID})
	require.NoError(t, err)
	awaitOpDone(t, or, delOp.Id)
}

// ---- SecurityGroup handler — full happy-path ----

func TestSecurityGroupHandler_FullFlow(t *testing.T) {
	sgRepo := newMockSGRepoForSvc()
	nr := newMockNetworkRepo()
	or := newMockOpsRepo()

	netID := ids.NewID(ids.PrefixNetwork)
	_, _ = nr.Insert(context.Background(), &domain.Network{ID: netID, FolderID: "f1", Name: "net"})

	sgSvc := svc.NewSecurityGroupService(sgRepo, nr, newMockFolderClient(true), or)
	h := NewSecurityGroupHandler(sgSvc)

	createOp, err := h.Create(context.Background(), &vpcv1.CreateSecurityGroupRequest{
		FolderId: "f1", Name: "sg", NetworkId: netID,
		RuleSpecs: []*vpcv1.SecurityGroupRuleSpec{
			{
				Direction: vpcv1.SecurityGroupRule_INGRESS,
				Ports:     &vpcv1.PortRange{FromPort: 22, ToPort: 22},
				Protocol:  &vpcv1.SecurityGroupRuleSpec_ProtocolName{ProtocolName: "tcp"},
				Target:    &vpcv1.SecurityGroupRuleSpec_CidrBlocks{CidrBlocks: &vpcv1.CidrBlocks{V4CidrBlocks: []string{"0.0.0.0/0"}}},
			},
		},
	})
	require.NoError(t, err)
	awaitOpDone(t, or, createOp.Id)

	resp, _ := h.List(context.Background(), &vpcv1.ListSecurityGroupsRequest{FolderId: "f1"})
	require.NotEmpty(t, resp.SecurityGroups)
	sgID := resp.SecurityGroups[0].Id

	_, err = h.Get(context.Background(), &vpcv1.GetSecurityGroupRequest{SecurityGroupId: sgID})
	require.NoError(t, err)

	updOp, err := h.Update(context.Background(), &vpcv1.UpdateSecurityGroupRequest{
		SecurityGroupId: sgID, Name: "sg-upd",
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"name"}},
	})
	require.NoError(t, err)
	awaitOpDone(t, or, updOp.Id)

	updRulesOp, err := h.UpdateRules(context.Background(), &vpcv1.UpdateSecurityGroupRulesRequest{
		SecurityGroupId: sgID,
		AdditionRuleSpecs: []*vpcv1.SecurityGroupRuleSpec{
			{
				Direction: vpcv1.SecurityGroupRule_EGRESS,
				Protocol:  &vpcv1.SecurityGroupRuleSpec_ProtocolName{ProtocolName: "tcp"},
				Target:    &vpcv1.SecurityGroupRuleSpec_CidrBlocks{CidrBlocks: &vpcv1.CidrBlocks{V4CidrBlocks: []string{"10.0.0.0/8"}}},
			},
		},
	})
	require.NoError(t, err)
	awaitOpDone(t, or, updRulesOp.Id)

	_, err = h.ListOperations(context.Background(), &vpcv1.ListSecurityGroupOperationsRequest{SecurityGroupId: sgID})
	require.NoError(t, err)

	moveOp, err := h.Move(context.Background(), &vpcv1.MoveSecurityGroupRequest{SecurityGroupId: sgID, DestinationFolderId: ids.NewID(ids.PrefixFolder)})
	require.NoError(t, err)
	awaitOpDone(t, or, moveOp.Id)

	delOp, err := h.Delete(context.Background(), &vpcv1.DeleteSecurityGroupRequest{SecurityGroupId: sgID})
	require.NoError(t, err)
	awaitOpDone(t, or, delOp.Id)
}

// ---- Gateway handler — Move/Delete happy ----

func TestGatewayHandler_MoveDelete(t *testing.T) {
	gwSvc, or := makeGatewayService(t)
	h := NewGatewayHandler(gwSvc)

	createOp, _ := h.Create(context.Background(), &vpcv1.CreateGatewayRequest{
		FolderId: "f1", Name: "gw1",
		Gateway: &vpcv1.CreateGatewayRequest_SharedEgressGatewaySpec{SharedEgressGatewaySpec: &vpcv1.SharedEgressGatewaySpec{}},
	})
	awaitOpDone(t, or, createOp.Id)

	resp, _ := h.List(context.Background(), &vpcv1.ListGatewaysRequest{FolderId: "f1"})
	require.NotEmpty(t, resp.Gateways)
	gwID := resp.Gateways[0].Id

	updOp, _ := h.Update(context.Background(), &vpcv1.UpdateGatewayRequest{
		GatewayId: gwID, Name: "gw-upd",
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"name"}},
	})
	awaitOpDone(t, or, updOp.Id)

	_, _ = h.ListOperations(context.Background(), &vpcv1.ListGatewayOperationsRequest{GatewayId: gwID})

	moveOp, _ := h.Move(context.Background(), &vpcv1.MoveGatewayRequest{GatewayId: gwID, DestinationFolderId: ids.NewID(ids.PrefixFolder)})
	awaitOpDone(t, or, moveOp.Id)

	delOp, _ := h.Delete(context.Background(), &vpcv1.DeleteGatewayRequest{GatewayId: gwID})
	awaitOpDone(t, or, delOp.Id)
}

// ---- Address handler — happy-path Update/Move/ListBySubnet ----

func TestAddressHandler_FullFlow(t *testing.T) {
	addrSvc, or := makeAddressService()
	h := NewAddressHandler(addrSvc, nil)

	createOp, _ := h.Create(context.Background(), &vpcv1.CreateAddressRequest{
		FolderId: "f1",
		Name:     "addr1",
		AddressSpec: &vpcv1.CreateAddressRequest_ExternalIpv4AddressSpec{
			ExternalIpv4AddressSpec: &vpcv1.ExternalIpv4AddressSpec{
				Address: "203.0.113.10", ZoneId: "ru-central1-a",
			},
		},
	})
	awaitOpDone(t, or, createOp.Id)

	listResp, _ := h.List(context.Background(), &vpcv1.ListAddressesRequest{FolderId: "f1"})
	require.NotEmpty(t, listResp.Addresses)
	addrID := listResp.Addresses[0].Id

	_, err := h.Get(context.Background(), &vpcv1.GetAddressRequest{AddressId: addrID})
	require.NoError(t, err)

	updOp, _ := h.Update(context.Background(), &vpcv1.UpdateAddressRequest{
		AddressId:  addrID,
		Name:       "addr1-upd",
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"name"}},
	})
	awaitOpDone(t, or, updOp.Id)

	_, err = h.ListOperations(context.Background(), &vpcv1.ListAddressOperationsRequest{AddressId: addrID})
	require.NoError(t, err)

	moveOp, _ := h.Move(context.Background(), &vpcv1.MoveAddressRequest{AddressId: addrID, DestinationFolderId: ids.NewID(ids.PrefixFolder)})
	awaitOpDone(t, or, moveOp.Id)

	delOp, _ := h.Delete(context.Background(), &vpcv1.DeleteAddressRequest{AddressId: addrID})
	awaitOpDone(t, or, delOp.Id)
}

func TestSubnetToProto_Fields(t *testing.T) {
	// Wave 2 batch A (KAC-94): Subnet → DTO type2pb. Тест проверяет тот же
	// контракт что и раньше, но через `subnetToPb` (DTO-реестр).
	rec := &domain.SubnetRecord{
		Subnet: domain.Subnet{
			ID:           "sub-1",
			FolderID:     "f1",
			Name:         domain.RcNameVPC("sub"),
			NetworkID:    "net-1",
			ZoneID:       "ru-central1-a",
			V4CidrBlocks: []string{"10.0.0.0/24"},
			DhcpOptions: &domain.DhcpOptions{
				DomainName:        "example.com",
				DomainNameServers: []string{"8.8.8.8"},
				NtpServers:        []string{"1.1.1.1"},
			},
		},
	}
	p, err := subnetToPb(rec)
	require.NoError(t, err)
	assert.Equal(t, "sub-1", p.Id)
	assert.Equal(t, "ru-central1-a", p.ZoneId)
	require.NotNil(t, p.DhcpOptions)
	assert.Equal(t, "example.com", p.DhcpOptions.DomainName)
}
