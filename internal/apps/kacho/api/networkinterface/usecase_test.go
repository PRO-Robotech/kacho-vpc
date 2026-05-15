package networkinterface

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/ports"
	"github.com/PRO-Robotech/kacho-vpc/internal/ports/portmock"
)

// Тесты NetworkInterface use-case'ов и handler'а. Wave 3 (KAC-94).
//
// NIC-specific:
//   - Нет Move RPC (NIC привязан к Subnet).
//   - Есть AttachToInstance / DetachFromInstance с atomic CAS на repo-уровне
//     (миграция 0016, KAC-52). На уровне unit-теста мы проверяем sync-семантику
//     handler'а; race-сценарии — `internal/repo/network_interface_attach_race_integration_test.go`.
//   - MAC аллокация — в `internal/service/mac.go` (не дёргаем напрямую).

// ---- in-memory NIC repo fake ----

// niRepoFake — реализация NetworkInterfaceRepo для unit-тестов use-case'ов.
// Параллель `internal/service/ni_test_helpers_test.go::niRepoFake`, но локальная
// (use-case-пакет не зависит от service-test-helper'а; параметризация типов та же).
type niRepoFake struct {
	mu   sync.Mutex
	data map[string]*domain.NetworkInterfaceRecord
}

func newNIRepoFake() *niRepoFake {
	return &niRepoFake{data: map[string]*domain.NetworkInterfaceRecord{}}
}

func (r *niRepoFake) Get(_ context.Context, id string) (*domain.NetworkInterfaceRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n, ok := r.data[id]
	if !ok {
		return nil, ports.ErrNotFound
	}
	cp := *n
	return &cp, nil
}

func (r *niRepoFake) List(_ context.Context, _ NetworkInterfaceFilter, _ Pagination) ([]*domain.NetworkInterfaceRecord, string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*domain.NetworkInterfaceRecord
	for _, n := range r.data {
		cp := *n
		out = append(out, &cp)
	}
	return out, "", nil
}

func (r *niRepoFake) Insert(_ context.Context, n *domain.NetworkInterface) (*domain.NetworkInterfaceRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec := &domain.NetworkInterfaceRecord{NetworkInterface: *n}
	r.data[n.ID] = rec
	return rec, nil
}

func (r *niRepoFake) UpdateMeta(_ context.Context, n *domain.NetworkInterface) (*domain.NetworkInterfaceRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec := &domain.NetworkInterfaceRecord{NetworkInterface: *n}
	r.data[n.ID] = rec
	return rec, nil
}

func (r *niRepoFake) SetUsedBy(_ context.Context, id, refType, refID, refName string, st domain.NetworkInterfaceStatus) (*domain.NetworkInterfaceRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n, ok := r.data[id]
	if !ok {
		return nil, ports.ErrNotFound
	}
	if refID == "" {
		refType, refName = "", ""
	}
	// CAS-семантика: если NIC уже attached к другому owner-у — FailedPrecondition
	// (зеркало repo-уровня, миграция 0016 / KAC-52).
	if refID != "" && n.UsedByID != "" && n.UsedByID != refID {
		return nil, ports.ErrFailedPrecondition
	}
	n.UsedByType, n.UsedByID, n.UsedByName, n.Status = refType, refID, refName, st
	return n, nil
}

func (r *niRepoFake) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.data[id]; !ok {
		return ports.ErrNotFound
	}
	delete(r.data, id)
	return nil
}

// ---- handler builder ----

func makeHandler(t *testing.T,
	nr *niRepoFake,
	sr *portmock.SubnetRepo,
	ar *portmock.AddressRepo,
	or *portmock.OpsRepo,
	fc *portmock.FolderClient,
) *Handler {
	t.Helper()
	create := NewCreateNetworkInterfaceUseCase(nr, sr, ar, fc, or)
	update := NewUpdateNetworkInterfaceUseCase(nr, ar, or)
	deleteUC := NewDeleteNetworkInterfaceUseCase(nr, ar, or)
	get := NewGetNetworkInterfaceUseCase(nr)
	list := NewListNetworkInterfacesUseCase(nr)
	attach := NewAttachToInstanceUseCase(nr, or)
	detach := NewDetachFromInstanceUseCase(nr, or)
	listOps := NewListOperationsUseCase(or)
	return NewHandler(create, update, deleteUC, get, list, attach, detach, listOps)
}

