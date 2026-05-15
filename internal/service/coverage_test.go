package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-corelib/ids"
)

// Этот файл расширяет service-тесты, покрывая validation paths и happy-path
// Operation envelope для всех ресурсов. Сценарии — из Postman master collection
// (NET-*, SUB-*, ADR-*, RT-*, SG-*, GW-*, PE-*). Fake-реализации port-ов —
// в `internal/ports/portmock` (shim — в mock_test.go).

// ---- NetworkService — extra coverage ----
//
// Wave 3a pilot (KAC-94): тесты Network переехали в use-case-пакет
// `internal/apps/kacho/api/network/usecase_test.go` после рефакторинга
// NetworkService → use-case'ы.

// ---- SubnetService — extra coverage ----

func TestSubnetService_Create_NoCidr_OK(t *testing.T) {
	// kacho-proto#8: v4_cidr_blocks больше не required — CIDR-less subnet легален.
	or := newMockOpsRepo()
	nr := newMockNetworkRepo()
	net := makeNetwork(nr)
	svc := NewSubnetService(newMockSubnetRepo(), nr, newMockFolderClient(true), or, nil)

	op, err := svc.Create(context.Background(), CreateSubnetReq{
		FolderID:  "f1",
		Name:      "sub1",
		NetworkID: net.ID,
		ZoneID:    "ru-central1-a",
		// V4CidrBlocks empty → теперь OK.
	})
	require.NoError(t, err)
	require.NotNil(t, op)
}

