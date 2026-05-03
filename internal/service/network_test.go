package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/status"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/PRO-Robotech/kacho-corelib/operations"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/service"
)

// ---- Mock: NetworkRepo ----

type mockNetworkRepo struct{ mock.Mock }

func (m *mockNetworkRepo) Get(ctx context.Context, id string) (*domain.Network, error) {
	args := m.Called(ctx, id)
	if v := args.Get(0); v != nil {
		return v.(*domain.Network), args.Error(1)
	}
	return nil, args.Error(1)
}
func (m *mockNetworkRepo) GetByFolderAndName(ctx context.Context, folderID, name string) (*domain.Network, error) {
	args := m.Called(ctx, folderID, name)
	if v := args.Get(0); v != nil {
		return v.(*domain.Network), args.Error(1)
	}
	return nil, args.Error(1)
}
func (m *mockNetworkRepo) List(ctx context.Context, filter service.ListFilter) ([]domain.Network, string, error) {
	args := m.Called(ctx, filter)
	return args.Get(0).([]domain.Network), args.String(1), args.Error(2)
}
func (m *mockNetworkRepo) Create(ctx context.Context, n *domain.Network) error {
	return m.Called(ctx, n).Error(0)
}
func (m *mockNetworkRepo) Update(ctx context.Context, n *domain.Network) error {
	return m.Called(ctx, n).Error(0)
}
func (m *mockNetworkRepo) SoftDelete(ctx context.Context, id string) error {
	return m.Called(ctx, id).Error(0)
}
func (m *mockNetworkRepo) HasDependents(ctx context.Context, id string) (bool, error) {
	args := m.Called(ctx, id)
	return args.Bool(0), args.Error(1)
}

// ---- Mock: OpsRepo ----

type mockOpsRepo struct{ mock.Mock }

func (m *mockOpsRepo) Create(ctx context.Context, op operations.Operation) error {
	return m.Called(ctx, op).Error(0)
}
func (m *mockOpsRepo) Get(ctx context.Context, id string) (*operations.Operation, error) {
	args := m.Called(ctx, id)
	if v := args.Get(0); v != nil {
		return v.(*operations.Operation), args.Error(1)
	}
	return nil, args.Error(1)
}
func (m *mockOpsRepo) List(ctx context.Context, filter operations.ListFilter) ([]operations.Operation, string, error) {
	args := m.Called(ctx, filter)
	return args.Get(0).([]operations.Operation), args.String(1), args.Error(2)
}
func (m *mockOpsRepo) MarkDone(ctx context.Context, id string, response *anypb.Any) error {
	return m.Called(ctx, id, response).Error(0)
}
func (m *mockOpsRepo) MarkError(ctx context.Context, id string, err *status.Status) error {
	return m.Called(ctx, id, err).Error(0)
}
func (m *mockOpsRepo) Cancel(ctx context.Context, id string) error {
	return m.Called(ctx, id).Error(0)
}

// ---- Mock: FolderClient ----

type mockFolderClient struct{ mock.Mock }

func (m *mockFolderClient) Exists(ctx context.Context, folderID string) (bool, error) {
	args := m.Called(ctx, folderID)
	return args.Bool(0), args.Error(1)
}

// ---- Tests ----

// TestNetworkService_Get_OK — Get возвращает Network по ID.
func TestNetworkService_Get_OK(t *testing.T) {
	nr := &mockNetworkRepo{}
	opsRepoMock := &mockOpsRepo{}
	fc := &mockFolderClient{}
	svc := service.NewNetworkService(nr, opsRepoMock, fc)
	ctx := context.Background()

	uid := "00000000-0000-0000-0000-000000000001"
	nr.On("Get", ctx, uid).Return(&domain.Network{ID: uid, Name: "net1"}, nil)

	n, err := svc.Get(ctx, uid)
	require.NoError(t, err)
	assert.Equal(t, uid, n.ID)
	nr.AssertExpectations(t)
}

// TestNetworkService_Get_EmptyID — Get с пустым ID → INVALID_ARGUMENT.
func TestNetworkService_Get_EmptyID(t *testing.T) {
	svc := service.NewNetworkService(&mockNetworkRepo{}, &mockOpsRepo{}, &mockFolderClient{})
	_, err := svc.Get(context.Background(), "")
	require.Error(t, err)
	st, ok := grpcstatus.FromError(err)
	require.True(t, ok)
	assert.Equal(t, grpccodes.InvalidArgument, st.Code())
}

// TestNetworkService_Get_NotFound — Get несуществующей Network → NOT_FOUND.
func TestNetworkService_Get_NotFound(t *testing.T) {
	nr := &mockNetworkRepo{}
	svc := service.NewNetworkService(nr, &mockOpsRepo{}, &mockFolderClient{})
	ctx := context.Background()

	uid := "00000000-0000-0000-0000-000000000002"
	nr.On("Get", ctx, uid).Return((*domain.Network)(nil), nil)

	_, err := svc.Get(ctx, uid)
	require.Error(t, err)
	st, ok := grpcstatus.FromError(err)
	require.True(t, ok)
	assert.Equal(t, grpccodes.NotFound, st.Code())
}

