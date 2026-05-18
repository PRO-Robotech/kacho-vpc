package privateendpoint

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	pepb "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1/privatelink"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/kachomock"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/repomock"
)

// Тесты PrivateEndpoint use-case'ов и handler'а. Wave 3b (KAC-94).
//
// Wave 5 replicate (KAC-94): PE use-case'ы переехали на CQRS-Repository
// (kachomock.Repository). NetworkReader / SubnetReader для request-path-precheck
// — пока legacy repomock'и (Network/Subnet ещё не на CQRS pilot'е). Parity с
// network/usecase_test.go.

// makeNetworkRecord — seed Network через legacy repomock (используется PE-precheck'ом).
func makeNetworkRecord(t *testing.T, nr *repomock.NetworkRepo) *kacho.NetworkRecord {
	t.Helper()
	netID := ids.NewID(ids.PrefixNetwork)
	rec, err := nr.Insert(context.Background(), &domain.Network{ID: netID, ProjectID: "f1", Name: "net"})
	require.NoError(t, err)
	return rec
}

func makeHandler(t *testing.T,
	kr *kachomock.Repository,
	nr *repomock.NetworkRepo,
	sr *repomock.SubnetRepo,
	or *repomock.OpsRepo,
	fc *repomock.ProjectClient,
) *Handler {
	t.Helper()
	create := NewCreatePrivateEndpointUseCase(kr, nr, sr, fc, or)
	update := NewUpdatePrivateEndpointUseCase(kr, or)
	deleteUC := NewDeletePrivateEndpointUseCase(kr, or)
	get := NewGetPrivateEndpointUseCase(kr)
	list := NewListPrivateEndpointsUseCase(kr)
	listOps := NewListOperationsUseCase(or)
	return NewHandler(create, update, deleteUC, get, list, listOps)
}

func minimalHandler(t *testing.T, folderOK bool) (*Handler, *repomock.OpsRepo, *kachomock.Repository, *repomock.NetworkRepo, *repomock.SubnetRepo) {
	t.Helper()
	kr := kachomock.NewRepository()
	nr := repomock.NewNetworkRepo()
	sr := repomock.NewSubnetRepo()
	or := repomock.NewOpsRepo()
	fc := &repomock.ProjectClient{OK: folderOK}
	return makeHandler(t, kr, nr, sr, or, fc), or, kr, nr, sr
}

// ---- Handler — sync paths ----

