package handler

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	fieldmaskpb "google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	svc "github.com/PRO-Robotech/kacho-vpc/internal/service"
)

// Этот файл — happy-path и error-path handler-тесты для остальных методов
// (Move/AddCidrBlocks/RemoveCidrBlocks/Relocate/ListUsedAddresses/UpdateRules/
// UpdateRule/PrivateEndpoint*). Fake-реализации port-ов — в
// `internal/ports/portmock` (shim — в mock_test.go).

// ---- Subnet handler — Move / AddCidrBlocks / RemoveCidrBlocks / Relocate / ListUsedAddresses ----

func TestSubnetHandler_Move_Validates(t *testing.T) {
	sr := newMockSubnetRepoForSvc()
	or := newMockOpsRepo()
	h := NewSubnetHandler(svc.NewSubnetService(sr, newMockNetworkRepo(), newMockFolderClient(true), or, nil))
	_, err := h.Move(context.Background(), &vpcv1.MoveSubnetRequest{SubnetId: ""})
	st, _ := grpcstatus.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestSubnetHandler_AddCidrBlocks_Validates(t *testing.T) {
	sr := newMockSubnetRepoForSvc()
	or := newMockOpsRepo()
	h := NewSubnetHandler(svc.NewSubnetService(sr, newMockNetworkRepo(), newMockFolderClient(true), or, nil))
	_, err := h.AddCidrBlocks(context.Background(), &vpcv1.AddSubnetCidrBlocksRequest{SubnetId: ""})
	st, _ := grpcstatus.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestSubnetHandler_RemoveCidrBlocks_Validates(t *testing.T) {
	sr := newMockSubnetRepoForSvc()
	or := newMockOpsRepo()
	h := NewSubnetHandler(svc.NewSubnetService(sr, newMockNetworkRepo(), newMockFolderClient(true), or, nil))
	_, err := h.RemoveCidrBlocks(context.Background(), &vpcv1.RemoveSubnetCidrBlocksRequest{SubnetId: ""})
	st, _ := grpcstatus.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestSubnetHandler_Relocate_Validates(t *testing.T) {
	sr := newMockSubnetRepoForSvc()
	or := newMockOpsRepo()
	h := NewSubnetHandler(svc.NewSubnetService(sr, newMockNetworkRepo(), newMockFolderClient(true), or, nil))
	_, err := h.Relocate(context.Background(), &vpcv1.RelocateSubnetRequest{SubnetId: ""})
	st, _ := grpcstatus.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestSubnetHandler_ListUsedAddresses_Validates(t *testing.T) {
	sr := newMockSubnetRepoForSvc()
	or := newMockOpsRepo()
	h := NewSubnetHandler(svc.NewSubnetService(sr, newMockNetworkRepo(), newMockFolderClient(true), or, nil))
	_, err := h.ListUsedAddresses(context.Background(), &vpcv1.ListUsedAddressesRequest{SubnetId: ""})
	st, _ := grpcstatus.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

// ---- Address handler — additional ----

func TestAddressHandler_Move_Validates(t *testing.T) {
	addrSvc, _ := makeAddressService()
	h := NewAddressHandler(addrSvc, nil)
	_, err := h.Move(context.Background(), &vpcv1.MoveAddressRequest{AddressId: ""})
	st, _ := grpcstatus.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestAddressHandler_GetByValue_Empty(t *testing.T) {
	addrSvc, _ := makeAddressService()
	h := NewAddressHandler(addrSvc, nil)
	_, err := h.GetByValue(context.Background(), &vpcv1.GetAddressByValueRequest{})
	require.Error(t, err)
}

func TestAddressHandler_ListBySubnet_NotFound(t *testing.T) {
	addrSvc, _ := makeAddressService()
	h := NewAddressHandler(addrSvc, nil)
	_, err := h.ListBySubnet(context.Background(), &vpcv1.ListAddressesBySubnetRequest{SubnetId: ids.NewID(ids.PrefixSubnet)})
	st, _ := grpcstatus.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

// ---- RouteTable handler — moved (Wave 3b) ----

// ---- SecurityGroup handler — moved to internal/apps/kacho/api/securitygroup/usecase_test.go (Wave 3) ----
// (Wave 3, KAC-94): Handler/Service-level tests, SGToProto conversion tests
// (TestSGToProto_Fields, TestRuleSpecFromProto_*) переехали в новый use-case-пакет.

// ---- PrivateEndpoint handler — moved to internal/apps/kacho/api/privateendpoint/usecase_test.go (Wave 3b) ----

// ---- pure converter functions ----

// ---- Network handler — happy path Update / List* / ListOperations ----
//
// Wave 3a pilot (KAC-94): Network-handler-тесты переехали в
// `internal/apps/kacho/api/network/usecase_test.go` (NetworkHandler удалён,
// Handler теперь живёт в use-case-пакете).

// ---- Subnet handler — full happy-path ----

func TestSubnetHandler_FullFlow(t *testing.T) {
	sr := newMockSubnetRepoForSvc()
	nr := newMockNetworkRepo()
	or := newMockOpsRepo()

	netID := ids.NewID(ids.PrefixNetwork)
	_, _ = nr.Insert(context.Background(), &domain.Network{ID: netID, FolderID: "f1", Name: "net"})

	subnetSvc := svc.NewSubnetService(sr, nr, newMockFolderClient(true), or, nil)
	h := NewSubnetHandler(subnetSvc)

	createOp, err := h.Create(context.Background(), &vpcv1.CreateSubnetRequest{
		FolderId: "f1", Name: "sub", NetworkId: netID,
		ZoneId:       "ru-central1-a",
		V4CidrBlocks: []string{"10.0.0.0/24"},
	})
	require.NoError(t, err)
	awaitOpDone(t, or, createOp.Id)

	resp, _ := h.List(context.Background(), &vpcv1.ListSubnetsRequest{FolderId: "f1"})
	require.Len(t, resp.Subnets, 1)
	subID := resp.Subnets[0].Id

	got, err := h.Get(context.Background(), &vpcv1.GetSubnetRequest{SubnetId: subID})
	require.NoError(t, err)
	assert.Equal(t, subID, got.Id)

	updOp, err := h.Update(context.Background(), &vpcv1.UpdateSubnetRequest{
		SubnetId:   subID,
		Name:       "sub-upd",
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"name"}},
	})
	require.NoError(t, err)
	awaitOpDone(t, or, updOp.Id)

	_, err = h.ListOperations(context.Background(), &vpcv1.ListSubnetOperationsRequest{SubnetId: subID})
	require.NoError(t, err)

	moveOp, err := h.Move(context.Background(), &vpcv1.MoveSubnetRequest{SubnetId: subID, DestinationFolderId: ids.NewID(ids.PrefixFolder)})
	require.NoError(t, err)
	awaitOpDone(t, or, moveOp.Id)

	delOp, err := h.Delete(context.Background(), &vpcv1.DeleteSubnetRequest{SubnetId: subID})
	require.NoError(t, err)
	awaitOpDone(t, or, delOp.Id)
}

// ---- RouteTable handler — moved (Wave 3b) ----

// ---- SecurityGroup handler — full happy-path — moved to internal/apps/kacho/api/securitygroup/usecase_test.go (Wave 3) ----

// ---- Gateway handler — moved (Wave 3b) ----

// ---- Address handler — happy-path Update/Move/ListBySubnet ----

func TestAddressHandler_FullFlow(t *testing.T) {
	addrSvc, or := makeAddressService()
	h := NewAddressHandler(addrSvc, nil)

	createOp, _ := h.Create(context.Background(), &vpcv1.CreateAddressRequest{
		FolderId: "f1",
		Name:     "addr1",
		AddressSpec: &vpcv1.CreateAddressRequest_ExternalIpv4AddressSpec{
			ExternalIpv4AddressSpec: &vpcv1.ExternalIpv4AddressSpec{
				Address: "203.0.113.10", ZoneId: "ru-central1-a",
			},
		},
	})
	awaitOpDone(t, or, createOp.Id)

	listResp, _ := h.List(context.Background(), &vpcv1.ListAddressesRequest{FolderId: "f1"})
	require.NotEmpty(t, listResp.Addresses)
	addrID := listResp.Addresses[0].Id

	_, err := h.Get(context.Background(), &vpcv1.GetAddressRequest{AddressId: addrID})
	require.NoError(t, err)

	updOp, _ := h.Update(context.Background(), &vpcv1.UpdateAddressRequest{
		AddressId:  addrID,
		Name:       "addr1-upd",
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"name"}},
	})
	awaitOpDone(t, or, updOp.Id)

	_, err = h.ListOperations(context.Background(), &vpcv1.ListAddressOperationsRequest{AddressId: addrID})
	require.NoError(t, err)

	moveOp, _ := h.Move(context.Background(), &vpcv1.MoveAddressRequest{AddressId: addrID, DestinationFolderId: ids.NewID(ids.PrefixFolder)})
	awaitOpDone(t, or, moveOp.Id)

	delOp, _ := h.Delete(context.Background(), &vpcv1.DeleteAddressRequest{AddressId: addrID})
	awaitOpDone(t, or, delOp.Id)
}

func TestSubnetToProto_Fields(t *testing.T) {
	// Wave 2 batch A (KAC-94): Subnet → DTO type2pb. Тест проверяет тот же
	// контракт что и раньше, но через `subnetToPb` (DTO-реестр).
	rec := &domain.SubnetRecord{
		Subnet: domain.Subnet{
			ID:           "sub-1",
			FolderID:     "f1",
			Name:         domain.RcNameVPC("sub"),
			NetworkID:    "net-1",
			ZoneID:       "ru-central1-a",
			V4CidrBlocks: []string{"10.0.0.0/24"},
			DhcpOptions: &domain.DhcpOptions{
				DomainName:        "example.com",
				DomainNameServers: []string{"8.8.8.8"},
				NtpServers:        []string{"1.1.1.1"},
			},
		},
	}
	p, err := subnetToPb(rec)
	require.NoError(t, err)
	assert.Equal(t, "sub-1", p.Id)
	assert.Equal(t, "ru-central1-a", p.ZoneId)
	require.NotNil(t, p.DhcpOptions)
	assert.Equal(t, "example.com", p.DhcpOptions.DomainName)
}
