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
	svc "github.com/PRO-Robotech/kacho-vpc/internal/service"
)

// Этот файл расширяет handler-тесты, покрывая методы Update/Move/ListOperations/Delete
// для всех публичных ресурсов. Сценарии заимствованы из Postman-коллекции
// (collections/kacho-vpc.postman_collection.json) — case-id'ы NET-*, SUB-*,
// ADR-*, RT-*, SG-*, GW-*. Покрывает sync InvalidArgument paths (наиболее
// частый сценарий валидации до Operation worker'а). Fake-реализации port-ов —
// в `internal/ports/portmock` (shim — в mock_test.go).

// ---- Network handler — additional coverage ----
//
// Wave 3a pilot (KAC-94): Network-handler-тесты переехали в
// `internal/apps/kacho/api/network/usecase_test.go` (NetworkHandler удалён,
// Handler теперь живёт в use-case-пакете).

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

// ---- RouteTable handler — moved to internal/apps/kacho/api/routetable/usecase_test.go (Wave 3b) ----

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

// ---- Gateway handler — moved to internal/apps/kacho/api/gateway/usecase_test.go (Wave 3b) ----
