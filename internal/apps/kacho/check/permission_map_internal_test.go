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

// TestPermissionMap_InternalAddressService_Mapped — IPAM-примитивы
// InternalAddressService гейтятся per-RPC FGA-Check'ом на самом ресурсе Address
// (object-scoped verb-bearing, зеркало публичного AddressService.{Get,Update}):
// мутации → v_update на vpc_address, чтение referrer'а → v_get. Это закрывает
// authz-bypass на internal listener'е :9091 (security-инвариант: authN+authZ и
// на internal'е тоже), не отклоняя легитимный cross-service IPAM-флоу от имени
// конечного пользователя-владельца Address.
func TestPermissionMap_InternalAddressService_Mapped(t *testing.T) {
	m := check.PermissionMap()

	mutate := []string{
		"AllocateInternalIP",
		"AllocateInternalIPv6",
		"AllocateExternalIP",
		"AllocateExternalIPv6",
		"SetAddressReference",
		"ClearAddressReference",
		"MarkAddressEphemeralInUse",
	}
	for _, rpc := range mutate {
		full := "/kacho.cloud.vpc.v1.InternalAddressService/" + rpc
		e, ok := m.Lookup(full)
		require.Truef(t, ok, "%s должен быть в PermissionMap (FGA-гейт на internal listener'е)", full)
		require.Equalf(t, "v_update", e.Relation, "%s relation", full)
		require.Falsef(t, e.Public, "%s гейтится Check'ом, не Public-skip", full)
		require.Falsef(t, e.ScopeFiltered, "%s не scope-filtered", full)
	}

	get, ok := m.Lookup("/kacho.cloud.vpc.v1.InternalAddressService/GetAddressReference")
	require.True(t, ok)
	require.Equal(t, "v_get", get.Relation, "чтение referrer'а гейтится v_get")
	require.False(t, get.Public)

	// Extract извлекает (vpc_address, <address_id>) из каждого request'а.
	objType, objID, err := m["/kacho.cloud.vpc.v1.InternalAddressService/AllocateExternalIP"].
		Extract(&vpcv1.AllocateExternalIPRequest{AddressId: "adr_alpha"})
	require.NoError(t, err)
	require.Equal(t, "vpc_address", objType)
	require.Equal(t, "adr_alpha", objID)

	objType, objID, err = m["/kacho.cloud.vpc.v1.InternalAddressService/AllocateExternalIPv6"].
		Extract(&vpcv1.AllocateExternalIPRequest{AddressId: "adr_alpha6"})
	require.NoError(t, err)
	require.Equal(t, "vpc_address", objType)
	require.Equal(t, "adr_alpha6", objID)

	objType, objID, err = m["/kacho.cloud.vpc.v1.InternalAddressService/SetAddressReference"].
		Extract(&vpcv1.SetAddressReferenceRequest{AddressId: "adr_beta"})
	require.NoError(t, err)
	require.Equal(t, "vpc_address", objType)
	require.Equal(t, "adr_beta", objID)

	objType, objID, err = m["/kacho.cloud.vpc.v1.InternalAddressService/GetAddressReference"].
		Extract(&vpcv1.GetAddressReferenceRequest{AddressId: "adr_gamma"})
	require.NoError(t, err)
	require.Equal(t, "vpc_address", objType)
	require.Equal(t, "adr_gamma", objID)
}

// TestPermissionMap_InternalAddressService_NoExemptGuard — блокирующий drift-guard:
// КАЖДЫЙ метод InternalAddressService (из proto-дескриптора) обязан присутствовать
// в Map и не быть exempt (Public=false). Если в сервис добавят RPC и забудут его
// замапить — гейт упадет здесь, а не молча пропустит запрос через methodIsInternal.
func TestPermissionMap_InternalAddressService_NoExemptGuard(t *testing.T) {
	m := check.PermissionMap()
	for _, md := range vpcv1.InternalAddressService_ServiceDesc.Methods {
		full := "/" + vpcv1.InternalAddressService_ServiceDesc.ServiceName + "/" + md.MethodName
		e, ok := m.Lookup(full)
		require.Truef(t, ok, "%s обязан быть в PermissionMap (drift-guard: не оставлять IPAM RPC exempt)", full)
		require.Falsef(t, e.Public, "%s не должен быть exempt (Public=false)", full)
	}
}
