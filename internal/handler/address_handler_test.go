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

func makeAddressService() (*svc.AddressService, *mockOpsRepo) {
	ar := &mockAddressRepo{data: make(map[string]*domain.Address)}
	sr := &mockSubnetRepo{data: make(map[string]*domain.Subnet)}
	or := newMockOpsRepo()
	return svc.NewAddressService(ar, sr, &mockFolderClient{exists: true}, or), or
}

type mockAddressRepo struct {
	mu   sync.Mutex
	data map[string]*domain.Address
}

func (r *mockAddressRepo) Get(_ context.Context, id string) (*domain.Address, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	a, ok := r.data[id]
	if !ok {
		return nil, svc.ErrNotFound
	}
	return a, nil
}

func (r *mockAddressRepo) List(_ context.Context, f svc.AddressFilter, _ svc.Pagination) ([]*domain.Address, string, error) {
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
		return nil, svc.ErrNotFound
	}
	r.data[a.ID] = a
	return a, nil
}

func (r *mockAddressRepo) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.data[id]; !ok {
		return svc.ErrNotFound
	}
	delete(r.data, id)
	return nil
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

type mockSubnetRepo struct {
	mu   sync.Mutex
	data map[string]*domain.Subnet
}

func (r *mockSubnetRepo) Get(_ context.Context, id string) (*domain.Subnet, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.data[id]
	if !ok {
		return nil, svc.ErrNotFound
	}
	return s, nil
}

func (r *mockSubnetRepo) List(_ context.Context, _ svc.SubnetFilter, _ svc.Pagination) ([]*domain.Subnet, string, error) {
	return nil, "", nil
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
	r.data[s.ID] = s
	return s, nil
}

func (r *mockSubnetRepo) Delete(_ context.Context, id string) error {
	return nil
}

func TestAddressHandler_Get_InvalidArg(t *testing.T) {
	addrSvc, _ := makeAddressService()
	h := NewAddressHandler(addrSvc)

	_, err := h.Get(context.Background(), &vpcv1.GetAddressRequest{AddressId: ""})
	require.Error(t, err)
	st, _ := grpcstatus.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestAddressHandler_Get_NotFound(t *testing.T) {
	addrSvc, _ := makeAddressService()
	h := NewAddressHandler(addrSvc)

	_, err := h.Get(context.Background(), &vpcv1.GetAddressRequest{AddressId: ids.NewUID()})
	require.Error(t, err)
	st, _ := grpcstatus.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestAddressHandler_List_Empty(t *testing.T) {
	addrSvc, _ := makeAddressService()
	h := NewAddressHandler(addrSvc)

	resp, err := h.List(context.Background(), &vpcv1.ListAddressesRequest{FolderId: "f1"})
	require.NoError(t, err)
	assert.Empty(t, resp.Addresses)
}

func TestAddressHandler_Create_External_OK(t *testing.T) {
	addrSvc, or := makeAddressService()
	h := NewAddressHandler(addrSvc)

	op, err := h.Create(context.Background(), &vpcv1.CreateAddressRequest{
		FolderId: "f1",
		Name:     "my-ip",
		AddressSpec: &vpcv1.CreateAddressRequest_ExternalIpv4AddressSpec{
			ExternalIpv4AddressSpec: &vpcv1.ExternalIpv4AddressSpec{
				Address: "203.0.113.20",
				ZoneId:  "ru-central1-a",
			},
		},
	})
	require.NoError(t, err)
	require.NotEmpty(t, op.Id)

	time.Sleep(100 * time.Millisecond)
	saved, _ := or.Get(context.Background(), op.Id)
	assert.True(t, saved.Done)
	assert.Nil(t, saved.Error)
}

func TestAddressHandler_Delete_InvalidArg(t *testing.T) {
	addrSvc, _ := makeAddressService()
	h := NewAddressHandler(addrSvc)

	_, err := h.Delete(context.Background(), &vpcv1.DeleteAddressRequest{AddressId: ""})
	require.Error(t, err)
	st, _ := grpcstatus.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}
