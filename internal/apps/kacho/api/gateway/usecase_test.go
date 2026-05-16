package gateway

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
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/kachomock"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/repomock"
)

// Тесты Gateway use-case'ов и handler'а. Wave 3b (KAC-94): сюда переехали
// прежние тесты `internal/handler/coverage*_test.go::Test{GatewayHandler_*,
// GatewayToProto_*}` и `internal/service/coverage2_test.go::Test{GatewayService_*}`.
//
// Wave 5 replicate (KAC-94, skill evgeniy §6 G.1-G.7): use-case-тесты работают
// через `kachomock.Repository` (in-memory CQRS-mock) вместо узкого
// `repomock.GatewayRepo`. mock поддерживает TX-семантику (Writer accumulate'ит
// в local map, Commit flush'ит в parent state) — это позволяет проверять
// корректность outbox-emit-в-той-же-TX без реального Postgres.

func makeHandler(t *testing.T,
	kr *kachomock.Repository,
	or *repomock.OpsRepo,
	fc *repomock.FolderClient,
) *Handler {
	t.Helper()
	create := NewCreateGatewayUseCase(kr, fc, or)
	update := NewUpdateGatewayUseCase(kr, or)
	deleteUC := NewDeleteGatewayUseCase(kr, or)
	move := NewMoveGatewayUseCase(kr, fc, or)
	get := NewGetGatewayUseCase(kr)
	list := NewListGatewaysUseCase(kr)
	listOps := NewListOperationsUseCase(or)
	return NewHandler(create, update, deleteUC, move, get, list, listOps)
}

func minimalHandler(t *testing.T, folderOK bool) (*Handler, *repomock.OpsRepo, *kachomock.Repository) {
	t.Helper()
	kr := kachomock.NewRepository()
	or := repomock.NewOpsRepo()
	fc := &repomock.FolderClient{OK: folderOK}
	return makeHandler(t, kr, or, fc), or, kr
}

// ---- Handler — sync paths ----