func TestSubnetService_Create_BadCidr(t *testing.T) {
	or := newMockOpsRepo()
	nr := newMockNetworkRepo()
	net := makeNetwork(nr)
	svc := NewSubnetService(newMockSubnetRepo(), nr, newMockFolderClient(true), or, nil)

	_, err := svc.Create(context.Background(), CreateSubnetReq{
		FolderID: "f1", Name: "sub1", NetworkID: net.ID, ZoneID: "ru-central1-a",
		V4CidrBlocks: []string{"10.0.0.5/24"}, // host-bits != 0
	})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestSubnetService_Move_Validates(t *testing.T) {
	or := newMockOpsRepo()
	svc := NewSubnetService(newMockSubnetRepo(), newMockNetworkRepo(), newMockFolderClient(true), or, nil)
	_, err := svc.Move(context.Background(), "", "f2")
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	_, err = svc.Move(context.Background(), ids.NewID(ids.PrefixSubnet), "")
	st, _ = status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestSubnetService_AddCidrBlocks_Validates(t *testing.T) {
	or := newMockOpsRepo()
	svc := NewSubnetService(newMockSubnetRepo(), newMockNetworkRepo(), newMockFolderClient(true), or, nil)
	_, err := svc.AddCidrBlocks(context.Background(), "", []string{"10.0.0.0/24"}, nil)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	_, err = svc.AddCidrBlocks(context.Background(), ids.NewID(ids.PrefixSubnet), nil, nil)
	st, _ = status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	_, err = svc.AddCidrBlocks(context.Background(), ids.NewID(ids.PrefixSubnet), []string{"10.0.0.5/24"}, nil)
	st, _ = status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	// v6 with non-zero host-bits → InvalidArgument (sync).
	_, err = svc.AddCidrBlocks(context.Background(), ids.NewID(ids.PrefixSubnet), nil, []string{"fd00::1/64"})
	st, _ = status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	// v6 that's actually IPv4 → InvalidArgument (sync).
	_, err = svc.AddCidrBlocks(context.Background(), ids.NewID(ids.PrefixSubnet), nil, []string{"10.0.0.0/24"})
	st, _ = status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	// overlapping v6 blocks in one request → FailedPrecondition (sync, mirrors v4).
	_, err = svc.AddCidrBlocks(context.Background(), ids.NewID(ids.PrefixSubnet), nil,
		[]string{"fd00::/32", "fd00::/64"})
	st, _ = status.FromError(err)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
}

func TestSubnetService_RemoveCidrBlocks_Validates(t *testing.T) {
	or := newMockOpsRepo()
	svc := NewSubnetService(newMockSubnetRepo(), newMockNetworkRepo(), newMockFolderClient(true), or, nil)
	_, err := svc.RemoveCidrBlocks(context.Background(), "", []string{"10.0.0.0/24"}, nil)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	_, err = svc.RemoveCidrBlocks(context.Background(), ids.NewID(ids.PrefixSubnet), nil, nil)
	st, _ = status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestSubnetService_Relocate_Validates(t *testing.T) {
	or := newMockOpsRepo()
	zones := newMockZoneRegistry("ru-central1-a", "ru-central1-b")
	svc := NewSubnetService(newMockSubnetRepo(), newMockNetworkRepo(), newMockFolderClient(true), or, zones)
	_, err := svc.Relocate(context.Background(), "", "ru-central1-b")
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	// Несуществующая zone отвергается с InvalidArgument (existence-check через
	// mockZoneRegistry — заменяет удалённый hardcode whitelist в corelib).
	_, err = svc.Relocate(context.Background(), ids.NewID(ids.PrefixSubnet), "invalid-zone")
	st, _ = status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestSubnetService_ListUsedAddresses_RequiresID(t *testing.T) {
	or := newMockOpsRepo()
	svc := NewSubnetService(newMockSubnetRepo(), newMockNetworkRepo(), newMockFolderClient(true), or, nil)
	_, _, _, err := svc.ListUsedAddresses(context.Background(), "", Pagination{})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestSubnetService_Delete_RequiresID(t *testing.T) {
	or := newMockOpsRepo()
	svc := NewSubnetService(newMockSubnetRepo(), newMockNetworkRepo(), newMockFolderClient(true), or, nil)
	_, err := svc.Delete(context.Background(), "")
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

// ---- AddressService — extra coverage ----

func TestAddressService_Move_Validates(t *testing.T) {
	or := newMockOpsRepo()
	svc := NewAddressService(newMockAddressRepo(), newMockSubnetRepo(), newMockFolderClient(true), or, nil)
	_, err := svc.Move(context.Background(), "", "f2")
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	_, err = svc.Move(context.Background(), ids.NewID(ids.PrefixAddress), "")
	st, _ = status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestAddressService_Delete_RequiresID(t *testing.T) {
	or := newMockOpsRepo()
	svc := NewAddressService(newMockAddressRepo(), newMockSubnetRepo(), newMockFolderClient(true), or, nil)
	_, err := svc.Delete(context.Background(), "")
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestAddressService_GetByValue_Empty(t *testing.T) {
	or := newMockOpsRepo()
	svc := NewAddressService(newMockAddressRepo(), newMockSubnetRepo(), newMockFolderClient(true), or, nil)
	_, err := svc.GetByValue(context.Background(), "", "", "")
	require.Error(t, err)
}

func TestAddressService_ListBySubnet_NotFound(t *testing.T) {
	or := newMockOpsRepo()
	svc := NewAddressService(newMockAddressRepo(), newMockSubnetRepo(), newMockFolderClient(true), or, nil)
	_, _, err := svc.ListBySubnet(context.Background(), ids.NewID(ids.PrefixSubnet), Pagination{})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestAddressService_ListOperations_UnknownID_Empty(t *testing.T) {
	or := newMockOpsRepo()
	svc := NewAddressService(newMockAddressRepo(), newMockSubnetRepo(), newMockFolderClient(true), or, nil)
	ops, _, err := svc.ListOperations(context.Background(), ids.NewID(ids.PrefixAddress), Pagination{})
	assert.NoError(t, err)
	assert.Empty(t, ops)
}

// ---- RouteTableService — extra coverage ----

func TestRouteTableService_Move_Validates(t *testing.T) {
	or := newMockOpsRepo()
	svc := NewRouteTableService(newMockRouteTableRepo(), newMockNetworkRepo(), newMockFolderClient(true), or)
	_, err := svc.Move(context.Background(), "", "f2")
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	_, err = svc.Move(context.Background(), ids.NewID(ids.PrefixRouteTable), "")
	st, _ = status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestRouteTableService_Delete_RequiresID(t *testing.T) {
	or := newMockOpsRepo()
	svc := NewRouteTableService(newMockRouteTableRepo(), newMockNetworkRepo(), newMockFolderClient(true), or)
	_, err := svc.Delete(context.Background(), "")
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestRouteTableService_ListOperations_NotFound(t *testing.T) {
	or := newMockOpsRepo()
	svc := NewRouteTableService(newMockRouteTableRepo(), newMockNetworkRepo(), newMockFolderClient(true), or)
	_, _, err := svc.ListOperations(context.Background(), ids.NewID(ids.PrefixRouteTable), Pagination{})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

// ---- SecurityGroupService — full coverage of validation paths ----

func TestSecurityGroupService_Create_RequiresFolder(t *testing.T) {
	or := newMockOpsRepo()
	svc := NewSecurityGroupService(newMockSGRepo(), newMockNetworkRepo(), newMockFolderClient(true), or)
	// folder_id обязателен.
	_, err := svc.Create(context.Background(), CreateSecurityGroupReq{Name: "sg"})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	// network_id больше НЕ обязателен (kacho-proto#8) — SG без сети создаётся.
	op, err := svc.Create(context.Background(), CreateSecurityGroupReq{FolderID: "f1", Name: "sg"})
	require.NoError(t, err)
	require.NotNil(t, op)
}

func TestSecurityGroupService_Update_Validates(t *testing.T) {
	or := newMockOpsRepo()
	svc := NewSecurityGroupService(newMockSGRepo(), newMockNetworkRepo(), newMockFolderClient(true), or)
	_, err := svc.Update(context.Background(), UpdateSecurityGroupReq{})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestSecurityGroupService_UpdateMask_UnknownField(t *testing.T) {
	or := newMockOpsRepo()
	svc := NewSecurityGroupService(newMockSGRepo(), newMockNetworkRepo(), newMockFolderClient(true), or)
	_, err := svc.Update(context.Background(), UpdateSecurityGroupReq{
		SecurityGroupID: ids.NewID(ids.PrefixSecurityGroup),
		UpdateMask:      []string{"unknown_field"},
	})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestSecurityGroupService_UpdateRules_RequiresID(t *testing.T) {
	or := newMockOpsRepo()
	svc := NewSecurityGroupService(newMockSGRepo(), newMockNetworkRepo(), newMockFolderClient(true), or)
	_, err := svc.UpdateRules(context.Background(), UpdateRulesReq{})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestSecurityGroupService_UpdateRule_RequiresIDs(t *testing.T) {
	or := newMockOpsRepo()
	svc := NewSecurityGroupService(newMockSGRepo(), newMockNetworkRepo(), newMockFolderClient(true), or)
	_, err := svc.UpdateRule(context.Background(), UpdateRuleReq{})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	_, err = svc.UpdateRule(context.Background(), UpdateRuleReq{SecurityGroupID: ids.NewID(ids.PrefixSecurityGroup)})
	st, _ = status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestSecurityGroupService_Delete_RequiresID(t *testing.T) {
	or := newMockOpsRepo()
	svc := NewSecurityGroupService(newMockSGRepo(), newMockNetworkRepo(), newMockFolderClient(true), or)
	_, err := svc.Delete(context.Background(), "")
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestSecurityGroupService_Move_Validates(t *testing.T) {
	or := newMockOpsRepo()
	svc := NewSecurityGroupService(newMockSGRepo(), newMockNetworkRepo(), newMockFolderClient(true), or)
	_, err := svc.Move(context.Background(), "", "f2")
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	_, err = svc.Move(context.Background(), ids.NewID(ids.PrefixSecurityGroup), "")
	st, _ = status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestSecurityGroupService_ListOperations_NotFound(t *testing.T) {
	or := newMockOpsRepo()
	svc := NewSecurityGroupService(newMockSGRepo(), newMockNetworkRepo(), newMockFolderClient(true), or)
	_, _, err := svc.ListOperations(context.Background(), ids.NewID(ids.PrefixSecurityGroup), Pagination{})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

// ---- GatewayService — validation paths ----

func TestGatewayService_Create_Validates(t *testing.T) {
	or := newMockOpsRepo()
	svc := NewGatewayService(newMockGatewayRepo(), newMockFolderClient(true), or)
	_, err := svc.Create(context.Background(), CreateGatewayReq{Name: "gw"})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestGatewayService_Create_BadName(t *testing.T) {
	or := newMockOpsRepo()
	svc := NewGatewayService(newMockGatewayRepo(), newMockFolderClient(true), or)
	// NameGateway strict — uppercase отвергается.
	_, err := svc.Create(context.Background(), CreateGatewayReq{FolderID: "f1", Name: "BadName"})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestGatewayService_Create_OK(t *testing.T) {
	or := newMockOpsRepo()
	svc := NewGatewayService(newMockGatewayRepo(), newMockFolderClient(true), or)
	op, err := svc.Create(context.Background(), CreateGatewayReq{FolderID: "f1", Name: "gw1", GatewayType: "shared_egress"})
	require.NoError(t, err)
	saved := awaitOpDone(t, or, op.ID)
	assert.True(t, saved.Done)
	assert.Nil(t, saved.Error)
}

func TestGatewayService_Update_RequiresID(t *testing.T) {
	or := newMockOpsRepo()
	svc := NewGatewayService(newMockGatewayRepo(), newMockFolderClient(true), or)
	_, err := svc.Update(context.Background(), UpdateGatewayReq{})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestGatewayService_Delete_RequiresID(t *testing.T) {
	or := newMockOpsRepo()
	svc := NewGatewayService(newMockGatewayRepo(), newMockFolderClient(true), or)
	_, err := svc.Delete(context.Background(), "")
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestGatewayService_Move_Validates(t *testing.T) {
	or := newMockOpsRepo()
	svc := NewGatewayService(newMockGatewayRepo(), newMockFolderClient(true), or)
	_, err := svc.Move(context.Background(), "", "f2")
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	_, err = svc.Move(context.Background(), ids.NewID(ids.PrefixGateway), "")
	st, _ = status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestGatewayService_ListOperations_NotFound(t *testing.T) {
	or := newMockOpsRepo()
	svc := NewGatewayService(newMockGatewayRepo(), newMockFolderClient(true), or)
	_, _, err := svc.ListOperations(context.Background(), ids.NewID(ids.PrefixGateway), Pagination{})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

// ---- validation utilities ----

func TestValidateCIDRPrefix_HappyPath(t *testing.T) {
	require.NoError(t, validateCIDRPrefix("v4_cidr_blocks[0]", "10.0.0.0/24"))
	require.NoError(t, validateCIDRPrefix("v4_cidr_blocks[0]", "192.168.0.0/16"))
}

func TestValidateCIDRPrefix_HostBits(t *testing.T) {
	err := validateCIDRPrefix("v4_cidr_blocks[0]", "10.0.0.5/24")
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestValidateCIDRPrefix_BadFormat(t *testing.T) {
	err := validateCIDRPrefix("v4_cidr_blocks[0]", "not-a-cidr")
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestCheckCIDRDisjoint_NoOverlap(t *testing.T) {
	require.NoError(t, checkCIDRDisjoint("v4_cidr_blocks", []string{"10.0.0.0/24", "10.1.0.0/24"}))
}

func TestCheckCIDRDisjoint_Overlap(t *testing.T) {
	err := checkCIDRDisjoint("v4_cidr_blocks", []string{"10.0.0.0/16", "10.0.0.0/24"})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
	assert.Contains(t, st.Message(), "overlap")
}

// ---- Trivial Get/List helpers ----

func TestSecurityGroupService_Get_NotFound(t *testing.T) {
	or := newMockOpsRepo()
	svc := NewSecurityGroupService(newMockSGRepo(), newMockNetworkRepo(), newMockFolderClient(true), or)
	_, err := svc.Get(context.Background(), ids.NewID(ids.PrefixSecurityGroup))
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestSecurityGroupService_List_Empty(t *testing.T) {
	or := newMockOpsRepo()
	svc := NewSecurityGroupService(newMockSGRepo(), newMockNetworkRepo(), newMockFolderClient(true), or)
	sgs, _, err := svc.List(context.Background(), SecurityGroupFilter{FolderID: "f1"}, Pagination{})
	require.NoError(t, err)
	assert.Empty(t, sgs)
}

func TestGatewayService_Get_NotFound(t *testing.T) {
	or := newMockOpsRepo()
	svc := NewGatewayService(newMockGatewayRepo(), newMockFolderClient(true), or)
	_, err := svc.Get(context.Background(), ids.NewID(ids.PrefixGateway))
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestGatewayService_List_Empty(t *testing.T) {
	or := newMockOpsRepo()
	svc := NewGatewayService(newMockGatewayRepo(), newMockFolderClient(true), or)
	gws, _, err := svc.List(context.Background(), GatewayFilter{FolderID: "f1"}, Pagination{})
	require.NoError(t, err)
	assert.Empty(t, gws)
}

// Wave 3a pilot (KAC-94): TestNetworkService_List{SecurityGroups,RouteTables}_NotFound
// переехали в `internal/apps/kacho/api/network/usecase_test.go`.

// ---- Subnet ListOperations + Get (помимо Create/Update) ----

func TestSubnetService_Get_NotFound(t *testing.T) {
	or := newMockOpsRepo()
	svc := NewSubnetService(newMockSubnetRepo(), newMockNetworkRepo(), newMockFolderClient(true), or, nil)
	_, err := svc.Get(context.Background(), ids.NewID(ids.PrefixSubnet))
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestSubnetService_List_Empty(t *testing.T) {
	or := newMockOpsRepo()
	svc := NewSubnetService(newMockSubnetRepo(), newMockNetworkRepo(), newMockFolderClient(true), or, nil)
	subs, _, err := svc.List(context.Background(), SubnetFilter{FolderID: "f1"}, Pagination{})
	require.NoError(t, err)
	assert.Empty(t, subs)
}

func TestSubnetService_ListOperations_UnknownID_Empty(t *testing.T) {
	or := newMockOpsRepo()
	svc := NewSubnetService(newMockSubnetRepo(), newMockNetworkRepo(), newMockFolderClient(true), or, nil)
	ops, _, err := svc.ListOperations(context.Background(), ids.NewID(ids.PrefixSubnet), Pagination{})
	assert.NoError(t, err)
	assert.Empty(t, ops)
}

// ---- Address Get ----

func TestAddressService_Get_NotFound(t *testing.T) {
	or := newMockOpsRepo()
	svc := NewAddressService(newMockAddressRepo(), newMockSubnetRepo(), newMockFolderClient(true), or, nil)
	_, err := svc.Get(context.Background(), ids.NewID(ids.PrefixAddress))
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

// ---- RouteTable Get ----

func TestRouteTableService_Get_NotFound(t *testing.T) {
	or := newMockOpsRepo()
	svc := NewRouteTableService(newMockRouteTableRepo(), newMockNetworkRepo(), newMockFolderClient(true), or)
	_, err := svc.Get(context.Background(), ids.NewID(ids.PrefixRouteTable))
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestRouteTableService_List_Empty(t *testing.T) {
	or := newMockOpsRepo()
	svc := NewRouteTableService(newMockRouteTableRepo(), newMockNetworkRepo(), newMockFolderClient(true), or)
	rts, _, err := svc.List(context.Background(), RouteTableFilter{FolderID: "f1"}, Pagination{})
	require.NoError(t, err)
	assert.Empty(t, rts)
}

// ---- PrivateEndpointService — full coverage ----

func makePEService() (*PrivateEndpointService, *mockOpsRepo) {
	or := newMockOpsRepo()
	return NewPrivateEndpointService(newMockPERepo(), newMockFolderClient(true), newMockNetworkRepo(), newMockSubnetRepo(), or), or
}

func TestPrivateEndpointService_Get_NotFound(t *testing.T) {
	svc, _ := makePEService()
	_, err := svc.Get(context.Background(), ids.NewID(ids.PrefixPrivateEndpoint))
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestPrivateEndpointService_List_Empty(t *testing.T) {
	svc, _ := makePEService()
	pes, _, err := svc.List(context.Background(), PrivateEndpointFilter{FolderID: "f1"}, Pagination{})
	require.NoError(t, err)
	assert.Empty(t, pes)
}

func TestPrivateEndpointService_Create_Validates(t *testing.T) {
	svc, _ := makePEService()
	_, err := svc.Create(context.Background(), CreatePrivateEndpointReq{Name: "pe"})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestPrivateEndpointService_Create_OK(t *testing.T) {
	or := newMockOpsRepo()
	nr := newMockNetworkRepo()
	net := makeNetwork(nr)
	sr := newMockSubnetRepo()
	sub := makeSubnet(sr, net.ID)
	svc := NewPrivateEndpointService(newMockPERepo(), newMockFolderClient(true), nr, sr, or)

	op, err := svc.Create(context.Background(), CreatePrivateEndpointReq{
		FolderID:    "f1",
		Name:        "pe1",
		NetworkID:   net.ID,
		SubnetID:    sub.ID,
		ServiceType: "dns",
	})
	require.NoError(t, err)
	saved := awaitOpDone(t, or, op.ID)
	assert.True(t, saved.Done)
	assert.Nil(t, saved.Error)
}

func TestPrivateEndpointService_Update_RequiresID(t *testing.T) {
	svc, _ := makePEService()
	_, err := svc.Update(context.Background(), UpdatePrivateEndpointReq{})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestPrivateEndpointService_Update_BadName(t *testing.T) {
	svc, _ := makePEService()
	_, err := svc.Update(context.Background(), UpdatePrivateEndpointReq{
		PrivateEndpointID: ids.NewID(ids.PrefixPrivateEndpoint),
		Name:              "1bad-starts-with-digit",
		UpdateMask:        []string{"name"},
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestPrivateEndpointService_Update_UnknownMask(t *testing.T) {
	svc, _ := makePEService()
	_, err := svc.Update(context.Background(), UpdatePrivateEndpointReq{
		PrivateEndpointID: ids.NewID(ids.PrefixPrivateEndpoint),
		UpdateMask:        []string{"unknown_field"},
	})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestPrivateEndpointService_Delete_RequiresID(t *testing.T) {
	svc, _ := makePEService()
	_, err := svc.Delete(context.Background(), "")
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestPrivateEndpointService_ListOperations_NotFound(t *testing.T) {
	svc, _ := makePEService()
	_, _, err := svc.ListOperations(context.Background(), ids.NewID(ids.PrefixPrivateEndpoint), Pagination{})
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}
