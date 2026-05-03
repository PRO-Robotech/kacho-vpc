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

// ---------- Mock AddressRepo ----------

type MockAddressRepo struct{ mock.Mock }

func (m *MockAddressRepo) GetByUID(ctx context.Context, uid string) (*domain.Address, error) {
	args := m.Called(ctx, uid)
	if v := args.Get(0); v != nil {
		return v.(*domain.Address), args.Error(1)
	}
	return nil, args.Error(1)
}
func (m *MockAddressRepo) GetByFolderAndName(ctx context.Context, folderID, name string) (*domain.Address, error) {
	args := m.Called(ctx, folderID, name)
	if v := args.Get(0); v != nil {
		return v.(*domain.Address), args.Error(1)
	}
	return nil, args.Error(1)
}
func (m *MockAddressRepo) List(ctx context.Context, selectors []service.Selector, page service.Pagination) ([]*domain.Address, string, int64, error) {
	args := m.Called(ctx, selectors, page)
	return args.Get(0).([]*domain.Address), args.String(1), args.Get(2).(int64), args.Error(3)
}
func (m *MockAddressRepo) SnapshotResourceVersion(ctx context.Context) (int64, error) {
	args := m.Called(ctx)
	return args.Get(0).(int64), args.Error(1)
}
func (m *MockAddressRepo) Insert(ctx context.Context, addr *domain.Address) (*domain.Address, error) {
	args := m.Called(ctx, addr)
	if v := args.Get(0); v != nil {
		return v.(*domain.Address), args.Error(1)
	}
	return nil, args.Error(1)
}
func (m *MockAddressRepo) Update(ctx context.Context, addr *domain.Address) (*domain.Address, error) {
	args := m.Called(ctx, addr)
	if v := args.Get(0); v != nil {
		return v.(*domain.Address), args.Error(1)
	}
	return nil, args.Error(1)
}
func (m *MockAddressRepo) UpdateStatus(ctx context.Context, uid, state string) error {
	args := m.Called(ctx, uid, state)
	return args.Error(0)
}
func (m *MockAddressRepo) HardDelete(ctx context.Context, uid string) error {
	args := m.Called(ctx, uid)
	return args.Error(0)
}
func (m *MockAddressRepo) HasDependents(ctx context.Context, uid string) (bool, error) {
	args := m.Called(ctx, uid)
	return args.Bool(0), args.Error(1)
}

// ---------- Tests ----------

// F1: Создание Address с автоматическим выделением IP из 203.0.113.0/24.
func TestAddressService_Upsert_AllocatesIP(t *testing.T) {
	addrRepo := new(MockAddressRepo)
	fc := new(MockFolderClient)
	svc := service.NewAddressService(addrRepo, fc)
	ctx := context.Background()

	folderID := "00000000-0000-0000-0000-000000000020"
	fc.On("Exists", ctx, folderID).Return(true, nil)
	addrRepo.On("GetByFolderAndName", ctx, folderID, "my-addr").Return((*domain.Address)(nil), nil)
	addrRepo.On("Insert", ctx, mock.AnythingOfType("*domain.Address")).Return(&domain.Address{
		UID:           "addr-uid",
		Name:          "my-addr",
		FolderID:      folderID,
		AddressType:   "EXTERNAL",
		State:         "RESERVED",
		AllocatedIPv4: "203.0.113.42",
	}, nil)

	result, err := svc.Upsert(ctx, &domain.Address{
		Name:     "my-addr",
		FolderID: folderID,
	})

	require.NoError(t, err)
	assert.Equal(t, "RESERVED", result.State)
	assert.Equal(t, "EXTERNAL", result.AddressType)
	assert.Contains(t, result.AllocatedIPv4, "203.0.113.")
}

// F2: INTERNAL address_type → INVALID_ARGUMENT.
func TestAddressService_Upsert_InternalType(t *testing.T) {
	addrRepo := new(MockAddressRepo)
	fc := new(MockFolderClient)
	svc := service.NewAddressService(addrRepo, fc)

	_, err := svc.Upsert(context.Background(), &domain.Address{
		Name:        "bad-addr",
		FolderID:    "00000000-0000-0000-0000-000000000020",
		AddressType: "INTERNAL",
	})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

// UpdateStatus: допустимый переход RESERVED→IN_USE.
func TestAddressService_UpdateStatus_ReservedToInUse(t *testing.T) {
	addrRepo := new(MockAddressRepo)
	fc := new(MockFolderClient)
	svc := service.NewAddressService(addrRepo, fc)
	ctx := context.Background()

	uid := "00000000-0000-0000-0000-000000000021"
	addrRepo.On("GetByUID", ctx, uid).Return(&domain.Address{UID: uid, State: "RESERVED"}, nil)
	addrRepo.On("UpdateStatus", ctx, uid, "IN_USE").Return(nil)

	err := svc.UpdateStatus(ctx, uid, "IN_USE")
	require.NoError(t, err)
}

// UpdateStatus: недопустимый переход RESERVED→RELEASED → FAILED_PRECONDITION.
func TestAddressService_UpdateStatus_InvalidTransition(t *testing.T) {
	addrRepo := new(MockAddressRepo)
	fc := new(MockFolderClient)
	svc := service.NewAddressService(addrRepo, fc)
	ctx := context.Background()

	uid := "00000000-0000-0000-0000-000000000022"
	addrRepo.On("GetByUID", ctx, uid).Return(&domain.Address{UID: uid, State: "RESERVED"}, nil)

	err := svc.UpdateStatus(ctx, uid, "RELEASED")

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
}
