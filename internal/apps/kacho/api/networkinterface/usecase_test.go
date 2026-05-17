package networkinterface

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/kachomock"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/repomock"
)

// Тесты NetworkInterface use-case'ов и handler'а.
//
// Wave 5 replicate (KAC-94, NIC batch): NIC use-case'ы переехали на CQRS-Repository
// (skill evgeniy §6 G.1-G.7). NIC-mock — `kachomock.Repository` (in-memory
// CQRS-impl с TX-семантикой и outbox-буфером); Address — пока legacy
// `repomock.AddressRepo` (CQRS-writer для Address-SetReference ещё не готов).
//
// A.7 sub-PR 3/6 (KAC-94): parent-Subnet validation через CQRS-Reader
// (`kachoRepo.Reader().Subnets().Get`), `*repomock.SubnetRepo` больше не нужен —
// fixture-Subnet seed'ится через `kachomock.SeedSubnet`.
//
// NIC-specific:
//   - Нет Move RPC (NIC привязан к Subnet).
//   - Есть AttachToInstance / DetachFromInstance с atomic CAS на repo-уровне
//     (миграция 0016, KAC-52). На уровне unit-теста мы проверяем sync-семантику
//     handler'а через kachomock, который зеркалит CAS-семантику; реальные
//     race-сценарии — `internal/repo/network_interface_attach_race_integration_test.go`
//     (legacy) и новый `internal/repo/kacho/pg/network_interface_integration_test.go`.

// ---- handler builder ----

func makeHandler(t *testing.T,
	kr *kachomock.Repository,
	ar *repomock.AddressRepo,
	or *repomock.OpsRepo,
	fc *repomock.ProjectClient,
) *Handler {
	t.Helper()
	create := NewCreateNetworkInterfaceUseCase(kr, ar, fc, or)
	update := NewUpdateNetworkInterfaceUseCase(kr, ar, or)
	deleteUC := NewDeleteNetworkInterfaceUseCase(kr, ar, or)
	get := NewGetNetworkInterfaceUseCase(kr)
	list := NewListNetworkInterfacesUseCase(kr)
	attach := NewAttachToInstanceUseCase(kr, or)
	detach := NewDetachFromInstanceUseCase(kr, or)
	listOps := NewListOperationsUseCase(or)
	return NewHandler(create, update, deleteUC, get, list, attach, detach, listOps)
}

func minimalHandler(t *testing.T, folderOK bool) (*Handler, *repomock.OpsRepo, *kachomock.Repository, *repomock.AddressRepo) {
	t.Helper()
	kr := kachomock.NewRepository()
	ar := repomock.NewAddressRepo()
	or := repomock.NewOpsRepo()
	fc := &repomock.ProjectClient{OK: folderOK}
	return makeHandler(t, kr, ar, or, fc), or, kr, ar
}

// preloadNIC помещает NIC прямо в state mock-Repository (как если бы он
// уже существовал) — обходим Writer-TX, потому что в тестах нужно arrange
// pre-existing state до Action.
func preloadNIC(t *testing.T, kr *kachomock.Repository, rec *kachorepo.NetworkInterfaceRecord) {
	t.Helper()
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = time.Now().UTC()
	}
	w, err := kr.Writer(context.Background())
	require.NoError(t, err)
	_, err = w.NetworkInterfaces().Insert(context.Background(), &rec.NetworkInterface)
	require.NoError(t, err)
	require.NoError(t, w.Commit())
}

// ---- Handler — sync paths ----

