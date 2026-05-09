package handler

import (
	"context"
	"sync"
	"testing"
	"time"

	googlerpcstatus "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-corelib/operations"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	svc "github.com/PRO-Robotech/kacho-vpc/internal/service"
)

// ---- mock NetworkRepo для handler-тестов ----

type mockNetworkRepo struct {
	mu   sync.Mutex
	data map[string]*domain.Network
}

func newMockNetworkRepo() *mockNetworkRepo {
	return &mockNetworkRepo{data: make(map[string]*domain.Network)}
}

func (r *mockNetworkRepo) Get(_ context.Context, id string) (*domain.Network, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n, ok := r.data[id]
	if !ok {
		return nil, svc.ErrNotFound
	}
	return n, nil
}

func (r *mockNetworkRepo) List(_ context.Context, f svc.NetworkFilter, _ svc.Pagination) ([]*domain.Network, string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var result []*domain.Network
	for _, n := range r.data {
		if f.FolderID == "" || n.FolderID == f.FolderID {
			result = append(result, n)
		}
	}
	return result, "", nil
}

func (r *mockNetworkRepo) Insert(_ context.Context, n *domain.Network) (*domain.Network, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.data[n.ID] = n
	return n, nil
}

func (r *mockNetworkRepo) Update(_ context.Context, n *domain.Network) (*domain.Network, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.data[n.ID]; !ok {
		return nil, svc.ErrNotFound
	}
	r.data[n.ID] = n
	return n, nil
}

func (r *mockNetworkRepo) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.data[id]; !ok {
		return svc.ErrNotFound
	}
	delete(r.data, id)
	return nil
}

func (r *mockNetworkRepo) SetFolderID(_ context.Context, id, folderID string) (*domain.Network, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n, ok := r.data[id]
	if !ok {
		return nil, svc.ErrNotFound
	}
	n.FolderID = folderID
	return n, nil
}

// ---- mock OpsRepo ----

type mockOpsRepo struct {
	mu  sync.Mutex
	ops map[string]*operations.Operation
}

func newMockOpsRepo() *mockOpsRepo {
	return &mockOpsRepo{ops: make(map[string]*operations.Operation)}
}

func (r *mockOpsRepo) Create(_ context.Context, op operations.Operation) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ops[op.ID] = &op
	return nil
}

func (r *mockOpsRepo) Get(_ context.Context, id string) (*operations.Operation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	op, ok := r.ops[id]
	if !ok {
		return nil, operations.ErrNotFound
	}
	// Возвращаем shallow-копию — чтобы caller не читал shared-state
	// после Release lock'a (race с MarkDone/MarkError).
	cp := *op
	return &cp, nil
}

func (r *mockOpsRepo) List(_ context.Context, _ operations.ListFilter) ([]operations.Operation, string, error) {
	return nil, "", nil
}

