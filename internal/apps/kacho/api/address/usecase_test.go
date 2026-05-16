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
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/repomock"
)

// Тесты Address use-case'ов и handler'а. Wave 3 (KAC-94): сюда переехали
// прежние тесты `internal/handler/address_handler_test.go`,
// `internal/service/address_test.go`, плюс Address-блок `coverage*_test.go`.
//
// pools=nil во всех use-case-тестах → AllocateExternalIP/v6 недоступны;
// проверяем pure-Create / Update / Delete / Move / Get / List paths. Allocate-
// flow покрыт в integration-тестах (`internal/repo/ipam_cascade_integration_test.go`
// + `internal/service/address_allocate_bench_test.go`).

// ---- helpers ----------------------------------------------------------------

func makeSubnet(sr *repomock.SubnetRepo, networkID string) *domain.Subnet {
	s := &domain.Subnet{
		ID:           ids.NewID(ids.PrefixSubnet),
		FolderID:     "f1",
		NetworkID:    networkID,
		Name:         domain.RcNameVPC("test-subnet"),
		V4CidrBlocks: []string{"10.0.0.0/24"},
	}
	_, _ = sr.Insert(context.Background(), s)
	return s
}

func makeHandler(t *testing.T,
	ar *repomock.AddressRepo,
	sr *repomock.SubnetRepo,
	or *repomock.OpsRepo,
	fc *repomock.FolderClient,
) *Handler {
	t.Helper()
	create := NewCreateAddressUseCase(ar, sr, fc, or, nil)
	update := NewUpdateAddressUseCase(ar, or)
	deleteUC := NewDeleteAddressUseCase(ar, or)
	move := NewMoveAddressUseCase(ar, fc, or)
	get := NewGetAddressUseCase(ar)
	getByValue := NewGetByValueUseCase(ar)
	list := NewListAddressesUseCase(ar)
	listBySubnet := NewListBySubnetUseCase(ar, sr)
	listOps := NewListOperationsUseCase(or)
	return NewHandler(create, update, deleteUC, move, get, getByValue, list, listBySubnet, listOps, nil)
}

