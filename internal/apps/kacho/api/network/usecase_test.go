package network

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
	"github.com/PRO-Robotech/kacho-vpc/internal/ports/portmock"
)

// Тесты Network use-case'ов и handler'а. Wave 3a pilot (KAC-94): сюда переехали
// прежние тесты `internal/service/network_test.go`,
// `internal/service/coverage_test.go::Test{NetworkService,…}` и
// `internal/handler/{handler,coverage,coverage2}_test.go::TestNetworkHandler_*`
// (последние теперь — против `*networkapp.Handler`).
//
// Mock-port'ы — переиспользуем `internal/ports/portmock` (уже реализует
// `internal/ports.NetworkRepo`, который ⊇ нашему локальному `NetworkRepo`).

// ---- builders ----

func makeHandler(t *testing.T,
	nr *portmock.NetworkRepo,
	sr *portmock.SubnetRepo,
	rtr *portmock.RouteTableRepo,
	sgr *portmock.SecurityGroupRepo,
	or *portmock.OpsRepo,
	fc *portmock.FolderClient,
	defaultSG SecurityGroupRepo,
) *Handler {
	t.Helper()
	// Маппим typed-nil-указатель → nil-интерфейс (иначе Go бы создал не-nil
	// интерфейс с nil-receiver, и use-case попытается вызвать List/...).
	var sReader SubnetReader
	if sr != nil {
		sReader = sr
	}
	var rtReader RouteTableReader
	if rtr != nil {
		rtReader = rtr
	}
	var sgRepoIface SecurityGroupRepo
	if sgr != nil {
		sgRepoIface = sgr
	}
	create := NewCreateNetworkUseCase(nr, fc, or, defaultSG)
	update := NewUpdateNetworkUseCase(nr, or)
	deleteUC := NewDeleteNetworkUseCase(nr, sReader, rtReader, sgRepoIface, or)
	move := NewMoveNetworkUseCase(nr, fc, or)
	get := NewGetNetworkUseCase(nr)
	list := NewListNetworksUseCase(nr)
	listSub := NewListSubnetsUseCase(nr, sReader)
	listSG := NewListSecurityGroupsUseCase(nr, sgRepoIface)
	listRT := NewListRouteTablesUseCase(nr, rtReader)
	listOps := NewListOperationsUseCase(or)
	return NewHandler(create, update, deleteUC, move, get, list, listSub, listSG, listRT, listOps)
}

// folder ok / ops repo / network repo с минимальной wiring — для тестов где
// child-reader'ы не требуются.
func minimalHandler(t *testing.T, folderOK bool) (*Handler, *portmock.OpsRepo, *portmock.NetworkRepo) {
	t.Helper()
	nr := portmock.NewNetworkRepo()
	or := portmock.NewOpsRepo()
	fc := &portmock.FolderClient{OK: folderOK}
	return makeHandler(t, nr, nil, nil, nil, or, fc, nil), or, nr
}

// ---- Handler — sync paths ----

