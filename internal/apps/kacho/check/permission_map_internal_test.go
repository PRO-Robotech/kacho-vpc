// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package check_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	vpcv1 "github.com/PRO-Robotech/kacho-vpc/proto/gen/go/kacho/cloud/vpc/v1"

	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/check"
)

// clusterRootID — singleton cluster object id (FGA object `cluster:cluster_kacho_root`),
// дублирует константу из kacho-iam (source of truth). Internal cluster-scoped RPC'и
// vpc гейтятся на этом объекте.
const clusterRootID = "cluster_kacho_root"

// TestPermissionMap_InternalNetwork_GetNetwork проверяет FGA-гейт internal GetNetwork:
// relation `system_viewer`, object `cluster:cluster_kacho_root` (proto-аннотация
// required_relation=system_viewer, object_type=cluster, from_request_field="*").
// Потребитель — оператор сети с least-priv system_viewer@cluster.
func TestPermissionMap_InternalNetwork_GetNetwork(t *testing.T) {
	m := check.PermissionMap()
	e, ok := m.Lookup("/kacho.cloud.vpc.v1.InternalNetworkService/GetNetwork")
	require.True(t, ok, "InternalNetworkService/GetNetwork должен быть в PermissionMap (FGA-гейт на internal listener'е)")
	require.Equal(t, "system_viewer", e.Relation)
	require.False(t, e.Public, "GetNetwork гейтится Check'ом, не Public-skip")
	require.False(t, e.ScopeFiltered)

	objType, objID, err := e.Extract(&vpcv1.GetInternalNetworkRequest{NetworkId: "enp_x"})
	require.NoError(t, err)
	require.Equal(t, "cluster", objType)
	require.Equal(t, clusterRootID, objID, "cluster-scope извлекает singleton id из request'а независимо")
}

// TestPermissionMap_InternalNetwork_SetDefaultSecurityGroupId проверяет, что мутация
// гейтится system_admin@cluster (proto required_relation=system_admin).
func TestPermissionMap_InternalNetwork_SetDefaultSecurityGroupId(t *testing.T) {
	m := check.PermissionMap()
	e, ok := m.Lookup("/kacho.cloud.vpc.v1.InternalNetworkService/SetDefaultSecurityGroupId")
	require.True(t, ok, "InternalNetworkService/SetDefaultSecurityGroupId должен быть в PermissionMap")
	require.Equal(t, "system_admin", e.Relation)

	objType, objID, err := e.Extract(&vpcv1.SetDefaultSecurityGroupIdRequest{NetworkId: "enp_x", SecurityGroupId: "sg_y"})
	require.NoError(t, err)
	require.Equal(t, "cluster", objType)
	require.Equal(t, clusterRootID, objID)
}

// TestPermissionMap_InternalAddressPool_AllSystemAdmin — все 11 RPC
// InternalAddressPoolService гейтятся system_admin@cluster (admin-only ресурс).
func TestPermissionMap_InternalAddressPool_AllSystemAdmin(t *testing.T) {
	m := check.PermissionMap()
	rpcs := []string{
		"Create", "Get", "List", "Update", "Delete",
		"AddCidrBlocks", "RemoveCidrBlocks",
		"BindAsNetworkDefault", "UnbindNetworkDefault",
		"ListAddresses", "GetUtilization",
	}
	for _, rpc := range rpcs {
		full := "/kacho.cloud.vpc.v1.InternalAddressPoolService/" + rpc
		e, ok := m.Lookup(full)
		require.Truef(t, ok, "%s должен быть в PermissionMap (system_admin@cluster)", full)
		require.Equalf(t, "system_admin", e.Relation, "%s relation", full)
	}
}

// TestPermissionMap_InternalAddressPool_Get_ClusterObject — представитель
// AddressPool RPC: object извлекается как cluster:cluster_kacho_root.
func TestPermissionMap_InternalAddressPool_Get_ClusterObject(t *testing.T) {
	m := check.PermissionMap()
	e, ok := m.Lookup("/kacho.cloud.vpc.v1.InternalAddressPoolService/Get")
	require.True(t, ok)
	require.Equal(t, "system_admin", e.Relation)

	objType, objID, err := e.Extract(&vpcv1.GetAddressPoolRequest{PoolId: "apl_x"})
	require.NoError(t, err)
	require.Equal(t, "cluster", objType)
	require.Equal(t, clusterRootID, objID)
}

// TestPermissionMap_InternalAddressService_NotMapped — IPAM-примитивы
// InternalAddressService НЕ в Map: они остаются exempt (skip через
// methodIsInternal в interceptor'е), авторизуются in-handler. Добавление их в
// Map сломало бы service→service IPAM-аллокацию.
func TestPermissionMap_InternalAddressService_NotMapped(t *testing.T) {
	m := check.PermissionMap()
	ipamRPCs := []string{
		"AllocateInternalIP",
		"AllocateInternalIPv6",
		"AllocateExternalIP",
		"SetAddressReference",
		"ClearAddressReference",
		"GetAddressReference",
		"MarkAddressEphemeralInUse",
	}
	for _, rpc := range ipamRPCs {
		full := "/kacho.cloud.vpc.v1.InternalAddressService/" + rpc
		_, ok := m.Lookup(full)
		require.Falsef(t, ok, "%s НЕ должен быть в PermissionMap (IPAM-примитив, остается exempt)", full)
	}
}