func minimalHandler(t *testing.T, folderOK bool) (*Handler, *portmock.OpsRepo, *niRepoFake, *portmock.SubnetRepo, *portmock.AddressRepo) {
	t.Helper()
	nr := newNIRepoFake()
	sr := portmock.NewSubnetRepo()
	ar := portmock.NewAddressRepo()
	or := portmock.NewOpsRepo()
	fc := &portmock.FolderClient{OK: folderOK}
	return makeHandler(t, nr, sr, ar, or, fc), or, nr, sr, ar
}

// ---- Handler — sync paths ----

func TestHandler_Get_InvalidArg(t *testing.T) {
	h, _, _, _, _ := minimalHandler(t, true)
	_, err := h.Get(context.Background(), &vpcv1.GetNetworkInterfaceRequest{NetworkInterfaceId: ""})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestHandler_Get_NotFound(t *testing.T) {
	h, _, _, _, _ := minimalHandler(t, true)
	_, err := h.Get(context.Background(), &vpcv1.GetNetworkInterfaceRequest{NetworkInterfaceId: ids.NewID(ids.PrefixSubnet)})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestHandler_List_Empty(t *testing.T) {
	h, _, _, _, _ := minimalHandler(t, true)
	resp, err := h.List(context.Background(), &vpcv1.ListNetworkInterfacesRequest{FolderId: "f1"})
	require.NoError(t, err)
	assert.Empty(t, resp.NetworkInterfaces)
}

func TestHandler_Create_Validates(t *testing.T) {
	h, _, _, _, _ := minimalHandler(t, true)
	_, err := h.Create(context.Background(), &vpcv1.CreateNetworkInterfaceRequest{Name: "nic"})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestHandler_Update_RequiresID(t *testing.T) {
	h, _, _, _, _ := minimalHandler(t, true)
	_, err := h.Update(context.Background(), &vpcv1.UpdateNetworkInterfaceRequest{NetworkInterfaceId: ""})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestHandler_Delete_InvalidArg(t *testing.T) {
	h, _, _, _, _ := minimalHandler(t, true)
	_, err := h.Delete(context.Background(), &vpcv1.DeleteNetworkInterfaceRequest{NetworkInterfaceId: ""})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestHandler_Attach_RequiresID(t *testing.T) {
	h, _, _, _, _ := minimalHandler(t, true)
	_, err := h.AttachToInstance(context.Background(), &vpcv1.AttachNetworkInterfaceRequest{NetworkInterfaceId: ""})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestHandler_Detach_RequiresID(t *testing.T) {
	h, _, _, _, _ := minimalHandler(t, true)
	_, err := h.DetachFromInstance(context.Background(), &vpcv1.DetachNetworkInterfaceRequest{NetworkInterfaceId: ""})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestHandler_ListOperations_RequiresID(t *testing.T) {
	h, _, _, _, _ := minimalHandler(t, true)
	_, err := h.ListOperations(context.Background(), &vpcv1.ListNetworkInterfaceOperationsRequest{NetworkInterfaceId: ""})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

// ---- use-case-level ----

func TestCreateUseCase_FolderRequired(t *testing.T) {
	nr := newNIRepoFake()
	sr := portmock.NewSubnetRepo()
	ar := portmock.NewAddressRepo()
	or := portmock.NewOpsRepo()
	uc := NewCreateNetworkInterfaceUseCase(nr, sr, ar, &portmock.FolderClient{OK: true}, or)

	_, err := uc.Execute(context.Background(), CreateInput{NetworkInterface: domain.NetworkInterface{Name: "nic"}})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestCreateUseCase_SubnetRequired(t *testing.T) {
	nr := newNIRepoFake()
	sr := portmock.NewSubnetRepo()
	ar := portmock.NewAddressRepo()
	or := portmock.NewOpsRepo()
	uc := NewCreateNetworkInterfaceUseCase(nr, sr, ar, &portmock.FolderClient{OK: true}, or)

	_, err := uc.Execute(context.Background(), CreateInput{NetworkInterface: domain.NetworkInterface{FolderID: "f1", Name: "nic"}})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestCreateUseCase_CardinalityV4_TooMany(t *testing.T) {
	nr := newNIRepoFake()
	sr := portmock.NewSubnetRepo()
	ar := portmock.NewAddressRepo()
	or := portmock.NewOpsRepo()
	uc := NewCreateNetworkInterfaceUseCase(nr, sr, ar, &portmock.FolderClient{OK: true}, or)

	_, err := uc.Execute(context.Background(), CreateInput{NetworkInterface: domain.NetworkInterface{
		FolderID:     "f1",
		Name:         "nic",
		SubnetID:     "e9bsub1",
		V4AddressIDs: []string{"e9ba1", "e9ba2"},
	}})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestCreateUseCase_OK(t *testing.T) {
	nr := newNIRepoFake()
	sr := portmock.NewSubnetRepo()
	ar := portmock.NewAddressRepo()
	or := portmock.NewOpsRepo()
	// preset subnet
	_, err := sr.Insert(context.Background(), &domain.Subnet{ID: "e9bsub1", FolderID: "f1", Name: "sn"})
	require.NoError(t, err)
	uc := NewCreateNetworkInterfaceUseCase(nr, sr, ar, &portmock.FolderClient{OK: true}, or)

	op, err := uc.Execute(context.Background(), CreateInput{NetworkInterface: domain.NetworkInterface{
		FolderID: "f1",
		Name:     "nic",
		SubnetID: "e9bsub1",
	}})
	require.NoError(t, err)
	require.NotEmpty(t, op.ID)

	saved := portmock.AwaitOpDone(t, or, op.ID)
	assert.True(t, saved.Done)
	assert.Nil(t, saved.Error)
}

func TestUpdateUseCase_RequiresID(t *testing.T) {
	uc := NewUpdateNetworkInterfaceUseCase(newNIRepoFake(), portmock.NewAddressRepo(), portmock.NewOpsRepo())
	_, err := uc.Execute(context.Background(), UpdateInput{NetworkInterfaceID: ""})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestDeleteUseCase_InvalidArg(t *testing.T) {
	uc := NewDeleteNetworkInterfaceUseCase(newNIRepoFake(), portmock.NewAddressRepo(), portmock.NewOpsRepo())
	_, err := uc.Execute(context.Background(), "")
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestListUseCase_RequiresFolder(t *testing.T) {
	uc := NewListNetworkInterfacesUseCase(newNIRepoFake())
	_, _, err := uc.Execute(context.Background(), NetworkInterfaceFilter{}, Pagination{})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestAttachUseCase_RequiresInstance(t *testing.T) {
	uc := NewAttachToInstanceUseCase(newNIRepoFake(), portmock.NewOpsRepo())
	_, err := uc.Execute(context.Background(), ids.NewID(ids.PrefixSubnet), "", "")
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestAttachUseCase_AlreadyAttachedDifferentOwner(t *testing.T) {
	nr := newNIRepoFake()
	or := portmock.NewOpsRepo()
	nicID := ids.NewID(ids.PrefixSubnet)
	nr.data[nicID] = &domain.NetworkInterfaceRecord{
		NetworkInterface: domain.NetworkInterface{
			ID:         nicID,
			FolderID:   "f1",
			SubnetID:   "e9bsub1",
			UsedByType: "compute_instance",
			UsedByID:   "other-instance",
			Status:     domain.NIStatusActive,
		},
	}
	uc := NewAttachToInstanceUseCase(nr, or)
	op, err := uc.Execute(context.Background(), nicID, "my-instance", "0")
	require.NoError(t, err)
	saved := portmock.AwaitOpDone(t, or, op.ID)
	require.NotNil(t, saved.Error, "should fail because NIC already attached to other-instance")
	assert.Equal(t, int32(codes.FailedPrecondition), saved.Error.Code)
}

func TestAttachUseCase_OK(t *testing.T) {
	nr := newNIRepoFake()
	or := portmock.NewOpsRepo()
	nicID := ids.NewID(ids.PrefixSubnet)
	nr.data[nicID] = &domain.NetworkInterfaceRecord{
		NetworkInterface: domain.NetworkInterface{
			ID:       nicID,
			FolderID: "f1",
			SubnetID: "e9bsub1",
			Status:   domain.NIStatusAvailable,
		},
	}
	uc := NewAttachToInstanceUseCase(nr, or)
	op, err := uc.Execute(context.Background(), nicID, "my-instance", "0")
	require.NoError(t, err)
	saved := portmock.AwaitOpDone(t, or, op.ID)
	require.Nil(t, saved.Error)
	require.NotNil(t, saved.Response)
}

func TestDetachUseCase_OK(t *testing.T) {
	nr := newNIRepoFake()
	or := portmock.NewOpsRepo()
	nicID := ids.NewID(ids.PrefixSubnet)
	nr.data[nicID] = &domain.NetworkInterfaceRecord{
		NetworkInterface: domain.NetworkInterface{
			ID:         nicID,
			FolderID:   "f1",
			SubnetID:   "e9bsub1",
			UsedByType: "compute_instance",
			UsedByID:   "my-instance",
			Status:     domain.NIStatusActive,
		},
	}
	uc := NewDetachFromInstanceUseCase(nr, or)
	op, err := uc.Execute(context.Background(), nicID)
	require.NoError(t, err)
	saved := portmock.AwaitOpDone(t, or, op.ID)
	require.Nil(t, saved.Error)
	// После detach NIC должен быть available и без owner.
	got, err := nr.Get(context.Background(), nicID)
	require.NoError(t, err)
	assert.Equal(t, "", got.UsedByID)
	assert.Equal(t, domain.NIStatusAvailable, got.Status)
}

// ---- delete response = Empty ----

func TestDeleteUseCase_BlockedByAttached(t *testing.T) {
	nr := newNIRepoFake()
	or := portmock.NewOpsRepo()
	ar := portmock.NewAddressRepo()
	nicID := ids.NewID(ids.PrefixSubnet)
	nr.data[nicID] = &domain.NetworkInterfaceRecord{
		NetworkInterface: domain.NetworkInterface{
			ID:         nicID,
			FolderID:   "f1",
			SubnetID:   "e9bsub1",
			UsedByType: "compute_instance",
			UsedByID:   "my-instance",
			Status:     domain.NIStatusActive,
		},
	}
	uc := NewDeleteNetworkInterfaceUseCase(nr, ar, or)
	op, err := uc.Execute(context.Background(), nicID)
	require.NoError(t, err)
	saved := portmock.AwaitOpDone(t, or, op.ID)
	require.NotNil(t, saved.Error)
	assert.Equal(t, int32(codes.FailedPrecondition), saved.Error.Code)
}

func TestDeleteUseCase_ResponseIsEmpty(t *testing.T) {
	nr := newNIRepoFake()
	or := portmock.NewOpsRepo()
	ar := portmock.NewAddressRepo()
	nicID := ids.NewID(ids.PrefixSubnet)
	nr.data[nicID] = &domain.NetworkInterfaceRecord{
		NetworkInterface: domain.NetworkInterface{
			ID:       nicID,
			FolderID: "f1",
			SubnetID: "e9bsub1",
			Status:   domain.NIStatusAvailable,
		},
	}
	uc := NewDeleteNetworkInterfaceUseCase(nr, ar, or)
	op, err := uc.Execute(context.Background(), nicID)
	require.NoError(t, err)
	saved := portmock.AwaitOpDone(t, or, op.ID)
	require.Nil(t, saved.Error)
	require.NotNil(t, saved.Response)
	var empty emptypb.Empty
	require.NoError(t, saved.Response.UnmarshalTo(&empty), "Delete response must be google.protobuf.Empty")
}

func TestNetworkInterfaceToPb_Fields(t *testing.T) {
	rec := &domain.NetworkInterfaceRecord{
		NetworkInterface: domain.NetworkInterface{
			ID:               "e9bnic1",
			FolderID:         "f1",
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
