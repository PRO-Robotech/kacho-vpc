package service

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// Этот файл расширяет service-тесты, покрывая validation paths и happy-path
// Operation envelope для всех ресурсов. Сценарии — из Postman master collection
// (NET-*, SUB-*, ADR-*, RT-*, SG-*, GW-*, PE-*).

// ---- Mock PE repo ----

type mockPERepo struct {
	mu   sync.Mutex
	data map[string]*domain.PrivateEndpoint
}

func newMockPERepo() *mockPERepo {
	return &mockPERepo{data: make(map[string]*domain.PrivateEndpoint)}
}

func (r *mockPERepo) Get(_ context.Context, id string) (*domain.PrivateEndpoint, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.data[id]
	if !ok {
		return nil, ErrNotFound
	}
	return p, nil
}

func (r *mockPERepo) List(_ context.Context, f PrivateEndpointFilter, _ Pagination) ([]*domain.PrivateEndpoint, string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*domain.PrivateEndpoint
	for _, p := range r.data {
		if f.FolderID != "" && p.FolderID != f.FolderID {
			continue
		}
		out = append(out, p)
	}
	return out, "", nil
}

func (r *mockPERepo) Insert(_ context.Context, p *domain.PrivateEndpoint) (*domain.PrivateEndpoint, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.data[p.ID] = p
	return p, nil
}

func (r *mockPERepo) Update(_ context.Context, p *domain.PrivateEndpoint) (*domain.PrivateEndpoint, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.data[p.ID] = p
	return p, nil
}

func (r *mockPERepo) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.data, id)
	return nil
}

// ---- Mock SG repo ----

type mockSGRepo struct {
	mu   sync.Mutex
	data map[string]*domain.SecurityGroup
}

func newMockSGRepo() *mockSGRepo {
	return &mockSGRepo{data: make(map[string]*domain.SecurityGroup)}
}

func (r *mockSGRepo) Get(_ context.Context, id string) (*domain.SecurityGroup, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	sg, ok := r.data[id]
	if !ok {
		return nil, ErrNotFound
	}
	return sg, nil
}

func (r *mockSGRepo) List(_ context.Context, f SecurityGroupFilter, _ Pagination) ([]*domain.SecurityGroup, string, error) {
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

func (r *mockSGRepo) Insert(_ context.Context, sg *domain.SecurityGroup) (*domain.SecurityGroup, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.data[sg.ID] = sg
	return sg, nil
}

func (r *mockSGRepo) Update(_ context.Context, sg *domain.SecurityGroup) (*domain.SecurityGroup, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.data[sg.ID] = sg
	return sg, nil
}

func (r *mockSGRepo) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.data, id)
	return nil
}

func (r *mockSGRepo) UpdateRules(_ context.Context, sgID string, _ []string, _ []domain.SecurityGroupRule) (*domain.SecurityGroup, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	sg, ok := r.data[sgID]
	if !ok {
		return nil, ErrNotFound
	}
	return sg, nil
}

func (r *mockSGRepo) UpdateRule(_ context.Context, sgID, _ string, _ string, _ map[string]string, _ []string) (*domain.SecurityGroup, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	sg, ok := r.data[sgID]
	if !ok {
		return nil, ErrNotFound
	}
	return sg, nil
}

func (r *mockSGRepo) SetFolderID(_ context.Context, id, folderID string) (*domain.SecurityGroup, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	sg, ok := r.data[id]
	if !ok {
		return nil, ErrNotFound
	}
	sg.FolderID = folderID
	return sg, nil
}

// ---- Mock Gateway repo ----

type mockGatewayRepo struct {
	mu   sync.Mutex
	data map[string]*domain.Gateway
}

func newMockGatewayRepo() *mockGatewayRepo {
	return &mockGatewayRepo{data: make(map[string]*domain.Gateway)}
}

func (r *mockGatewayRepo) Get(_ context.Context, id string) (*domain.Gateway, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	g, ok := r.data[id]
	if !ok {
		return nil, ErrNotFound
	}
	return g, nil
}