func minimalHandler(t *testing.T, folderOK bool) (*Handler, *repomock.OpsRepo, *repomock.AddressRepo, *repomock.SubnetRepo) {
	t.Helper()
	ar := repomock.NewAddressRepo()
	sr := repomock.NewSubnetRepo()
	or := repomock.NewOpsRepo()
	fc := &repomock.FolderClient{OK: folderOK}
	return makeHandler(t, ar, sr, or, fc), or, ar, sr
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
	resp, err := h.List(context.Background(), &vpcv1.ListAddressesRequest{FolderId: "f1"})
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

func TestHandler_Move_RequiresID(t *testing.T) {
	h, _, _, _ := minimalHandler(t, true)
	_, err := h.Move(context.Background(), &vpcv1.MoveAddressRequest{AddressId: ""})
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
	ar := repomock.NewAddressRepo()
	sr := repomock.NewSubnetRepo()
	or := repomock.NewOpsRepo()
	uc := NewCreateAddressUseCase(ar, sr, &repomock.FolderClient{OK: true}, or, nil)

	_, err := uc.Execute(context.Background(), CreateInput{FolderID: "f1"})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestCreateUseCase_RequiresFolder(t *testing.T) {
	ar := repomock.NewAddressRepo()
	sr := repomock.NewSubnetRepo()
	or := repomock.NewOpsRepo()
	uc := NewCreateAddressUseCase(ar, sr, &repomock.FolderClient{OK: true}, or, nil)

	_, err := uc.Execute(context.Background(), CreateInput{
		ExternalSpec: &ExternalAddrSpec{ZoneID: "ru-central1-a"},
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestCreateUseCase_External_OK(t *testing.T) {
	ar := repomock.NewAddressRepo()
	sr := repomock.NewSubnetRepo()
	or := repomock.NewOpsRepo()
	uc := NewCreateAddressUseCase(ar, sr, &repomock.FolderClient{OK: true}, or, nil)
	listUC := NewListAddressesUseCase(ar)

	op, err := uc.Execute(context.Background(), CreateInput{
		FolderID: "f1",
		Name:     "addr1",
		ExternalSpec: &ExternalAddrSpec{
			Address: "203.0.113.10",
			ZoneID:  "ru-central1-a",
		},
	})
	require.NoError(t, err)
	require.NotEmpty(t, op.ID)

	savedOp := repomock.AwaitOpDone(t, or, op.ID)
	assert.True(t, savedOp.Done)
	assert.Nil(t, savedOp.Error)

	addrs, _, err := listUC.Execute(context.Background(), AddressFilter{FolderID: "f1"}, Pagination{})
	require.NoError(t, err)
	require.Len(t, addrs, 1)
	assert.Equal(t, domain.AddressTypeExternal, addrs[0].Type)
	assert.Equal(t, "203.0.113.10", addrs[0].ExternalIpv4.Address)
}

// pools=nil → service stays pure (no auto-alloc); используется при load-test
// конфигурациях / unit-тестах без IPAM.
func TestCreateUseCase_External_NoAutoAlloc_PoolsNil(t *testing.T) {
	ar := repomock.NewAddressRepo()
	sr := repomock.NewSubnetRepo()
	or := repomock.NewOpsRepo()
	uc := NewCreateAddressUseCase(ar, sr, &repomock.FolderClient{OK: true}, or, nil)
	listUC := NewListAddressesUseCase(ar)

	op, err := uc.Execute(context.Background(), CreateInput{
		FolderID:     "f1",
		ExternalSpec: &ExternalAddrSpec{ZoneID: "ru-central1-a"},
	})
	require.NoError(t, err)
	repomock.AwaitOpDone(t, or, op.ID)

	addrs, _, _ := listUC.Execute(context.Background(), AddressFilter{FolderID: "f1"}, Pagination{})
	require.Len(t, addrs, 1)
	assert.Equal(t, "", addrs[0].ExternalIpv4.Address,
		"with pools=nil use-case must NOT auto-allocate")
	assert.Equal(t, "ru-central1-a", addrs[0].ExternalIpv4.ZoneID)
}

func TestCreateUseCase_Internal_WithSubnet(t *testing.T) {
	ar := repomock.NewAddressRepo()
	sr := repomock.NewSubnetRepo()
	or := repomock.NewOpsRepo()
	sub := makeSubnet(sr, ids.NewID(ids.PrefixNetwork))
	uc := NewCreateAddressUseCase(ar, sr, &repomock.FolderClient{OK: true}, or, nil)
	listUC := NewListAddressesUseCase(ar)

	op, err := uc.Execute(context.Background(), CreateInput{
		FolderID: "f1",
		InternalSpec: &InternalAddrSpec{
			SubnetID: sub.ID,
		},
	})
	require.NoError(t, err)
	repomock.AwaitOpDone(t, or, op.ID)

	addrs, _, _ := listUC.Execute(context.Background(), AddressFilter{FolderID: "f1"}, Pagination{})
	require.Len(t, addrs, 1)
	assert.Equal(t, domain.AddressTypeInternal, addrs[0].Type)
	assert.Equal(t, sub.ID, addrs[0].InternalIpv4.SubnetID)
}

// Sync-валидация: explicit IP вне CIDR subnet → InvalidArgument до Operation.
func TestCreateUseCase_Internal_ExplicitIP_OutOfCIDR(t *testing.T) {
	ar := repomock.NewAddressRepo()
	sr := repomock.NewSubnetRepo()
	or := repomock.NewOpsRepo()
	sub := makeSubnet(sr, ids.NewID(ids.PrefixNetwork))
	uc := NewCreateAddressUseCase(ar, sr, &repomock.FolderClient{OK: true}, or, nil)

	_, err := uc.Execute(context.Background(), CreateInput{
		FolderID: "f1",
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
	ar := repomock.NewAddressRepo()
	sr := repomock.NewSubnetRepo()
	or := repomock.NewOpsRepo()
	sub := makeSubnet(sr, ids.NewID(ids.PrefixNetwork))
	uc := NewCreateAddressUseCase(ar, sr, &repomock.FolderClient{OK: true}, or, nil)
	listUC := NewListAddressesUseCase(ar)

	op, err := uc.Execute(context.Background(), CreateInput{
		FolderID: "f1",
		InternalSpec: &InternalAddrSpec{
			SubnetID: sub.ID,
			Address:  "10.0.0.5",
		},
	})
	require.NoError(t, err)
	repomock.AwaitOpDone(t, or, op.ID)
	addrs, _, _ := listUC.Execute(context.Background(), AddressFilter{FolderID: "f1"}, Pagination{})
	require.Len(t, addrs, 1)
	assert.Equal(t, "10.0.0.5", addrs[0].InternalIpv4.Address)
}

func TestCreateUseCase_Internal_ExplicitIP_BadFormat(t *testing.T) {
	ar := repomock.NewAddressRepo()
	sr := repomock.NewSubnetRepo()
	or := repomock.NewOpsRepo()
	sub := makeSubnet(sr, ids.NewID(ids.PrefixNetwork))
	uc := NewCreateAddressUseCase(ar, sr, &repomock.FolderClient{OK: true}, or, nil)

	_, err := uc.Execute(context.Background(), CreateInput{
		FolderID: "f1",
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
	ar := repomock.NewAddressRepo()
	or := repomock.NewOpsRepo()
	rec := &kachorepo.AddressRecord{Address: domain.Address{
		ID:                 ids.NewID(ids.PrefixAddress),
		FolderID:           "f1",
		Name:               "addr",
		DeletionProtection: false,
	}}
	ar.Seed(rec)

	uc := NewUpdateAddressUseCase(ar, or)
	op, err := uc.Execute(context.Background(), UpdateInput{
		AddressID:          rec.ID,
		DeletionProtection: true,
		UpdateMask:         []string{"deletion_protection"},
	})
	require.NoError(t, err)
	repomock.AwaitOpDone(t, or, op.ID)

	got, _ := ar.Get(context.Background(), rec.ID)
	assert.True(t, got.DeletionProtection)
}

func TestUpdateUseCase_UnknownMask(t *testing.T) {
	uc := NewUpdateAddressUseCase(repomock.NewAddressRepo(), repomock.NewOpsRepo())
	_, err := uc.Execute(context.Background(), UpdateInput{
		AddressID:  ids.NewID(ids.PrefixAddress),
		UpdateMask: []string{"unknown_field"},
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestDeleteUseCase_NotFound(t *testing.T) {
	uc := NewDeleteAddressUseCase(repomock.NewAddressRepo(), repomock.NewOpsRepo())
	_, err := uc.Execute(context.Background(), ids.NewID(ids.PrefixAddress))
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestDeleteUseCase_DeletionProtection(t *testing.T) {
	ar := repomock.NewAddressRepo()
	or := repomock.NewOpsRepo()
	rec := &kachorepo.AddressRecord{Address: domain.Address{
		ID:                 ids.NewID(ids.PrefixAddress),
		FolderID:           "f1",
		DeletionProtection: true,
	}}
	ar.Seed(rec)
	uc := NewDeleteAddressUseCase(ar, or)
	_, err := uc.Execute(context.Background(), rec.ID)
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
}

func TestDeleteUseCase_InUseByNIC(t *testing.T) {
	ar := repomock.NewAddressRepo()
	or := repomock.NewOpsRepo()
	addrID := ids.NewID(ids.PrefixAddress)
	rec := &kachorepo.AddressRecord{Address: domain.Address{
		ID:       addrID,
		FolderID: "f1",
		Used:     true,
	}}
	ar.Seed(rec)
	// Add a referrer row через SetReference (mock-impl) — addresses.used уже true.
	_, _ = ar.SetReference(context.Background(), &domain.AddressReference{
		AddressID: addrID, ReferrerType: niReferrerType, ReferrerID: "e9bnic1", ReferrerName: "nic-1",
	})
	uc := NewDeleteAddressUseCase(ar, or)
	_, err := uc.Execute(context.Background(), addrID)
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
}

func TestMoveUseCase_Validates(t *testing.T) {
	uc := NewMoveAddressUseCase(repomock.NewAddressRepo(), &repomock.FolderClient{OK: true}, repomock.NewOpsRepo())
	_, err := uc.Execute(context.Background(), "", "f2")
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	_, err = uc.Execute(context.Background(), ids.NewID(ids.PrefixAddress), "")
	st, _ = status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestListUseCase_RequiresFolder(t *testing.T) {
	uc := NewListAddressesUseCase(repomock.NewAddressRepo())
	_, _, err := uc.Execute(context.Background(), AddressFilter{}, Pagination{})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestListBySubnetUseCase_NotFound(t *testing.T) {
	uc := NewListBySubnetUseCase(repomock.NewAddressRepo(), repomock.NewSubnetRepo())
	_, _, err := uc.Execute(context.Background(), ids.NewID(ids.PrefixSubnet), Pagination{})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestGetByValueUseCase_Empty(t *testing.T) {
	uc := NewGetByValueUseCase(repomock.NewAddressRepo())
	_, err := uc.Execute(context.Background(), "", "", "")
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
		FolderId: "f1", Name: "addr",
		AddressSpec: &vpcv1.CreateAddressRequest_ExternalIpv4AddressSpec{
			ExternalIpv4AddressSpec: &vpcv1.ExternalIpv4AddressSpec{
				Address: "203.0.113.10",
				ZoneId:  "ru-central1-a",
			},
		},
	})
	require.NoError(t, err)
	repomock.AwaitOpDone(t, or, createOp.Id)

	resp, _ := h.List(context.Background(), &vpcv1.ListAddressesRequest{FolderId: "f1"})
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

	moveOp, err := h.Move(context.Background(), &vpcv1.MoveAddressRequest{
		AddressId: addrID, DestinationFolderId: "f2",
	})
	require.NoError(t, err)
	repomock.AwaitOpDone(t, or, moveOp.Id)

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
			ID:       "e9b-test",
			FolderID: "f1",
			Type:     domain.AddressTypeExternal,
			ExternalIpv4: &domain.ExternalIpv4Spec{
				Address: "203.0.113.5",
				ZoneID:  "ru-central1-a",
			},
		},
	}
	p, err := addressToPb(rec)
	require.NoError(t, err)
	assert.Equal(t, "e9b-test", p.Id)
	assert.Equal(t, "203.0.113.5", p.GetExternalIpv4Address().GetAddress())
}