func TestHandler_Get_InvalidArg(t *testing.T) {
	h, _, _, _ := minimalHandler(t, true)
	_, err := h.Get(context.Background(), &vpcv1.GetNetworkInterfaceRequest{NetworkInterfaceId: ""})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestHandler_Get_NotFound(t *testing.T) {
	h, _, _, _ := minimalHandler(t, true)
	_, err := h.Get(context.Background(), &vpcv1.GetNetworkInterfaceRequest{NetworkInterfaceId: ids.NewID(ids.PrefixSubnet)})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestHandler_List_Empty(t *testing.T) {
	h, _, _, _ := minimalHandler(t, true)
	resp, err := h.List(context.Background(), &vpcv1.ListNetworkInterfacesRequest{ProjectId: "f1"})
	require.NoError(t, err)
	assert.Empty(t, resp.NetworkInterfaces)
}

func TestHandler_Create_Validates(t *testing.T) {
	h, _, _, _ := minimalHandler(t, true)
	_, err := h.Create(context.Background(), &vpcv1.CreateNetworkInterfaceRequest{Name: "nic"})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestHandler_Update_RequiresID(t *testing.T) {
	h, _, _, _ := minimalHandler(t, true)
	_, err := h.Update(context.Background(), &vpcv1.UpdateNetworkInterfaceRequest{NetworkInterfaceId: ""})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestHandler_Delete_InvalidArg(t *testing.T) {
	h, _, _, _ := minimalHandler(t, true)
	_, err := h.Delete(context.Background(), &vpcv1.DeleteNetworkInterfaceRequest{NetworkInterfaceId: ""})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestHandler_Attach_RequiresID(t *testing.T) {
	h, _, _, _ := minimalHandler(t, true)
	_, err := h.AttachToInstance(context.Background(), &vpcv1.AttachNetworkInterfaceRequest{NetworkInterfaceId: ""})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestHandler_Detach_RequiresID(t *testing.T) {
	h, _, _, _ := minimalHandler(t, true)
	_, err := h.DetachFromInstance(context.Background(), &vpcv1.DetachNetworkInterfaceRequest{NetworkInterfaceId: ""})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestHandler_ListOperations_RequiresID(t *testing.T) {
	h, _, _, _ := minimalHandler(t, true)
	_, err := h.ListOperations(context.Background(), &vpcv1.ListNetworkInterfaceOperationsRequest{NetworkInterfaceId: ""})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

// ---- use-case-level ----

func TestCreateUseCase_FolderRequired(t *testing.T) {
	kr := kachomock.NewRepository()
	ar := repomock.NewAddressRepo()
	or := repomock.NewOpsRepo()
	uc := NewCreateNetworkInterfaceUseCase(kr, ar, &repomock.ProjectClient{OK: true}, or)

	_, err := uc.Execute(context.Background(), CreateInput{NetworkInterface: domain.NetworkInterface{Name: "nic"}})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestCreateUseCase_SubnetRequired(t *testing.T) {
	kr := kachomock.NewRepository()
	ar := repomock.NewAddressRepo()
	or := repomock.NewOpsRepo()
	uc := NewCreateNetworkInterfaceUseCase(kr, ar, &repomock.ProjectClient{OK: true}, or)

	_, err := uc.Execute(context.Background(), CreateInput{NetworkInterface: domain.NetworkInterface{ProjectID: "f1", Name: "nic"}})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestCreateUseCase_CardinalityV4_TooMany(t *testing.T) {
	kr := kachomock.NewRepository()
	ar := repomock.NewAddressRepo()
	or := repomock.NewOpsRepo()
	uc := NewCreateNetworkInterfaceUseCase(kr, ar, &repomock.ProjectClient{OK: true}, or)

	_, err := uc.Execute(context.Background(), CreateInput{NetworkInterface: domain.NetworkInterface{
		ProjectID:     "f1",
		Name:         "nic",
		SubnetID:     "e9bsub1",
		V4AddressIDs: []string{"e9ba1", "e9ba2"},
	}})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestCreateUseCase_OK(t *testing.T) {
	kr := kachomock.NewRepository()
	ar := repomock.NewAddressRepo()
	or := repomock.NewOpsRepo()
	// preset subnet — fixture в kachomock (CQRS-Reader.Subnets().Get).
	kr.SeedSubnet(&kachorepo.SubnetRecord{
		Subnet: domain.Subnet{ID: "e9bsub1", ProjectID: "f1", Name: domain.RcNameVPC("sn")},
	})
	uc := NewCreateNetworkInterfaceUseCase(kr, ar, &repomock.ProjectClient{OK: true}, or)

	op, err := uc.Execute(context.Background(), CreateInput{NetworkInterface: domain.NetworkInterface{
		ProjectID: "f1",
		Name:     "nic",
		SubnetID: "e9bsub1",
	}})
	require.NoError(t, err)
	require.NotEmpty(t, op.ID)

	saved := repomock.AwaitOpDone(t, or, op.ID)
	assert.True(t, saved.Done)
	assert.Nil(t, saved.Error)
}

func TestUpdateUseCase_RequiresID(t *testing.T) {
	uc := NewUpdateNetworkInterfaceUseCase(kachomock.NewRepository(), repomock.NewAddressRepo(), repomock.NewOpsRepo())
	_, err := uc.Execute(context.Background(), UpdateInput{NetworkInterfaceID: ""})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestDeleteUseCase_InvalidArg(t *testing.T) {
	uc := NewDeleteNetworkInterfaceUseCase(kachomock.NewRepository(), repomock.NewAddressRepo(), repomock.NewOpsRepo())
	_, err := uc.Execute(context.Background(), "")
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestListUseCase_RequiresFolder(t *testing.T) {
	uc := NewListNetworkInterfacesUseCase(kachomock.NewRepository())
	_, _, err := uc.Execute(context.Background(), NetworkInterfaceFilter{}, Pagination{})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestAttachUseCase_RequiresInstance(t *testing.T) {
	uc := NewAttachToInstanceUseCase(kachomock.NewRepository(), repomock.NewOpsRepo())
	_, err := uc.Execute(context.Background(), ids.NewID(ids.PrefixSubnet), "", "")
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestAttachUseCase_AlreadyAttachedDifferentOwner(t *testing.T) {
	kr := kachomock.NewRepository()
	or := repomock.NewOpsRepo()
	nicID := ids.NewID(ids.PrefixSubnet)
	preloadNIC(t, kr, &kachorepo.NetworkInterfaceRecord{
		NetworkInterface: domain.NetworkInterface{
			ID:         nicID,
			ProjectID:   "f1",
			SubnetID:   "e9bsub1",
			UsedByType: "compute_instance",
			UsedByID:   "other-instance",
			Status:     domain.NIStatusActive,
		},
	})
	uc := NewAttachToInstanceUseCase(kr, or)
	op, err := uc.Execute(context.Background(), nicID, "my-instance", "0")
	require.NoError(t, err)
	saved := repomock.AwaitOpDone(t, or, op.ID)
	require.NotNil(t, saved.Error, "should fail because NIC already attached to other-instance")
	assert.Equal(t, int32(codes.FailedPrecondition), saved.Error.Code)
}

func TestAttachUseCase_OK(t *testing.T) {
	kr := kachomock.NewRepository()
	or := repomock.NewOpsRepo()
	nicID := ids.NewID(ids.PrefixSubnet)
	preloadNIC(t, kr, &kachorepo.NetworkInterfaceRecord{
		NetworkInterface: domain.NetworkInterface{
			ID:       nicID,
			ProjectID: "f1",
			SubnetID: "e9bsub1",
			Status:   domain.NIStatusAvailable,
		},
	})
	uc := NewAttachToInstanceUseCase(kr, or)
	op, err := uc.Execute(context.Background(), nicID, "my-instance", "0")
	require.NoError(t, err)
	saved := repomock.AwaitOpDone(t, or, op.ID)
	require.Nil(t, saved.Error)
	require.NotNil(t, saved.Response)
}

func TestDetachUseCase_OK(t *testing.T) {
	kr := kachomock.NewRepository()
	or := repomock.NewOpsRepo()
	nicID := ids.NewID(ids.PrefixSubnet)
	preloadNIC(t, kr, &kachorepo.NetworkInterfaceRecord{
		NetworkInterface: domain.NetworkInterface{
			ID:         nicID,
			ProjectID:   "f1",
			SubnetID:   "e9bsub1",
			UsedByType: "compute_instance",
			UsedByID:   "my-instance",
			Status:     domain.NIStatusActive,
		},
	})
	uc := NewDetachFromInstanceUseCase(kr, or)
	op, err := uc.Execute(context.Background(), nicID)
	require.NoError(t, err)
	saved := repomock.AwaitOpDone(t, or, op.ID)
	require.Nil(t, saved.Error)
	// После detach NIC должен быть available и без owner.
	rd, err := kr.Reader(context.Background())
	require.NoError(t, err)
	defer func() { _ = rd.Close() }()
	got, err := rd.NetworkInterfaces().Get(context.Background(), nicID)
	require.NoError(t, err)
	assert.Equal(t, "", got.UsedByID)
	assert.Equal(t, domain.NIStatusAvailable, got.Status)
}

// ---- delete precondition / response = Empty ----

func TestDeleteUseCase_BlockedByAttached(t *testing.T) {
	kr := kachomock.NewRepository()
	or := repomock.NewOpsRepo()
	ar := repomock.NewAddressRepo()
	nicID := ids.NewID(ids.PrefixSubnet)
	preloadNIC(t, kr, &kachorepo.NetworkInterfaceRecord{
		NetworkInterface: domain.NetworkInterface{
			ID:         nicID,
			ProjectID:   "f1",
			SubnetID:   "e9bsub1",
			UsedByType: "compute_instance",
			UsedByID:   "my-instance",
			Status:     domain.NIStatusActive,
		},
	})
	uc := NewDeleteNetworkInterfaceUseCase(kr, ar, or)
	op, err := uc.Execute(context.Background(), nicID)
	require.NoError(t, err)
	saved := repomock.AwaitOpDone(t, or, op.ID)
	require.NotNil(t, saved.Error)
	assert.Equal(t, int32(codes.FailedPrecondition), saved.Error.Code)
}

func TestDeleteUseCase_ResponseIsEmpty(t *testing.T) {
	kr := kachomock.NewRepository()
	or := repomock.NewOpsRepo()
	ar := repomock.NewAddressRepo()
	nicID := ids.NewID(ids.PrefixSubnet)
	preloadNIC(t, kr, &kachorepo.NetworkInterfaceRecord{
		NetworkInterface: domain.NetworkInterface{
			ID:       nicID,
			ProjectID: "f1",
			SubnetID: "e9bsub1",
			Status:   domain.NIStatusAvailable,
		},
	})
	uc := NewDeleteNetworkInterfaceUseCase(kr, ar, or)
	op, err := uc.Execute(context.Background(), nicID)
	require.NoError(t, err)
	saved := repomock.AwaitOpDone(t, or, op.ID)
	require.Nil(t, saved.Error)
	require.NotNil(t, saved.Response)
	var empty emptypb.Empty
	require.NoError(t, saved.Response.UnmarshalTo(&empty), "Delete response must be google.protobuf.Empty")
}

func TestNetworkInterfaceToPb_Fields(t *testing.T) {
	rec := &kachorepo.NetworkInterfaceRecord{
		NetworkInterface: domain.NetworkInterface{
			ID:               "e9bnic1",
			ProjectID:         "f1",
			Name:             domain.RcNameVPC("nic"),
			Description:      domain.RcDescription("desc"),
			Labels:           domain.LabelsFromMap(map[string]string{"env": "test"}),
			SubnetID:         "e9bsub1",
			V4AddressIDs:     []string{"e9ba1"},
			SecurityGroupIDs: []string{"enpsg1"},
			MAC:              "0e:11:22:33:44:55",
			Status:           domain.NIStatusActive,
			UsedByType:       "compute_instance",
			UsedByID:         "compute1",
		},
	}
	out, err := networkInterfaceToPb(rec)
	require.NoError(t, err)
	assert.Equal(t, "e9bnic1", out.Id)
	assert.Equal(t, vpcv1.NetworkInterface_ACTIVE, out.Status)
	assert.Equal(t, "0e:11:22:33:44:55", out.MacAddress)
}
