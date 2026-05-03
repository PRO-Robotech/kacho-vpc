package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/service"
)

// ---------- Mock SubnetRepo ----------

type MockSubnetRepo struct{ mock.Mock }

func (m *MockSubnetRepo) GetByUID(ctx context.Context, uid string) (*domain.Subnet, error) {
	args := m.Called(ctx, uid)
	if v := args.Get(0); v != nil {
		return v.(*domain.Subnet), args.Error(1)
	}
	return nil, args.Error(1)
}
func (m *MockSubnetRepo) GetByFolderAndName(ctx context.Context, folderID, name string) (*domain.Subnet, error) {
	args := m.Called(ctx, folderID, name)
	if v := args.Get(0); v != nil {
		return v.(*domain.Subnet), args.Error(1)
	}
	return nil, args.Error(1)
}
func (m *MockSubnetRepo) List(ctx context.Context, selectors []service.Selector, page service.Pagination) ([]*domain.Subnet, string, int64, error) {
	args := m.Called(ctx, selectors, page)
	return args.Get(0).([]*domain.Subnet), args.String(1), args.Get(2).(int64), args.Error(3)
}
func (m *MockSubnetRepo) SnapshotResourceVersion(ctx context.Context) (int64, error) {
	args := m.Called(ctx)
	return args.Get(0).(int64), args.Error(1)
}
func (m *MockSubnetRepo) Insert(ctx context.Context, subnet *domain.Subnet) (*domain.Subnet, error) {
	args := m.Called(ctx, subnet)
	if v := args.Get(0); v != nil {
		return v.(*domain.Subnet), args.Error(1)
	}
	return nil, args.Error(1)
}
func (m *MockSubnetRepo) Update(ctx context.Context, subnet *domain.Subnet) (*domain.Subnet, error) {
	args := m.Called(ctx, subnet)
	if v := args.Get(0); v != nil {
		return v.(*domain.Subnet), args.Error(1)
	}
	return nil, args.Error(1)
}
func (m *MockSubnetRepo) HardDelete(ctx context.Context, uid string) error {
	args := m.Called(ctx, uid)
	return args.Error(0)
}

// ---------- Tests ----------

// C1: Создание Subnet с корректным network_id + folder_id.
func TestSubnetService_Upsert_Create(t *testing.T) {
	subnetRepo := new(MockSubnetRepo)
	netRepo := new(MockNetworkRepo)
	fc := new(MockFolderClient)
	svc := service.NewSubnetService(subnetRepo, netRepo, fc)
	ctx := context.Background()

	folderID := "00000000-0000-0000-0000-000000000010"
	netID := "00000000-0000-0000-0000-000000000011"

	netRepo.On("GetByUID", ctx, netID).Return(&domain.Network{
		UID: netID, CloudID: "cld1", OrganizationID: "org1",
	}, nil)
	fc.On("Exists", ctx, folderID).Return(true, nil)
	subnetRepo.On("GetByFolderAndName", ctx, folderID, "test-subnet").Return((*domain.Subnet)(nil), nil)
	subnetRepo.On("Insert", ctx, mock.AnythingOfType("*domain.Subnet")).Return(&domain.Subnet{
		UID:       "subnet-uid",
		Name:      "test-subnet",
		FolderID:  folderID,
		NetworkID: netID,
		CIDRBlock: "10.0.0.0/24",
		State:     "ACTIVE",
	}, nil)

	result, err := svc.Upsert(ctx, &domain.Subnet{
		Name:      "test-subnet",
		FolderID:  folderID,
		NetworkID: netID,
		CIDRBlock: "10.0.0.0/24",
	})

	require.NoError(t, err)
	assert.Equal(t, "ACTIVE", result.State)
	assert.Equal(t, "10.0.0.0/24", result.CIDRBlock)
}

// I19: host-bits-set CIDR → INVALID_ARGUMENT.
func TestSubnetService_Upsert_HostBitsSet(t *testing.T) {
	subnetRepo := new(MockSubnetRepo)
	netRepo := new(MockNetworkRepo)
	fc := new(MockFolderClient)
	svc := service.NewSubnetService(subnetRepo, netRepo, fc)
	ctx := context.Background()

	_, err := svc.Upsert(ctx, &domain.Subnet{
		Name:      "bad-subnet",
		FolderID:  "00000000-0000-0000-0000-000000000010",
		NetworkID: "00000000-0000-0000-0000-000000000011",
		CIDRBlock: "10.0.0.1/24", // host bits set
	})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

// Невалидный CIDR формат → INVALID_ARGUMENT.
func TestSubnetService_Upsert_InvalidCIDR(t *testing.T) {
	subnetRepo := new(MockSubnetRepo)
	netRepo := new(MockNetworkRepo)
	fc := new(MockFolderClient)
	svc := service.NewSubnetService(subnetRepo, netRepo, fc)

	_, err := svc.Upsert(context.Background(), &domain.Subnet{
		Name:      "bad",
		FolderID:  "00000000-0000-0000-0000-000000000010",
		NetworkID: "00000000-0000-0000-0000-000000000011",
		CIDRBlock: "not-a-cidr",
	})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

// Network не существует → NOT_FOUND.
func TestSubnetService_Upsert_NetworkNotFound(t *testing.T) {
	subnetRepo := new(MockSubnetRepo)
	netRepo := new(MockNetworkRepo)
	fc := new(MockFolderClient)
	svc := service.NewSubnetService(subnetRepo, netRepo, fc)
	ctx := context.Background()

	netID := "00000000-0000-0000-0000-000000000099"
	netRepo.On("GetByUID", ctx, netID).Return((*domain.Network)(nil), nil)

	_, err := svc.Upsert(ctx, &domain.Subnet{
		Name:      "sub",
		FolderID:  "00000000-0000-0000-0000-000000000010",
		NetworkID: netID,
		CIDRBlock: "10.0.0.0/24",
	})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
}
