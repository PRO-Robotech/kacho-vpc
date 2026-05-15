package handler

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	svc "github.com/PRO-Robotech/kacho-vpc/internal/service"
)

// Fake-реализации port-ов — в `internal/ports/portmock` (shim — в mock_test.go).
// См. TODO #12.

// ---- Subnet handler tests ----

func TestSubnetHandler_Get_InvalidArg(t *testing.T) {
	sr := newMockSubnetRepoForSvc()
	nr := newMockNetworkRepo()
	or := newMockOpsRepo()
	subnetSvc := svc.NewSubnetService(sr, nr, newMockFolderClient(true), or, nil)
	h := NewSubnetHandler(subnetSvc)

	_, err := h.Get(context.Background(), &vpcv1.GetSubnetRequest{SubnetId: ""})
	require.Error(t, err)
	st, _ := grpcstatus.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestSubnetHandler_Get_NotFound(t *testing.T) {
	sr := newMockSubnetRepoForSvc()
	nr := newMockNetworkRepo()
	or := newMockOpsRepo()
	subnetSvc := svc.NewSubnetService(sr, nr, newMockFolderClient(true), or, nil)
	h := NewSubnetHandler(subnetSvc)

	_, err := h.Get(context.Background(), &vpcv1.GetSubnetRequest{SubnetId: ids.NewID(ids.PrefixSubnet)})
	require.Error(t, err)
	st, _ := grpcstatus.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestSubnetHandler_Create_OK(t *testing.T) {
	sr := newMockSubnetRepoForSvc()
	nr := newMockNetworkRepo()
	or := newMockOpsRepo()
	// Создаём сеть чтобы subnet service нашёл её
	netID := ids.NewID(ids.PrefixNetwork)
	nr.Insert(context.Background(), &domain.Network{ID: netID, FolderID: "f1", Name: "net"})
	subnetSvc := svc.NewSubnetService(sr, nr, newMockFolderClient(true), or, nil)
	h := NewSubnetHandler(subnetSvc)

	op, err := h.Create(context.Background(), &vpcv1.CreateSubnetRequest{
		FolderId:     "f1",
		Name:         "sub1",
		NetworkId:    netID,
		ZoneId:       "ru-central1-a",
		V4CidrBlocks: []string{"10.0.0.0/24"},
	})
	require.NoError(t, err)
	assert.NotEmpty(t, op.Id)

	saved := awaitOpDone(t, or, op.Id)
	assert.True(t, saved.Done)
}

func TestSubnetHandler_List_Empty(t *testing.T) {
	sr := newMockSubnetRepoForSvc()
	nr := newMockNetworkRepo()
	or := newMockOpsRepo()
	subnetSvc := svc.NewSubnetService(sr, nr, newMockFolderClient(true), or, nil)
	h := NewSubnetHandler(subnetSvc)

	resp, err := h.List(context.Background(), &vpcv1.ListSubnetsRequest{FolderId: "f1"})
	require.NoError(t, err)
	assert.Empty(t, resp.Subnets)
}

func TestSubnetHandler_Delete_InvalidArg(t *testing.T) {
	sr := newMockSubnetRepoForSvc()
	nr := newMockNetworkRepo()
	or := newMockOpsRepo()
	subnetSvc := svc.NewSubnetService(sr, nr, newMockFolderClient(true), or, nil)
	h := NewSubnetHandler(subnetSvc)

	_, err := h.Delete(context.Background(), &vpcv1.DeleteSubnetRequest{SubnetId: ""})
	require.Error(t, err)
	st, _ := grpcstatus.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

// ---- RouteTable handler tests — moved to internal/apps/kacho/api/routetable/usecase_test.go (Wave 3b) ----
