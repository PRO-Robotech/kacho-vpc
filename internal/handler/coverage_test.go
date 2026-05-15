package handler

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	svc "github.com/PRO-Robotech/kacho-vpc/internal/service"
)

// Этот файл расширяет handler-тесты, покрывая методы Update/Move/ListOperations/Delete
// для всех публичных ресурсов. Сценарии заимствованы из Postman-коллекции
// (collections/kacho-vpc.postman_collection.json) — case-id'ы NET-*, SUB-*,
// ADR-*, RT-*, SG-*, GW-*. Покрывает sync InvalidArgument paths (наиболее
// частый сценарий валидации до Operation worker'а). Fake-реализации port-ов —
// в `internal/ports/portmock` (shim — в mock_test.go).

// ---- Network handler — additional coverage ----

func TestNetworkHandler_Update_OK(t *testing.T) {
	nr := newMockNetworkRepo()
	or := newMockOpsRepo()
	networkSvc := svc.NewNetworkService(nr, nil, nil, nil, newMockFolderClient(true), or, nil)
	h := NewNetworkHandler(networkSvc)

	createOp, err := h.Create(context.Background(), &vpcv1.CreateNetworkRequest{FolderId: "f1", Name: "n1"})
	require.NoError(t, err)
	awaitOpDone(t, or, createOp.Id)

	resp, err := h.List(context.Background(), &vpcv1.ListNetworksRequest{FolderId: "f1"})
	require.NoError(t, err)
	require.Len(t, resp.Networks, 1)

	updOp, err := h.Update(context.Background(), &vpcv1.UpdateNetworkRequest{
		NetworkId: resp.Networks[0].Id,
		Name:      "n1-upd",
	})
	require.NoError(t, err)
	awaitOpDone(t, or, updOp.Id)
}

