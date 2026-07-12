// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package check_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"

	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/check"
)

// INV-2a — AuthN+AuthZ на :9091 ВЕЗДЕ: каждый RPC internal
// InternalNetworkInterfaceService проходит per-RPC FGA-Check (не только mTLS-транспорт).
// Attach/Detach — editor на vpc_network_interface:<nic_id> (object-scoped, анти-BOLA);
// ListByInstance — viewer cluster-scoped (batched read). Proto-аннотации
// (internal_network_interface_service.proto) отражены в PermissionMap 1:1.

// TestPermissionMap_InternalNIC_Attach_EditorScoped — Attach гейтится editor на
// самом NIC (vpc_network_interface:<nic_id> из request-поля nic_id).
func TestPermissionMap_InternalNIC_Attach_EditorScoped(t *testing.T) {
	m := check.PermissionMap()
	e, ok := m.Lookup("/kacho.cloud.vpc.v1.InternalNetworkInterfaceService/Attach")
	require.True(t, ok, "InternalNetworkInterfaceService/Attach должен быть в PermissionMap (INV-2a)")
	require.Equal(t, "editor", e.Relation)
	require.False(t, e.Public, "Attach гейтится Check'ом, не Public-skip")

	objType, objID, err := e.Extract(&vpcv1.AttachNetworkInterfaceRequest{NicId: "nic_x", InstanceId: "epdinst01"})
	require.NoError(t, err)
	require.Equal(t, "vpc_network_interface", objType, "object-scoped на самом NIC (анти-BOLA)")
	require.Equal(t, "nic_x", objID)
}

// TestPermissionMap_InternalNIC_Detach_EditorScoped — Detach гейтится editor на NIC.
func TestPermissionMap_InternalNIC_Detach_EditorScoped(t *testing.T) {
	m := check.PermissionMap()
	e, ok := m.Lookup("/kacho.cloud.vpc.v1.InternalNetworkInterfaceService/Detach")
	require.True(t, ok, "InternalNetworkInterfaceService/Detach должен быть в PermissionMap")
	require.Equal(t, "editor", e.Relation)

	objType, objID, err := e.Extract(&vpcv1.DetachNetworkInterfaceRequest{NicId: "nic_y", InstanceId: "epdinst02"})
	require.NoError(t, err)
	require.Equal(t, "vpc_network_interface", objType)
	require.Equal(t, "nic_y", objID)
}

// TestPermissionMap_InternalNIC_ListByInstance_ViewerCluster — ListByInstance
// (batched read) гейтится viewer cluster-scoped.
func TestPermissionMap_InternalNIC_ListByInstance_ViewerCluster(t *testing.T) {
	m := check.PermissionMap()
	e, ok := m.Lookup("/kacho.cloud.vpc.v1.InternalNetworkInterfaceService/ListByInstance")
	require.True(t, ok, "InternalNetworkInterfaceService/ListByInstance должен быть в PermissionMap")
	require.Equal(t, "viewer", e.Relation)

	objType, objID, err := e.Extract(&vpcv1.ListNetworkInterfacesByInstanceRequest{InstanceIds: []string{"epdinst03"}})
	require.NoError(t, err)
	require.Equal(t, "cluster", objType)
	require.Equal(t, clusterRootID, objID)
}

// TestPermissionMap_InternalNIC_NoExemptGuard — блокирующий drift-guard: КАЖДЫЙ метод
// InternalNetworkInterfaceService (из proto-дескриптора) обязан быть в Map и не быть
// exempt (Public=false). Забытый RPC → гейт упадёт здесь, а не пропустит запрос.
func TestPermissionMap_InternalNIC_NoExemptGuard(t *testing.T) {
	m := check.PermissionMap()
	for _, md := range vpcv1.InternalNetworkInterfaceService_ServiceDesc.Methods {
		full := "/" + vpcv1.InternalNetworkInterfaceService_ServiceDesc.ServiceName + "/" + md.MethodName
		e, ok := m.Lookup(full)
		require.Truef(t, ok, "%s обязан быть в PermissionMap (INV-2a drift-guard)", full)
		require.Falsef(t, e.Public, "%s не должен быть exempt (Public=false)", full)
	}
}

// TestInterceptor_InternalNIC_Attach_Deny_ObjectScoped — behaviour-level INV-2a: на
// internal listener'е per-RPC Check РЕАЛЬНО вызывается с object-scope
// (editor @ vpc_network_interface:<nic_id>) и deny → PERMISSION_DENIED, handler НЕ
// вызывается. Assert `calls==1` ловит регрессию, где метод выпал из PermissionMap:
// тогда interceptor вернул бы PermissionDenied по unmapped-fail-closed (calls==0),
// а не по настоящей object-scoped авторизации.
func TestInterceptor_InternalNIC_Attach_Deny_ObjectScoped(t *testing.T) {
	intr, calls := newTestInterceptor(t, func(_ context.Context, subject, relation, object string) (bool, error) {
		require.Equal(t, "user:usr_mallory", subject)
		require.Equal(t, "editor", relation)
		require.Equal(t, "vpc_network_interface:nic_victim", object, "Check против ЦЕЛЕВОГО NIC (анти-BOLA)")
		return false, nil // deny
	})
	uIntr := intr.Unary()

	handlerCalled := false
	handler := func(ctx context.Context, req any) (any, error) {
		handlerCalled = true
		return "should not be returned", nil
	}
	info := &grpc.UnaryServerInfo{FullMethod: "/kacho.cloud.vpc.v1.InternalNetworkInterfaceService/Attach"}
	ctx := principalCtx("user", "usr_mallory")
	req := &vpcv1.AttachNetworkInterfaceRequest{NicId: "nic_victim", InstanceId: "epdinst_attacker"}

	_, err := uIntr(ctx, req, info, handler)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.PermissionDenied, st.Code(), "deny → PERMISSION_DENIED (INV-2a)")
	require.False(t, handlerCalled, "handler НЕ вызывается на DENY")
	require.Equal(t, 1, *calls, "Check РЕАЛЬНО вызван (не unmapped-fail-closed): метод в PermissionMap")
}
