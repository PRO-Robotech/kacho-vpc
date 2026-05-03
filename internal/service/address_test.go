package service

import (
	"context"
	"testing"
	"time"

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

	time.Sleep(100 * time.Millisecond)

	savedOp, _ := or.Get(context.Background(), op.ID)
	assert.True(t, savedOp.Done)
	assert.Nil(t, savedOp.Error)

	// Проверяем тип адреса
	addrs, _, err := svc.List(context.Background(), AddressFilter{FolderID: "f1"}, Pagination{})
	require.NoError(t, err)
	require.Len(t, addrs, 1)
	assert.Equal(t, domain.AddressTypeExternal, addrs[0].Type)
	assert.Equal(t, "203.0.113.10", addrs[0].ExternalIpv4.Address)
}

func TestAddressService_Create_External_AutoAlloc(t *testing.T) {
	ar := newMockAddressRepo()
	sr := newMockSubnetRepo()
	or := newMockOpsRepo()
	svc := NewAddressService(ar, sr, &mockFolderClient{exists: true}, or)

	// Без явного адреса — должен выделить автоматически из 203.0.113.0/24
	op, err := svc.Create(context.Background(), CreateAddressReq{
		FolderID:     "f1",
		ExternalSpec: &ExternalAddrSpec{ZoneID: "ru-central1-a"},
	})
	require.NoError(t, err)

	time.Sleep(100 * time.Millisecond)

	savedOp, _ := or.Get(context.Background(), op.ID)
	assert.True(t, savedOp.Done)
	assert.Nil(t, savedOp.Error)

	addrs, _, _ := svc.List(context.Background(), AddressFilter{FolderID: "f1"}, Pagination{})
	require.Len(t, addrs, 1)
	addr := addrs[0].ExternalIpv4.Address
	assert.Contains(t, addr, "203.0.113.")
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

	time.Sleep(100 * time.Millisecond)

	savedOp, _ := or.Get(context.Background(), op.ID)
	assert.True(t, savedOp.Done)
	assert.Nil(t, savedOp.Error)

	addrs, _, _ := svc.List(context.Background(), AddressFilter{FolderID: "f1"}, Pagination{})
	require.Len(t, addrs, 1)
	assert.Equal(t, domain.AddressTypeInternal, addrs[0].Type)
	assert.Equal(t, sub.ID, addrs[0].InternalIpv4.SubnetID)
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
	time.Sleep(100 * time.Millisecond)
	_ = createOp

	addrs, _, _ := svc.List(context.Background(), AddressFilter{FolderID: "f1"}, Pagination{})
	require.Len(t, addrs, 1)
	addrID := addrs[0].ID

	updOp, err := svc.Update(context.Background(), UpdateAddressReq{
		AddressID:          addrID,
		DeletionProtection: true,
		UpdateMask:         []string{"deletion_protection"},
	})
	require.NoError(t, err)
	time.Sleep(100 * time.Millisecond)
	savedOp, _ := or.Get(context.Background(), updOp.ID)
	assert.True(t, savedOp.Done)

	a, _ := svc.Get(context.Background(), addrID)
	assert.True(t, a.DeletionProtection)
}

func TestAddressService_Delete_NotFound(t *testing.T) {
	ar := newMockAddressRepo()
	sr := newMockSubnetRepo()
	or := newMockOpsRepo()
	svc := NewAddressService(ar, sr, &mockFolderClient{exists: true}, or)

	op, err := svc.Delete(context.Background(), ids.NewUID())
	require.NoError(t, err)

	time.Sleep(100 * time.Millisecond)
	savedOp, _ := or.Get(context.Background(), op.ID)
	assert.True(t, savedOp.Done)
	assert.NotNil(t, savedOp.Error) // NotFound внутри goroutine
}
