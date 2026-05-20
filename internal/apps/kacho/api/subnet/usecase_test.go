package subnet

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
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/kachomock"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/repomock"
)

// Тесты Subnet use-case'ов и handler'а. Wave 5 replicate (KAC-94): Subnet
// переехал на CQRS-Repository. Mock — `kachomock.NewRepository()` (in-memory
// CQRS-impl с TX-семантикой Subnet/Network/SG state и outbox-буфером).

// testZone — фиктивная зона, которую mock-zoneReg считает существующей.
const testZone = "ru-central1-a"

func makeHandler(t *testing.T,
	kr *kachomock.Repository,
	or *repomock.OpsRepo,
	fc *repomock.ProjectClient,
	zr *repomock.ZoneRegistry,
) *Handler {
	t.Helper()
	create := NewCreateSubnetUseCase(kr, fc, zr, or)
	update := NewUpdateSubnetUseCase(kr, or)
	deleteUC := NewDeleteSubnetUseCase(kr, nil, or)
	move := NewMoveSubnetUseCase(kr, fc, or)
	get := NewGetSubnetUseCase(kr)
	list := NewListSubnetsUseCase(kr, nil)
	addCidr := NewAddCidrBlocksUseCase(kr, or)
	removeCidr := NewRemoveCidrBlocksUseCase(kr, or)
	relocate := NewRelocateUseCase(kr, zr)
	listUsedAddrs := NewListUsedAddressesUseCase(kr, nil)
	listOps := NewListOperationsUseCase(or)
	return NewHandler(create, update, deleteUC, move, get, list,
		addCidr, removeCidr, relocate, listUsedAddrs, listOps)
}

// minimalHandler собирает Handler с in-memory kachomock.Repository и одной
// seed-Network в folder "f1". Возвращает Handler, OpsRepo (для AwaitOpDone),
// Repository (для прямого доступа к стейту) и id seed-network'а.
func minimalHandler(t *testing.T, folderOK bool) (*Handler, *repomock.OpsRepo, *kachomock.Repository, string) {
	t.Helper()
	kr := kachomock.NewRepository()
	or := repomock.NewOpsRepo()
	fc := &repomock.ProjectClient{OK: folderOK}
	zr := repomock.NewZoneRegistry(testZone)

	// Seed Network через kachomock writer (committed state, видим Reader'ом).
	netID := ids.NewID(ids.PrefixNetwork)
	seedNetwork(t, kr, "f1", netID)

	return makeHandler(t, kr, or, fc, zr), or, kr, netID
}

// seedNetwork helper — committed Network через writer-TX.
func seedNetwork(t *testing.T, kr *kachomock.Repository, folderID, networkID string) {
	t.Helper()
	ctx := context.Background()
	w, err := kr.Writer(ctx)
	require.NoError(t, err)
	_, err = w.Networks().Insert(ctx, &domain.Network{ID: networkID, ProjectID: folderID, Name: domain.RcNameVPC("net-for-test")})
	require.NoError(t, err)
	require.NoError(t, w.Commit())
}

// ---- Handler — sync paths ----

