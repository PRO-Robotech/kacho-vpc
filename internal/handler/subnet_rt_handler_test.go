package handler

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	svc "github.com/PRO-Robotech/kacho-vpc/internal/service"
)

// ---- mock repos for subnet/rt tests ----

type mockSubnetRepoForSvc struct {
	mu   sync.Mutex
	data map[string]*domain.Subnet
}

func newMockSubnetRepoForSvc() *mockSubnetRepoForSvc {
	return &mockSubnetRepoForSvc{data: make(map[string]*domain.Subnet)}
}

func (r *mockSubnetRepoForSvc) Get(_ context.Context, id string) (*domain.Subnet, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.data[id]
	if !ok {
		return nil, svc.ErrNotFound
	}
	return s, nil
}
func (r *mockSubnetRepoForSvc) List(_ context.Context, f svc.SubnetFilter, _ svc.Pagination) ([]*domain.Subnet, string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var result []*domain.Subnet
	for _, s := range r.data {
		if f.FolderID == "" || s.FolderID == f.FolderID {
			result = append(result, s)
		}
	}
	return result, "", nil
}
func (r *mockSubnetRepoForSvc) Insert(_ context.Context, s *domain.Subnet) (*domain.Subnet, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.data[s.ID] = s
	return s, nil
}
func (r *mockSubnetRepoForSvc) Update(_ context.Context, s *domain.Subnet) (*domain.Subnet, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.data[s.ID] = s
	return s, nil
}
func (r *mockSubnetRepoForSvc) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.data[id]; !ok {
		return svc.ErrNotFound
	}
	delete(r.data, id)
	return nil
}
func (r *mockSubnetRepoForSvc) SetFolderID(_ context.Context, id, folderID string) (*domain.Subnet, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.data[id]
	if !ok {
		return nil, svc.ErrNotFound
	}
	s.FolderID = folderID
	return s, nil
}
func (r *mockSubnetRepoForSvc) SetCidrBlocks(_ context.Context, id string, v4 []string) (*domain.Subnet, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.data[id]
	if !ok {
		return nil, svc.ErrNotFound
	}
	s.V4CidrBlocks = v4
	return s, nil
}
func (r *mockSubnetRepoForSvc) SetZoneID(_ context.Context, id, zoneID string) (*domain.Subnet, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.data[id]
	if !ok {
		return nil, svc.ErrNotFound
	}
	s.ZoneID = zoneID
	return s, nil
}
func (r *mockSubnetRepoForSvc) AddressesBySubnet(_ context.Context, _ string, _ svc.Pagination) ([]*domain.Address, string, error) {
	return nil, "", nil
}

type mockRouteTableRepoForSvc struct {
	mu   sync.Mutex
	data map[string]*domain.RouteTable
}

func newMockRouteTableRepoForSvc() *mockRouteTableRepoForSvc {
	return &mockRouteTableRepoForSvc{data: make(map[string]*domain.RouteTable)}
}
func (r *mockRouteTableRepoForSvc) Get(_ context.Context, id string) (*domain.RouteTable, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rt, ok := r.data[id]
	if !ok {
		return nil, svc.ErrNotFound
	}
	return rt, nil
}
func (r *mockRouteTableRepoForSvc) List(_ context.Context, f svc.RouteTableFilter, _ svc.Pagination) ([]*domain.RouteTable, string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var result []*domain.RouteTable
	for _, rt := range r.data {
		if f.FolderID == "" || rt.FolderID == f.FolderID {
			result = append(result, rt)
		}
	}
	return result, "", nil
}
func (r *mockRouteTableRepoForSvc) Insert(_ context.Context, rt *domain.RouteTable) (*domain.RouteTable, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.data[rt.ID] = rt
	return rt, nil
}
func (r *mockRouteTableRepoForSvc) Update(_ context.Context, rt *domain.RouteTable) (*domain.RouteTable, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.data[rt.ID] = rt
	return rt, nil
}
func (r *mockRouteTableRepoForSvc) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.data[id]; !ok {
		return svc.ErrNotFound
	}
	delete(r.data, id)
	return nil
}
func (r *mockRouteTableRepoForSvc) SetFolderID(_ context.Context, id, folderID string) (*domain.RouteTable, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rt, ok := r.data[id]
	if !ok {
		return nil, svc.ErrNotFound
	}
	rt.FolderID = folderID
	return rt, nil
}

// ---- Subnet handler tests ----

