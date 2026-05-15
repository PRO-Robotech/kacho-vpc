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

func makeNetwork(nr *mockNetworkRepo) *domain.Network {
	n := &domain.Network{
		ID:       ids.NewID(ids.PrefixNetwork),
		FolderID: "f1",
		Name:     domain.RcNameVPC("test-network"),
	}
	_, _ = nr.Insert(context.Background(), n)
	return n
}

func TestSubnetService_Create_ValidationError(t *testing.T) {
	nr := newMockNetworkRepo()
	sr := newMockSubnetRepo()
	or := newMockOpsRepo()
	svc := NewSubnetService(sr, nr, newMockFolderClient(true), or, nil)

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
	svc := NewSubnetService(sr, nr, newMockFolderClient(true), or, nil)

	op, err := svc.Create(context.Background(), CreateSubnetReq{
		FolderID:     "f1",
		Name:         "sub1",
		NetworkID:    net.ID,
		ZoneID:       "ru-central1-a",
		V4CidrBlocks: []string{"10.0.0.0/24"},
	})
	require.NoError(t, err)
	require.NotEmpty(t, op.ID)

	savedOp := awaitOpDone(t, or, op.ID)
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
	svc := NewSubnetService(sr, nr, newMockFolderClient(true), or, nil)

	// Verbatim YC: parent-network existence is checked synchronously, BEFORE
	// the Operation — client gets a sync NotFound. См. kacho-vpc#8.
	_, err := svc.Create(context.Background(), CreateSubnetReq{
		FolderID:     "f1",
		Name:         "sub1",
		NetworkID:    ids.NewID(ids.PrefixNetwork), // well-formed-но-несуществующий
		ZoneID:       "ru-central1-a",
		V4CidrBlocks: []string{"10.0.0.0/24"},
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestSubnetService_Update_CidrBlocks_Immutable(t *testing.T) {
	// SU-CIDR-IM-1 (kacho-vpc#10, probe 2026-05-11): verbatim YC НЕ отвергает
	// v4_cidr_blocks в update_mask — запрос принимается (200 → Operation), но
	// репозиторный Update не перезаписывает CIDR-колонки (defensive depth), т.е.
	// изменение CIDR через Update — no-op. См. 07-known-divergences.md.
	nr := newMockNetworkRepo()
	net := makeNetwork(nr)
	sr := newMockSubnetRepo()
	or := newMockOpsRepo()
	svc := NewSubnetService(sr, nr, newMockFolderClient(true), or, nil)

	createOp, _ := svc.Create(context.Background(), CreateSubnetReq{
		FolderID:     "f1",
		Name:         "sub1",
		NetworkID:    net.ID,
		ZoneID:       "ru-central1-a",
		V4CidrBlocks: []string{"10.0.0.0/24"},
	})
	awaitOpDone(t, or, createOp.ID)

	subs, _, _ := svc.List(context.Background(), SubnetFilter{FolderID: "f1"}, Pagination{})
	require.Len(t, subs, 1)
	subID := subs[0].ID

	// Update с новыми CIDRs принимается (Operation), но CIDR не меняется.
	updOp, err := svc.Update(context.Background(), UpdateSubnetReq{
		SubnetID:     subID,
		Name:         "sub1",
		V4CidrBlocks: []string{"10.0.0.0/24", "10.1.0.0/24"},
		UpdateMask:   []string{"v4_cidr_blocks"},
	})
	require.NoError(t, err)
	awaitOpDone(t, or, updOp.ID)

	// Проверяем что CIDR-блоки не изменились (no-op в repo).
	sub, _ := svc.Get(context.Background(), subID)
	assert.Equal(t, []string{"10.0.0.0/24"}, sub.V4CidrBlocks)

	// network_id / zone_id — по-прежнему hard-immutable → InvalidArgument при
	// явном указании в update_mask.
	_, err = svc.Update(context.Background(), UpdateSubnetReq{
		SubnetID:   subID,
		Name:       "sub1",
		UpdateMask: []string{"zone_id"},
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestSubnetService_Delete_BlockedByNIC(t *testing.T) {
	// KAC-33: NIC→Subnet FK is ON DELETE RESTRICT — any NIC (attached or not)
	// hard-blocks its subnet. Delete the NIC first.
	nr := newMockNetworkRepo()
	net := makeNetwork(nr)
	sr := newMockSubnetRepo()
	or := newMockOpsRepo()
	sub := makeSubnet(sr, net.ID)

	nicRepo := newNIRepoFake()
	nicRepo.data["e9bnic1"] = &domain.NetworkInterface{
		ID: "e9bnic1", FolderID: "f1", SubnetID: sub.ID,
		Status: domain.NIStatusAvailable,
	}

	svc := NewSubnetService(sr, nr, newMockFolderClient(true), or, nil)
	svc.SetNICRepo(nicRepo)

	// Delete блокируется sync — FailedPrecondition (даже если NIC не приаттачен).
	_, err := svc.Delete(context.Background(), sub.ID)
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
	assert.Contains(t, st.Message(), "network interface")
	assert.Contains(t, st.Message(), "e9bnic1")

	// Удалили NIC → Delete проходит (Operation создаётся).
	delete(nicRepo.data, "e9bnic1")
	op, err := svc.Delete(context.Background(), sub.ID)
	require.NoError(t, err)
	require.NotNil(t, op)
}
