package handler

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-corelib/operations"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	svc "github.com/PRO-Robotech/kacho-vpc/internal/service"
)

// Этот файл расширяет handler-тесты, покрывая методы Update/Move/ListOperations/Delete
// для всех публичных ресурсов. Сценарии заимствованы из Postman-коллекции
// (collections/kacho-vpc.postman_collection.json) — case-id'ы NET-*, SUB-*,
// ADR-*, RT-*, SG-*, GW-*. Покрывает sync InvalidArgument paths (наиболее
// частый сценарий валидации до Operation worker'а).

// ---- minimal mock SG repo ----

type mockSGRepoForSvc struct {
	mu   sync.Mutex
	data map[string]*domain.SecurityGroup
}

func newMockSGRepoForSvc() *mockSGRepoForSvc {
	return &mockSGRepoForSvc{data: make(map[string]*domain.SecurityGroup)}
}

func (r *mockSGRepoForSvc) Get(_ context.Context, id string) (*domain.SecurityGroup, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	sg, ok := r.data[id]
	if !ok {
		return nil, svc.ErrNotFound
	}
	return sg, nil
}

func (r *mockSGRepoForSvc) List(_ context.Context, f svc.SecurityGroupFilter, _ svc.Pagination) ([]*domain.SecurityGroup, string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*domain.SecurityGroup
	for _, sg := range r.data {
		if f.FolderID != "" && sg.FolderID != f.FolderID {
			continue
		}
		if f.NetworkID != "" && sg.NetworkID != f.NetworkID {
			continue
		}
		out = append(out, sg)
	}
	return out, "", nil
}

func (r *mockSGRepoForSvc) Insert(_ context.Context, sg *domain.SecurityGroup) (*domain.SecurityGroup, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.data[sg.ID] = sg
	return sg, nil
}

func (r *mockSGRepoForSvc) Update(_ context.Context, sg *domain.SecurityGroup) (*domain.SecurityGroup, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.data[sg.ID] = sg
	return sg, nil
}

func (r *mockSGRepoForSvc) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.data, id)
	return nil
}

func (r *mockSGRepoForSvc) UpdateRules(_ context.Context, sgID string, _ []string, _ []domain.SecurityGroupRule) (*domain.SecurityGroup, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	sg, ok := r.data[sgID]
	if !ok {
		return nil, svc.ErrNotFound
	}
	return sg, nil
}

func (r *mockSGRepoForSvc) UpdateRule(_ context.Context, sgID, _ string, _ string, _ map[string]string, _ []string) (*domain.SecurityGroup, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	sg, ok := r.data[sgID]
	if !ok {
		return nil, svc.ErrNotFound
	}
	return sg, nil
}

func (r *mockSGRepoForSvc) SetFolderID(_ context.Context, id, folderID string) (*domain.SecurityGroup, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	sg, ok := r.data[id]
	if !ok {
		return nil, svc.ErrNotFound
	}
	sg.FolderID = folderID
	return sg, nil
}

// ---- minimal mock Gateway repo ----

type mockGatewayRepoForSvc struct {
	mu   sync.Mutex
	data map[string]*domain.Gateway
}

func newMockGatewayRepoForSvc() *mockGatewayRepoForSvc {
	return &mockGatewayRepoForSvc{data: make(map[string]*domain.Gateway)}
}

func (r *mockGatewayRepoForSvc) Get(_ context.Context, id string) (*domain.Gateway, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	g, ok := r.data[id]
	if !ok {
		return nil, svc.ErrNotFound
	}
	return g, nil
}

func (r *mockGatewayRepoForSvc) List(_ context.Context, f svc.GatewayFilter, _ svc.Pagination) ([]*domain.Gateway, string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*domain.Gateway
	for _, g := range r.data {
		if f.FolderID != "" && g.FolderID != f.FolderID {
			continue
		}
		out = append(out, g)
	}
	return out, "", nil
}

func (r *mockGatewayRepoForSvc) Insert(_ context.Context, g *domain.Gateway) (*domain.Gateway, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.data[g.ID] = g
	return g, nil
}

func (r *mockGatewayRepoForSvc) Update(_ context.Context, g *domain.Gateway) (*domain.Gateway, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.data[g.ID] = g
	return g, nil
}

func (r *mockGatewayRepoForSvc) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.data, id)
	return nil
}

func (r *mockGatewayRepoForSvc) SetFolderID(_ context.Context, id, folderID string) (*domain.Gateway, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	g, ok := r.data[id]
	if !ok {
		return nil, svc.ErrNotFound
	}
	g.FolderID = folderID
	return g, nil
}

