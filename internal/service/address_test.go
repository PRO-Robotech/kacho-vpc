package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

func makeSubnet(sr *mockSubnetRepo, networkID string) *domain.Subnet {
	s := &domain.Subnet{
		ID:           ids.NewUID(),
		FolderID:     "f1",
		NetworkID:    networkID,
		Name:         "test-subnet",
		V4CidrBlocks: []string{"10.0.0.0/24"},
	}
	_, _ = sr.Insert(context.Background(), s)
	return s
}

func TestAddressService_Create_NoSpec(t *testing.T) {
	ar := newMockAddressRepo()
	sr := newMockSubnetRepo()
	or := newMockOpsRepo()
	svc := NewAddressService(ar, sr, &mockFolderClient{exists: true}, or)

	_, err := svc.Create(context.Background(), CreateAddressReq{FolderID: "f1"})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestAddressService_Create_External_OK(t *testing.T) {
	ar := newMockAddressRepo()
	sr := newMockSubnetRepo()
	or := newMockOpsRepo()
	svc := NewAddressService(ar, sr, &mockFolderClient{exists: true}, or)

	op, err := svc.Create(context.Background(), CreateAddressReq{
		FolderID: "f1",
		Name:     "addr1",
		ExternalSpec: &ExternalAddrSpec{
			Address: "203.0.113.10",
			ZoneID:  "ru-central1-a",
		},
	})
	require.NoError(t, err)
	require.NotEmpty(t, op.ID)

	savedOp := awaitOpDone(t, or, op.ID)
	assert.True(t, savedOp.Done)
	assert.Nil(t, savedOp.Error)

	// Проверяем тип адреса
	addrs, _, err := svc.List(context.Background(), AddressFilter{FolderID: "f1"}, Pagination{})
	require.NoError(t, err)
	require.Len(t, addrs, 1)
	assert.Equal(t, domain.AddressTypeExternal, addrs[0].Type)
	assert.Equal(t, "203.0.113.10", addrs[0].ExternalIpv4.Address)
}

func TestAddressService_Create_External_NoAutoAlloc(t *testing.T) {
	ar := newMockAddressRepo()
	sr := newMockSubnetRepo()
	or := newMockOpsRepo()
	svc := NewAddressService(ar, sr, &mockFolderClient{exists: true}, or)

	// Без явного адреса — service остаётся pure: address.address пуст.
	// Аллокация — задача kacho-vpc-controllers (allocator reconciler-loop).
	// См. POST-PROCESSING-IN-CONTROLLERS.md.
	op, err := svc.Create(context.Background(), CreateAddressReq{
		FolderID:     "f1",
		ExternalSpec: &ExternalAddrSpec{ZoneID: "ru-central1-a"},
	})
	require.NoError(t, err)

	savedOp := awaitOpDone(t, or, op.ID)
	assert.True(t, savedOp.Done)
	assert.Nil(t, savedOp.Error)

	addrs, _, _ := svc.List(context.Background(), AddressFilter{FolderID: "f1"}, Pagination{})
	require.Len(t, addrs, 1)
	assert.Equal(t, "", addrs[0].ExternalIpv4.Address,
		"service must NOT auto-allocate IP — controller's job")
	assert.Equal(t, "ru-central1-a", addrs[0].ExternalIpv4.ZoneID)
}

func TestAddressService_Create_Internal_WithSubnet(t *testing.T) {
	ar := newMockAddressRepo()
	sr := newMockSubnetRepo()
	or := newMockOpsRepo()
	sub := makeSubnet(sr, ids.NewUID())
	svc := NewAddressService(ar, sr, &mockFolderClient{exists: true}, or)

	op, err := svc.Create(context.Background(), CreateAddressReq{
		FolderID: "f1",
		InternalSpec: &InternalAddrSpec{
			SubnetID: sub.ID,
		},
	})
	require.NoError(t, err)

	savedOp := awaitOpDone(t, or, op.ID)
	assert.True(t, savedOp.Done)
	assert.Nil(t, savedOp.Error)

	addrs, _, _ := svc.List(context.Background(), AddressFilter{FolderID: "f1"}, Pagination{})
	require.Len(t, addrs, 1)
	assert.Equal(t, domain.AddressTypeInternal, addrs[0].Type)
	assert.Equal(t, sub.ID, addrs[0].InternalIpv4.SubnetID)
}

// Sync-валидация: explicit IP вне CIDR subnet → InvalidArgument до Operation.
// См. EXPLICIT-IP-CIDR-VALIDATION.md.
func TestAddressService_Create_Internal_ExplicitIP_OutOfCIDR(t *testing.T) {
	ar := newMockAddressRepo()
	sr := newMockSubnetRepo()
	or := newMockOpsRepo()
	sub := makeSubnet(sr, ids.NewUID()) // 10.0.0.0/24
	svc := NewAddressService(ar, sr, &mockFolderClient{exists: true}, or)

	_, err := svc.Create(context.Background(), CreateAddressReq{
		FolderID: "f1",
		InternalSpec: &InternalAddrSpec{
			SubnetID: sub.ID,
			Address:  "192.168.99.100", // вне 10.0.0.0/24
		},
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "10.0.0.0/24")
}

// Sync-валидация: explicit IP внутри CIDR subnet — Operation success.
func TestAddressService_Create_Internal_ExplicitIP_InCIDR(t *testing.T) {
	ar := newMockAddressRepo()
	sr := newMockSubnetRepo()
	or := newMockOpsRepo()
	sub := makeSubnet(sr, ids.NewUID()) // 10.0.0.0/24
	svc := NewAddressService(ar, sr, &mockFolderClient{exists: true}, or)

	op, err := svc.Create(context.Background(), CreateAddressReq{
		FolderID: "f1",
		InternalSpec: &InternalAddrSpec{
			SubnetID: sub.ID,
			Address:  "10.0.0.50",
		},
	})
	require.NoError(t, err)
	savedOp := awaitOpDone(t, or, op.ID)
	assert.True(t, savedOp.Done)
	assert.Nil(t, savedOp.Error)
}

// Если subnet не найден sync-ом — валидация пропускается, NotFound будет
// async через doCreate (verbatim YC, см. YC-DIFF-INVALID-PARENT-CODE.md).
func TestAddressService_Create_Internal_ExplicitIP_SubnetNotFound_Async(t *testing.T) {
	ar := newMockAddressRepo()
	sr := newMockSubnetRepo()
	or := newMockOpsRepo()
	svc := NewAddressService(ar, sr, &mockFolderClient{exists: true}, or)

	op, err := svc.Create(context.Background(), CreateAddressReq{
		FolderID: "f1",
		InternalSpec: &InternalAddrSpec{
			SubnetID: "e9bnonexistentxxxxxx",
			Address:  "192.168.99.100",
		},
	})
	require.NoError(t, err)
	savedOp := awaitOpDone(t, or, op.ID)
	assert.True(t, savedOp.Done)
	require.NotNil(t, savedOp.Error)
	assert.Equal(t, int32(codes.NotFound), savedOp.Error.Code)
}

// Sync-валидация: invalid IP-формат → InvalidArgument.
func TestAddressService_Create_Internal_ExplicitIP_BadFormat(t *testing.T) {
	ar := newMockAddressRepo()
	sr := newMockSubnetRepo()
	or := newMockOpsRepo()
	sub := makeSubnet(sr, ids.NewUID())
	svc := NewAddressService(ar, sr, &mockFolderClient{exists: true}, or)

	_, err := svc.Create(context.Background(), CreateAddressReq{
		FolderID: "f1",
		InternalSpec: &InternalAddrSpec{
			SubnetID: sub.ID,
			Address:  "not-an-ip",
		},
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestAddressService_Update_DeletionProtection(t *testing.T) {
	ar := newMockAddressRepo()
	sr := newMockSubnetRepo()
	or := newMockOpsRepo()
	svc := NewAddressService(ar, sr, &mockFolderClient{exists: true}, or)

	createOp, _ := svc.Create(context.Background(), CreateAddressReq{
		FolderID:     "f1",
		ExternalSpec: &ExternalAddrSpec{Address: "203.0.113.50"},
	})
	awaitOpDone(t, or, createOp.ID)

	addrs, _, _ := svc.List(context.Background(), AddressFilter{FolderID: "f1"}, Pagination{})
	require.Len(t, addrs, 1)
	addrID := addrs[0].ID

	updOp, err := svc.Update(context.Background(), UpdateAddressReq{
		AddressID:          addrID,
		DeletionProtection: true,
		UpdateMask:         []string{"deletion_protection"},
	})
	require.NoError(t, err)
	savedOp := awaitOpDone(t, or, updOp.ID)
	assert.True(t, savedOp.Done)

	a, _ := svc.Get(context.Background(), addrID)
	assert.True(t, a.DeletionProtection)
}

func TestAddressService_Delete_NotFound(t *testing.T) {
	ar := newMockAddressRepo()
	sr := newMockSubnetRepo()
	or := newMockOpsRepo()
	svc := NewAddressService(ar, sr, &mockFolderClient{exists: true}, or)

	// Delete теперь делает sync Get для проверки deletion_protection,
	// поэтому NotFound возвращается синхронно, а не внутри goroutine.
	_, err := svc.Delete(context.Background(), ids.NewUID())
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestAddressService_Delete_DeletionProtection(t *testing.T) {
	ar := newMockAddressRepo()
	sr := newMockSubnetRepo()
	or := newMockOpsRepo()

	// Положим адрес с включённой защитой от удаления.
	addrID := ids.NewUID()
	a := &domain.Address{
		ID:                 addrID,
		FolderID:           "f1",
		Type:               domain.AddressTypeExternal,
		IpVersion:          domain.IpVersionIPv4,
		ExternalIpv4:       &domain.ExternalIpv4Spec{Address: "203.0.113.99"},
		DeletionProtection: true,
		Reserved:           true,
	}
	_, _ = ar.Insert(context.Background(), a)

	svc := NewAddressService(ar, sr, &mockFolderClient{exists: true}, or)

	// Sync FAILED_PRECONDITION; Operation НЕ создаётся.
	_, err := svc.Delete(context.Background(), addrID)
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.FailedPrecondition, st.Code())

	// Адрес остался на месте.
	got, getErr := svc.Get(context.Background(), addrID)
	require.NoError(t, getErr)
	assert.True(t, got.DeletionProtection)
}