func (r *mockGatewayRepo) List(_ context.Context, f GatewayFilter, _ Pagination) ([]*domain.Gateway, string, error) {
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

func (r *mockGatewayRepo) Insert(_ context.Context, g *domain.Gateway) (*domain.Gateway, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.data[g.ID] = g
	return g, nil
}

func (r *mockGatewayRepo) Update(_ context.Context, g *domain.Gateway) (*domain.Gateway, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.data[g.ID] = g
	return g, nil
}

func (r *mockGatewayRepo) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.data, id)
	return nil
}

func (r *mockGatewayRepo) SetFolderID(_ context.Context, id, folderID string) (*domain.Gateway, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	g, ok := r.data[id]
	if !ok {
		return nil, ErrNotFound
	}
	g.FolderID = folderID
	return g, nil
}

// ---- NetworkService — extra coverage ----

func TestNetworkService_Move_Validates(t *testing.T) {
	or := newMockOpsRepo()
	svc := NewNetworkService(newMockNetworkRepo(), nil, nil, nil, &mockFolderClient{exists: true}, or)
	_, err := svc.Move(context.Background(), "", "f2")
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	_, err = svc.Move(context.Background(), ids.NewUID(), "")
	st, _ = status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestNetworkService_Delete_OK(t *testing.T) {
	nr := newMockNetworkRepo()
	or := newMockOpsRepo()
	svc := NewNetworkService(nr, nil, nil, nil, &mockFolderClient{exists: true}, or)

	createOp, _ := svc.Create(context.Background(), CreateNetworkReq{FolderID: "f1", Name: "n"})
	awaitOpDone(t, or, createOp.ID)

	nets, _, _ := svc.List(context.Background(), NetworkFilter{FolderID: "f1"}, Pagination{})
	require.Len(t, nets, 1)
	delOp, err := svc.Delete(context.Background(), nets[0].ID)
	require.NoError(t, err)
	saved := awaitOpDone(t, or, delOp.ID)
	assert.True(t, saved.Done)
	assert.Nil(t, saved.Error)
}

func TestNetworkService_ListOperations_NotFound(t *testing.T) {
	or := newMockOpsRepo()
	svc := NewNetworkService(newMockNetworkRepo(), nil, nil, nil, &mockFolderClient{exists: true}, or)
	_, _, err := svc.ListOperations(context.Background(), ids.NewUID(), Pagination{})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestNetworkService_ListSubnets_NetworkNotFound(t *testing.T) {
	or := newMockOpsRepo()
	svc := NewNetworkService(newMockNetworkRepo(), newMockSubnetRepo(), nil, nil, &mockFolderClient{exists: true}, or)
	_, _, err := svc.ListSubnets(context.Background(), ids.NewUID(), Pagination{})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

// ---- SubnetService — extra coverage ----

func TestSubnetService_Create_RequiredCidr(t *testing.T) {
	or := newMockOpsRepo()
	nr := newMockNetworkRepo()
	net := makeNetwork(nr)
	svc := NewSubnetService(newMockSubnetRepo(), nr, &mockFolderClient{exists: true}, or, nil)

	_, err := svc.Create(context.Background(), CreateSubnetReq{
		FolderID:  "f1",
		Name:      "sub1",
		NetworkID: net.ID,
		ZoneID:    "ru-central1-a",
		// V4CidrBlocks empty → InvalidArgument (TODO #2)
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "v4_cidr_blocks")
}

func TestSubnetService_Create_BadCidr(t *testing.T) {
	or := newMockOpsRepo()
	nr := newMockNetworkRepo()
	net := makeNetwork(nr)
	svc := NewSubnetService(newMockSubnetRepo(), nr, &mockFolderClient{exists: true}, or, nil)

	_, err := svc.Create(context.Background(), CreateSubnetReq{
		FolderID: "f1", Name: "sub1", NetworkID: net.ID, ZoneID: "ru-central1-a",
		V4CidrBlocks: []string{"10.0.0.5/24"}, // host-bits != 0
	})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestSubnetService_Move_Validates(t *testing.T) {
	or := newMockOpsRepo()
	svc := NewSubnetService(newMockSubnetRepo(), newMockNetworkRepo(), &mockFolderClient{exists: true}, or, nil)
	_, err := svc.Move(context.Background(), "", "f2")
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	_, err = svc.Move(context.Background(), ids.NewUID(), "")
	st, _ = status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestSubnetService_AddCidrBlocks_Validates(t *testing.T) {
	or := newMockOpsRepo()
	svc := NewSubnetService(newMockSubnetRepo(), newMockNetworkRepo(), &mockFolderClient{exists: true}, or, nil)
	_, err := svc.AddCidrBlocks(context.Background(), "", []string{"10.0.0.0/24"})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	_, err = svc.AddCidrBlocks(context.Background(), ids.NewUID(), nil)
	st, _ = status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	_, err = svc.AddCidrBlocks(context.Background(), ids.NewUID(), []string{"10.0.0.5/24"})
	st, _ = status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestSubnetService_RemoveCidrBlocks_Validates(t *testing.T) {
	or := newMockOpsRepo()
	svc := NewSubnetService(newMockSubnetRepo(), newMockNetworkRepo(), &mockFolderClient{exists: true}, or, nil)
	_, err := svc.RemoveCidrBlocks(context.Background(), "", []string{"10.0.0.0/24"})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	_, err = svc.RemoveCidrBlocks(context.Background(), ids.NewUID(), nil)
	st, _ = status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestSubnetService_Relocate_Validates(t *testing.T) {
	or := newMockOpsRepo()
	zones := newMockZoneRegistry("ru-central1-a", "ru-central1-b")
	svc := NewSubnetService(newMockSubnetRepo(), newMockNetworkRepo(), &mockFolderClient{exists: true}, or, zones)
	_, err := svc.Relocate(context.Background(), "", "ru-central1-b")
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	// Несуществующая zone отвергается с InvalidArgument (existence-check через
	// mockZoneRegistry — заменяет удалённый hardcode whitelist в corelib).
	_, err = svc.Relocate(context.Background(), ids.NewUID(), "invalid-zone")
	st, _ = status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestSubnetService_ListUsedAddresses_RequiresID(t *testing.T) {
	or := newMockOpsRepo()
	svc := NewSubnetService(newMockSubnetRepo(), newMockNetworkRepo(), &mockFolderClient{exists: true}, or, nil)
	_, _, err := svc.ListUsedAddresses(context.Background(), "", Pagination{})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestSubnetService_Delete_RequiresID(t *testing.T) {
	or := newMockOpsRepo()
	svc := NewSubnetService(newMockSubnetRepo(), newMockNetworkRepo(), &mockFolderClient{exists: true}, or, nil)
	_, err := svc.Delete(context.Background(), "")
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

// ---- AddressService — extra coverage ----

func TestAddressService_Move_Validates(t *testing.T) {
	or := newMockOpsRepo()
	svc := NewAddressService(newMockAddressRepo(), newMockSubnetRepo(), &mockFolderClient{exists: true}, or, nil)
	_, err := svc.Move(context.Background(), "", "f2")
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	_, err = svc.Move(context.Background(), ids.NewUID(), "")
	st, _ = status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestAddressService_Delete_RequiresID(t *testing.T) {
	or := newMockOpsRepo()
	svc := NewAddressService(newMockAddressRepo(), newMockSubnetRepo(), &mockFolderClient{exists: true}, or, nil)
	_, err := svc.Delete(context.Background(), "")
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestAddressService_GetByValue_Empty(t *testing.T) {
	or := newMockOpsRepo()
	svc := NewAddressService(newMockAddressRepo(), newMockSubnetRepo(), &mockFolderClient{exists: true}, or, nil)
	_, err := svc.GetByValue(context.Background(), "", "", "")
	require.Error(t, err)
}

func TestAddressService_ListBySubnet_NotFound(t *testing.T) {
	or := newMockOpsRepo()
	svc := NewAddressService(newMockAddressRepo(), newMockSubnetRepo(), &mockFolderClient{exists: true}, or, nil)
	_, _, err := svc.ListBySubnet(context.Background(), ids.NewUID(), Pagination{})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestAddressService_ListOperations_NotFound(t *testing.T) {
	or := newMockOpsRepo()
	svc := NewAddressService(newMockAddressRepo(), newMockSubnetRepo(), &mockFolderClient{exists: true}, or, nil)
	_, _, err := svc.ListOperations(context.Background(), ids.NewUID(), Pagination{})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

// ---- RouteTableService — extra coverage ----

func TestRouteTableService_Move_Validates(t *testing.T) {
	or := newMockOpsRepo()
	svc := NewRouteTableService(newMockRouteTableRepo(), newMockNetworkRepo(), &mockFolderClient{exists: true}, or)
	_, err := svc.Move(context.Background(), "", "f2")
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	_, err = svc.Move(context.Background(), ids.NewUID(), "")
	st, _ = status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestRouteTableService_Delete_RequiresID(t *testing.T) {
	or := newMockOpsRepo()
	svc := NewRouteTableService(newMockRouteTableRepo(), newMockNetworkRepo(), &mockFolderClient{exists: true}, or)
	_, err := svc.Delete(context.Background(), "")
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestRouteTableService_ListOperations_NotFound(t *testing.T) {
	or := newMockOpsRepo()
	svc := NewRouteTableService(newMockRouteTableRepo(), newMockNetworkRepo(), &mockFolderClient{exists: true}, or)
	_, _, err := svc.ListOperations(context.Background(), ids.NewUID(), Pagination{})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

// ---- SecurityGroupService — full coverage of validation paths ----

func TestSecurityGroupService_Create_RequiresFolderAndNetwork(t *testing.T) {
	or := newMockOpsRepo()
	svc := NewSecurityGroupService(newMockSGRepo(), newMockNetworkRepo(), &mockFolderClient{exists: true}, or)
	_, err := svc.Create(context.Background(), CreateSecurityGroupReq{Name: "sg"})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	_, err = svc.Create(context.Background(), CreateSecurityGroupReq{FolderID: "f1", Name: "sg"})
	st, _ = status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestSecurityGroupService_Update_Validates(t *testing.T) {
	or := newMockOpsRepo()
	svc := NewSecurityGroupService(newMockSGRepo(), newMockNetworkRepo(), &mockFolderClient{exists: true}, or)
	_, err := svc.Update(context.Background(), UpdateSecurityGroupReq{})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestSecurityGroupService_UpdateMask_UnknownField(t *testing.T) {
	or := newMockOpsRepo()
	svc := NewSecurityGroupService(newMockSGRepo(), newMockNetworkRepo(), &mockFolderClient{exists: true}, or)
	_, err := svc.Update(context.Background(), UpdateSecurityGroupReq{
		SecurityGroupID: ids.NewUID(),
		UpdateMask:      []string{"unknown_field"},
	})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestSecurityGroupService_UpdateRules_RequiresID(t *testing.T) {
	or := newMockOpsRepo()
	svc := NewSecurityGroupService(newMockSGRepo(), newMockNetworkRepo(), &mockFolderClient{exists: true}, or)
	_, err := svc.UpdateRules(context.Background(), UpdateRulesReq{})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestSecurityGroupService_UpdateRule_RequiresIDs(t *testing.T) {
	or := newMockOpsRepo()
	svc := NewSecurityGroupService(newMockSGRepo(), newMockNetworkRepo(), &mockFolderClient{exists: true}, or)
	_, err := svc.UpdateRule(context.Background(), UpdateRuleReq{})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	_, err = svc.UpdateRule(context.Background(), UpdateRuleReq{SecurityGroupID: ids.NewUID()})
	st, _ = status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestSecurityGroupService_Delete_RequiresID(t *testing.T) {
	or := newMockOpsRepo()
	svc := NewSecurityGroupService(newMockSGRepo(), newMockNetworkRepo(), &mockFolderClient{exists: true}, or)
	_, err := svc.Delete(context.Background(), "")
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestSecurityGroupService_Move_Validates(t *testing.T) {
	or := newMockOpsRepo()
	svc := NewSecurityGroupService(newMockSGRepo(), newMockNetworkRepo(), &mockFolderClient{exists: true}, or)
	_, err := svc.Move(context.Background(), "", "f2")
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	_, err = svc.Move(context.Background(), ids.NewUID(), "")
	st, _ = status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestSecurityGroupService_ListOperations_NotFound(t *testing.T) {
	or := newMockOpsRepo()
	svc := NewSecurityGroupService(newMockSGRepo(), newMockNetworkRepo(), &mockFolderClient{exists: true}, or)
	_, _, err := svc.ListOperations(context.Background(), ids.NewUID(), Pagination{})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

// ---- GatewayService — validation paths ----

func TestGatewayService_Create_Validates(t *testing.T) {
	or := newMockOpsRepo()
	svc := NewGatewayService(newMockGatewayRepo(), &mockFolderClient{exists: true}, or)
	_, err := svc.Create(context.Background(), CreateGatewayReq{Name: "gw"})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestGatewayService_Create_BadName(t *testing.T) {
	or := newMockOpsRepo()
	svc := NewGatewayService(newMockGatewayRepo(), &mockFolderClient{exists: true}, or)
	// NameGateway strict — uppercase отвергается.
	_, err := svc.Create(context.Background(), CreateGatewayReq{FolderID: "f1", Name: "BadName"})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestGatewayService_Create_OK(t *testing.T) {
	or := newMockOpsRepo()
	svc := NewGatewayService(newMockGatewayRepo(), &mockFolderClient{exists: true}, or)
	op, err := svc.Create(context.Background(), CreateGatewayReq{FolderID: "f1", Name: "gw1", GatewayType: "shared_egress"})
	require.NoError(t, err)
	saved := awaitOpDone(t, or, op.ID)
	assert.True(t, saved.Done)
	assert.Nil(t, saved.Error)
}

func TestGatewayService_Update_RequiresID(t *testing.T) {
	or := newMockOpsRepo()
	svc := NewGatewayService(newMockGatewayRepo(), &mockFolderClient{exists: true}, or)
	_, err := svc.Update(context.Background(), UpdateGatewayReq{})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestGatewayService_Delete_RequiresID(t *testing.T) {
	or := newMockOpsRepo()
	svc := NewGatewayService(newMockGatewayRepo(), &mockFolderClient{exists: true}, or)
	_, err := svc.Delete(context.Background(), "")
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestGatewayService_Move_Validates(t *testing.T) {
	or := newMockOpsRepo()
	svc := NewGatewayService(newMockGatewayRepo(), &mockFolderClient{exists: true}, or)
	_, err := svc.Move(context.Background(), "", "f2")
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	_, err = svc.Move(context.Background(), ids.NewUID(), "")
	st, _ = status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestGatewayService_ListOperations_NotFound(t *testing.T) {
	or := newMockOpsRepo()
	svc := NewGatewayService(newMockGatewayRepo(), &mockFolderClient{exists: true}, or)
	_, _, err := svc.ListOperations(context.Background(), ids.NewUID(), Pagination{})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

// ---- validation utilities ----

func TestValidateCIDRPrefix_HappyPath(t *testing.T) {
	require.NoError(t, validateCIDRPrefix("v4_cidr_blocks[0]", "10.0.0.0/24"))
	require.NoError(t, validateCIDRPrefix("v4_cidr_blocks[0]", "192.168.0.0/16"))
}

func TestValidateCIDRPrefix_HostBits(t *testing.T) {
	err := validateCIDRPrefix("v4_cidr_blocks[0]", "10.0.0.5/24")
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestValidateCIDRPrefix_BadFormat(t *testing.T) {
	err := validateCIDRPrefix("v4_cidr_blocks[0]", "not-a-cidr")
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestCheckCIDRDisjoint_NoOverlap(t *testing.T) {
	require.NoError(t, checkCIDRDisjoint([]string{"10.0.0.0/24", "10.1.0.0/24"}))
}

func TestCheckCIDRDisjoint_Overlap(t *testing.T) {
	err := checkCIDRDisjoint([]string{"10.0.0.0/16", "10.0.0.0/24"})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
	assert.Contains(t, st.Message(), "overlap")
}

// ---- Trivial Get/List helpers ----

func TestSecurityGroupService_Get_NotFound(t *testing.T) {
	or := newMockOpsRepo()
	svc := NewSecurityGroupService(newMockSGRepo(), newMockNetworkRepo(), &mockFolderClient{exists: true}, or)
	_, err := svc.Get(context.Background(), ids.NewUID())
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestSecurityGroupService_List_Empty(t *testing.T) {
	or := newMockOpsRepo()
	svc := NewSecurityGroupService(newMockSGRepo(), newMockNetworkRepo(), &mockFolderClient{exists: true}, or)
	sgs, _, err := svc.List(context.Background(), SecurityGroupFilter{FolderID: "f1"}, Pagination{})
	require.NoError(t, err)
	assert.Empty(t, sgs)
}

func TestGatewayService_Get_NotFound(t *testing.T) {
	or := newMockOpsRepo()
	svc := NewGatewayService(newMockGatewayRepo(), &mockFolderClient{exists: true}, or)
	_, err := svc.Get(context.Background(), ids.NewUID())
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestGatewayService_List_Empty(t *testing.T) {
	or := newMockOpsRepo()
	svc := NewGatewayService(newMockGatewayRepo(), &mockFolderClient{exists: true}, or)
	gws, _, err := svc.List(context.Background(), GatewayFilter{FolderID: "f1"}, Pagination{})
	require.NoError(t, err)
	assert.Empty(t, gws)
}

func TestNetworkService_ListSecurityGroups_NotFound(t *testing.T) {
	or := newMockOpsRepo()
	sgSvc := NewSecurityGroupService(newMockSGRepo(), newMockNetworkRepo(), &mockFolderClient{exists: true}, or)
	svc := NewNetworkService(newMockNetworkRepo(), nil, nil, sgSvc, &mockFolderClient{exists: true}, or)
	_, _, err := svc.ListSecurityGroups(context.Background(), ids.NewUID(), Pagination{})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestNetworkService_ListRouteTables_NotFound(t *testing.T) {
	or := newMockOpsRepo()
	svc := NewNetworkService(newMockNetworkRepo(), nil, newMockRouteTableRepo(), nil, &mockFolderClient{exists: true}, or)
	_, _, err := svc.ListRouteTables(context.Background(), ids.NewUID(), Pagination{})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

// ---- Subnet ListOperations + Get (помимо Create/Update) ----

func TestSubnetService_Get_NotFound(t *testing.T) {
	or := newMockOpsRepo()
	svc := NewSubnetService(newMockSubnetRepo(), newMockNetworkRepo(), &mockFolderClient{exists: true}, or, nil)
	_, err := svc.Get(context.Background(), ids.NewUID())
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestSubnetService_List_Empty(t *testing.T) {
	or := newMockOpsRepo()
	svc := NewSubnetService(newMockSubnetRepo(), newMockNetworkRepo(), &mockFolderClient{exists: true}, or, nil)
	subs, _, err := svc.List(context.Background(), SubnetFilter{FolderID: "f1"}, Pagination{})
	require.NoError(t, err)
	assert.Empty(t, subs)
}

func TestSubnetService_ListOperations_NotFound(t *testing.T) {
	or := newMockOpsRepo()
	svc := NewSubnetService(newMockSubnetRepo(), newMockNetworkRepo(), &mockFolderClient{exists: true}, or, nil)
	_, _, err := svc.ListOperations(context.Background(), ids.NewUID(), Pagination{})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

// ---- Address Get ----

func TestAddressService_Get_NotFound(t *testing.T) {
	or := newMockOpsRepo()
	svc := NewAddressService(newMockAddressRepo(), newMockSubnetRepo(), &mockFolderClient{exists: true}, or, nil)
	_, err := svc.Get(context.Background(), ids.NewUID())
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

// ---- RouteTable Get ----

func TestRouteTableService_Get_NotFound(t *testing.T) {
	or := newMockOpsRepo()
	svc := NewRouteTableService(newMockRouteTableRepo(), newMockNetworkRepo(), &mockFolderClient{exists: true}, or)
	_, err := svc.Get(context.Background(), ids.NewUID())
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestRouteTableService_List_Empty(t *testing.T) {
	or := newMockOpsRepo()
	svc := NewRouteTableService(newMockRouteTableRepo(), newMockNetworkRepo(), &mockFolderClient{exists: true}, or)
	rts, _, err := svc.List(context.Background(), RouteTableFilter{FolderID: "f1"}, Pagination{})
	require.NoError(t, err)
	assert.Empty(t, rts)
}

// ---- PrivateEndpointService — full coverage ----

func makePEService() (*PrivateEndpointService, *mockOpsRepo) {
	or := newMockOpsRepo()
	return NewPrivateEndpointService(newMockPERepo(), &mockFolderClient{exists: true}, newMockNetworkRepo(), newMockSubnetRepo(), or), or
}

func TestPrivateEndpointService_Get_NotFound(t *testing.T) {
	svc, _ := makePEService()
	_, err := svc.Get(context.Background(), ids.NewUID())
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestPrivateEndpointService_List_Empty(t *testing.T) {
	svc, _ := makePEService()
	pes, _, err := svc.List(context.Background(), PrivateEndpointFilter{FolderID: "f1"}, Pagination{})
	require.NoError(t, err)
	assert.Empty(t, pes)
}

func TestPrivateEndpointService_Create_Validates(t *testing.T) {
	svc, _ := makePEService()
	_, err := svc.Create(context.Background(), CreatePrivateEndpointReq{Name: "pe"})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestPrivateEndpointService_Create_OK(t *testing.T) {
	or := newMockOpsRepo()
	nr := newMockNetworkRepo()
	net := makeNetwork(nr)
	sr := newMockSubnetRepo()
	sub := makeSubnet(sr, net.ID)
	svc := NewPrivateEndpointService(newMockPERepo(), &mockFolderClient{exists: true}, nr, sr, or)

	op, err := svc.Create(context.Background(), CreatePrivateEndpointReq{
		FolderID:    "f1",
		Name:        "pe1",
		NetworkID:   net.ID,
		SubnetID:    sub.ID,
		ServiceType: "dns",
	})
	require.NoError(t, err)
	saved := awaitOpDone(t, or, op.ID)
	assert.True(t, saved.Done)
	assert.Nil(t, saved.Error)
}

func TestPrivateEndpointService_Update_RequiresID(t *testing.T) {
	svc, _ := makePEService()
	_, err := svc.Update(context.Background(), UpdatePrivateEndpointReq{})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestPrivateEndpointService_Update_BadName(t *testing.T) {
	svc, _ := makePEService()
	_, err := svc.Update(context.Background(), UpdatePrivateEndpointReq{
		PrivateEndpointID: ids.NewUID(),
		Name:              "1bad-starts-with-digit",
		UpdateMask:        []string{"name"},
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestPrivateEndpointService_Update_UnknownMask(t *testing.T) {
	svc, _ := makePEService()
	_, err := svc.Update(context.Background(), UpdatePrivateEndpointReq{
		PrivateEndpointID: ids.NewUID(),
		UpdateMask:        []string{"unknown_field"},
	})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestPrivateEndpointService_Delete_RequiresID(t *testing.T) {
	svc, _ := makePEService()
	_, err := svc.Delete(context.Background(), "")
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestPrivateEndpointService_ListOperations_NotFound(t *testing.T) {
	svc, _ := makePEService()
	_, _, err := svc.ListOperations(context.Background(), ids.NewUID(), Pagination{})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}
