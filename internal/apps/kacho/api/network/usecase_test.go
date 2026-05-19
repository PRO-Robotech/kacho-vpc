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
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/kachomock"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/repomock"
)

// Тесты Network use-case'ов и handler'а. Wave 3a (KAC-94): сюда переехали
// прежние тесты `internal/service/network_test.go`,
// `internal/service/coverage_test.go::Test{NetworkService,…}` и
// `internal/handler/{handler,coverage,coverage2}_test.go::TestNetworkHandler_*`
// (последние теперь — против `*networkapp.Handler`).
//
// Wave 5 pilot (KAC-94): Network use-case'ы переехали на CQRS-Repository.
// Network-mock — `kachomock.NewRepository()` (in-memory CQRS-impl с TX-семантикой
// и outbox-буфером). Остальные ресурсы (Subnet/RouteTable/SecurityGroup/...) —
// пока legacy `repomock.*` (replicate-фаза).

// ---- builders ----

func makeHandler(t *testing.T,
	kr *kachomock.Repository,
	sr *repomock.SubnetRepo,
	rtr *repomock.RouteTableRepo,
	sgr *repomock.SecurityGroupRepo,
	or *repomock.OpsRepo,
	fc *repomock.ProjectClient,
	defaultSGInline bool,
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
	create := NewCreateNetworkUseCase(kr, fc, or, defaultSGInline)
	update := NewUpdateNetworkUseCase(kr, or)
	deleteUC := NewDeleteNetworkUseCase(kr, sReader, rtReader, sgRepoIface, or)
	move := NewMoveNetworkUseCase(kr, fc, or)
	get := NewGetNetworkUseCase(kr)
	list := NewListNetworksUseCase(kr, nil) // KAC-127: authz nil → legacy unfiltered list path
	listSub := NewListSubnetsUseCase(kr, sReader)
	listSG := NewListSecurityGroupsUseCase(kr, sgRepoIface)
	listRT := NewListRouteTablesUseCase(kr, rtReader)
	listOps := NewListOperationsUseCase(or)
	return NewHandler(create, update, deleteUC, move, get, list, listSub, listSG, listRT, listOps)
}