func TestNetworkHandler_Update_InvalidArg(t *testing.T) {
	or := newMockOpsRepo()
	h := NewNetworkHandler(svc.NewNetworkService(newMockNetworkRepo(), nil, nil, nil, newMockFolderClient(true), or, nil))
	_, err := h.Update(context.Background(), &vpcv1.UpdateNetworkRequest{NetworkId: ""})
	st, _ := grpcstatus.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestNetworkHandler_Move_Validates(t *testing.T) {
	or := newMockOpsRepo()
	h := NewNetworkHandler(svc.NewNetworkService(newMockNetworkRepo(), nil, nil, nil, newMockFolderClient(true), or, nil))
	_, err := h.Move(context.Background(), &vpcv1.MoveNetworkRequest{NetworkId: ""})
	st, _ := grpcstatus.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestNetworkHandler_ListOperations_RequiresID(t *testing.T) {
	or := newMockOpsRepo()
	h := NewNetworkHandler(svc.NewNetworkService(newMockNetworkRepo(), nil, nil, nil, newMockFolderClient(true), or, nil))
	_, err := h.ListOperations(context.Background(), &vpcv1.ListNetworkOperationsRequest{NetworkId: ""})
	st, _ := grpcstatus.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

// ---- Subnet handler — additional coverage ----

func TestSubnetHandler_Update_InvalidArg(t *testing.T) {
	sr := newMockSubnetRepoForSvc()
	or := newMockOpsRepo()
	h := NewSubnetHandler(svc.NewSubnetService(sr, newMockNetworkRepo(), newMockFolderClient(true), or, nil))
	_, err := h.Update(context.Background(), &vpcv1.UpdateSubnetRequest{SubnetId: ""})
	st, _ := grpcstatus.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestSubnetHandler_ListOperations_RequiresID(t *testing.T) {
	sr := newMockSubnetRepoForSvc()
	or := newMockOpsRepo()
	h := NewSubnetHandler(svc.NewSubnetService(sr, newMockNetworkRepo(), newMockFolderClient(true), or, nil))
	_, err := h.ListOperations(context.Background(), &vpcv1.ListSubnetOperationsRequest{SubnetId: ""})
	st, _ := grpcstatus.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

// ---- Address handler — additional coverage ----

func TestAddressHandler_Update_InvalidArg(t *testing.T) {
	addrSvc, _ := makeAddressService()
	h := NewAddressHandler(addrSvc, nil)
	_, err := h.Update(context.Background(), &vpcv1.UpdateAddressRequest{AddressId: ""})
	st, _ := grpcstatus.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestAddressHandler_ListOperations_RequiresID(t *testing.T) {
	addrSvc, _ := makeAddressService()
	h := NewAddressHandler(addrSvc, nil)
	_, err := h.ListOperations(context.Background(), &vpcv1.ListAddressOperationsRequest{AddressId: ""})
	st, _ := grpcstatus.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

// ---- RouteTable handler — additional coverage ----

func TestRouteTableHandler_Update_InvalidArg(t *testing.T) {
	rtr := newMockRouteTableRepoForSvc()
	or := newMockOpsRepo()
	h := NewRouteTableHandler(svc.NewRouteTableService(rtr, newMockNetworkRepo(), newMockFolderClient(true), or))
	_, err := h.Update(context.Background(), &vpcv1.UpdateRouteTableRequest{RouteTableId: ""})
	st, _ := grpcstatus.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestRouteTableHandler_ListOperations_RequiresID(t *testing.T) {
	rtr := newMockRouteTableRepoForSvc()
	or := newMockOpsRepo()
	h := NewRouteTableHandler(svc.NewRouteTableService(rtr, newMockNetworkRepo(), newMockFolderClient(true), or))
	_, err := h.ListOperations(context.Background(), &vpcv1.ListRouteTableOperationsRequest{RouteTableId: ""})
	st, _ := grpcstatus.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

// ---- SecurityGroup handler ----

func makeSGService(t *testing.T) (*svc.SecurityGroupService, *mockOpsRepo) {
	t.Helper()
	or := newMockOpsRepo()
	return svc.NewSecurityGroupService(newMockSGRepoForSvc(), newMockNetworkRepo(), newMockFolderClient(true), or), or
}

func TestSecurityGroupHandler_Get_InvalidArg(t *testing.T) {
	sgSvc, _ := makeSGService(t)
	h := NewSecurityGroupHandler(sgSvc)
	_, err := h.Get(context.Background(), &vpcv1.GetSecurityGroupRequest{SecurityGroupId: ""})
	st, _ := grpcstatus.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestSecurityGroupHandler_Get_NotFound(t *testing.T) {
	sgSvc, _ := makeSGService(t)
	h := NewSecurityGroupHandler(sgSvc)
	_, err := h.Get(context.Background(), &vpcv1.GetSecurityGroupRequest{SecurityGroupId: ids.NewID(ids.PrefixSecurityGroup)})
	st, _ := grpcstatus.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestSecurityGroupHandler_List_Empty(t *testing.T) {
	sgSvc, _ := makeSGService(t)
	h := NewSecurityGroupHandler(sgSvc)
	resp, err := h.List(context.Background(), &vpcv1.ListSecurityGroupsRequest{FolderId: "f1"})
	require.NoError(t, err)
	assert.Empty(t, resp.SecurityGroups)
}

func TestSecurityGroupHandler_Create_Validates(t *testing.T) {
	sgSvc, _ := makeSGService(t)
	h := NewSecurityGroupHandler(sgSvc)
	_, err := h.Create(context.Background(), &vpcv1.CreateSecurityGroupRequest{Name: "sg"})
	st, _ := grpcstatus.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestSecurityGroupHandler_Update_InvalidArg(t *testing.T) {
	sgSvc, _ := makeSGService(t)
	h := NewSecurityGroupHandler(sgSvc)
	_, err := h.Update(context.Background(), &vpcv1.UpdateSecurityGroupRequest{SecurityGroupId: ""})
	st, _ := grpcstatus.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestSecurityGroupHandler_Delete_InvalidArg(t *testing.T) {
	sgSvc, _ := makeSGService(t)
	h := NewSecurityGroupHandler(sgSvc)
	_, err := h.Delete(context.Background(), &vpcv1.DeleteSecurityGroupRequest{SecurityGroupId: ""})
	st, _ := grpcstatus.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestSecurityGroupHandler_ListOperations_RequiresID(t *testing.T) {
	sgSvc, _ := makeSGService(t)
	h := NewSecurityGroupHandler(sgSvc)
	_, err := h.ListOperations(context.Background(), &vpcv1.ListSecurityGroupOperationsRequest{SecurityGroupId: ""})
	st, _ := grpcstatus.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

// ---- Gateway handler ----

func makeGatewayService(t *testing.T) (*svc.GatewayService, *mockOpsRepo) {
	t.Helper()
	or := newMockOpsRepo()
	return svc.NewGatewayService(newMockGatewayRepoForSvc(), newMockFolderClient(true), or), or
}

func TestGatewayHandler_Get_InvalidArg(t *testing.T) {
	gwSvc, _ := makeGatewayService(t)
	h := NewGatewayHandler(gwSvc)
	_, err := h.Get(context.Background(), &vpcv1.GetGatewayRequest{GatewayId: ""})
	st, _ := grpcstatus.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestGatewayHandler_Get_NotFound(t *testing.T) {
	gwSvc, _ := makeGatewayService(t)
	h := NewGatewayHandler(gwSvc)
	_, err := h.Get(context.Background(), &vpcv1.GetGatewayRequest{GatewayId: ids.NewID(ids.PrefixGateway)})
	st, _ := grpcstatus.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestGatewayHandler_List_Empty(t *testing.T) {
	gwSvc, _ := makeGatewayService(t)
	h := NewGatewayHandler(gwSvc)
	resp, err := h.List(context.Background(), &vpcv1.ListGatewaysRequest{FolderId: "f1"})
	require.NoError(t, err)
	assert.Empty(t, resp.Gateways)
}

func TestGatewayHandler_Create_OK(t *testing.T) {
	gwSvc, or := makeGatewayService(t)
	h := NewGatewayHandler(gwSvc)
	op, err := h.Create(context.Background(), &vpcv1.CreateGatewayRequest{
		FolderId: "f1",
		Name:     "gw1",
		Gateway:  &vpcv1.CreateGatewayRequest_SharedEgressGatewaySpec{SharedEgressGatewaySpec: &vpcv1.SharedEgressGatewaySpec{}},
	})
	require.NoError(t, err)
	assert.NotEmpty(t, op.Id)
	awaitOpDone(t, or, op.Id)
}

func TestGatewayHandler_Update_InvalidArg(t *testing.T) {
	gwSvc, _ := makeGatewayService(t)
	h := NewGatewayHandler(gwSvc)
	_, err := h.Update(context.Background(), &vpcv1.UpdateGatewayRequest{GatewayId: ""})
	st, _ := grpcstatus.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestGatewayHandler_Delete_InvalidArg(t *testing.T) {
	gwSvc, _ := makeGatewayService(t)
	h := NewGatewayHandler(gwSvc)
	_, err := h.Delete(context.Background(), &vpcv1.DeleteGatewayRequest{GatewayId: ""})
	st, _ := grpcstatus.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestGatewayHandler_Move_Path(t *testing.T) {
	gwSvc, _ := makeGatewayService(t)
	h := NewGatewayHandler(gwSvc)
	_, err := h.Move(context.Background(), &vpcv1.MoveGatewayRequest{GatewayId: ""})
	st, _ := grpcstatus.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestGatewayHandler_ListOperations_RequiresID(t *testing.T) {
	gwSvc, _ := makeGatewayService(t)
	h := NewGatewayHandler(gwSvc)
	_, err := h.ListOperations(context.Background(), &vpcv1.ListGatewayOperationsRequest{GatewayId: ""})
	st, _ := grpcstatus.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestGatewayToProto_SharedEgress(t *testing.T) {
	rec := &domain.GatewayRecord{
		Gateway: domain.Gateway{
			ID:          "gw-1",
			FolderID:    "f1",
			Name:        domain.RcNameVPC("gw1"),
			Description: domain.RcDescription("desc"),
			Labels:      domain.LabelsFromMap(map[string]string{"env": "prod"}),
			GatewayType: domain.GatewayTypeSharedEgress,
		},
	}
	p, err := gatewayToPb(rec)
	require.NoError(t, err)
	assert.Equal(t, "gw-1", p.Id)
	assert.NotNil(t, p.GetSharedEgressGateway())
}
