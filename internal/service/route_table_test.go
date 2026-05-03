package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

func TestRouteTableService_Create_ValidationError(t *testing.T) {
	nr := newMockNetworkRepo()
	rtr := newMockRouteTableRepo()
	or := newMockOpsRepo()
	svc := NewRouteTableService(rtr, nr, &mockFolderClient{exists: true}, or)

	// Пустой network_id
	_, err := svc.Create(context.Background(), CreateRouteTableReq{FolderID: "f1", Name: "rt1"})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestRouteTableService_Create_OK(t *testing.T) {
	nr := newMockNetworkRepo()
	net := makeNetwork(nr)
	rtr := newMockRouteTableRepo()
	or := newMockOpsRepo()
	svc := NewRouteTableService(rtr, nr, &mockFolderClient{exists: true}, or)

	op, err := svc.Create(context.Background(), CreateRouteTableReq{
		FolderID:  "f1",
		Name:      "rt1",
		NetworkID: net.ID,
		StaticRoutes: []domain.StaticRoute{
			{DestinationPrefix: "0.0.0.0/0", NextHopAddress: "192.168.0.1"},
		},
	})
	require.NoError(t, err)
	require.NotEmpty(t, op.ID)

	time.Sleep(100 * time.Millisecond)

	savedOp, _ := or.Get(context.Background(), op.ID)
	assert.True(t, savedOp.Done)
	assert.Nil(t, savedOp.Error)

	rts, _, err := svc.List(context.Background(), RouteTableFilter{FolderID: "f1"}, Pagination{})
	require.NoError(t, err)
	require.Len(t, rts, 1)
	require.Len(t, rts[0].StaticRoutes, 1)
	assert.Equal(t, "0.0.0.0/0", rts[0].StaticRoutes[0].DestinationPrefix)
	assert.Equal(t, "192.168.0.1", rts[0].StaticRoutes[0].NextHopAddress)
}

func TestRouteTableService_Update_StaticRoutes(t *testing.T) {
	nr := newMockNetworkRepo()
	net := makeNetwork(nr)
	rtr := newMockRouteTableRepo()
	or := newMockOpsRepo()
	svc := NewRouteTableService(rtr, nr, &mockFolderClient{exists: true}, or)

	createOp, _ := svc.Create(context.Background(), CreateRouteTableReq{
		FolderID:  "f1",
		Name:      "rt1",
		NetworkID: net.ID,
	})
	time.Sleep(100 * time.Millisecond)
	_ = createOp

	rts, _, _ := svc.List(context.Background(), RouteTableFilter{FolderID: "f1"}, Pagination{})
	require.Len(t, rts, 1)
	rtID := rts[0].ID

	updOp, err := svc.Update(context.Background(), UpdateRouteTableReq{
		RouteTableID: rtID,
		Name:         "rt1",
		StaticRoutes: []domain.StaticRoute{
			{DestinationPrefix: "10.0.0.0/8", NextHopAddress: "192.168.1.1"},
		},
		UpdateMask: []string{"static_routes"},
	})
	require.NoError(t, err)
	time.Sleep(100 * time.Millisecond)

	savedOp, _ := or.Get(context.Background(), updOp.ID)
	assert.True(t, savedOp.Done)

	rt, _ := svc.Get(context.Background(), rtID)
	require.Len(t, rt.StaticRoutes, 1)
	assert.Equal(t, "10.0.0.0/8", rt.StaticRoutes[0].DestinationPrefix)
}
