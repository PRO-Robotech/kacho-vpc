package routetable

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/repomock"
)

// Тесты RouteTable use-case'ов и handler'а. Wave 3b (KAC-94): сюда переехали
// прежние тесты `internal/handler/coverage*_test.go::Test{RouteTableHandler_*,
// RouteTableToProto_*}` и `internal/service/route_table_test.go`,
// `internal/service/coverage2_test.go::TestRouteTableService_*`.

func makeNetworkRecord(t *testing.T, nr *repomock.NetworkRepo) *domain.NetworkRecord {
	t.Helper()
	netID := ids.NewID(ids.PrefixNetwork)
	rec, err := nr.Insert(context.Background(), &domain.Network{ID: netID, FolderID: "f1", Name: "net"})
	require.NoError(t, err)
	return rec
}

func makeHandler(t *testing.T,
	rtr *repomock.RouteTableRepo,
	nr *repomock.NetworkRepo,
	or *repomock.OpsRepo,
	fc *repomock.FolderClient,
) *Handler {
	t.Helper()
	create := NewCreateRouteTableUseCase(rtr, nr, fc, or)
	update := NewUpdateRouteTableUseCase(rtr, or)
	deleteUC := NewDeleteRouteTableUseCase(rtr, or)
	move := NewMoveRouteTableUseCase(rtr, fc, or)
	get := NewGetRouteTableUseCase(rtr)
	list := NewListRouteTablesUseCase(rtr)
	listOps := NewListOperationsUseCase(or)
	return NewHandler(create, update, deleteUC, move, get, list, listOps)
}

func minimalHandler(t *testing.T, folderOK bool) (*Handler, *repomock.OpsRepo, *repomock.RouteTableRepo, *repomock.NetworkRepo) {
	t.Helper()
	rtr := repomock.NewRouteTableRepo()
	nr := repomock.NewNetworkRepo()
	or := repomock.NewOpsRepo()
	fc := &repomock.FolderClient{OK: folderOK}
	return makeHandler(t, rtr, nr, or, fc), or, rtr, nr
}

// ---- Handler — sync paths ----