func TestHandler_Get_InvalidArg(t *testing.T) {
	h, _, _, _, _ := minimalHandler(t, true)
	_, err := h.Get(context.Background(), &pepb.GetPrivateEndpointRequest{PrivateEndpointId: ""})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestHandler_Get_NotFound(t *testing.T) {
	h, _, _, _, _ := minimalHandler(t, true)
	_, err := h.Get(context.Background(), &pepb.GetPrivateEndpointRequest{PrivateEndpointId: ids.NewID(ids.PrefixPrivateEndpoint)})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestHandler_List_Empty(t *testing.T) {
	h, _, _, _, _ := minimalHandler(t, true)
	resp, err := h.List(context.Background(), &pepb.ListPrivateEndpointsRequest{
		Container: &pepb.ListPrivateEndpointsRequest_ProjectId{ProjectId: "f1"},
	})
	require.NoError(t, err)
	assert.Empty(t, resp.PrivateEndpoints)
}

func TestHandler_Create_Validates(t *testing.T) {
	h, _, _, _, _ := minimalHandler(t, true)
	_, err := h.Create(context.Background(), &pepb.CreatePrivateEndpointRequest{Name: "pe"})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestHandler_Update_RequiresID(t *testing.T) {
	h, _, _, _, _ := minimalHandler(t, true)
	_, err := h.Update(context.Background(), &pepb.UpdatePrivateEndpointRequest{PrivateEndpointId: ""})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestHandler_Delete_InvalidArg(t *testing.T) {
	h, _, _, _, _ := minimalHandler(t, true)
	_, err := h.Delete(context.Background(), &pepb.DeletePrivateEndpointRequest{PrivateEndpointId: ""})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestHandler_ListOperations_RequiresID(t *testing.T) {
	h, _, _, _, _ := minimalHandler(t, true)
	_, err := h.ListOperations(context.Background(), &pepb.ListPrivateEndpointOperationsRequest{PrivateEndpointId: ""})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

// ---- use-case-level ----

func TestCreateUseCase_NetworkIDRequired(t *testing.T) {
	kr := kachomock.NewRepository()
	nr := repomock.NewNetworkRepo()
	sr := repomock.NewSubnetRepo()
	or := repomock.NewOpsRepo()
	uc := NewCreatePrivateEndpointUseCase(kr, nr, sr, &repomock.ProjectClient{OK: true}, or)

	// network_id required.
	_, err := uc.Execute(context.Background(), domain.PrivateEndpoint{ProjectID: "f1", Name: "pe1"})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestCreateUseCase_OK(t *testing.T) {
	kr := kachomock.NewRepository()
	nr := repomock.NewNetworkRepo()
	sr := repomock.NewSubnetRepo()
	or := repomock.NewOpsRepo()
	net := makeNetworkRecord(t, nr)
	uc := NewCreatePrivateEndpointUseCase(kr, nr, sr, &repomock.ProjectClient{OK: true}, or)

	op, err := uc.Execute(context.Background(), domain.PrivateEndpoint{
		ProjectID:   "f1",
		NetworkID:   net.ID,
		Name:        domain.RcNameVPC("pe1"),
		ServiceType: domain.PrivateEndpointServiceTypeObjectStorage,
	})
	require.NoError(t, err)
	require.NotEmpty(t, op.ID)

	saved := repomock.AwaitOpDone(t, or, op.ID)
	assert.True(t, saved.Done)
	assert.Nil(t, saved.Error)

	// Resource записан в kacho-mock через writer-TX (after Commit).
	got := kr.PrivateEndpoints()
	require.Len(t, got, 1)
	assert.Equal(t, "f1", got[0].ProjectID)
	assert.Equal(t, domain.RcNameVPC("pe1"), got[0].Name)

	// Outbox-event PrivateEndpoint.CREATED — атомарно с DML.
	outbox := kr.Outbox()
	require.NotEmpty(t, outbox)
	assert.Equal(t, "PrivateEndpoint", outbox[0].Resource)
	assert.Equal(t, "CREATED", outbox[0].Action)
}

func TestUpdateUseCase_RequiresID(t *testing.T) {
	uc := NewUpdatePrivateEndpointUseCase(kachomock.NewRepository(), repomock.NewOpsRepo())
	_, err := uc.Execute(context.Background(), UpdateInput{PrivateEndpointID: ""})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestDeleteUseCase_InvalidArg(t *testing.T) {
	uc := NewDeletePrivateEndpointUseCase(kachomock.NewRepository(), repomock.NewOpsRepo())
	_, err := uc.Execute(context.Background(), "")
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestListUseCase_RequiresFolder(t *testing.T) {
	uc := NewListPrivateEndpointsUseCase(kachomock.NewRepository())
	_, _, err := uc.Execute(context.Background(), PrivateEndpointFilter{}, Pagination{})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

// ---- delete response = Empty + Outbox DELETED ----

func TestHandler_Delete_ResponseIsEmpty(t *testing.T) {
	h, or, kr, _, _ := minimalHandler(t, true)
	// Pre-create PE через writer.PrivateEndpoints().Insert + Commit, чтобы
	// Delete-flow имел что удалять.
	ctx := context.Background()
	w, err := kr.Writer(ctx)
	require.NoError(t, err)
	peID := ids.NewID(ids.PrefixPrivateEndpoint)
	_, err = w.PrivateEndpoints().Insert(ctx, &domain.PrivateEndpoint{
		ID: peID, ProjectID: "f1", Name: domain.RcNameVPC("pe-del"),
		NetworkID: "net-1", ServiceType: domain.PrivateEndpointServiceTypeObjectStorage,
		Status: domain.PrivateEndpointStatusAvailable,
	})
	require.NoError(t, err)
	require.NoError(t, w.Commit())

	delOp, err := h.Delete(ctx, &pepb.DeletePrivateEndpointRequest{PrivateEndpointId: peID})
	require.NoError(t, err)
	saved := repomock.AwaitOpDone(t, or, delOp.Id)
	require.Nil(t, saved.Error)
	require.NotNil(t, saved.Response)
	var empty emptypb.Empty
	require.NoError(t, saved.Response.UnmarshalTo(&empty), "Delete response must be google.protobuf.Empty")

	// Outbox содержит CREATED (seed) + DELETED.
	outbox := kr.Outbox()
	var sawDeleted bool
	for _, ev := range outbox {
		if ev.Resource == "PrivateEndpoint" && ev.Action == "DELETED" && ev.ID == peID {
			sawDeleted = true
		}
	}
	assert.True(t, sawDeleted, "expected PrivateEndpoint.DELETED outbox event")
}

func TestPrivateEndpointToPb_Fields(t *testing.T) {
	rec := &kacho.PrivateEndpointRecord{
		PrivateEndpoint: domain.PrivateEndpoint{
			ID:          "pe-1",
			ProjectID:   "f1",
			Name:        domain.RcNameVPC("pe"),
			Description: domain.RcDescription("desc"),
			Labels:      domain.LabelsFromMap(map[string]string{"env": "test"}),
			NetworkID:   "net-1",
			SubnetID:    "sub-1",
			IPAddress:   "10.0.0.5",
			AddressID:   "adr-1",
			ServiceType: domain.PrivateEndpointServiceTypeObjectStorage,
			Status:      domain.PrivateEndpointStatusAvailable,
			DnsOptions:  map[string]any{"private_dns_records_enabled": true},
		},
	}
	out, err := privateEndpointToPb(rec)
	require.NoError(t, err)
	assert.Equal(t, "pe-1", out.Id)
	assert.Equal(t, pepb.PrivateEndpoint_AVAILABLE, out.Status)
	require.NotNil(t, out.Address)
	assert.Equal(t, "10.0.0.5", out.Address.Address)
	require.NotNil(t, out.DnsOptions)
	assert.True(t, out.DnsOptions.PrivateDnsRecordsEnabled)
	assert.NotNil(t, out.GetObjectStorage())
}