// awaitOpDone — детерминированно ждёт завершения worker-горутины (см. TODO #10
// в kacho-vpc/TODO.md). Падает через 2s.
func awaitOpDone(t testingT, r *mockOpsRepo, opID string) *operations.Operation {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		op, err := r.Get(context.Background(), opID)
		if err == nil && op.Done {
			return op
		}
		if time.Now().After(deadline) {
			t.Fatalf("operation %s did not finish within 2s", opID)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

type testingT interface {
	Helper()
	Fatalf(format string, args ...any)
}

func (r *mockOpsRepo) MarkDone(_ context.Context, id string, resp *anypb.Any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if op, ok := r.ops[id]; ok {
		op.Done = true
		op.Response = resp
	}
	return nil
}

func (r *mockOpsRepo) MarkError(_ context.Context, id string, errStatus *googlerpcstatus.Status) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if op, ok := r.ops[id]; ok {
		op.Done = true
		op.Error = errStatus
	}
	return nil
}

func (r *mockOpsRepo) Cancel(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	op, ok := r.ops[id]
	if !ok {
		return operations.ErrNotFound
	}
	op.Done = true
	return nil
}

// ---- mock FolderClient ----

type mockFolderClient struct{ exists bool }

func (c *mockFolderClient) Exists(_ context.Context, _ string) (bool, error) {
	return c.exists, nil
}

// GetCloudID — mock-stub.
func (c *mockFolderClient) GetCloudID(_ context.Context, _ string) (string, error) {
	return "", nil
}

// ---- tests ----

func TestNetworkHandler_Get_InvalidArg(t *testing.T) {
	nr := newMockNetworkRepo()
	or := newMockOpsRepo()
	networkSvc := svc.NewNetworkService(nr, nil, nil, nil, &mockFolderClient{exists: true}, or)
	h := NewNetworkHandler(networkSvc)

	_, err := h.Get(context.Background(), &vpcv1.GetNetworkRequest{NetworkId: ""})
	require.Error(t, err)
	st, _ := grpcstatus.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestNetworkHandler_Get_NotFound(t *testing.T) {
	nr := newMockNetworkRepo()
	or := newMockOpsRepo()
	networkSvc := svc.NewNetworkService(nr, nil, nil, nil, &mockFolderClient{exists: true}, or)
	h := NewNetworkHandler(networkSvc)

	_, err := h.Get(context.Background(), &vpcv1.GetNetworkRequest{NetworkId: ids.NewUID()})
	require.Error(t, err)
	st, _ := grpcstatus.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestNetworkHandler_Create_OK(t *testing.T) {
	nr := newMockNetworkRepo()
	or := newMockOpsRepo()
	networkSvc := svc.NewNetworkService(nr, nil, nil, nil, &mockFolderClient{exists: true}, or)
	h := NewNetworkHandler(networkSvc)

	op, err := h.Create(context.Background(), &vpcv1.CreateNetworkRequest{
		FolderId: "f1",
		Name:     "net1",
	})
	require.NoError(t, err)
	assert.NotEmpty(t, op.Id)

	saved := awaitOpDone(t, or, op.Id)
	assert.True(t, saved.Done)
}

func TestNetworkHandler_List_Empty(t *testing.T) {
	nr := newMockNetworkRepo()
	or := newMockOpsRepo()
	networkSvc := svc.NewNetworkService(nr, nil, nil, nil, &mockFolderClient{exists: true}, or)
	h := NewNetworkHandler(networkSvc)

	resp, err := h.List(context.Background(), &vpcv1.ListNetworksRequest{FolderId: "f1"})
	require.NoError(t, err)
	assert.Empty(t, resp.Networks)
}

func TestNetworkHandler_Delete_InvalidArg(t *testing.T) {
	nr := newMockNetworkRepo()
	or := newMockOpsRepo()
	networkSvc := svc.NewNetworkService(nr, nil, nil, nil, &mockFolderClient{exists: true}, or)
	h := NewNetworkHandler(networkSvc)

	_, err := h.Delete(context.Background(), &vpcv1.DeleteNetworkRequest{NetworkId: ""})
	require.Error(t, err)
	st, _ := grpcstatus.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestNetworkToProto_Fields(t *testing.T) {
	n := &domain.Network{
		ID:          "net-123",
		FolderID:    "folder-1",
		Name:        "my-net",
		Description: "desc",
		Labels:      map[string]string{"env": "test"},
	}
	p := networkToProto(n)
	assert.Equal(t, "net-123", p.Id)
	assert.Equal(t, "folder-1", p.FolderId)
	assert.Equal(t, "my-net", p.Name)
	assert.Equal(t, "desc", p.Description)
	assert.Equal(t, "test", p.Labels["env"])
}

func TestAddressToProto_External(t *testing.T) {
	a := &domain.Address{
		ID:       "addr-1",
		FolderID: "f1",
		Type:     domain.AddressTypeExternal,
		ExternalIpv4: &domain.ExternalIpv4Spec{
			Address: "203.0.113.10",
			ZoneID:  "ru-central1-a",
		},
	}
	p := addressToProto(a)
	assert.Equal(t, "addr-1", p.Id)
	ext := p.GetExternalIpv4Address()
	require.NotNil(t, ext)
	assert.Equal(t, "203.0.113.10", ext.Address)
	assert.Equal(t, "ru-central1-a", ext.ZoneId)
}

func TestAddressToProto_Internal(t *testing.T) {
	a := &domain.Address{
		ID:       "addr-2",
		FolderID: "f1",
		Type:     domain.AddressTypeInternal,
		InternalIpv4: &domain.InternalIpv4Spec{
			Address:  "10.0.0.5",
			SubnetID: "subnet-1",
		},
	}
	p := addressToProto(a)
	intAddr := p.GetInternalIpv4Address()
	require.NotNil(t, intAddr)
	assert.Equal(t, "10.0.0.5", intAddr.Address)
	assert.Equal(t, "subnet-1", intAddr.GetSubnetId())
}

func TestRouteTableToProto_StaticRoutes(t *testing.T) {
	rt := &domain.RouteTable{
		ID:        "rt-1",
		FolderID:  "f1",
		NetworkID: "net-1",
		StaticRoutes: []domain.StaticRoute{
			{DestinationPrefix: "0.0.0.0/0", NextHopAddress: "192.168.0.1"},
		},
	}
	p := routeTableToProto(rt)
	require.Len(t, p.StaticRoutes, 1)
	assert.Equal(t, "0.0.0.0/0", p.StaticRoutes[0].GetDestinationPrefix())
	assert.Equal(t, "192.168.0.1", p.StaticRoutes[0].GetNextHopAddress())
}

func TestSubnetToProto_CidrBlocks(t *testing.T) {
	s := &domain.Subnet{
		ID:           "sub-1",
		FolderID:     "f1",
		V4CidrBlocks: []string{"10.0.0.0/24", "10.1.0.0/24"},
	}
	p := subnetToProto(s)
	assert.Equal(t, []string{"10.0.0.0/24", "10.1.0.0/24"}, p.V4CidrBlocks)
}