func TestHandler_Get_InvalidArg(t *testing.T) {
	h, _, _, _ := minimalHandler(t, true)
	_, err := h.Get(context.Background(), &vpcv1.GetRouteTableRequest{RouteTableId: ""})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestHandler_Get_NotFound(t *testing.T) {
	h, _, _, _ := minimalHandler(t, true)
	_, err := h.Get(context.Background(), &vpcv1.GetRouteTableRequest{RouteTableId: ids.NewID(ids.PrefixRouteTable)})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestHandler_List_Empty(t *testing.T) {
	h, _, _, _ := minimalHandler(t, true)
	resp, err := h.List(context.Background(), &vpcv1.ListRouteTablesRequest{FolderId: "f1"})
	require.NoError(t, err)
	assert.Empty(t, resp.RouteTables)
}

func TestHandler_Update_InvalidArg(t *testing.T) {
	h, _, _, _ := minimalHandler(t, true)
	_, err := h.Update(context.Background(), &vpcv1.UpdateRouteTableRequest{RouteTableId: ""})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestHandler_Delete_InvalidArg(t *testing.T) {
	h, _, _, _ := minimalHandler(t, true)
	_, err := h.Delete(context.Background(), &vpcv1.DeleteRouteTableRequest{RouteTableId: ""})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestHandler_Move_RequiresID(t *testing.T) {
	h, _, _, _ := minimalHandler(t, true)
	_, err := h.Move(context.Background(), &vpcv1.MoveRouteTableRequest{RouteTableId: ""})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestHandler_ListOperations_RequiresID(t *testing.T) {
	h, _, _, _ := minimalHandler(t, true)
	_, err := h.ListOperations(context.Background(), &vpcv1.ListRouteTableOperationsRequest{RouteTableId: ""})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

// ---- use-case-level ----

func TestCreateUseCase_ValidationError(t *testing.T) {
	rtr := repomock.NewRouteTableRepo()
	nr := repomock.NewNetworkRepo()
	or := repomock.NewOpsRepo()
	uc := NewCreateRouteTableUseCase(rtr, nr, &repomock.FolderClient{OK: true}, or)

	// network_id required.
	_, err := uc.Execute(context.Background(), CreateInput{RouteTable: domain.RouteTable{FolderID: "f1", Name: "rt1"}})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestCreateUseCase_OK(t *testing.T) {
	rtr := repomock.NewRouteTableRepo()
	nr := repomock.NewNetworkRepo()
	or := repomock.NewOpsRepo()
	net := makeNetworkRecord(t, nr)
	uc := NewCreateRouteTableUseCase(rtr, nr, &repomock.FolderClient{OK: true}, or)

	op, err := uc.Execute(context.Background(), CreateInput{RouteTable: domain.RouteTable{
		FolderID:  "f1",
		NetworkID: net.ID,
		Name:      domain.RcNameVPC("rt1"),
		StaticRoutes: []domain.StaticRoute{
			{DestinationPrefix: "0.0.0.0/0", NextHopAddress: "192.168.0.1"},
		},
	}})
	require.NoError(t, err)
	require.NotEmpty(t, op.ID)

	saved := repomock.AwaitOpDone(t, or, op.ID)
	assert.True(t, saved.Done)
	assert.Nil(t, saved.Error)
}

func TestCreateUseCase_BadStaticRoute(t *testing.T) {
	rtr := repomock.NewRouteTableRepo()
	nr := repomock.NewNetworkRepo()
	or := repomock.NewOpsRepo()
	net := makeNetworkRecord(t, nr)
	uc := NewCreateRouteTableUseCase(rtr, nr, &repomock.FolderClient{OK: true}, or)

	_, err := uc.Execute(context.Background(), CreateInput{RouteTable: domain.RouteTable{
		FolderID:  "f1",
		NetworkID: net.ID,
		Name:      domain.RcNameVPC("rt-bad"),
		StaticRoutes: []domain.StaticRoute{
			{DestinationPrefix: "10.0.0.5/24", NextHopAddress: "192.168.0.1"}, // host-bits set
		},
	}})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestUpdateUseCase_StaticRoutes(t *testing.T) {
	rtr := repomock.NewRouteTableRepo()
	nr := repomock.NewNetworkRepo()
	or := repomock.NewOpsRepo()
	net := makeNetworkRecord(t, nr)
	createUC := NewCreateRouteTableUseCase(rtr, nr, &repomock.FolderClient{OK: true}, or)
	updateUC := NewUpdateRouteTableUseCase(rtr, or)
	getUC := NewGetRouteTableUseCase(rtr)
	listUC := NewListRouteTablesUseCase(rtr)

	createOp, _ := createUC.Execute(context.Background(), CreateInput{RouteTable: domain.RouteTable{
		FolderID: "f1", NetworkID: net.ID, Name: domain.RcNameVPC("rt1"),
	}})
	repomock.AwaitOpDone(t, or, createOp.ID)

	rts, _, _ := listUC.Execute(context.Background(), RouteTableFilter{FolderID: "f1"}, Pagination{})
	require.Len(t, rts, 1)
	rtID := rts[0].ID

	updOp, err := updateUC.Execute(context.Background(), UpdateInput{
		RouteTableID: rtID,
		RouteTable: domain.RouteTable{
			Name: domain.RcNameVPC("rt1"),
			StaticRoutes: []domain.StaticRoute{
				{DestinationPrefix: "10.0.0.0/8", NextHopAddress: "192.168.1.1"},
			},
		},
		UpdateMask: []string{"static_routes"},
	})
	require.NoError(t, err)
	saved := repomock.AwaitOpDone(t, or, updOp.ID)
	assert.True(t, saved.Done)

	rt, _ := getUC.Execute(context.Background(), rtID)
	require.Len(t, rt.StaticRoutes, 1)
	assert.Equal(t, "10.0.0.0/8", rt.StaticRoutes[0].DestinationPrefix)
}

func TestUpdateUseCase_UnknownMask(t *testing.T) {
	uc := NewUpdateRouteTableUseCase(repomock.NewRouteTableRepo(), repomock.NewOpsRepo())
	_, err := uc.Execute(context.Background(), UpdateInput{
		RouteTableID: ids.NewID(ids.PrefixRouteTable),
		UpdateMask:   []string{"unknown_field"},
	})
	require.Error(t, err)
}

func TestUpdateUseCase_ImmutableNetworkID(t *testing.T) {
	uc := NewUpdateRouteTableUseCase(repomock.NewRouteTableRepo(), repomock.NewOpsRepo())
	_, err := uc.Execute(context.Background(), UpdateInput{
		RouteTableID: ids.NewID(ids.PrefixRouteTable),
		UpdateMask:   []string{"network_id"},
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestDeleteUseCase_InvalidArg(t *testing.T) {
	uc := NewDeleteRouteTableUseCase(repomock.NewRouteTableRepo(), repomock.NewOpsRepo())
	_, err := uc.Execute(context.Background(), "")
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestMoveUseCase_Validates(t *testing.T) {
	uc := NewMoveRouteTableUseCase(repomock.NewRouteTableRepo(), &repomock.FolderClient{OK: true}, repomock.NewOpsRepo())
	_, err := uc.Execute(context.Background(), "", "f2")
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	_, err = uc.Execute(context.Background(), ids.NewID(ids.PrefixRouteTable), "")
	st, _ = status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestListUseCase_RequiresFolder(t *testing.T) {
	uc := NewListRouteTablesUseCase(repomock.NewRouteTableRepo())
	_, _, err := uc.Execute(context.Background(), RouteTableFilter{}, Pagination{})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

// ---- Handler happy-path ----

func TestHandler_FullFlow(t *testing.T) {
	h, or, _, nr := minimalHandler(t, true)
	net := makeNetworkRecord(t, nr)

	createOp, err := h.Create(context.Background(), &vpcv1.CreateRouteTableRequest{
		FolderId: "f1", Name: "rt", NetworkId: net.ID,
	})
	require.NoError(t, err)
	repomock.AwaitOpDone(t, or, createOp.Id)

	resp, _ := h.List(context.Background(), &vpcv1.ListRouteTablesRequest{FolderId: "f1"})
	require.Len(t, resp.RouteTables, 1)
	rtID := resp.RouteTables[0].Id

	_, err = h.Get(context.Background(), &vpcv1.GetRouteTableRequest{RouteTableId: rtID})
	require.NoError(t, err)

	updOp, err := h.Update(context.Background(), &vpcv1.UpdateRouteTableRequest{
		RouteTableId: rtID, Name: "rt-upd",
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"name"}},
	})
	require.NoError(t, err)
	repomock.AwaitOpDone(t, or, updOp.Id)

	_, err = h.ListOperations(context.Background(), &vpcv1.ListRouteTableOperationsRequest{RouteTableId: rtID})
	require.NoError(t, err)

	moveOp, err := h.Move(context.Background(), &vpcv1.MoveRouteTableRequest{RouteTableId: rtID, DestinationFolderId: ids.NewID(ids.PrefixFolder)})
	require.NoError(t, err)
	repomock.AwaitOpDone(t, or, moveOp.Id)

	delOp, err := h.Delete(context.Background(), &vpcv1.DeleteRouteTableRequest{RouteTableId: rtID})
	require.NoError(t, err)
	saved := repomock.AwaitOpDone(t, or, delOp.Id)
	require.Nil(t, saved.Error)
	require.NotNil(t, saved.Response)
	var empty emptypb.Empty
	require.NoError(t, saved.Response.UnmarshalTo(&empty), "Delete response must be google.protobuf.Empty")
}

func TestRouteTableToPb_StaticRoutes(t *testing.T) {
	rec := &domain.RouteTableRecord{
		RouteTable: domain.RouteTable{
			ID:        "rt-1",
			FolderID:  "f1",
			NetworkID: "net-1",
			StaticRoutes: []domain.StaticRoute{
				{DestinationPrefix: "0.0.0.0/0", NextHopAddress: "192.168.0.1"},
			},
		},
	}
	p, err := routeTableToPb(rec)
	require.NoError(t, err)
	require.Len(t, p.StaticRoutes, 1)
	assert.Equal(t, "0.0.0.0/0", p.StaticRoutes[0].GetDestinationPrefix())
	assert.Equal(t, "192.168.0.1", p.StaticRoutes[0].GetNextHopAddress())
}
