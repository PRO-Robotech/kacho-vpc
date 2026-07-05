// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

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
// NIC use-case'ы работают поверх CQRS-Repository. NIC-mock — `kachomock.Repository`
// (in-memory CQRS-impl с TX-семантикой и outbox-буфером); Address-attach/detach
// идёт через тот же writer-TX (`w.Addresses()`), поэтому адреса seed'ятся в
// `kachomock` (`SeedAddress`), а не в отдельный store. Parent-Subnet валидируется
// через CQRS-Reader (`kachoRepo.Reader().Subnets().Get`), fixture-Subnet seed'ится
// через `kachomock.SeedSubnet`.
//
// NIC-specific:
//   - Нет Move RPC (NIC привязан к Subnet).
//   - NIC.used_by через публичный RPC не выставляется (attach/detach отдельными RPC
//     не поддерживаются). Реальные race-сценарии repo-уровня —
//     `internal/repo/kacho/pg/network_interface_integration_test.go`.

// ---- handler builder ----

func makeHandler(t *testing.T,
	kr *kachomock.Repository,
	or *repomock.OpsRepo,
	fc *repomock.ProjectClient,
) *Handler {
	t.Helper()
	create := NewCreateNetworkInterfaceUseCase(kr, fc, or)
	update := NewUpdateNetworkInterfaceUseCase(kr, or)
	deleteUC := NewDeleteNetworkInterfaceUseCase(kr, or)
	get := NewGetNetworkInterfaceUseCase(kr, nil)
	list := NewListNetworkInterfacesUseCase(kr, nil)
	listOps := NewListOperationsUseCase(or)
	return NewHandler(create, update, deleteUC, get, list, listOps)
}