func TestHandler_Get_InvalidArg(t *testing.T) {
	h, _, _, _ := minimalHandler(t, true)
	_, err := h.Get(context.Background(), &vpcv1.GetSubnetRequest{SubnetId: ""})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestHandler_Get_NotFound(t *testing.T) {
	h, _, _, _ := minimalHandler(t, true)
	_, err := h.Get(context.Background(), &vpcv1.GetSubnetRequest{SubnetId: ids.NewID(ids.PrefixSubnet)})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestHandler_Get_InvalidIDFormat(t *testing.T) {
	h, _, _, _ := minimalHandler(t, true)
	_, err := h.Get(context.Background(), &vpcv1.GetSubnetRequest{SubnetId: "not-a-real-id"})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestHandler_List_Empty(t *testing.T) {
	h, _, _, _ := minimalHandler(t, true)
	resp, err := h.List(context.Background(), &vpcv1.ListSubnetsRequest{ProjectId: "f1"})
	require.NoError(t, err)
	assert.Empty(t, resp.Subnets)
}

func TestHandler_List_RequiresFolder(t *testing.T) {
	h, _, _, _ := minimalHandler(t, true)
	_, err := h.List(context.Background(), &vpcv1.ListSubnetsRequest{ProjectId: ""})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestHandler_Update_InvalidArg(t *testing.T) {
	h, _, _, _ := minimalHandler(t, true)
	_, err := h.Update(context.Background(), &vpcv1.UpdateSubnetRequest{SubnetId: ""})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestHandler_Delete_InvalidArg(t *testing.T) {
	h, _, _, _ := minimalHandler(t, true)
	_, err := h.Delete(context.Background(), &vpcv1.DeleteSubnetRequest{SubnetId: ""})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestHandler_Move_RequiresID(t *testing.T) {
	h, _, _, _ := minimalHandler(t, true)
	_, err := h.Move(context.Background(), &vpcv1.MoveSubnetRequest{SubnetId: ""})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestHandler_AddCidrBlocks_RequiresID(t *testing.T) {
	h, _, _, _ := minimalHandler(t, true)
	_, err := h.AddCidrBlocks(context.Background(), &vpcv1.AddSubnetCidrBlocksRequest{SubnetId: ""})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestHandler_RemoveCidrBlocks_RequiresID(t *testing.T) {
	h, _, _, _ := minimalHandler(t, true)
	_, err := h.RemoveCidrBlocks(context.Background(), &vpcv1.RemoveSubnetCidrBlocksRequest{SubnetId: ""})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestHandler_Relocate_RequiresID(t *testing.T) {
	h, _, _, _ := minimalHandler(t, true)
	_, err := h.Relocate(context.Background(), &vpcv1.RelocateSubnetRequest{SubnetId: ""})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestHandler_ListUsedAddresses_RequiresID(t *testing.T) {
	h, _, _, _ := minimalHandler(t, true)
	_, err := h.ListUsedAddresses(context.Background(), &vpcv1.ListUsedAddressesRequest{SubnetId: ""})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestHandler_ListOperations_RequiresID(t *testing.T) {
	h, _, _, _ := minimalHandler(t, true)
	_, err := h.ListOperations(context.Background(), &vpcv1.ListSubnetOperationsRequest{SubnetId: ""})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

// ---- use-case-level (Create) ----

func TestCreateUseCase_ValidationError(t *testing.T) {
	kr := kachomock.NewRepository()
	or := repomock.NewOpsRepo()
	uc := NewCreateSubnetUseCase(kr, &repomock.ProjectClient{OK: true},
		repomock.NewZoneRegistry(testZone), or)

	// project_id required.
	netID := ids.NewID(ids.PrefixNetwork)
	_, err := uc.Execute(context.Background(), domain.Subnet{NetworkID: netID, ZoneID: testZone})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())

	// network_id required (empty + invalid id format).
	_, err = uc.Execute(context.Background(), domain.Subnet{
		ProjectID: "f1", NetworkID: "", ZoneID: testZone,
	})
	require.Error(t, err)

	// zone_id required.
	_, err = uc.Execute(context.Background(), domain.Subnet{
		ProjectID: "f1", NetworkID: netID, ZoneID: "",
	})
	require.Error(t, err)
	st, _ = status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())

	// unknown zone.
	_, err = uc.Execute(context.Background(), domain.Subnet{
		ProjectID: "f1", NetworkID: netID, ZoneID: "ru-central1-z",
	})
	require.Error(t, err)
	st, _ = status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())

	// host-bits != 0 → InvalidArgument.
	_, err = uc.Execute(context.Background(), domain.Subnet{
		ProjectID: "f1", NetworkID: netID, ZoneID: testZone,
		V4CidrBlocks: []string{"10.0.0.5/24"},
	})
	require.Error(t, err)
	st, _ = status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())

	// /29 → InvalidArgument "Illegal argument Invalid network prefix /29".
	_, err = uc.Execute(context.Background(), domain.Subnet{
		ProjectID: "f1", NetworkID: netID, ZoneID: testZone,
		V4CidrBlocks: []string{"10.0.0.0/29"},
	})
	require.Error(t, err)
	st, _ = status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

// TestCreateUseCase_FolderNotFound — KAC-94 / skill evgeniy I.4: sync
// folder.Exists precheck удалён (race-prone). Verbatim-YC NotFound теперь
// возвращается через `operation.error` из async `doCreate`, не через
// sync-status. Поэтому: Execute → не ошибка; AwaitOpDone → Operation.Done=true
// с Error.Code == NotFound.
func TestCreateUseCase_FolderNotFound(t *testing.T) {
	kr := kachomock.NewRepository()
	or := repomock.NewOpsRepo()
	uc := NewCreateSubnetUseCase(kr, &repomock.ProjectClient{OK: false},
		repomock.NewZoneRegistry(testZone), or)

	netID := ids.NewID(ids.PrefixNetwork)
	seedNetwork(t, kr, "f1", netID)

	op, err := uc.Execute(context.Background(), domain.Subnet{
		ProjectID: "f1", NetworkID: netID, ZoneID: testZone,
		Name: domain.RcNameVPC("sub1"),
	})
	require.NoError(t, err)
	require.NotEmpty(t, op.ID)

	saved := repomock.AwaitOpDone(t, or, op.ID)
	require.True(t, saved.Done)
	require.NotNil(t, saved.Error, "operation should fail in worker — folder missing")
	assert.Equal(t, int32(codes.NotFound), saved.Error.Code)
}

func TestCreateUseCase_NetworkNotFound(t *testing.T) {
	kr := kachomock.NewRepository()
	or := repomock.NewOpsRepo()
	uc := NewCreateSubnetUseCase(kr, &repomock.ProjectClient{OK: true},
		repomock.NewZoneRegistry(testZone), or)

	_, err := uc.Execute(context.Background(), domain.Subnet{
		ProjectID: "f1", NetworkID: ids.NewID(ids.PrefixNetwork), ZoneID: testZone,
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestCreateUseCase_OK(t *testing.T) {
	h, or, kr, netID := minimalHandler(t, true)

	op, err := h.Create(context.Background(), &vpcv1.CreateSubnetRequest{
		ProjectId:    "f1",
		NetworkId:    netID,
		Name:         "sub1",
		ZoneId:       testZone,
		V4CidrBlocks: []string{"10.0.0.0/24"},
	})
	require.NoError(t, err)
	require.NotEmpty(t, op.Id)

	saved := repomock.AwaitOpDone(t, or, op.Id)
	assert.True(t, saved.Done)
	assert.Nil(t, saved.Error)

	// Verify Subnet committed в kachomock state.
	subs := kr.Subnets()
	require.Len(t, subs, 1)
	assert.Equal(t, "sub1", string(subs[0].Name))

	// Outbox: Subnet.CREATED event.
	events := kr.Outbox()
	require.GreaterOrEqual(t, len(events), 1)
	hasSubCreate := false
	for _, e := range events {
		if e.Resource == "Subnet" && e.Action == "CREATED" {
			hasSubCreate = true
		}
	}
	assert.True(t, hasSubCreate, "Subnet.CREATED outbox event expected")
}

func TestCreateUseCase_DuplicateName(t *testing.T) {
	h, or, _, netID := minimalHandler(t, true)

	// Первый Create — OK.
	op1, err := h.Create(context.Background(), &vpcv1.CreateSubnetRequest{
		ProjectId: "f1", NetworkId: netID, Name: "dup", ZoneId: testZone,
		V4CidrBlocks: []string{"10.0.0.0/24"},
	})
	require.NoError(t, err)
	repomock.AwaitOpDone(t, or, op1.Id)

	// Второй Create с тем же name — sync AlreadyExists.
	_, err = h.Create(context.Background(), &vpcv1.CreateSubnetRequest{
		ProjectId: "f1", NetworkId: netID, Name: "dup", ZoneId: testZone,
		V4CidrBlocks: []string{"10.0.1.0/24"},
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.AlreadyExists, st.Code())
}

// ---- use-case-level (Update) ----

func TestUpdateUseCase_ImmutableNetworkID(t *testing.T) {
	uc := NewUpdateSubnetUseCase(kachomock.NewRepository(), repomock.NewOpsRepo())
	_, err := uc.Execute(context.Background(), UpdateInput{
		SubnetID:   ids.NewID(ids.PrefixSubnet),
		UpdateMask: []string{"network_id"},
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestUpdateUseCase_ImmutableZoneID(t *testing.T) {
	uc := NewUpdateSubnetUseCase(kachomock.NewRepository(), repomock.NewOpsRepo())
	_, err := uc.Execute(context.Background(), UpdateInput{
		SubnetID:   ids.NewID(ids.PrefixSubnet),
		UpdateMask: []string{"zone_id"},
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestUpdateUseCase_UnknownMask(t *testing.T) {
	uc := NewUpdateSubnetUseCase(kachomock.NewRepository(), repomock.NewOpsRepo())
	_, err := uc.Execute(context.Background(), UpdateInput{
		SubnetID:   ids.NewID(ids.PrefixSubnet),
		UpdateMask: []string{"unknown_field"},
	})
	require.Error(t, err)
}

// ---- use-case-level (Delete) ----

func TestDeleteUseCase_InvalidArg(t *testing.T) {
	uc := NewDeleteSubnetUseCase(kachomock.NewRepository(), nil, repomock.NewOpsRepo())
	_, err := uc.Execute(context.Background(), "")
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

// ---- use-case-level (Move) ----

func TestMoveUseCase_Validates(t *testing.T) {
	uc := NewMoveSubnetUseCase(kachomock.NewRepository(), &repomock.ProjectClient{OK: true}, repomock.NewOpsRepo())
	_, err := uc.Execute(context.Background(), "", "f2")
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())

	_, err = uc.Execute(context.Background(), ids.NewID(ids.PrefixSubnet), "")
	st, _ = status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

// ---- use-case-level (List) ----

func TestListUseCase_RequiresFolder(t *testing.T) {
	uc := NewListSubnetsUseCase(kachomock.NewRepository(), nil)
	_, _, err := uc.Execute(context.Background(), "", SubnetFilter{}, Pagination{})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestListOperationsUseCase_UnknownID_Empty(t *testing.T) {
	// История операций должна оставаться доступной после Delete.
	uc := NewListOperationsUseCase(repomock.NewOpsRepo())
	ops, _, err := uc.Execute(context.Background(), ids.NewID(ids.PrefixSubnet), Pagination{})
	assert.NoError(t, err)
	assert.Empty(t, ops)
}

// ---- use-case-level (AddCidrBlocks) ----

func TestAddCidrBlocksUseCase_RequiresAny(t *testing.T) {
	uc := NewAddCidrBlocksUseCase(kachomock.NewRepository(), repomock.NewOpsRepo())
	_, err := uc.Execute(context.Background(), ids.NewID(ids.PrefixSubnet), nil, nil)
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestAddCidrBlocksUseCase_BadV4(t *testing.T) {
	uc := NewAddCidrBlocksUseCase(kachomock.NewRepository(), repomock.NewOpsRepo())
	_, err := uc.Execute(context.Background(), ids.NewID(ids.PrefixSubnet), []string{"10.0.0.5/24"}, nil)
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

// ---- use-case-level (RemoveCidrBlocks) ----

func TestRemoveCidrBlocksUseCase_RequiresAny(t *testing.T) {
	uc := NewRemoveCidrBlocksUseCase(kachomock.NewRepository(), repomock.NewOpsRepo())
	_, err := uc.Execute(context.Background(), ids.NewID(ids.PrefixSubnet), nil, nil)
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

// ---- use-case-level (Relocate) ----

func TestRelocateUseCase_AlwaysFailedPrecondition(t *testing.T) {
	// Verbatim YC: Relocate ВСЕГДА отвергается с FAILED_PRECONDITION "Invalid
	// subnet state" — даже для свежей подсети.
	kr := kachomock.NewRepository()
	zr := repomock.NewZoneRegistry(testZone)

	netID := ids.NewID(ids.PrefixNetwork)
	subID := ids.NewID(ids.PrefixSubnet)
	// Seed Subnet через committed writer.
	ctx := context.Background()
	w, err := kr.Writer(ctx)
	require.NoError(t, err)
	_, err = w.Subnets().Insert(ctx, &domain.Subnet{
		ID: subID, ProjectID: "f1", NetworkID: netID, ZoneID: testZone,
	})
	require.NoError(t, err)
	require.NoError(t, w.Commit())

	uc := NewRelocateUseCase(kr, zr)
	op, err := uc.Execute(context.Background(), subID, testZone)
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
	assert.Nil(t, op, "Operation must NOT be created when Relocate sync-fails")
}

// ---- use-case-level (ListUsedAddresses) ----

func TestListUsedAddressesUseCase_RequiresExistence(t *testing.T) {
	uc := NewListUsedAddressesUseCase(kachomock.NewRepository(), nil)
	// Несуществующий id → NotFound (через repo.Get).
	_, _, _, err := uc.Execute(context.Background(), ids.NewID(ids.PrefixSubnet), Pagination{})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

// ---- Handler happy-path ----

func TestHandler_FullFlow(t *testing.T) {
	h, or, _, netID := minimalHandler(t, true)

	// Create
	createOp, err := h.Create(context.Background(), &vpcv1.CreateSubnetRequest{
		ProjectId: "f1", NetworkId: netID, Name: "sub1", ZoneId: testZone,
		V4CidrBlocks: []string{"10.0.0.0/24"},
	})
	require.NoError(t, err)
	repomock.AwaitOpDone(t, or, createOp.Id)

	// List
	resp, err := h.List(context.Background(), &vpcv1.ListSubnetsRequest{ProjectId: "f1"})
	require.NoError(t, err)
	require.NotEmpty(t, resp.Subnets)
	subID := resp.Subnets[0].Id

	// Get
	got, err := h.Get(context.Background(), &vpcv1.GetSubnetRequest{SubnetId: subID})
	require.NoError(t, err)
	assert.Equal(t, "sub1", got.Name)

	// Update
	updOp, err := h.Update(context.Background(), &vpcv1.UpdateSubnetRequest{
		SubnetId: subID, Name: "sub-upd",
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"name"}},
	})
	require.NoError(t, err)
	repomock.AwaitOpDone(t, or, updOp.Id)

	got, _ = h.Get(context.Background(), &vpcv1.GetSubnetRequest{SubnetId: subID})
	assert.Equal(t, "sub-upd", got.Name)

	// AddCidrBlocks
	addOp, err := h.AddCidrBlocks(context.Background(), &vpcv1.AddSubnetCidrBlocksRequest{
		SubnetId:     subID,
		V4CidrBlocks: []string{"10.1.0.0/24"},
	})
	require.NoError(t, err)
	repomock.AwaitOpDone(t, or, addOp.Id)

	// RemoveCidrBlocks
	rmOp, err := h.RemoveCidrBlocks(context.Background(), &vpcv1.RemoveSubnetCidrBlocksRequest{
		SubnetId:     subID,
		V4CidrBlocks: []string{"10.1.0.0/24"},
	})
	require.NoError(t, err)
	repomock.AwaitOpDone(t, or, rmOp.Id)

	// ListOperations
	_, err = h.ListOperations(context.Background(), &vpcv1.ListSubnetOperationsRequest{SubnetId: subID})
	require.NoError(t, err)

	// ListUsedAddresses (пустой результат — нет адресов в mock'е)
	_, err = h.ListUsedAddresses(context.Background(), &vpcv1.ListUsedAddressesRequest{SubnetId: subID})
	require.NoError(t, err)

	// Move
	moveOp, err := h.Move(context.Background(), &vpcv1.MoveSubnetRequest{SubnetId: subID, DestinationProjectId: ids.NewID(ids.PrefixFolder)})
	require.NoError(t, err)
	repomock.AwaitOpDone(t, or, moveOp.Id)

	// Relocate — всегда FailedPrecondition
	_, err = h.Relocate(context.Background(), &vpcv1.RelocateSubnetRequest{SubnetId: subID, DestinationZoneId: testZone})
	require.Error(t, err)

	// Delete (sub в f2 теперь — owner был f1 при Get'е выше; в этом тесте
	// AssertFolderOwnership не запрещает: см. minimalHandler — context без tenant).
	delOp, err := h.Delete(context.Background(), &vpcv1.DeleteSubnetRequest{SubnetId: subID})
	require.NoError(t, err)
	repomock.AwaitOpDone(t, or, delOp.Id)
}

func TestHandler_Delete_ResponseIsEmpty(t *testing.T) {
	// Operation.response для Delete должен быть google.protobuf.Empty
	// (proto-options contract — защита от регрессии).
	h, or, _, netID := minimalHandler(t, true)

	createOp, err := h.Create(context.Background(), &vpcv1.CreateSubnetRequest{
		ProjectId: "f1", NetworkId: netID, Name: "del-resp-test", ZoneId: testZone,
		V4CidrBlocks: []string{"10.0.0.0/24"},
	})
	require.NoError(t, err)
	repomock.AwaitOpDone(t, or, createOp.Id)

	resp, _ := h.List(context.Background(), &vpcv1.ListSubnetsRequest{ProjectId: "f1"})
	require.Len(t, resp.Subnets, 1)

	delOp, err := h.Delete(context.Background(), &vpcv1.DeleteSubnetRequest{SubnetId: resp.Subnets[0].Id})
	require.NoError(t, err)
	saved := repomock.AwaitOpDone(t, or, delOp.Id)
	require.Nil(t, saved.Error)
	require.NotNil(t, saved.Response)

	var empty emptypb.Empty
	err = saved.Response.UnmarshalTo(&empty)
	require.NoError(t, err, "Delete response must be google.protobuf.Empty (proto-options contract)")
}

func TestSubnetToPb_RoundTrip(t *testing.T) {
	rec := &kachorepo.SubnetRecord{
		Subnet: domain.Subnet{
			ID:           "s-1",
			ProjectID:    "f1",
			Name:         domain.RcNameVPC("sub1"),
			Description:  domain.RcDescription("desc"),
			Labels:       domain.LabelsFromMap(map[string]string{"env": "prod"}),
			NetworkID:    "n-1",
			ZoneID:       testZone,
			V4CidrBlocks: []string{"10.0.0.0/24"},
		},
	}
	p, err := subnetToPb(rec)
	require.NoError(t, err)
	assert.Equal(t, "s-1", p.Id)
	assert.Equal(t, "sub1", p.Name)
}