func TestHandler_Get_InvalidArg(t *testing.T) {
	h, _, _ := minimalHandler(t, true)
	_, err := h.Get(context.Background(), &vpcv1.GetGatewayRequest{GatewayId: ""})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestHandler_Get_NotFound(t *testing.T) {
	h, _, _ := minimalHandler(t, true)
	_, err := h.Get(context.Background(), &vpcv1.GetGatewayRequest{GatewayId: ids.NewID(ids.PrefixGateway)})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestHandler_List_Empty(t *testing.T) {
	h, _, _ := minimalHandler(t, true)
	resp, err := h.List(context.Background(), &vpcv1.ListGatewaysRequest{FolderId: "f1"})
	require.NoError(t, err)
	assert.Empty(t, resp.Gateways)
}

func TestHandler_Update_InvalidArg(t *testing.T) {
	h, _, _ := minimalHandler(t, true)
	_, err := h.Update(context.Background(), &vpcv1.UpdateGatewayRequest{GatewayId: ""})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestHandler_Delete_InvalidArg(t *testing.T) {
	h, _, _ := minimalHandler(t, true)
	_, err := h.Delete(context.Background(), &vpcv1.DeleteGatewayRequest{GatewayId: ""})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestHandler_Move_RequiresID(t *testing.T) {
	h, _, _ := minimalHandler(t, true)
	_, err := h.Move(context.Background(), &vpcv1.MoveGatewayRequest{GatewayId: ""})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestHandler_ListOperations_RequiresID(t *testing.T) {
	h, _, _ := minimalHandler(t, true)
	_, err := h.ListOperations(context.Background(), &vpcv1.ListGatewayOperationsRequest{GatewayId: ""})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

// ---- use-case-level ----

func TestCreateUseCase_ValidationError(t *testing.T) {
	kr := kachomock.NewRepository()
	or := repomock.NewOpsRepo()
	uc := NewCreateGatewayUseCase(kr, &repomock.FolderClient{OK: true}, or)

	// folder_id required.
	_, err := uc.Execute(context.Background(), CreateInput{Gateway: domain.Gateway{Name: "gw1", GatewayType: domain.GatewayTypeSharedEgress}})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())

	// Bad name (strict NameGateway rejects uppercase).
	_, err = uc.Execute(context.Background(), CreateInput{Gateway: domain.Gateway{
		FolderID:    "f1",
		Name:        domain.RcNameVPC("BadCaps"),
		GatewayType: domain.GatewayTypeSharedEgress,
	}})
	require.Error(t, err)
	st, _ = status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())

	// Missing gateway_type.
	_, err = uc.Execute(context.Background(), CreateInput{Gateway: domain.Gateway{
		FolderID: "f1",
		Name:     domain.RcNameVPC("gw1"),
	}})
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
	uc := NewCreateGatewayUseCase(kr, &repomock.FolderClient{OK: false}, or)

	op, err := uc.Execute(context.Background(), CreateInput{Gateway: domain.Gateway{
		FolderID:    "f1",
		Name:        domain.RcNameVPC("gw1"),
		GatewayType: domain.GatewayTypeSharedEgress,
	}})
	require.NoError(t, err)
	require.NotEmpty(t, op.ID)

	saved := repomock.AwaitOpDone(t, or, op.ID)
	require.True(t, saved.Done)
	require.NotNil(t, saved.Error, "operation should fail in worker — folder missing")
	assert.Equal(t, int32(codes.NotFound), saved.Error.Code)
}

func TestCreateUseCase_OK(t *testing.T) {
	kr := kachomock.NewRepository()
	or := repomock.NewOpsRepo()
	uc := NewCreateGatewayUseCase(kr, &repomock.FolderClient{OK: true}, or)

	op, err := uc.Execute(context.Background(), CreateInput{Gateway: domain.Gateway{
		FolderID:    "f1",
		Name:        domain.RcNameVPC("gw1"),
		Description: domain.RcDescription("desc"),
		GatewayType: domain.GatewayTypeSharedEgress,
	}})
	require.NoError(t, err)
	require.NotEmpty(t, op.ID)

	saved := repomock.AwaitOpDone(t, or, op.ID)
	assert.True(t, saved.Done)
	assert.Nil(t, saved.Error)

	// kachomock buffer'ит outbox-event'ы и публикует их по Commit. Проверяем,
	// что Gateway.CREATED был эмитирован (atomicity DML + outbox в одной
	// writer-TX — G.5).
	events := kr.Outbox()
	require.Len(t, events, 1)
	assert.Equal(t, "Gateway", events[0].Resource)
	assert.Equal(t, "CREATED", events[0].Action)
}

func TestDeleteUseCase_InvalidArg(t *testing.T) {
	uc := NewDeleteGatewayUseCase(kachomock.NewRepository(), repomock.NewOpsRepo())
	_, err := uc.Execute(context.Background(), "")
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestMoveUseCase_Validates(t *testing.T) {
	uc := NewMoveGatewayUseCase(kachomock.NewRepository(), &repomock.FolderClient{OK: true}, repomock.NewOpsRepo())
	_, err := uc.Execute(context.Background(), "", "f2")
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	_, err = uc.Execute(context.Background(), ids.NewID(ids.PrefixGateway), "")
	st, _ = status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestListUseCase_RequiresFolder(t *testing.T) {
	uc := NewListGatewaysUseCase(kachomock.NewRepository())
	_, _, err := uc.Execute(context.Background(), GatewayFilter{}, Pagination{})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestListOperationsUseCase_UnknownID_Empty(t *testing.T) {
	// История операций должна оставаться доступной после Delete.
	uc := NewListOperationsUseCase(repomock.NewOpsRepo())
	ops, _, err := uc.Execute(context.Background(), ids.NewID(ids.PrefixGateway), Pagination{})
	assert.NoError(t, err)
	assert.Empty(t, ops)
}

// ---- Handler happy-path ----

func TestHandler_Create_OK(t *testing.T) {
	h, or, _ := minimalHandler(t, true)
	op, err := h.Create(context.Background(), &vpcv1.CreateGatewayRequest{
		FolderId: "f1",
		Name:     "gw1",
		Gateway:  &vpcv1.CreateGatewayRequest_SharedEgressGatewaySpec{SharedEgressGatewaySpec: &vpcv1.SharedEgressGatewaySpec{}},
	})
	require.NoError(t, err)
	assert.NotEmpty(t, op.Id)
	saved := repomock.AwaitOpDone(t, or, op.Id)
	assert.True(t, saved.Done)
}

func TestHandler_Delete_ResponseIsEmpty(t *testing.T) {
	// Operation.response для Delete должен быть google.protobuf.Empty
	// (proto-options contract — защита от регрессии).
	h, or, _ := minimalHandler(t, true)

	createOp, err := h.Create(context.Background(), &vpcv1.CreateGatewayRequest{
		FolderId: "f1", Name: "del-resp-test",
		Gateway: &vpcv1.CreateGatewayRequest_SharedEgressGatewaySpec{SharedEgressGatewaySpec: &vpcv1.SharedEgressGatewaySpec{}},
	})
	require.NoError(t, err)
	repomock.AwaitOpDone(t, or, createOp.Id)

	resp, _ := h.List(context.Background(), &vpcv1.ListGatewaysRequest{FolderId: "f1"})
	require.Len(t, resp.Gateways, 1)

	delOp, err := h.Delete(context.Background(), &vpcv1.DeleteGatewayRequest{GatewayId: resp.Gateways[0].Id})
	require.NoError(t, err)
	saved := repomock.AwaitOpDone(t, or, delOp.Id)
	require.Nil(t, saved.Error)
	require.NotNil(t, saved.Response)

	var empty emptypb.Empty
	err = saved.Response.UnmarshalTo(&empty)
	require.NoError(t, err, "Delete response must be google.protobuf.Empty (proto-options contract)")
}

func TestHandler_FullFlow(t *testing.T) {
	h, or, _ := minimalHandler(t, true)

	createOp, err := h.Create(context.Background(), &vpcv1.CreateGatewayRequest{
		FolderId: "f1", Name: "gw1",
		Gateway: &vpcv1.CreateGatewayRequest_SharedEgressGatewaySpec{SharedEgressGatewaySpec: &vpcv1.SharedEgressGatewaySpec{}},
	})
	require.NoError(t, err)
	repomock.AwaitOpDone(t, or, createOp.Id)

	resp, _ := h.List(context.Background(), &vpcv1.ListGatewaysRequest{FolderId: "f1"})
	require.NotEmpty(t, resp.Gateways)
	gwID := resp.Gateways[0].Id

	updOp, err := h.Update(context.Background(), &vpcv1.UpdateGatewayRequest{
		GatewayId: gwID, Name: "gw-upd",
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"name"}},
	})
	require.NoError(t, err)
	repomock.AwaitOpDone(t, or, updOp.Id)

	got, _ := h.Get(context.Background(), &vpcv1.GetGatewayRequest{GatewayId: gwID})
	assert.Equal(t, "gw-upd", got.Name)

	_, err = h.ListOperations(context.Background(), &vpcv1.ListGatewayOperationsRequest{GatewayId: gwID})
	require.NoError(t, err)

	moveOp, err := h.Move(context.Background(), &vpcv1.MoveGatewayRequest{GatewayId: gwID, DestinationFolderId: ids.NewID(ids.PrefixFolder)})
	require.NoError(t, err)
	repomock.AwaitOpDone(t, or, moveOp.Id)

	delOp, err := h.Delete(context.Background(), &vpcv1.DeleteGatewayRequest{GatewayId: gwID})
	require.NoError(t, err)
	repomock.AwaitOpDone(t, or, delOp.Id)
}

func TestUpdateUseCase_BadName(t *testing.T) {
	kr := kachomock.NewRepository()
	or := repomock.NewOpsRepo()
	uc := NewUpdateGatewayUseCase(kr, or)
	_, err := uc.Execute(context.Background(), UpdateInput{
		GatewayID:  ids.NewID(ids.PrefixGateway),
		Gateway:    domain.Gateway{Name: domain.RcNameVPC("BadCaps")},
		UpdateMask: []string{"name"},
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestUpdateUseCase_UnknownMask(t *testing.T) {
	kr := kachomock.NewRepository()
	or := repomock.NewOpsRepo()
	uc := NewUpdateGatewayUseCase(kr, or)
	_, err := uc.Execute(context.Background(), UpdateInput{
		GatewayID:  ids.NewID(ids.PrefixGateway),
		UpdateMask: []string{"unknown_field"},
	})
	require.Error(t, err)
}

func TestGatewayToPb_SharedEgress(t *testing.T) {
	rec := &kacho.GatewayRecord{
		Gateway: domain.Gateway{
			ID:          "gw-1",
			FolderID:    "f1",
			Name:        domain.RcNameVPC("gw1"),
			Description: domain.RcDescription("desc"),
			Labels:      domain.LabelsFromMap(map[string]string{"env": "prod"}),
			GatewayType: domain.GatewayTypeSharedEgress,
		},
	}
	p, err := gatewayToPb(rec)
	require.NoError(t, err)
	assert.Equal(t, "gw-1", p.Id)
	assert.NotNil(t, p.GetSharedEgressGateway())
}
