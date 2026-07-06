// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package address

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

// Тесты Address use-case'ов и handler'а.
//
// Address use-cases работают через CQRS-Repository — mock здесь
// `kachomock.NewRepository()` (in-memory CQRS-impl с TX-семантикой и
// outbox-буфером). SubnetReader пока legacy `repomock.SubnetRepo` — он
// удовлетворяет узкому интерфейсу SubnetReader, которого ждет Address-UC.
//
// pools=nil во всех use-case-тестах → AllocateExternalIP/v6 недоступны;
// проверяем pure-Create / Update / Delete / Get / List paths. Allocate-flow
// покрыт в integration-тестах.

// ---- helpers ----------------------------------------------------------------

func makeSubnet(sr *repomock.SubnetRepo, networkID string) *domain.Subnet {
	s := &domain.Subnet{
		ID:           ids.NewID(ids.PrefixSubnet),
		ProjectID:    "f1",
		NetworkID:    networkID,
		Name:         domain.RcNameVPC("test-subnet"),
		V4CidrBlocks: []string{"10.0.0.0/24"},
	}
	_, _ = sr.Insert(context.Background(), s)
	return s
}

func makeHandler(t *testing.T,
	kr *kachomock.Repository,
	sr *repomock.SubnetRepo,
	or *repomock.OpsRepo,
	fc *repomock.ProjectClient,
) *Handler {
	t.Helper()
	create := NewCreateAddressUseCase(kr, sr, fc, or, nil)
	update := NewUpdateAddressUseCase(kr, or)
	deleteUC := NewDeleteAddressUseCase(kr, or)
	get := NewGetAddressUseCase(kr, nil)
	getByValue := NewGetByValueUseCase(kr, nil)
	list := NewListAddressesUseCase(kr, nil)
	listBySubnet := NewListBySubnetUseCase(kr, sr)
	listOps := NewListOperationsUseCase(or)
	return NewHandler(create, update, deleteUC, get, getByValue, list, listBySubnet, listOps, nil)
}

func minimalHandler(t *testing.T, projectOK bool) (*Handler, *repomock.OpsRepo, *kachomock.Repository, *repomock.SubnetRepo) {
	t.Helper()
	kr := kachomock.NewRepository()
	sr := repomock.NewSubnetRepo()
	or := repomock.NewOpsRepo()
	fc := &repomock.ProjectClient{OK: projectOK}
	return makeHandler(t, kr, sr, or, fc), or, kr, sr
}

// ---- Handler — sync paths ---------------------------------------------------