// folder ok / ops repo / network repo с минимальной wiring — для тестов где
// child-reader'ы не требуются. defaultSGInline=false — без default-SG creation.
func minimalHandler(t *testing.T, folderOK bool) (*Handler, *repomock.OpsRepo, *kachomock.Repository) {
	t.Helper()
	kr := kachomock.NewRepository()
	or := repomock.NewOpsRepo()
	fc := &repomock.ProjectClient{OK: folderOK}
	return makeHandler(t, kr, nil, nil, nil, or, fc, false), or, kr
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
	resp, err := h.List(context.Background(), &vpcv1.ListNetworksRequest{ProjectId: "f1"})
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
	kr := kachomock.NewRepository()
	or := repomock.NewOpsRepo()
	uc := NewCreateNetworkUseCase(kr, &repomock.ProjectClient{OK: true}, or, false)

	// project_id required.
	_, err := uc.Execute(context.Background(), domain.Network{Name: "test"})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())

	// invalid name (starts with digit, NameVPC permissive но цифра в начале запрещена).
	_, err = uc.Execute(context.Background(), domain.Network{
		ProjectID: "f1",
		Name:      domain.RcNameVPC("1bad"),
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
	uc := NewCreateNetworkUseCase(kr, &repomock.ProjectClient{OK: false}, or, false)

	op, err := uc.Execute(context.Background(), domain.Network{
		ProjectID: "f1",
		Name:      domain.RcNameVPC("net1"),
	})
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
	uc := NewCreateNetworkUseCase(kr, &repomock.ProjectClient{OK: true}, or, false)

	op, err := uc.Execute(context.Background(), domain.Network{
		ProjectID:   "f1",
		Name:        domain.RcNameVPC("net1"),
		Description: domain.RcDescription("desc"),
	})
	require.NoError(t, err)
	require.NotEmpty(t, op.ID)

	saved := repomock.AwaitOpDone(t, or, op.ID)
	assert.True(t, saved.Done)
	assert.Nil(t, saved.Error)
}

// TestCreateUseCase_DefaultSGInline_Atomic — Wave 5 batch 33/34 (KAC-94, skill
// evgeniy I.9 / I.10): при defaultSGInline=true Network.Create в одной writer-TX
// создаёт Network + default-SG + проставляет default_security_group_id. Все три
// DML и три outbox-event'а commit'ятся атомарно.
func TestCreateUseCase_DefaultSGInline_Atomic(t *testing.T) {
	kr := kachomock.NewRepository()
	or := repomock.NewOpsRepo()
	uc := NewCreateNetworkUseCase(kr, &repomock.ProjectClient{OK: true}, or, true)

	op, err := uc.Execute(context.Background(), domain.Network{
		ProjectID: "f1",
		Name:      domain.RcNameVPC("net-with-sg"),
	})
	require.NoError(t, err)
	saved := repomock.AwaitOpDone(t, or, op.ID)
	require.True(t, saved.Done)
	require.Nil(t, saved.Error)

	// Network виден; default_security_group_id заполнен.
	nets := kr.Networks()
	require.Len(t, nets, 1)
	assert.NotEmpty(t, nets[0].DefaultSecurityGroupID, "default_security_group_id должен быть заполнен")

	// SG виден; default-for-network=true; network_id указывает на новую сеть.
	sgs := kr.SecurityGroups()
	require.Len(t, sgs, 1)
	assert.True(t, sgs[0].DefaultForNetwork)
	assert.Equal(t, nets[0].ID, sgs[0].NetworkID)
	assert.Equal(t, sgs[0].ID, nets[0].DefaultSecurityGroupID)

	// Три outbox-события в правильной последовательности: Network.CREATED →
	// SecurityGroup.CREATED → Network.UPDATED.
	events := kr.Outbox()
	require.Len(t, events, 3, "ожидаем 3 outbox-event в одной writer-TX")
	assert.Equal(t, "Network", events[0].Resource)
	assert.Equal(t, "CREATED", events[0].Action)
	assert.Equal(t, "SecurityGroup", events[1].Resource)
	assert.Equal(t, "CREATED", events[1].Action)
	assert.Equal(t, "Network", events[2].Resource)
	assert.Equal(t, "UPDATED", events[2].Action)
}

// TestCreateDefaultSGUseCase_Execute_Composes — фокус-тест выделенного
// `CreateDefaultSGUseCase` (skill evgeniy I.9-residual): use-case работает в
// УЖЕ открытой caller'ом writer-TX, делает Insert(SG) + outbox-emit +
// SetDefaultSGID + outbox-emit и возвращает updated NetworkRecord. Сам TX не
// открывает и не commit'ит — это ответственность caller'а.
func TestCreateDefaultSGUseCase_Execute_Composes(t *testing.T) {
	kr := kachomock.NewRepository()
	ctx := context.Background()

	// Caller-аналог: открыл writer-TX, вставил Network. Это симулирует то, что
	// `CreateNetworkUseCase.doCreate` делает ДО вызова CreateDefaultSGUseCase.
	w, err := kr.Writer(ctx)
	require.NoError(t, err)
	defer w.Abort()

	net := domain.Network{
		ID:        ids.NewID(ids.PrefixNetwork),
		ProjectID: "f1",
		Name:      domain.RcNameVPC("net-for-sg"),
	}
	created, err := w.Networks().Insert(ctx, &net)
	require.NoError(t, err)
	require.NoError(t, w.Outbox().Emit(ctx, "Network", created.ID, "CREATED", map[string]any{}))

	// Сам use-case под тестом.
	uc := NewCreateDefaultSGUseCase()
	upd, err := uc.Execute(ctx, w, created.Network)
	require.NoError(t, err)
	require.NotNil(t, upd)
	assert.NotEmpty(t, upd.DefaultSecurityGroupID, "Execute должен вернуть NetworkRecord с заполненным default_security_group_id")

	// Commit делает caller — без него ни Network, ни SG, ни outbox не видны
	// параллельному reader'у. Это buys atomic-семантику: Abort на любой ошибке
	// откатил бы и Network, и SG, и все 3 outbox-event'а.
	require.NoError(t, w.Commit())

	// Post-commit видимость: 1 Network (с заполненным default_sg_id), 1 SG,
	// 3 outbox-event'а — Network.CREATED (от caller'а) → SecurityGroup.CREATED →
	// Network.UPDATED (две последние эмитирует use-case под тестом).
	nets := kr.Networks()
	require.Len(t, nets, 1)
	assert.Equal(t, upd.ID, nets[0].ID)
	assert.NotEmpty(t, nets[0].DefaultSecurityGroupID)

	sgs := kr.SecurityGroups()
	require.Len(t, sgs, 1)
	assert.True(t, sgs[0].DefaultForNetwork)
	assert.Equal(t, net.ID, sgs[0].NetworkID)
	assert.Equal(t, sgs[0].ID, nets[0].DefaultSecurityGroupID)

	events := kr.Outbox()
	require.Len(t, events, 3)
	assert.Equal(t, "Network", events[0].Resource)
	assert.Equal(t, "CREATED", events[0].Action)
	assert.Equal(t, "SecurityGroup", events[1].Resource)
	assert.Equal(t, "CREATED", events[1].Action)
	assert.Equal(t, "Network", events[2].Resource)
	assert.Equal(t, "UPDATED", events[2].Action)
}

// TestCreateUseCase_DefaultSGInline_OFF — defaultSGInline=false: Network есть,
// SG нет, outbox содержит ровно 1 событие Network.CREATED.
func TestCreateUseCase_DefaultSGInline_OFF(t *testing.T) {
	kr := kachomock.NewRepository()
	or := repomock.NewOpsRepo()
	uc := NewCreateNetworkUseCase(kr, &repomock.ProjectClient{OK: true}, or, false)

	op, err := uc.Execute(context.Background(), domain.Network{
		ProjectID: "f1",
		Name:      domain.RcNameVPC("net-no-sg"),
	})
	require.NoError(t, err)
	saved := repomock.AwaitOpDone(t, or, op.ID)
	require.True(t, saved.Done)
	require.Nil(t, saved.Error)

	assert.Len(t, kr.Networks(), 1)
	assert.Empty(t, kr.SecurityGroups())
	events := kr.Outbox()
	require.Len(t, events, 1)
	assert.Equal(t, "Network", events[0].Resource)
	assert.Equal(t, "CREATED", events[0].Action)
}

func TestDeleteUseCase_InvalidArg(t *testing.T) {
	uc := NewDeleteNetworkUseCase(kachomock.NewRepository(), nil, nil, nil, repomock.NewOpsRepo())
	_, err := uc.Execute(context.Background(), "")
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestMoveUseCase_Validates(t *testing.T) {
	uc := NewMoveNetworkUseCase(kachomock.NewRepository(), &repomock.ProjectClient{OK: true}, repomock.NewOpsRepo())
	_, err := uc.Execute(context.Background(), "", "f2")
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	_, err = uc.Execute(context.Background(), ids.NewID(ids.PrefixNetwork), "")
	st, _ = status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestListUseCase_RequiresFolder(t *testing.T) {
	uc := NewListNetworksUseCase(kachomock.NewRepository(), nil)
	_, _, err := uc.Execute(context.Background(), "", NetworkFilter{}, Pagination{})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestListOperationsUseCase_UnknownID_Empty(t *testing.T) {
	// История операций должна оставаться доступной после Delete — unknown id
	// ≠ NotFound, это пустой список.
	uc := NewListOperationsUseCase(repomock.NewOpsRepo())
	ops, _, err := uc.Execute(context.Background(), ids.NewID(ids.PrefixNetwork), Pagination{})
	assert.NoError(t, err)
	assert.Empty(t, ops)
}

func TestListSubnetsUseCase_NetworkNotFound(t *testing.T) {
	uc := NewListSubnetsUseCase(kachomock.NewRepository(), repomock.NewSubnetRepo())
	_, _, err := uc.Execute(context.Background(), ids.NewID(ids.PrefixNetwork), Pagination{})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestListSecurityGroupsUseCase_NetworkNotFound(t *testing.T) {
	uc := NewListSecurityGroupsUseCase(kachomock.NewRepository(), repomock.NewSecurityGroupRepo())
	_, _, err := uc.Execute(context.Background(), ids.NewID(ids.PrefixNetwork), Pagination{})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestListRouteTablesUseCase_NetworkNotFound(t *testing.T) {
	uc := NewListRouteTablesUseCase(kachomock.NewRepository(), repomock.NewRouteTableRepo())
	_, _, err := uc.Execute(context.Background(), ids.NewID(ids.PrefixNetwork), Pagination{})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

// ---- Handler — happy-path Create / List / Update / Delete ----

func TestHandler_Create_OK(t *testing.T) {
	h, or, _ := minimalHandler(t, true)
	op, err := h.Create(context.Background(), &vpcv1.CreateNetworkRequest{
		ProjectId: "f1",
		Name:      "net1",
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

	createOp, err := h.Create(context.Background(), &vpcv1.CreateNetworkRequest{ProjectId: "f1", Name: "del-resp-test"})
	require.NoError(t, err)
	repomock.AwaitOpDone(t, or, createOp.Id)

	resp, _ := h.List(context.Background(), &vpcv1.ListNetworksRequest{ProjectId: "f1"})
	require.Len(t, resp.Networks, 1)

	delOp, err := h.Delete(context.Background(), &vpcv1.DeleteNetworkRequest{NetworkId: resp.Networks[0].Id})
	require.NoError(t, err)
	saved := repomock.AwaitOpDone(t, or, delOp.Id)
	require.Nil(t, saved.Error)
	require.NotNil(t, saved.Response)

	var empty emptypb.Empty
	err = saved.Response.UnmarshalTo(&empty)
	require.NoError(t, err, "Delete response must be google.protobuf.Empty (proto-options contract)")
}

func TestHandler_Update_MaskApplication(t *testing.T) {
	h, or, _ := minimalHandler(t, true)
	// Создаём сеть
	createOp, err := h.Create(context.Background(), &vpcv1.CreateNetworkRequest{ProjectId: "f1", Name: "n1"})
	require.NoError(t, err)
	savedOp := repomock.AwaitOpDone(t, or, createOp.Id)
	require.NotNil(t, savedOp.Metadata)

	resp, _ := h.List(context.Background(), &vpcv1.ListNetworksRequest{ProjectId: "f1"})
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
	savedUpdOp := repomock.AwaitOpDone(t, or, updOp.Id)
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
	kr := kachomock.NewRepository()
	or := repomock.NewOpsRepo()
	sr := repomock.NewSubnetRepo()
	rtr := repomock.NewRouteTableRepo()
	h := makeHandler(t, kr, sr, rtr, nil, or, &repomock.ProjectClient{OK: true}, false)

	createOp, err := h.Create(context.Background(), &vpcv1.CreateNetworkRequest{ProjectId: "f1", Name: "n"})
	require.NoError(t, err)
	repomock.AwaitOpDone(t, or, createOp.Id)

	resp, _ := h.List(context.Background(), &vpcv1.ListNetworksRequest{ProjectId: "f1"})
	require.Len(t, resp.Networks, 1)
	netID := resp.Networks[0].Id

	updOp, err := h.Update(context.Background(), &vpcv1.UpdateNetworkRequest{
		NetworkId: netID, Name: "n-upd",
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"name"}},
	})
	require.NoError(t, err)
	repomock.AwaitOpDone(t, or, updOp.Id)
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
		NetworkId: netID, DestinationProjectId: ids.NewID(ids.PrefixFolder),
	})
	require.NoError(t, err)
	repomock.AwaitOpDone(t, or, moveOp.Id)

	// Delete (без child-resources)
	delOp, err := h.Delete(context.Background(), &vpcv1.DeleteNetworkRequest{NetworkId: netID})
	require.NoError(t, err)
	repomock.AwaitOpDone(t, or, delOp.Id)
}