func TestHandler_Get_InvalidArg(t *testing.T) {
	h, _, _ := minimalHandler(t, true)
	_, err := h.Get(context.Background(), &vpcv1.GetNetworkRequest{NetworkId: ""})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestHandler_Get_NotFound(t *testing.T) {
	h, _, _ := minimalHandler(t, true)
	_, err := h.Get(context.Background(), &vpcv1.GetNetworkRequest{NetworkId: ids.NewID(ids.PrefixNetwork)})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestHandler_List_Empty(t *testing.T) {
	h, _, _ := minimalHandler(t, true)
	resp, err := h.List(context.Background(), &vpcv1.ListNetworksRequest{FolderId: "f1"})
	require.NoError(t, err)
	assert.Empty(t, resp.Networks)
}

func TestHandler_Delete_InvalidArg(t *testing.T) {
	h, _, _ := minimalHandler(t, true)
	_, err := h.Delete(context.Background(), &vpcv1.DeleteNetworkRequest{NetworkId: ""})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

// ---- use-case-level (без handler'а) ----

func TestCreateUseCase_ValidationError(t *testing.T) {
	nr := portmock.NewNetworkRepo()
	or := portmock.NewOpsRepo()
	uc := NewCreateNetworkUseCase(nr, &portmock.FolderClient{OK: true}, or, nil)

	// folder_id required.
	_, err := uc.Execute(context.Background(), CreateInput{Network: domain.Network{Name: "test"}})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())

	// invalid name (starts with digit, NameVPC permissive но цифра в начале запрещена).
	_, err = uc.Execute(context.Background(), CreateInput{Network: domain.Network{
		FolderID: "f1",
		Name:     domain.RcNameVPC("1bad"),
	}})
	require.Error(t, err)
	st, _ = status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestCreateUseCase_FolderNotFound(t *testing.T) {
	nr := portmock.NewNetworkRepo()
	or := portmock.NewOpsRepo()
	uc := NewCreateNetworkUseCase(nr, &portmock.FolderClient{OK: false}, or, nil)

	_, err := uc.Execute(context.Background(), CreateInput{Network: domain.Network{
		FolderID: "f1",
		Name:     domain.RcNameVPC("net1"),
	}})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestCreateUseCase_OK(t *testing.T) {
	nr := portmock.NewNetworkRepo()
	or := portmock.NewOpsRepo()
	uc := NewCreateNetworkUseCase(nr, &portmock.FolderClient{OK: true}, or, nil)

	op, err := uc.Execute(context.Background(), CreateInput{Network: domain.Network{
		FolderID:    "f1",
		Name:        domain.RcNameVPC("net1"),
		Description: domain.RcDescription("desc"),
	}})
	require.NoError(t, err)
	require.NotEmpty(t, op.ID)

	saved := portmock.AwaitOpDone(t, or, op.ID)
	assert.True(t, saved.Done)
	assert.Nil(t, saved.Error)
}

func TestDeleteUseCase_InvalidArg(t *testing.T) {
	uc := NewDeleteNetworkUseCase(portmock.NewNetworkRepo(), nil, nil, nil, portmock.NewOpsRepo())
	_, err := uc.Execute(context.Background(), "")
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestMoveUseCase_Validates(t *testing.T) {
	uc := NewMoveNetworkUseCase(portmock.NewNetworkRepo(), &portmock.FolderClient{OK: true}, portmock.NewOpsRepo())
	_, err := uc.Execute(context.Background(), "", "f2")
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	_, err = uc.Execute(context.Background(), ids.NewID(ids.PrefixNetwork), "")
	st, _ = status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestListUseCase_RequiresFolder(t *testing.T) {
	uc := NewListNetworksUseCase(portmock.NewNetworkRepo())
	_, _, err := uc.Execute(context.Background(), NetworkFilter{}, Pagination{})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestListOperationsUseCase_UnknownID_Empty(t *testing.T) {
	// История операций должна оставаться доступной после Delete — unknown id
	// ≠ NotFound, это пустой список.
	uc := NewListOperationsUseCase(portmock.NewOpsRepo())
	ops, _, err := uc.Execute(context.Background(), ids.NewID(ids.PrefixNetwork), Pagination{})
	assert.NoError(t, err)
	assert.Empty(t, ops)
}

func TestListSubnetsUseCase_NetworkNotFound(t *testing.T) {
	uc := NewListSubnetsUseCase(portmock.NewNetworkRepo(), portmock.NewSubnetRepo())
	_, _, err := uc.Execute(context.Background(), ids.NewID(ids.PrefixNetwork), Pagination{})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestListSecurityGroupsUseCase_NetworkNotFound(t *testing.T) {
	uc := NewListSecurityGroupsUseCase(portmock.NewNetworkRepo(), portmock.NewSecurityGroupRepo())
	_, _, err := uc.Execute(context.Background(), ids.NewID(ids.PrefixNetwork), Pagination{})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestListRouteTablesUseCase_NetworkNotFound(t *testing.T) {
	uc := NewListRouteTablesUseCase(portmock.NewNetworkRepo(), portmock.NewRouteTableRepo())
	_, _, err := uc.Execute(context.Background(), ids.NewID(ids.PrefixNetwork), Pagination{})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

// ---- Handler — happy-path Create / List / Update / Delete ----

func TestHandler_Create_OK(t *testing.T) {
	h, or, _ := minimalHandler(t, true)
	op, err := h.Create(context.Background(), &vpcv1.CreateNetworkRequest{
		FolderId: "f1",
		Name:     "net1",
	})
	require.NoError(t, err)
	assert.NotEmpty(t, op.Id)
	saved := portmock.AwaitOpDone(t, or, op.Id)
	assert.True(t, saved.Done)
}

func TestHandler_Delete_ResponseIsEmpty(t *testing.T) {
	// Operation.response для Delete должен быть google.protobuf.Empty
	// (proto-options contract — защита от регрессии).
	h, or, _ := minimalHandler(t, true)

	createOp, err := h.Create(context.Background(), &vpcv1.CreateNetworkRequest{FolderId: "f1", Name: "del-resp-test"})
	require.NoError(t, err)
	portmock.AwaitOpDone(t, or, createOp.Id)

	resp, _ := h.List(context.Background(), &vpcv1.ListNetworksRequest{FolderId: "f1"})
	require.Len(t, resp.Networks, 1)

	delOp, err := h.Delete(context.Background(), &vpcv1.DeleteNetworkRequest{NetworkId: resp.Networks[0].Id})
	require.NoError(t, err)
	saved := portmock.AwaitOpDone(t, or, delOp.Id)
	require.Nil(t, saved.Error)
	require.NotNil(t, saved.Response)

	var empty emptypb.Empty
	err = saved.Response.UnmarshalTo(&empty)
	require.NoError(t, err, "Delete response must be google.protobuf.Empty (proto-options contract)")
}

func TestHandler_Update_MaskApplication(t *testing.T) {
	h, or, _ := minimalHandler(t, true)
	// Создаём сеть
	createOp, err := h.Create(context.Background(), &vpcv1.CreateNetworkRequest{FolderId: "f1", Name: "n1"})
	require.NoError(t, err)
	savedOp := portmock.AwaitOpDone(t, or, createOp.Id)
	require.NotNil(t, savedOp.Metadata)

	resp, _ := h.List(context.Background(), &vpcv1.ListNetworksRequest{FolderId: "f1"})
	require.Len(t, resp.Networks, 1)
	netID := resp.Networks[0].Id

	// Update с маской — только name.
	updOp, err := h.Update(context.Background(), &vpcv1.UpdateNetworkRequest{
		NetworkId:   netID,
		Name:        "n1-updated",
		Description: "new desc",
		UpdateMask:  &fieldmaskpb.FieldMask{Paths: []string{"name"}},
	})
	require.NoError(t, err)
	savedUpdOp := portmock.AwaitOpDone(t, or, updOp.Id)
	assert.True(t, savedUpdOp.Done)

	got, _ := h.Get(context.Background(), &vpcv1.GetNetworkRequest{NetworkId: netID})
	assert.Equal(t, "n1-updated", got.Name)
	assert.Equal(t, "", got.Description) // маска не включала description
}

func TestHandler_Update_InvalidArg(t *testing.T) {
	h, _, _ := minimalHandler(t, true)
	_, err := h.Update(context.Background(), &vpcv1.UpdateNetworkRequest{NetworkId: ""})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestHandler_Move_RequiresID(t *testing.T) {
	h, _, _ := minimalHandler(t, true)
	_, err := h.Move(context.Background(), &vpcv1.MoveNetworkRequest{NetworkId: ""})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestHandler_ListOperations_RequiresID(t *testing.T) {
	h, _, _ := minimalHandler(t, true)
	_, err := h.ListOperations(context.Background(), &vpcv1.ListNetworkOperationsRequest{NetworkId: ""})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestHandler_Update_Happy(t *testing.T) {
	nr := portmock.NewNetworkRepo()
	or := portmock.NewOpsRepo()
	sr := portmock.NewSubnetRepo()
	rtr := portmock.NewRouteTableRepo()
	h := makeHandler(t, nr, sr, rtr, nil, or, &portmock.FolderClient{OK: true}, nil)

	createOp, err := h.Create(context.Background(), &vpcv1.CreateNetworkRequest{FolderId: "f1", Name: "n"})
	require.NoError(t, err)
	portmock.AwaitOpDone(t, or, createOp.Id)

	resp, _ := h.List(context.Background(), &vpcv1.ListNetworksRequest{FolderId: "f1"})
	require.Len(t, resp.Networks, 1)
	netID := resp.Networks[0].Id

	updOp, err := h.Update(context.Background(), &vpcv1.UpdateNetworkRequest{
		NetworkId: netID, Name: "n-upd",
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"name"}},
	})
	require.NoError(t, err)
	portmock.AwaitOpDone(t, or, updOp.Id)
	got, _ := h.Get(context.Background(), &vpcv1.GetNetworkRequest{NetworkId: netID})
	assert.Equal(t, "n-upd", got.Name)

	// child list happy
	_, err = h.ListSubnets(context.Background(), &vpcv1.ListNetworkSubnetsRequest{NetworkId: netID})
	require.NoError(t, err)
	_, err = h.ListRouteTables(context.Background(), &vpcv1.ListNetworkRouteTablesRequest{NetworkId: netID})
	require.NoError(t, err)
	_, err = h.ListOperations(context.Background(), &vpcv1.ListNetworkOperationsRequest{NetworkId: netID})
	require.NoError(t, err)

	// Move в другой folder (folder mock возвращает OK)
	moveOp, err := h.Move(context.Background(), &vpcv1.MoveNetworkRequest{
		NetworkId: netID, DestinationFolderId: ids.NewID(ids.PrefixFolder),
	})
	require.NoError(t, err)
	portmock.AwaitOpDone(t, or, moveOp.Id)

	// Delete (без child-resources)
	delOp, err := h.Delete(context.Background(), &vpcv1.DeleteNetworkRequest{NetworkId: netID})
	require.NoError(t, err)
	portmock.AwaitOpDone(t, or, delOp.Id)
}