func minimalHandler(t *testing.T, projectOK bool) (*Handler, *repomock.OpsRepo, *kachomock.Repository) {
	t.Helper()
	kr := kachomock.NewRepository()
	or := repomock.NewOpsRepo()
	fc := &repomock.ProjectClient{OK: projectOK}
	return makeHandler(t, kr, or, fc), or, kr
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
	h, _, _ := minimalHandler(t, true)
	_, err := h.Get(context.Background(), &vpcv1.GetNetworkInterfaceRequest{NetworkInterfaceId: ""})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestHandler_Get_NotFound(t *testing.T) {
	h, _, _ := minimalHandler(t, true)
	_, err := h.Get(context.Background(), &vpcv1.GetNetworkInterfaceRequest{NetworkInterfaceId: ids.NewID(ids.PrefixSubnet)})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestHandler_List_Empty(t *testing.T) {
	h, _, _ := minimalHandler(t, true)
	resp, err := h.List(context.Background(), &vpcv1.ListNetworkInterfacesRequest{ProjectId: "f1"})
	require.NoError(t, err)
	assert.Empty(t, resp.NetworkInterfaces)
}

func TestHandler_Create_Validates(t *testing.T) {
	h, _, _ := minimalHandler(t, true)
	_, err := h.Create(context.Background(), &vpcv1.CreateNetworkInterfaceRequest{Name: "nic"})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestHandler_Update_RequiresID(t *testing.T) {
	h, _, _ := minimalHandler(t, true)
	_, err := h.Update(context.Background(), &vpcv1.UpdateNetworkInterfaceRequest{NetworkInterfaceId: ""})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestHandler_Delete_InvalidArg(t *testing.T) {
	h, _, _ := minimalHandler(t, true)
	_, err := h.Delete(context.Background(), &vpcv1.DeleteNetworkInterfaceRequest{NetworkInterfaceId: ""})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestHandler_ListOperations_RequiresID(t *testing.T) {
	h, _, _ := minimalHandler(t, true)
	_, err := h.ListOperations(context.Background(), &vpcv1.ListNetworkInterfaceOperationsRequest{NetworkInterfaceId: ""})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

// ---- use-case-level ----

func TestCreateUseCase_ProjectRequired(t *testing.T) {
	kr := kachomock.NewRepository()
	or := repomock.NewOpsRepo()
	uc := NewCreateNetworkInterfaceUseCase(kr, &repomock.ProjectClient{OK: true}, or)

	_, err := uc.Execute(context.Background(), CreateInput{NetworkInterface: domain.NetworkInterface{Name: "nic"}})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestCreateUseCase_SubnetRequired(t *testing.T) {
	kr := kachomock.NewRepository()
	or := repomock.NewOpsRepo()
	uc := NewCreateNetworkInterfaceUseCase(kr, &repomock.ProjectClient{OK: true}, or)

	_, err := uc.Execute(context.Background(), CreateInput{NetworkInterface: domain.NetworkInterface{ProjectID: "f1", Name: "nic"}})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestCreateUseCase_CardinalityV4_TooMany(t *testing.T) {
	kr := kachomock.NewRepository()
	or := repomock.NewOpsRepo()
	uc := NewCreateNetworkInterfaceUseCase(kr, &repomock.ProjectClient{OK: true}, or)

	_, err := uc.Execute(context.Background(), CreateInput{NetworkInterface: domain.NetworkInterface{
		ProjectID:    "f1",
		Name:         "nic",
		SubnetID:     "e9bsub1",
		V4AddressIDs: []string{"e9ba1", "e9ba2"},
	}})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

// TestCreateUseCase_AttachesAddressInSameWriterTX — Create аттачит pre-reserved
// Address в ТОЙ ЖЕ writer-TX, что и Insert NIC (атомарность: reservation +
// insert коммитятся/откатываются вместе; на краше нет orphan used=true без NIC).
//
// RED→GREEN: до фикса Create аттачил через отдельный injected addressRepo (иной
// store/TX), поэтому адрес, засиженный в NIC-репо (kachomock), там не находился →
// op падал "address ... not found". После фикса attach идёт через
// `w.Addresses()` (единый writer-TX) → адрес найден, помечен used=true и
// закоммичен атомарно с NIC.
func TestCreateUseCase_AttachesAddressInSameWriterTX(t *testing.T) {
	kr := kachomock.NewRepository()
	or := repomock.NewOpsRepo()
	kr.SeedSubnet(&kachorepo.SubnetRecord{
		Subnet: domain.Subnet{ID: "e9bsub1", ProjectID: "f1", Name: domain.RcNameVPC("sn")},
	})
	// Pre-reserved internal-IPv4 адрес в NIC-репо (kachomock) — used=false.
	kr.SeedAddress(&kachorepo.AddressRecord{Address: domain.Address{
		ID: "e9ba1", ProjectID: "f1", Type: domain.AddressTypeInternal,
		IpVersion: domain.IpVersionIPv4, Used: false,
		InternalIpv4: &domain.InternalIpv4Spec{SubnetID: "e9bsub1", Address: "10.0.0.5"},
	}})
	uc := NewCreateNetworkInterfaceUseCase(kr, &repomock.ProjectClient{OK: true}, or)

	op, err := uc.Execute(context.Background(), CreateInput{NetworkInterface: domain.NetworkInterface{
		ProjectID:    "f1",
		Name:         "nic",
		SubnetID:     "e9bsub1",
		V4AddressIDs: []string{"e9ba1"},
	}})
	require.NoError(t, err)

	saved := repomock.AwaitOpDone(t, or, op.ID)
	require.True(t, saved.Done)
	require.Nil(t, saved.Error, "attach+insert must commit atomically in one writer-TX")

	// Адрес помечен used=true в committed-state NIC-репо (attach осел вместе с NIC).
	rd, err := kr.Reader(context.Background())
	require.NoError(t, err)
	a, err := rd.Addresses().Get(context.Background(), "e9ba1")
	_ = rd.Close()
	require.NoError(t, err)
	require.True(t, a.Used, "attached address must be used=true after commit")
}

func TestCreateUseCase_OK(t *testing.T) {
	kr := kachomock.NewRepository()
	or := repomock.NewOpsRepo()
	// заранее заданный subnet — fixture в kachomock (CQRS-Reader.Subnets().Get).
	kr.SeedSubnet(&kachorepo.SubnetRecord{
		Subnet: domain.Subnet{ID: "e9bsub1", ProjectID: "f1", Name: domain.RcNameVPC("sn")},
	})
	uc := NewCreateNetworkInterfaceUseCase(kr, &repomock.ProjectClient{OK: true}, or)

	op, err := uc.Execute(context.Background(), CreateInput{NetworkInterface: domain.NetworkInterface{
		ProjectID: "f1",
		Name:      "nic",
		SubnetID:  "e9bsub1",
	}})
	require.NoError(t, err)
	require.NotEmpty(t, op.ID)

	saved := repomock.AwaitOpDone(t, or, op.ID)
	assert.True(t, saved.Done)
	assert.Nil(t, saved.Error)
}

// TestCreateUseCase_ProjectNotFound — при отсутствующем Project async doCreate
// возвращает NotFound с каноническим текстом "<Resource> %s not found".
func TestCreateUseCase_ProjectNotFound(t *testing.T) {
	kr := kachomock.NewRepository()
	or := repomock.NewOpsRepo()
	kr.SeedSubnet(&kachorepo.SubnetRecord{
		Subnet: domain.Subnet{ID: "e9bsub1", ProjectID: "f1", Name: domain.RcNameVPC("sn")},
	})
	uc := NewCreateNetworkInterfaceUseCase(kr, &repomock.ProjectClient{OK: false}, or)

	op, err := uc.Execute(context.Background(), CreateInput{NetworkInterface: domain.NetworkInterface{
		ProjectID: "f1",
		Name:      "nic",
		SubnetID:  "e9bsub1",
	}})
	require.NoError(t, err)
	require.NotEmpty(t, op.ID)

	saved := repomock.AwaitOpDone(t, or, op.ID)
	require.True(t, saved.Done)
	require.NotNil(t, saved.Error, "operation should fail in worker — project missing")
	assert.Equal(t, int32(codes.NotFound), saved.Error.Code)
	assert.Equal(t, "Project f1 not found", saved.Error.Message)
}

func TestUpdateUseCase_RequiresID(t *testing.T) {
	uc := NewUpdateNetworkInterfaceUseCase(kachomock.NewRepository(), repomock.NewOpsRepo())
	_, err := uc.Execute(context.Background(), UpdateInput{NetworkInterfaceID: ""})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestDeleteUseCase_InvalidArg(t *testing.T) {
	uc := NewDeleteNetworkInterfaceUseCase(kachomock.NewRepository(), repomock.NewOpsRepo())
	_, err := uc.Execute(context.Background(), "")
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestListUseCase_RequiresProject(t *testing.T) {
	uc := NewListNetworkInterfacesUseCase(kachomock.NewRepository(), nil)
	_, _, err := uc.Execute(context.Background(), "", NetworkInterfaceFilter{}, Pagination{})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

// ---- delete precondition / response = Empty ----

func TestDeleteUseCase_BlockedByAttached(t *testing.T) {
	kr := kachomock.NewRepository()
	or := repomock.NewOpsRepo()
	nicID := ids.NewID(ids.PrefixSubnet)
	preloadNIC(t, kr, &kachorepo.NetworkInterfaceRecord{
		NetworkInterface: domain.NetworkInterface{
			ID:         nicID,
			ProjectID:  "f1",
			SubnetID:   "e9bsub1",
			UsedByType: "compute_instance",
			UsedByID:   "my-instance",
			Status:     domain.NIStatusActive,
		},
	})
	uc := NewDeleteNetworkInterfaceUseCase(kr, or)
	op, err := uc.Execute(context.Background(), nicID)
	require.NoError(t, err)
	saved := repomock.AwaitOpDone(t, or, op.ID)
	require.NotNil(t, saved.Error)
	assert.Equal(t, int32(codes.FailedPrecondition), saved.Error.Code)
}

func TestDeleteUseCase_ResponseIsEmpty(t *testing.T) {
	kr := kachomock.NewRepository()
	or := repomock.NewOpsRepo()
	nicID := ids.NewID(ids.PrefixSubnet)
	preloadNIC(t, kr, &kachorepo.NetworkInterfaceRecord{
		NetworkInterface: domain.NetworkInterface{
			ID:        nicID,
			ProjectID: "f1",
			SubnetID:  "e9bsub1",
			Status:    domain.NIStatusAvailable,
		},
	})
	uc := NewDeleteNetworkInterfaceUseCase(kr, or)
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
			ProjectID:        "f1",
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