// TestNetworkService_Create_OK — Create возвращает Operation (sync part only).
// Async goroutine не тестируется здесь — для неё нужен integration test.
func TestNetworkService_Create_OK(t *testing.T) {
	nr := &mockNetworkRepo{}
	opsRepoMock := &mockOpsRepo{}
	fc := &mockFolderClient{}
	svc := service.NewNetworkService(nr, opsRepoMock, fc)
	ctx := context.Background()

	folderID := "10000000-0000-0000-0000-000000000001"
	// Разрешаем вызовы из горутины (anyMock, чтобы горутина не падала)
	fc.On("Exists", mock.Anything, folderID).Return(true, nil).Maybe()
	nr.On("Create", mock.Anything, mock.AnythingOfType("*domain.Network")).Return(nil).Maybe()
	nr.On("Get", mock.Anything, mock.AnythingOfType("string")).Return(&domain.Network{
		ID: "new-id", Name: "test-net", CreatedAt: time.Now(),
		Status: domain.NetworkStatusProvisioning,
	}, nil).Maybe()
	nr.On("Update", mock.Anything, mock.AnythingOfType("*domain.Network")).Return(nil).Maybe()
	opsRepoMock.On("Create", ctx, mock.AnythingOfType("operations.Operation")).Return(nil)
	opsRepoMock.On("MarkDone", mock.Anything, mock.AnythingOfType("string"), mock.Anything).Return(nil).Maybe()
	opsRepoMock.On("MarkError", mock.Anything, mock.AnythingOfType("string"), mock.Anything).Return(nil).Maybe()

	op, err := svc.Create(ctx, folderID, "test-net", "desc", nil)
	require.NoError(t, err)
	assert.NotEmpty(t, op.ID)
	assert.False(t, op.Done) // Operation создана, но ещё не завершена
	opsRepoMock.AssertCalled(t, "Create", ctx, mock.AnythingOfType("operations.Operation"))
}

// TestNetworkService_Create_MissingFolderID — Create без folder_id → INVALID_ARGUMENT.
func TestNetworkService_Create_MissingFolderID(t *testing.T) {
	svc := service.NewNetworkService(&mockNetworkRepo{}, &mockOpsRepo{}, &mockFolderClient{})
	_, err := svc.Create(context.Background(), "", "net", "", nil)
	require.Error(t, err)
	st, ok := grpcstatus.FromError(err)
	require.True(t, ok)
	assert.Equal(t, grpccodes.InvalidArgument, st.Code())
}

// TestNetworkService_Create_MissingName — Create без name → INVALID_ARGUMENT.
func TestNetworkService_Create_MissingName(t *testing.T) {
	svc := service.NewNetworkService(&mockNetworkRepo{}, &mockOpsRepo{}, &mockFolderClient{})
	_, err := svc.Create(context.Background(), "10000000-0000-0000-0000-000000000001", "", "", nil)
	require.Error(t, err)
	st, ok := grpcstatus.FromError(err)
	require.True(t, ok)
	assert.Equal(t, grpccodes.InvalidArgument, st.Code())
}

// TestNetworkService_Delete_NotFound — Delete несуществующей Network → NOT_FOUND.
func TestNetworkService_Delete_NotFound(t *testing.T) {
	nr := &mockNetworkRepo{}
	svc := service.NewNetworkService(nr, &mockOpsRepo{}, &mockFolderClient{})
	ctx := context.Background()

	uid := "00000000-0000-0000-0000-000000000003"
	nr.On("Get", ctx, uid).Return((*domain.Network)(nil), nil)

	_, err := svc.Delete(ctx, uid)
	require.Error(t, err)
	st, ok := grpcstatus.FromError(err)
	require.True(t, ok)
	assert.Equal(t, grpccodes.NotFound, st.Code())
}

// TestNetworkService_Delete_OK — Delete возвращает Operation.
func TestNetworkService_Delete_OK(t *testing.T) {
	nr := &mockNetworkRepo{}
	opsRepoMock := &mockOpsRepo{}
	svc := service.NewNetworkService(nr, opsRepoMock, &mockFolderClient{})
	ctx := context.Background()

	uid := "00000000-0000-0000-0000-000000000004"
	nr.On("Get", ctx, uid).Return(&domain.Network{
		ID: uid, Name: "net", CreatedAt: time.Now(),
	}, nil)
	opsRepoMock.On("Create", ctx, mock.AnythingOfType("operations.Operation")).Return(nil)
	// Allow async goroutine calls
	nr.On("HasDependents", mock.Anything, uid).Return(false, nil).Maybe()
	nr.On("SoftDelete", mock.Anything, uid).Return(nil).Maybe()
	opsRepoMock.On("MarkDone", mock.Anything, mock.AnythingOfType("string"), mock.Anything).Return(nil).Maybe()
	opsRepoMock.On("MarkError", mock.Anything, mock.AnythingOfType("string"), mock.Anything).Return(nil).Maybe()

	op, err := svc.Delete(ctx, uid)
	require.NoError(t, err)
	assert.NotEmpty(t, op.ID)
}

// TestNetworkService_Update_ResourceVersionMismatch проверяется только при интеграционном тесте
// (требует реальную БД с записями), поэтому здесь пропускается.
func TestNetworkService_Update_MissingID(t *testing.T) {
	svc := service.NewNetworkService(&mockNetworkRepo{}, &mockOpsRepo{}, &mockFolderClient{})
	_, err := svc.Update(context.Background(), "", "", "net", "", nil, nil)
	require.Error(t, err)
	st, ok := grpcstatus.FromError(err)
	require.True(t, ok)
	assert.Equal(t, grpccodes.InvalidArgument, st.Code())
}
