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

// ---------- Mocks ----------

type MockNetworkRepo struct{ mock.Mock }

func (m *MockNetworkRepo) GetByUID(ctx context.Context, uid string) (*domain.Network, error) {
	args := m.Called(ctx, uid)
	if v := args.Get(0); v != nil {
		return v.(*domain.Network), args.Error(1)
	}
	return nil, args.Error(1)
}
func (m *MockNetworkRepo) GetByFolderAndName(ctx context.Context, folderID, name string) (*domain.Network, error) {
	args := m.Called(ctx, folderID, name)
	if v := args.Get(0); v != nil {
		return v.(*domain.Network), args.Error(1)
	}
	return nil, args.Error(1)
}
func (m *MockNetworkRepo) List(ctx context.Context, selectors []service.Selector, page service.Pagination) ([]*domain.Network, string, int64, error) {
	args := m.Called(ctx, selectors, page)
	return args.Get(0).([]*domain.Network), args.String(1), args.Get(2).(int64), args.Error(3)
}
func (m *MockNetworkRepo) SnapshotResourceVersion(ctx context.Context) (int64, error) {
	args := m.Called(ctx)
	return args.Get(0).(int64), args.Error(1)
}
func (m *MockNetworkRepo) Insert(ctx context.Context, net *domain.Network) (*domain.Network, error) {
	args := m.Called(ctx, net)
	if v := args.Get(0); v != nil {
		return v.(*domain.Network), args.Error(1)
	}
	return nil, args.Error(1)
}
func (m *MockNetworkRepo) Update(ctx context.Context, net *domain.Network) (*domain.Network, error) {
	args := m.Called(ctx, net)
	if v := args.Get(0); v != nil {
		return v.(*domain.Network), args.Error(1)
	}
	return nil, args.Error(1)
}
func (m *MockNetworkRepo) HardDelete(ctx context.Context, uid string) error {
	args := m.Called(ctx, uid)
	return args.Error(0)
}
func (m *MockNetworkRepo) HasDependents(ctx context.Context, uid string) (bool, error) {
	args := m.Called(ctx, uid)
	return args.Bool(0), args.Error(1)
}

type MockFolderClient struct{ mock.Mock }

func (m *MockFolderClient) Exists(ctx context.Context, folderID string) (bool, error) {
	args := m.Called(ctx, folderID)
	return args.Bool(0), args.Error(1)
}

// ---------- Tests ----------

// B1: Создание Network с существующим Folder → CREATED.
func TestNetworkService_Upsert_Create(t *testing.T) {
	netRepo := new(MockNetworkRepo)
	fc := new(MockFolderClient)

	svc := service.NewNetworkService(netRepo, fc)
	ctx := context.Background()

	folderID := "00000000-0000-0000-0000-000000000001"
	fc.On("Exists", ctx, folderID).Return(true, nil)
	netRepo.On("GetByFolderAndName", ctx, folderID, "test-net").Return((*domain.Network)(nil), nil)
	netRepo.On("Insert", ctx, mock.AnythingOfType("*domain.Network")).Return(&domain.Network{
		UID:             "uid-1",
		Name:            "test-net",
		FolderID:        folderID,
		State:           "ACTIVE",
		ResourceVersion: 1,
	}, nil)

	result, err := svc.Upsert(ctx, &domain.Network{
		Name:     "test-net",
		FolderID: folderID,
	})

	require.NoError(t, err)
	assert.NotEmpty(t, result.UID)
	assert.Equal(t, "ACTIVE", result.State)
	fc.AssertExpectations(t)
	netRepo.AssertExpectations(t)
}

// Upsert без folder_id → INVALID_ARGUMENT.
func TestNetworkService_Upsert_MissingFolderID(t *testing.T) {
	netRepo := new(MockNetworkRepo)
	fc := new(MockFolderClient)
	svc := service.NewNetworkService(netRepo, fc)

	_, err := svc.Upsert(context.Background(), &domain.Network{Name: "net"})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

// Upsert с несуществующим Folder → NOT_FOUND.
func TestNetworkService_Upsert_FolderNotFound(t *testing.T) {
	netRepo := new(MockNetworkRepo)
	fc := new(MockFolderClient)
	svc := service.NewNetworkService(netRepo, fc)
	ctx := context.Background()

	folderID := "00000000-0000-0000-0000-000000000002"
	fc.On("Exists", ctx, folderID).Return(false, nil)

	_, err := svc.Upsert(ctx, &domain.Network{Name: "net", FolderID: folderID})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
}

// B5: Delete Network с зависимостями → FAILED_PRECONDITION.
func TestNetworkService_Delete_WithDependents(t *testing.T) {
	netRepo := new(MockNetworkRepo)
	fc := new(MockFolderClient)
	svc := service.NewNetworkService(netRepo, fc)
	ctx := context.Background()

	uid := "00000000-0000-0000-0000-000000000003"
	netRepo.On("GetByUID", ctx, uid).Return(&domain.Network{UID: uid, Name: "net"}, nil)
	netRepo.On("HasDependents", ctx, uid).Return(true, nil)

	err := svc.Delete(ctx, uid)

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
}

// Delete несуществующей Network → NOT_FOUND.
func TestNetworkService_Delete_NotFound(t *testing.T) {
	netRepo := new(MockNetworkRepo)
	fc := new(MockFolderClient)
	svc := service.NewNetworkService(netRepo, fc)
	ctx := context.Background()

	uid := "00000000-0000-0000-0000-000000000004"
	netRepo.On("GetByUID", ctx, uid).Return((*domain.Network)(nil), nil)

	err := svc.Delete(ctx, uid)

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
}
