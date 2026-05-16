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
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/repomock"
)

// Тесты PrivateEndpoint use-case'ов и handler'а. Wave 3b (KAC-94).

func makeNetworkRecord(t *testing.T, nr *repomock.NetworkRepo) *domain.NetworkRecord {
	t.Helper()
	netID := ids.NewID(ids.PrefixNetwork)
	rec, err := nr.Insert(context.Background(), &domain.Network{ID: netID, FolderID: "f1", Name: "net"})
	require.NoError(t, err)
	return rec
}

func makeHandler(t *testing.T,
	pr *repomock.PrivateEndpointRepo,
	nr *repomock.NetworkRepo,
	sr *repomock.SubnetRepo,
	or *repomock.OpsRepo,
	fc *repomock.FolderClient,
) *Handler {
	t.Helper()
	create := NewCreatePrivateEndpointUseCase(pr, nr, sr, fc, or)
	update := NewUpdatePrivateEndpointUseCase(pr, or)
	deleteUC := NewDeletePrivateEndpointUseCase(pr, or)
	get := NewGetPrivateEndpointUseCase(pr)
	list := NewListPrivateEndpointsUseCase(pr)
	listOps := NewListOperationsUseCase(or)
	return NewHandler(create, update, deleteUC, get, list, listOps)
}

func minimalHandler(t *testing.T, folderOK bool) (*Handler, *repomock.OpsRepo, *repomock.PrivateEndpointRepo, *repomock.NetworkRepo, *repomock.SubnetRepo) {
	t.Helper()
	pr := repomock.NewPrivateEndpointRepo()
	nr := repomock.NewNetworkRepo()
	sr := repomock.NewSubnetRepo()
	or := repomock.NewOpsRepo()
	fc := &repomock.FolderClient{OK: folderOK}
	return makeHandler(t, pr, nr, sr, or, fc), or, pr, nr, sr
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
		Container: &pepb.ListPrivateEndpointsRequest_FolderId{FolderId: "f1"},
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
	pr := repomock.NewPrivateEndpointRepo()
	nr := repomock.NewNetworkRepo()
	sr := repomock.NewSubnetRepo()
	or := repomock.NewOpsRepo()
	uc := NewCreatePrivateEndpointUseCase(pr, nr, sr, &repomock.FolderClient{OK: true}, or)

	// network_id required.
	_, err := uc.Execute(context.Background(), CreateInput{PrivateEndpoint: domain.PrivateEndpoint{FolderID: "f1", Name: "pe1"}})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestCreateUseCase_OK(t *testing.T) {
	pr := repomock.NewPrivateEndpointRepo()
	nr := repomock.NewNetworkRepo()
	sr := repomock.NewSubnetRepo()
	or := repomock.NewOpsRepo()
	net := makeNetworkRecord(t, nr)
	uc := NewCreatePrivateEndpointUseCase(pr, nr, sr, &repomock.FolderClient{OK: true}, or)

	op, err := uc.Execute(context.Background(), CreateInput{PrivateEndpoint: domain.PrivateEndpoint{
		FolderID:    "f1",
		NetworkID:   net.ID,
		Name:        domain.RcNameVPC("pe1"),
		ServiceType: domain.PrivateEndpointServiceTypeObjectStorage,
	}})
	require.NoError(t, err)
	require.NotEmpty(t, op.ID)

	saved := repomock.AwaitOpDone(t, or, op.ID)
	assert.True(t, saved.Done)
	assert.Nil(t, saved.Error)
}

func TestUpdateUseCase_RequiresID(t *testing.T) {
	uc := NewUpdatePrivateEndpointUseCase(repomock.NewPrivateEndpointRepo(), repomock.NewOpsRepo())
	_, err := uc.Execute(context.Background(), UpdateInput{PrivateEndpointID: ""})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestDeleteUseCase_InvalidArg(t *testing.T) {
	uc := NewDeletePrivateEndpointUseCase(repomock.NewPrivateEndpointRepo(), repomock.NewOpsRepo())
	_, err := uc.Execute(context.Background(), "")
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestListUseCase_RequiresFolder(t *testing.T) {
	uc := NewListPrivateEndpointsUseCase(repomock.NewPrivateEndpointRepo())
	_, _, err := uc.Execute(context.Background(), PrivateEndpointFilter{}, Pagination{})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

// ---- delete response = Empty ----

func TestHandler_Delete_ResponseIsEmpty(t *testing.T) {
	h, or, pr, _, _ := minimalHandler(t, true)
	// Pre-create PE via repo direct, so we have something to delete (Create flow
	// requires Network; PE existence в repo достаточно для Delete pathway).
	peID := ids.NewID(ids.PrefixPrivateEndpoint)
	_, err := pr.Insert(context.Background(), &domain.PrivateEndpoint{
		ID: peID, FolderID: "f1", Name: domain.RcNameVPC("pe-del"),
		NetworkID: "net-1", ServiceType: domain.PrivateEndpointServiceTypeObjectStorage,
		Status: domain.PrivateEndpointStatusAvailable,
	})
	require.NoError(t, err)

	delOp, err := h.Delete(context.Background(), &pepb.DeletePrivateEndpointRequest{PrivateEndpointId: peID})
	require.NoError(t, err)
	saved := repomock.AwaitOpDone(t, or, delOp.Id)
	require.Nil(t, saved.Error)
	require.NotNil(t, saved.Response)
	var empty emptypb.Empty
	require.NoError(t, saved.Response.UnmarshalTo(&empty), "Delete response must be google.protobuf.Empty")
}

func TestPrivateEndpointToPb_Fields(t *testing.T) {
	rec := &domain.PrivateEndpointRecord{
		PrivateEndpoint: domain.PrivateEndpoint{
			ID:          "pe-1",
			FolderID:    "f1",
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
