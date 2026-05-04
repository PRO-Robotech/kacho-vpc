package service

import (
	"context"
	"sync"

	"google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/PRO-Robotech/kacho-corelib/operations"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// ---- mock NetworkRepo ----

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
		return nil, ErrNotFound
	}
	return n, nil
}

func (r *mockNetworkRepo) List(_ context.Context, f NetworkFilter, _ Pagination) ([]*domain.Network, string, error) {
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
		return nil, ErrNotFound
	}
	r.data[n.ID] = n
	return n, nil
}

func (r *mockNetworkRepo) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.data[id]; !ok {
		return ErrNotFound
	}
	delete(r.data, id)
	return nil
}

func (r *mockNetworkRepo) SetFolderID(_ context.Context, id, folderID string) (*domain.Network, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n, ok := r.data[id]
	if !ok {
		return nil, ErrNotFound
	}
	n.FolderID = folderID
	return n, nil
}

// ---- mock SubnetRepo ----

type mockSubnetRepo struct {
	mu   sync.Mutex
	data map[string]*domain.Subnet
}

func newMockSubnetRepo() *mockSubnetRepo {
	return &mockSubnetRepo{data: make(map[string]*domain.Subnet)}
}

func (r *mockSubnetRepo) Get(_ context.Context, id string) (*domain.Subnet, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.data[id]
	if !ok {
		return nil, ErrNotFound
	}
	return s, nil
}

func (r *mockSubnetRepo) List(_ context.Context, f SubnetFilter, _ Pagination) ([]*domain.Subnet, string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var result []*domain.Subnet
	for _, s := range r.data {
		if (f.FolderID == "" || s.FolderID == f.FolderID) &&
			(f.NetworkID == "" || s.NetworkID == f.NetworkID) {
			result = append(result, s)
		}
	}
	return result, "", nil
}

func (r *mockSubnetRepo) Insert(_ context.Context, s *domain.Subnet) (*domain.Subnet, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.data[s.ID] = s
	return s, nil
}

func (r *mockSubnetRepo) Update(_ context.Context, s *domain.Subnet) (*domain.Subnet, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.data[s.ID]; !ok {
		return nil, ErrNotFound
	}
	r.data[s.ID] = s
	return s, nil
}

func (r *mockSubnetRepo) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.data[id]; !ok {
		return ErrNotFound
	}
	delete(r.data, id)
	return nil
}

func (r *mockSubnetRepo) SetFolderID(_ context.Context, id, folderID string) (*domain.Subnet, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.data[id]
	if !ok {
		return nil, ErrNotFound
	}
	s.FolderID = folderID
	return s, nil
}

// ---- mock AddressRepo ----

type mockAddressRepo struct {
	mu   sync.Mutex
	data map[string]*domain.Address
}

func newMockAddressRepo() *mockAddressRepo {
	return &mockAddressRepo{data: make(map[string]*domain.Address)}
}

func (r *mockAddressRepo) Get(_ context.Context, id string) (*domain.Address, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	a, ok := r.data[id]
	if !ok {
		return nil, ErrNotFound
	}
	return a, nil
}

func (r *mockAddressRepo) List(_ context.Context, f AddressFilter, _ Pagination) ([]*domain.Address, string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var result []*domain.Address
	for _, a := range r.data {
		if f.FolderID == "" || a.FolderID == f.FolderID {
			result = append(result, a)
		}
	}
	return result, "", nil
}

func (r *mockAddressRepo) Insert(_ context.Context, a *domain.Address) (*domain.Address, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.data[a.ID] = a
	return a, nil
}

func (r *mockAddressRepo) Update(_ context.Context, a *domain.Address) (*domain.Address, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.data[a.ID]; !ok {
		return nil, ErrNotFound
	}
	r.data[a.ID] = a
	return a, nil
}

func (r *mockAddressRepo) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.data[id]; !ok {
		return ErrNotFound
	}
	delete(r.data, id)
	return nil
}

func (r *mockAddressRepo) SetFolderID(_ context.Context, id, folderID string) (*domain.Address, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	a, ok := r.data[id]
	if !ok {
		return nil, ErrNotFound
	}
	a.FolderID = folderID
	return a, nil
}

func (r *mockAddressRepo) ExistsIP(_ context.Context, ip string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, a := range r.data {
		if a.ExternalIpv4 != nil && a.ExternalIpv4.Address == ip {
			return true, nil
		}
		if a.InternalIpv4 != nil && a.InternalIpv4.Address == ip {
			return true, nil
		}
	}
	return false, nil
}

// ---- mock RouteTableRepo ----

type mockRouteTableRepo struct {
	mu   sync.Mutex
	data map[string]*domain.RouteTable
}

func newMockRouteTableRepo() *mockRouteTableRepo {
	return &mockRouteTableRepo{data: make(map[string]*domain.RouteTable)}
}

func (r *mockRouteTableRepo) Get(_ context.Context, id string) (*domain.RouteTable, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rt, ok := r.data[id]
	if !ok {
		return nil, ErrNotFound
	}
	return rt, nil
}

func (r *mockRouteTableRepo) List(_ context.Context, f RouteTableFilter, _ Pagination) ([]*domain.RouteTable, string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var result []*domain.RouteTable
	for _, rt := range r.data {
		if (f.FolderID == "" || rt.FolderID == f.FolderID) &&
			(f.NetworkID == "" || rt.NetworkID == f.NetworkID) {
			result = append(result, rt)
		}
	}
	return result, "", nil
}

func (r *mockRouteTableRepo) Insert(_ context.Context, rt *domain.RouteTable) (*domain.RouteTable, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.data[rt.ID] = rt
	return rt, nil
}

func (r *mockRouteTableRepo) Update(_ context.Context, rt *domain.RouteTable) (*domain.RouteTable, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.data[rt.ID]; !ok {
		return nil, ErrNotFound
	}
	r.data[rt.ID] = rt
	return rt, nil
}

func (r *mockRouteTableRepo) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.data[id]; !ok {
		return ErrNotFound
	}
	delete(r.data, id)
	return nil
}

func (r *mockRouteTableRepo) SetFolderID(_ context.Context, id, folderID string) (*domain.RouteTable, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rt, ok := r.data[id]
	if !ok {
		return nil, ErrNotFound
	}
	rt.FolderID = folderID
	return rt, nil
}

// ---- mock FolderClient ----

type mockFolderClient struct {
	exists bool
}

func (c *mockFolderClient) Exists(_ context.Context, _ string) (bool, error) {
	return c.exists, nil
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
	return op, nil
}

func (r *mockOpsRepo) List(_ context.Context, _ operations.ListFilter) ([]operations.Operation, string, error) {
	return nil, "", nil
}

func (r *mockOpsRepo) MarkDone(_ context.Context, id string, resp *anypb.Any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	op, ok := r.ops[id]
	if !ok {
		return operations.ErrNotFound
	}
	op.Done = true
	op.Response = resp
	return nil
}

func (r *mockOpsRepo) MarkError(_ context.Context, id string, errStatus *status.Status) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	op, ok := r.ops[id]
	if !ok {
		return operations.ErrNotFound
	}
	op.Done = true
	op.Error = errStatus
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