func TestSubnetHandler_Get_InvalidArg(t *testing.T) {
	sr := newMockSubnetRepoForSvc()
	nr := newMockNetworkRepo()
	or := newMockOpsRepo()
	subnetSvc := svc.NewSubnetService(sr, nr, &mockFolderClient{exists: true}, or)
	h := NewSubnetHandler(subnetSvc)

	_, err := h.Get(context.Background(), &vpcv1.GetSubnetRequest{SubnetId: ""})
	require.Error(t, err)
	st, _ := grpcstatus.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestSubnetHandler_Get_NotFound(t *testing.T) {
	sr := newMockSubnetRepoForSvc()
	nr := newMockNetworkRepo()
	or := newMockOpsRepo()
	subnetSvc := svc.NewSubnetService(sr, nr, &mockFolderClient{exists: true}, or)
	h := NewSubnetHandler(subnetSvc)

	_, err := h.Get(context.Background(), &vpcv1.GetSubnetRequest{SubnetId: ids.NewUID()})
	require.Error(t, err)
	st, _ := grpcstatus.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestSubnetHandler_Create_OK(t *testing.T) {
	sr := newMockSubnetRepoForSvc()
	nr := newMockNetworkRepo()
	or := newMockOpsRepo()
	// Создаём сеть чтобы subnet service нашёл её
	netID := ids.NewUID()
	nr.Insert(context.Background(), &domain.Network{ID: netID, FolderID: "f1", Name: "net"})
	subnetSvc := svc.NewSubnetService(sr, nr, &mockFolderClient{exists: true}, or)
	h := NewSubnetHandler(subnetSvc)

	op, err := h.Create(context.Background(), &vpcv1.CreateSubnetRequest{
		FolderId:     "f1",
		Name:         "sub1",
		NetworkId:    netID,
		ZoneId:       "ru-central1-a",
		V4CidrBlocks: []string{"10.0.0.0/24"},
	})
	require.NoError(t, err)
	assert.NotEmpty(t, op.Id)

	time.Sleep(100 * time.Millisecond)
	saved, _ := or.Get(context.Background(), op.Id)
	assert.True(t, saved.Done)
}

func TestSubnetHandler_List_Empty(t *testing.T) {
	sr := newMockSubnetRepoForSvc()
	nr := newMockNetworkRepo()
	or := newMockOpsRepo()
	subnetSvc := svc.NewSubnetService(sr, nr, &mockFolderClient{exists: true}, or)
	h := NewSubnetHandler(subnetSvc)

	resp, err := h.List(context.Background(), &vpcv1.ListSubnetsRequest{FolderId: "f1"})
	require.NoError(t, err)
	assert.Empty(t, resp.Subnets)
}

func TestSubnetHandler_Delete_InvalidArg(t *testing.T) {
	sr := newMockSubnetRepoForSvc()
	nr := newMockNetworkRepo()
	or := newMockOpsRepo()
	subnetSvc := svc.NewSubnetService(sr, nr, &mockFolderClient{exists: true}, or)
	h := NewSubnetHandler(subnetSvc)

	_, err := h.Delete(context.Background(), &vpcv1.DeleteSubnetRequest{SubnetId: ""})
	require.Error(t, err)
	st, _ := grpcstatus.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

// ---- RouteTable handler tests ----

func TestRouteTableHandler_Get_InvalidArg(t *testing.T) {
	rtr := newMockRouteTableRepoForSvc()
	nr := newMockNetworkRepo()
	or := newMockOpsRepo()
	rtSvc := svc.NewRouteTableService(rtr, nr, &mockFolderClient{exists: true}, or)
	h := NewRouteTableHandler(rtSvc)

	_, err := h.Get(context.Background(), &vpcv1.GetRouteTableRequest{RouteTableId: ""})
	require.Error(t, err)
	st, _ := grpcstatus.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestRouteTableHandler_List_Empty(t *testing.T) {
	rtr := newMockRouteTableRepoForSvc()
	nr := newMockNetworkRepo()
	or := newMockOpsRepo()
	rtSvc := svc.NewRouteTableService(rtr, nr, &mockFolderClient{exists: true}, or)
	h := NewRouteTableHandler(rtSvc)

	resp, err := h.List(context.Background(), &vpcv1.ListRouteTablesRequest{FolderId: "f1"})
	require.NoError(t, err)
	assert.Empty(t, resp.RouteTables)
}

func TestRouteTableHandler_Create_OK(t *testing.T) {
	rtr := newMockRouteTableRepoForSvc()
	nr := newMockNetworkRepo()
	or := newMockOpsRepo()
	netID := ids.NewUID()
	nr.Insert(context.Background(), &domain.Network{ID: netID, FolderID: "f1", Name: "net"})
	rtSvc := svc.NewRouteTableService(rtr, nr, &mockFolderClient{exists: true}, or)
	h := NewRouteTableHandler(rtSvc)

	op, err := h.Create(context.Background(), &vpcv1.CreateRouteTableRequest{
		FolderId:  "f1",
		Name:      "rt1",
		NetworkId: netID,
		StaticRoutes: []*vpcv1.StaticRoute{
			{
				Destination: &vpcv1.StaticRoute_DestinationPrefix{DestinationPrefix: "0.0.0.0/0"},
				NextHop:     &vpcv1.StaticRoute_NextHopAddress{NextHopAddress: "192.168.0.1"},
			},
		},
	})
	require.NoError(t, err)
	assert.NotEmpty(t, op.Id)

	time.Sleep(100 * time.Millisecond)
	saved, _ := or.Get(context.Background(), op.Id)
	assert.True(t, saved.Done)
}

func TestRouteTableHandler_Delete_InvalidArg(t *testing.T) {
	rtr := newMockRouteTableRepoForSvc()
	nr := newMockNetworkRepo()
	or := newMockOpsRepo()
	rtSvc := svc.NewRouteTableService(rtr, nr, &mockFolderClient{exists: true}, or)
	h := NewRouteTableHandler(rtSvc)

	_, err := h.Delete(context.Background(), &vpcv1.DeleteRouteTableRequest{RouteTableId: ""})
	require.Error(t, err)
	st, _ := grpcstatus.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}