// Compile-time check that interface is satisfied (lazy via constructor).
var _ operations.Repo = (*mockOpsRepo)(nil)

// ---- Network handler — additional coverage ----

func TestNetworkHandler_Update_OK(t *testing.T) {
	nr := newMockNetworkRepo()
	or := newMockOpsRepo()
	networkSvc := svc.NewNetworkService(nr, nil, nil, nil, &mockFolderClient{exists: true}, or)
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
	h := NewNetworkHandler(svc.NewNetworkService(newMockNetworkRepo(), nil, nil, nil, &mockFolderClient{exists: true}, or))
	_, err := h.Update(context.Background(), &vpcv1.UpdateNetworkRequest{NetworkId: ""})
	st, _ := grpcstatus.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestNetworkHandler_Move_Validates(t *testing.T) {
	or := newMockOpsRepo()
	h := NewNetworkHandler(svc.NewNetworkService(newMockNetworkRepo(), nil, nil, nil, &mockFolderClient{exists: true}, or))
	_, err := h.Move(context.Background(), &vpcv1.MoveNetworkRequest{NetworkId: ""})
	st, _ := grpcstatus.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestNetworkHandler_ListOperations_RequiresID(t *testing.T) {
	or := newMockOpsRepo()
	h := NewNetworkHandler(svc.NewNetworkService(newMockNetworkRepo(), nil, nil, nil, &mockFolderClient{exists: true}, or))
	_, err := h.ListOperations(context.Background(), &vpcv1.ListNetworkOperationsRequest{NetworkId: ""})
	st, _ := grpcstatus.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

// ---- Subnet handler — additional coverage ----

func TestSubnetHandler_Update_InvalidArg(t *testing.T) {
	sr := newMockSubnetRepoForSvc()
	or := newMockOpsRepo()
	h := NewSubnetHandler(svc.NewSubnetService(sr, newMockNetworkRepo(), &mockFolderClient{exists: true}, or))
	_, err := h.Update(context.Background(), &vpcv1.UpdateSubnetRequest{SubnetId: ""})
	st, _ := grpcstatus.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestSubnetHandler_ListOperations_RequiresID(t *testing.T) {
	sr := newMockSubnetRepoForSvc()
	or := newMockOpsRepo()
	h := NewSubnetHandler(svc.NewSubnetService(sr, newMockNetworkRepo(), &mockFolderClient{exists: true}, or))
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
	h := NewRouteTableHandler(svc.NewRouteTableService(rtr, newMockNetworkRepo(), &mockFolderClient{exists: true}, or))
	_, err := h.Update(context.Background(), &vpcv1.UpdateRouteTableRequest{RouteTableId: ""})
	st, _ := grpcstatus.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestRouteTableHandler_ListOperations_RequiresID(t *testing.T) {
	rtr := newMockRouteTableRepoForSvc()
	or := newMockOpsRepo()
	h := NewRouteTableHandler(svc.NewRouteTableService(rtr, newMockNetworkRepo(), &mockFolderClient{exists: true}, or))
	_, err := h.ListOperations(context.Background(), &vpcv1.ListRouteTableOperationsRequest{RouteTableId: ""})
	st, _ := grpcstatus.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

// ---- SecurityGroup handler ----

func makeSGService(t *testing.T) (*svc.SecurityGroupService, *mockOpsRepo) {
	t.Helper()
	or := newMockOpsRepo()
	return svc.NewSecurityGroupService(newMockSGRepoForSvc(), newMockNetworkRepo(), &mockFolderClient{exists: true}, or), or
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
	_, err := h.Get(context.Background(), &vpcv1.GetSecurityGroupRequest{SecurityGroupId: ids.NewUID()})
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
	return svc.NewGatewayService(newMockGatewayRepoForSvc(), &mockFolderClient{exists: true}, or), or
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
	_, err := h.Get(context.Background(), &vpcv1.GetGatewayRequest{GatewayId: ids.NewUID()})
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
	g := &domain.Gateway{
		ID:          "gw-1",
		FolderID:    "f1",
		Name:        "gw1",
		Description: "desc",
		Labels:      map[string]string{"env": "prod"},
		GatewayType: "shared_egress",
	}
	p := gatewayToProto(g)
	assert.Equal(t, "gw-1", p.Id)
	assert.NotNil(t, p.GetSharedEgressGateway())
}
