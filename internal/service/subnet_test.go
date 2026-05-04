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

func makeNetwork(nr *mockNetworkRepo) *domain.Network {
	n := &domain.Network{
		ID:       ids.NewUID(),
		FolderID: "f1",
		Name:     "test-network",
	}
	_, _ = nr.Insert(context.Background(), n)
	return n
}

func TestSubnetService_Create_ValidationError(t *testing.T) {
	nr := newMockNetworkRepo()
	sr := newMockSubnetRepo()
	or := newMockOpsRepo()
	svc := NewSubnetService(sr, nr, &mockFolderClient{exists: true}, or)

	// Пустой network_id
	_, err := svc.Create(context.Background(), CreateSubnetReq{FolderID: "f1", Name: "sub1"})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestSubnetService_Create_OK(t *testing.T) {
	nr := newMockNetworkRepo()
	net := makeNetwork(nr)
	sr := newMockSubnetRepo()
	or := newMockOpsRepo()
	svc := NewSubnetService(sr, nr, &mockFolderClient{exists: true}, or)

	op, err := svc.Create(context.Background(), CreateSubnetReq{
		FolderID:     "f1",
		Name:         "sub1",
		NetworkID:    net.ID,
		ZoneID:       "ru-central1-a",
		V4CidrBlocks: []string{"10.0.0.0/24"},
	})
	require.NoError(t, err)
	require.NotEmpty(t, op.ID)

	time.Sleep(100 * time.Millisecond)

	savedOp, _ := or.Get(context.Background(), op.ID)
	assert.True(t, savedOp.Done)
	assert.Nil(t, savedOp.Error)

	// Проверяем что подсеть сохранилась с V4CidrBlocks
	subs, _, err := svc.List(context.Background(), SubnetFilter{FolderID: "f1"}, Pagination{})
	require.NoError(t, err)
	require.Len(t, subs, 1)
	assert.Equal(t, []string{"10.0.0.0/24"}, subs[0].V4CidrBlocks)
}

func TestSubnetService_Create_NetworkNotFound(t *testing.T) {
	nr := newMockNetworkRepo()
	sr := newMockSubnetRepo()
	or := newMockOpsRepo()
	svc := NewSubnetService(sr, nr, &mockFolderClient{exists: true}, or)

	op, err := svc.Create(context.Background(), CreateSubnetReq{
		FolderID:     "f1",
		Name:         "sub1",
		NetworkID:    "nonexistent",
		ZoneID:       "ru-central1-a",
		V4CidrBlocks: []string{"10.0.0.0/24"},
	})
	require.NoError(t, err)

	time.Sleep(100 * time.Millisecond)

	savedOp, _ := or.Get(context.Background(), op.ID)
	assert.True(t, savedOp.Done)
	assert.NotNil(t, savedOp.Error) // должна быть ошибка NotFound
}

func TestSubnetService_Update_CidrBlocks_Immutable(t *testing.T) {
	// SU-CIDR-IM-1: v4_cidr_blocks immutable after Subnet.Create. Любая
	// попытка изменить через update_mask → InvalidArgument до запуска
	// Operation worker'а. См. SU-CIDR-IM-1-mutable-cidr.md.
	nr := newMockNetworkRepo()
	net := makeNetwork(nr)
	sr := newMockSubnetRepo()
	or := newMockOpsRepo()
	svc := NewSubnetService(sr, nr, &mockFolderClient{exists: true}, or)

	createOp, _ := svc.Create(context.Background(), CreateSubnetReq{
		FolderID:     "f1",
		Name:         "sub1",
		NetworkID:    net.ID,
		ZoneID:       "ru-central1-a",
		V4CidrBlocks: []string{"10.0.0.0/24"},
	})
	time.Sleep(100 * time.Millisecond)
	_ = createOp

	subs, _, _ := svc.List(context.Background(), SubnetFilter{FolderID: "f1"}, Pagination{})
	require.Len(t, subs, 1)
	subID := subs[0].ID

	// Попытка Update с новыми CIDRs должна провалиться синхронно.
	_, err := svc.Update(context.Background(), UpdateSubnetReq{
		SubnetID:     subID,
		Name:         "sub1",
		V4CidrBlocks: []string{"10.0.0.0/24", "10.1.0.0/24"},
		UpdateMask:   []string{"v4_cidr_blocks"},
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())

	// Проверяем что CIDR-блоки не изменились.
	sub, _ := svc.Get(context.Background(), subID)
	assert.Equal(t, []string{"10.0.0.0/24"}, sub.V4CidrBlocks)
}