func TestHandler_Get_InvalidArg(t *testing.T) {
	h, _, _, _ := minimalHandler(t, true)
	_, err := h.Get(context.Background(), &vpcv1.GetAddressRequest{AddressId: ""})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestHandler_Get_NotFound(t *testing.T) {
	h, _, _, _ := minimalHandler(t, true)
	_, err := h.Get(context.Background(), &vpcv1.GetAddressRequest{AddressId: ids.NewID(ids.PrefixAddress)})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestHandler_List_Empty(t *testing.T) {
	h, _, _, _ := minimalHandler(t, true)
	resp, err := h.List(context.Background(), &vpcv1.ListAddressesRequest{ProjectId: "f1"})
	require.NoError(t, err)
	assert.Empty(t, resp.Addresses)
}

func TestHandler_Update_InvalidArg(t *testing.T) {
	h, _, _, _ := minimalHandler(t, true)
	_, err := h.Update(context.Background(), &vpcv1.UpdateAddressRequest{AddressId: ""})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestHandler_Delete_InvalidArg(t *testing.T) {
	h, _, _, _ := minimalHandler(t, true)
	_, err := h.Delete(context.Background(), &vpcv1.DeleteAddressRequest{AddressId: ""})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestHandler_ListOperations_RequiresID(t *testing.T) {
	h, _, _, _ := minimalHandler(t, true)
	_, err := h.ListOperations(context.Background(), &vpcv1.ListAddressOperationsRequest{AddressId: ""})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestHandler_GetByValue_Empty(t *testing.T) {
	h, _, _, _ := minimalHandler(t, true)
	_, err := h.GetByValue(context.Background(), &vpcv1.GetAddressByValueRequest{})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestHandler_ListBySubnet_RequiresID(t *testing.T) {
	h, _, _, _ := minimalHandler(t, true)
	_, err := h.ListBySubnet(context.Background(), &vpcv1.ListAddressesBySubnetRequest{SubnetId: ""})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

// ---- use-case-level ---------------------------------------------------------

func TestCreateUseCase_NoSpec(t *testing.T) {
	kr := kachomock.NewRepository()
	sr := repomock.NewSubnetRepo()
	or := repomock.NewOpsRepo()
	uc := NewCreateAddressUseCase(kr, sr, &repomock.ProjectClient{OK: true}, or, nil)

	_, err := uc.Execute(context.Background(), CreateInput{ProjectID: "f1"})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestCreateUseCase_RequiresProject(t *testing.T) {
	kr := kachomock.NewRepository()
	sr := repomock.NewSubnetRepo()
	or := repomock.NewOpsRepo()
	uc := NewCreateAddressUseCase(kr, sr, &repomock.ProjectClient{OK: true}, or, nil)

	_, err := uc.Execute(context.Background(), CreateInput{
		ExternalSpec: &ExternalAddrSpec{ZoneID: "zone-a"},
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestCreateUseCase_External_OK(t *testing.T) {
	kr := kachomock.NewRepository()
	sr := repomock.NewSubnetRepo()
	or := repomock.NewOpsRepo()
	uc := NewCreateAddressUseCase(kr, sr, &repomock.ProjectClient{OK: true}, or, nil)
	listUC := NewListAddressesUseCase(kr, nil)

	op, err := uc.Execute(context.Background(), CreateInput{
		ProjectID: "f1",
		Name:      "addr1",
		ExternalSpec: &ExternalAddrSpec{
			Address: "203.0.113.10",
			ZoneID:  "zone-a",
		},
	})
	require.NoError(t, err)
	require.NotEmpty(t, op.ID)

	savedOp := repomock.AwaitOpDone(t, or, op.ID)
	assert.True(t, savedOp.Done)
	assert.Nil(t, savedOp.Error)

	addrs, _, err := listUC.Execute(context.Background(), "", AddressFilter{ProjectID: "f1"}, Pagination{})
	require.NoError(t, err)
	require.Len(t, addrs, 1)
	assert.Equal(t, domain.AddressTypeExternal, addrs[0].Type)
	assert.Equal(t, "203.0.113.10", addrs[0].ExternalIpv4.Address)
}

// TestCreateUseCase_ProjectNotFound — отсутствующий Project: async doCreate
// возвращает NotFound с каноническим текстом "<Resource> %s not found".
func TestCreateUseCase_ProjectNotFound(t *testing.T) {
	kr := kachomock.NewRepository()
	sr := repomock.NewSubnetRepo()
	or := repomock.NewOpsRepo()
	uc := NewCreateAddressUseCase(kr, sr, &repomock.ProjectClient{OK: false}, or, nil)

	op, err := uc.Execute(context.Background(), CreateInput{
		ProjectID: "f1",
		Name:      "addr1",
		ExternalSpec: &ExternalAddrSpec{
			Address: "203.0.113.10",
			ZoneID:  "zone-a",
		},
	})
	require.NoError(t, err)
	require.NotEmpty(t, op.ID)

	savedOp := repomock.AwaitOpDone(t, or, op.ID)
	require.True(t, savedOp.Done)
	require.NotNil(t, savedOp.Error, "operation should fail in worker — project missing")
	assert.Equal(t, int32(codes.NotFound), savedOp.Error.Code)
	assert.Equal(t, "Project f1 not found", savedOp.Error.Message)
}

// pools=nil → service stays pure (no auto-alloc); используется при load-test
// конфигурациях / unit-тестах без IPAM.
func TestCreateUseCase_External_NoAutoAlloc_PoolsNil(t *testing.T) {
	kr := kachomock.NewRepository()
	sr := repomock.NewSubnetRepo()
	or := repomock.NewOpsRepo()
	uc := NewCreateAddressUseCase(kr, sr, &repomock.ProjectClient{OK: true}, or, nil)
	listUC := NewListAddressesUseCase(kr, nil)

	op, err := uc.Execute(context.Background(), CreateInput{
		ProjectID:    "f1",
		ExternalSpec: &ExternalAddrSpec{ZoneID: "zone-a"},
	})
	require.NoError(t, err)
	repomock.AwaitOpDone(t, or, op.ID)

	addrs, _, _ := listUC.Execute(context.Background(), "", AddressFilter{ProjectID: "f1"}, Pagination{})
	require.Len(t, addrs, 1)
	assert.Equal(t, "", addrs[0].ExternalIpv4.Address,
		"with pools=nil use-case must NOT auto-allocate")
	assert.Equal(t, "zone-a", addrs[0].ExternalIpv4.ZoneID)
}

// TestCreateUseCase_CrossProjectSubnet_Denied — BOLA-guard: internal Address в
// проекте "f1" НЕ может ссылаться на Subnet чужого проекта ("other"). Cross-project
// → NotFound (тот же ответ, что для несуществующего subnet — без existence-oracle).
// RED до фикса: reference на чужую подсеть проходил (subnetReader.Get не сверял
// project) и внутренний адрес аллоцировался в чужой сети.
func TestCreateUseCase_CrossProjectSubnet_Denied(t *testing.T) {
	kr := kachomock.NewRepository()
	sr := repomock.NewSubnetRepo()
	or := repomock.NewOpsRepo()
	// Subnet принадлежит проекту "other".
	foreign := &domain.Subnet{
		ID:           ids.NewID(ids.PrefixSubnet),
		ProjectID:    "other",
		NetworkID:    ids.NewID(ids.PrefixNetwork),
		Name:         domain.RcNameVPC("sn"),
		V4CidrBlocks: []string{"10.0.0.0/24"},
	}
	_, _ = sr.Insert(context.Background(), foreign)
	uc := NewCreateAddressUseCase(kr, sr, &repomock.ProjectClient{OK: true}, or, nil)

	_, err := uc.Execute(context.Background(), CreateInput{
		ProjectID:    "f1",
		InternalSpec: &InternalAddrSpec{SubnetID: foreign.ID},
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
	assert.Equal(t, "Subnet "+foreign.ID+" not found", st.Message())
}

func TestCreateUseCase_Internal_WithSubnet(t *testing.T) {
	kr := kachomock.NewRepository()
	sr := repomock.NewSubnetRepo()
	or := repomock.NewOpsRepo()
	sub := makeSubnet(sr, ids.NewID(ids.PrefixNetwork))
	uc := NewCreateAddressUseCase(kr, sr, &repomock.ProjectClient{OK: true}, or, nil)
	listUC := NewListAddressesUseCase(kr, nil)

	op, err := uc.Execute(context.Background(), CreateInput{
		ProjectID: "f1",
		InternalSpec: &InternalAddrSpec{
			SubnetID: sub.ID,
		},
	})
	require.NoError(t, err)
	repomock.AwaitOpDone(t, or, op.ID)

	addrs, _, _ := listUC.Execute(context.Background(), "", AddressFilter{ProjectID: "f1"}, Pagination{})
	require.Len(t, addrs, 1)
	assert.Equal(t, domain.AddressTypeInternal, addrs[0].Type)
	assert.Equal(t, sub.ID, addrs[0].InternalIpv4.SubnetID)
}

// Sync-валидация: explicit IP вне CIDR subnet → InvalidArgument до Operation.
func TestCreateUseCase_Internal_ExplicitIP_OutOfCIDR(t *testing.T) {
	kr := kachomock.NewRepository()
	sr := repomock.NewSubnetRepo()
	or := repomock.NewOpsRepo()
	sub := makeSubnet(sr, ids.NewID(ids.PrefixNetwork))
	uc := NewCreateAddressUseCase(kr, sr, &repomock.ProjectClient{OK: true}, or, nil)

	_, err := uc.Execute(context.Background(), CreateInput{
		ProjectID: "f1",
		InternalSpec: &InternalAddrSpec{
			SubnetID: sub.ID,
			Address:  "192.168.1.5", // вне 10.0.0.0/24
		},
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestCreateUseCase_Internal_ExplicitIP_InCIDR(t *testing.T) {
	kr := kachomock.NewRepository()
	sr := repomock.NewSubnetRepo()
	or := repomock.NewOpsRepo()
	sub := makeSubnet(sr, ids.NewID(ids.PrefixNetwork))
	uc := NewCreateAddressUseCase(kr, sr, &repomock.ProjectClient{OK: true}, or, nil)
	listUC := NewListAddressesUseCase(kr, nil)

	op, err := uc.Execute(context.Background(), CreateInput{
		ProjectID: "f1",
		InternalSpec: &InternalAddrSpec{
			SubnetID: sub.ID,
			Address:  "10.0.0.5",
		},
	})
	require.NoError(t, err)
	repomock.AwaitOpDone(t, or, op.ID)
	addrs, _, _ := listUC.Execute(context.Background(), "", AddressFilter{ProjectID: "f1"}, Pagination{})
	require.Len(t, addrs, 1)
	assert.Equal(t, "10.0.0.5", addrs[0].InternalIpv4.Address)
}

func TestCreateUseCase_Internal_ExplicitIP_BadFormat(t *testing.T) {
	kr := kachomock.NewRepository()
	sr := repomock.NewSubnetRepo()
	or := repomock.NewOpsRepo()
	sub := makeSubnet(sr, ids.NewID(ids.PrefixNetwork))
	uc := NewCreateAddressUseCase(kr, sr, &repomock.ProjectClient{OK: true}, or, nil)

	_, err := uc.Execute(context.Background(), CreateInput{
		ProjectID: "f1",
		InternalSpec: &InternalAddrSpec{
			SubnetID: sub.ID,
			Address:  "not-an-ip",
		},
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestUpdateUseCase_DeletionProtection(t *testing.T) {
	kr := kachomock.NewRepository()
	or := repomock.NewOpsRepo()
	addrID := ids.NewID(ids.PrefixAddress)
	rec := &kachorepo.AddressRecord{Address: domain.Address{
		ID:                 addrID,
		ProjectID:          "f1",
		Name:               "addr",
		DeletionProtection: false,
	}}
	kr.SeedAddress(rec)

	uc := NewUpdateAddressUseCase(kr, or)
	op, err := uc.Execute(context.Background(), UpdateInput{
		AddressID:          addrID,
		DeletionProtection: true,
		UpdateMask:         []string{"deletion_protection"},
	})
	require.NoError(t, err)
	repomock.AwaitOpDone(t, or, op.ID)

	rd, _ := kr.Reader(context.Background())
	got, _ := rd.Addresses().Get(context.Background(), addrID)
	_ = rd.Close()
	assert.True(t, got.DeletionProtection)
}

func TestUpdateUseCase_UnknownMask(t *testing.T) {
	kr := kachomock.NewRepository()
	uc := NewUpdateAddressUseCase(kr, repomock.NewOpsRepo())
	_, err := uc.Execute(context.Background(), UpdateInput{
		AddressID:  ids.NewID(ids.PrefixAddress),
		UpdateMask: []string{"unknown_field"},
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestDeleteUseCase_NotFound(t *testing.T) {
	kr := kachomock.NewRepository()
	uc := NewDeleteAddressUseCase(kr, repomock.NewOpsRepo())
	_, err := uc.Execute(context.Background(), ids.NewID(ids.PrefixAddress))
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestDeleteUseCase_DeletionProtection(t *testing.T) {
	kr := kachomock.NewRepository()
	or := repomock.NewOpsRepo()
	addrID := ids.NewID(ids.PrefixAddress)
	rec := &kachorepo.AddressRecord{Address: domain.Address{
		ID:                 addrID,
		ProjectID:          "f1",
		DeletionProtection: true,
	}}
	kr.SeedAddress(rec)
	uc := NewDeleteAddressUseCase(kr, or)
	_, err := uc.Execute(context.Background(), addrID)
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
}

// `Used=true` блокирует Delete независимо от того, есть ли referrer-row.
// kachomock's GetReference возвращает ErrNotFound (stub) — Delete UC все равно
// должен вернуть FailedPrecondition "in use" (общий случай).
func TestDeleteUseCase_InUseByNIC(t *testing.T) {
	kr := kachomock.NewRepository()
	or := repomock.NewOpsRepo()
	addrID := ids.NewID(ids.PrefixAddress)
	rec := &kachorepo.AddressRecord{Address: domain.Address{
		ID:        addrID,
		ProjectID: "f1",
		Used:      true,
	}}
	kr.SeedAddress(rec)
	uc := NewDeleteAddressUseCase(kr, or)
	_, err := uc.Execute(context.Background(), addrID)
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
}

func TestListUseCase_RequiresProject(t *testing.T) {
	kr := kachomock.NewRepository()
	uc := NewListAddressesUseCase(kr, nil)
	_, _, err := uc.Execute(context.Background(), "", AddressFilter{}, Pagination{})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestListBySubnetUseCase_NotFound(t *testing.T) {
	kr := kachomock.NewRepository()
	uc := NewListBySubnetUseCase(kr, repomock.NewSubnetRepo())
	_, _, err := uc.Execute(context.Background(), ids.NewID(ids.PrefixSubnet), Pagination{})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestGetByValueUseCase_Empty(t *testing.T) {
	kr := kachomock.NewRepository()
	uc := NewGetByValueUseCase(kr, nil)
	_, err := uc.Execute(context.Background(), "", "", "", "")
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestListOperationsUseCase_UnknownID_Empty(t *testing.T) {
	or := repomock.NewOpsRepo()
	uc := NewListOperationsUseCase(or)
	ops, _, err := uc.Execute(context.Background(), ids.NewID(ids.PrefixAddress), Pagination{})
	require.NoError(t, err)
	assert.Empty(t, ops)
}

// ---- Handler happy-path -----------------------------------------------------

func TestHandler_FullFlow(t *testing.T) {
	h, or, _, _ := minimalHandler(t, true)

	createOp, err := h.Create(context.Background(), &vpcv1.CreateAddressRequest{
		ProjectId: "f1", Name: "addr",
		AddressSpec: &vpcv1.CreateAddressRequest_ExternalIpv4AddressSpec{
			ExternalIpv4AddressSpec: &vpcv1.ExternalIpv4AddressSpec{
				Address: "203.0.113.10",
				ZoneId:  "zone-a",
			},
		},
	})
	require.NoError(t, err)
	repomock.AwaitOpDone(t, or, createOp.Id)

	resp, _ := h.List(context.Background(), &vpcv1.ListAddressesRequest{ProjectId: "f1"})
	require.Len(t, resp.Addresses, 1)
	addrID := resp.Addresses[0].Id

	_, err = h.Get(context.Background(), &vpcv1.GetAddressRequest{AddressId: addrID})
	require.NoError(t, err)

	updOp, err := h.Update(context.Background(), &vpcv1.UpdateAddressRequest{
		AddressId: addrID, Name: "addr-upd",
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"name"}},
	})
	require.NoError(t, err)
	repomock.AwaitOpDone(t, or, updOp.Id)

	_, err = h.ListOperations(context.Background(), &vpcv1.ListAddressOperationsRequest{AddressId: addrID})
	require.NoError(t, err)

	delOp, err := h.Delete(context.Background(), &vpcv1.DeleteAddressRequest{AddressId: addrID})
	require.NoError(t, err)
	saved := repomock.AwaitOpDone(t, or, delOp.Id)
	require.Nil(t, saved.Error)
	require.NotNil(t, saved.Response)
	var empty emptypb.Empty
	require.NoError(t, saved.Response.UnmarshalTo(&empty), "Delete response must be google.protobuf.Empty")
}

func TestAddressToPb_External(t *testing.T) {
	rec := &kachorepo.AddressRecord{
		Address: domain.Address{
			ID:        "e9b-test",
			ProjectID: "f1",
			Type:      domain.AddressTypeExternal,
			ExternalIpv4: &domain.ExternalIpv4Spec{
				Address: "203.0.113.5",
				ZoneID:  "zone-a",
			},
		},
	}
	p, err := addressToPb(rec)
	require.NoError(t, err)
	assert.Equal(t, "e9b-test", p.Id)
	assert.Equal(t, "203.0.113.5", p.GetExternalIpv4Address().GetAddress())
}

// Публичная проекция used_by выдает referrer'а с type/id/name/owned — tenant
// видит, кто владеет/использует адрес (напр. network_load_balancer).
func TestAddressToPb_UsedByReferrerNameAndOwned(t *testing.T) {
	rec := &kachorepo.AddressRecord{
		Address: domain.Address{
			ID:        "e9b-usedby",
			ProjectID: "f1",
			Type:      domain.AddressTypeInternal,
			IpVersion: domain.IpVersionIPv4,
			Used:      true,
			UsedBy: []*domain.AddressReference{{
				AddressID:    "e9b-usedby",
				ReferrerType: "network_load_balancer",
				ReferrerID:   "nlb00000000000001",
				ReferrerName: "lb-name",
				Owned:        true,
			}},
		},
	}
	p, err := addressToPb(rec)
	require.NoError(t, err)
	require.Len(t, p.UsedBy, 1)
	ref := p.UsedBy[0]
	assert.Equal(t, "network_load_balancer", ref.GetReferrer().GetType())
	assert.Equal(t, "nlb00000000000001", ref.GetReferrer().GetId())
	assert.Equal(t, "lb-name", ref.GetReferrer().GetName())
	assert.True(t, ref.GetOwned())
}

// Delete-guard: referrer типа network_load_balancer дает обобщенное сообщение
// (шаблон общий с NIC), а не generic "is in use".
func TestDeleteUseCase_InUseByLoadBalancer_GeneralizedMessage(t *testing.T) {
	kr := kachomock.NewRepository()
	or := repomock.NewOpsRepo()
	addrID := ids.NewID(ids.PrefixAddress)
	kr.SeedAddress(&kachorepo.AddressRecord{Address: domain.Address{
		ID:        addrID,
		ProjectID: "prj-A",
		Used:      true,
	}})
	kr.SeedReference(&domain.AddressReference{
		AddressID:    addrID,
		ReferrerType: "network_load_balancer",
		ReferrerID:   "nlb00000000000001",
		ReferrerName: "lb-name",
		Owned:        true,
	})
	uc := NewDeleteAddressUseCase(kr, or)
	_, err := uc.Execute(context.Background(), addrID)
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
	assert.Equal(t,
		"address "+addrID+" is in use by network_load_balancer lb-name; detach it before deleting the address",
		st.Message())
}

// Delete-guard: NIC-referrer дает тот же шаблон (human-label "network interface").
func TestDeleteUseCase_InUseByNIC_GeneralizedMessage(t *testing.T) {
	kr := kachomock.NewRepository()
	or := repomock.NewOpsRepo()
	addrID := ids.NewID(ids.PrefixAddress)
	kr.SeedAddress(&kachorepo.AddressRecord{Address: domain.Address{
		ID:        addrID,
		ProjectID: "prj-A",
		Used:      true,
	}})
	kr.SeedReference(&domain.AddressReference{
		AddressID:    addrID,
		ReferrerType: "network_interface",
		ReferrerID:   "eni0000000000001",
		ReferrerName: "nic-1",
	})
	uc := NewDeleteAddressUseCase(kr, or)
	_, err := uc.Execute(context.Background(), addrID)
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
	assert.Equal(t,
		"address "+addrID+" is in use by network interface nic-1; detach it before deleting the address",
		st.Message())
}
