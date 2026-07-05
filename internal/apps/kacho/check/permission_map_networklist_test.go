// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package check_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"

	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/check"
)

// TestPermissionMap_NetworkList_ServerSideCheck — security-регресс-гейт
// (SEC audit 2026-07-05, CWE-862/CWE-639/OWASP A01).
//
// NetworkService/List обязан гейтиться server-side FGA-Check'ом (`viewer` на
// `project:<project_id>`) точно так же, как остальные 6 top-level List RPC
// (Subnet/Address/RouteTable/SecurityGroup/Gateway/NetworkInterface). Пометка
// ScopeFiltered=true отдавала бы DecisionInternal в authz-interceptor'е →
// per-RPC Check пропускался, и единственным гейтом оставался handler-side
// tenant.AssertProjectOwnership, который доверяет client-supplied
// x-kacho-admin / x-kacho-project-id metadata. При выключенном data-level
// list-filter (helm-default `listFilter.enabled=false`) это открывало
// cross-project enumeration Network'ов через подделанный admin-заголовок.
//
// Инвариант: НИ ОДИН top-level project List не должен быть ScopeFiltered —
// объектная авторизация не должна деградировать до header-trusted пути.
func TestPermissionMap_NetworkList_ServerSideCheck(t *testing.T) {
	m := check.PermissionMap()
	e, ok := m.Lookup("/kacho.cloud.vpc.v1.NetworkService/List")
	require.True(t, ok, "NetworkService/List должен быть в PermissionMap")
	require.Falsef(t, e.ScopeFiltered,
		"NetworkService/List не должен быть ScopeFiltered — иначе per-RPC Check пропускается и остаётся только header-trusted AssertProjectOwnership (cross-project enumeration)")
	require.Equal(t, "viewer", e.Relation,
		"top-level project List гейтится tier viewer на project (parity с остальными List)")

	objType, objID, err := e.Extract(&vpcv1.ListNetworksRequest{ProjectId: "prj_x"})
	require.NoError(t, err)
	require.Equal(t, "project", objType, "List авторизуется на parent project scope")
	require.Equal(t, "prj_x", objID)
}

// TestPermissionMap_AllTopLevelProjectList_NotScopeFiltered — общий guard: все
// top-level project List RPC (Extract → project) обязаны иметь реальный
// server-side Check, ни один не помечен ScopeFiltered.
func TestPermissionMap_AllTopLevelProjectList_NotScopeFiltered(t *testing.T) {
	m := check.PermissionMap()
	topLevelLists := []string{
		"/kacho.cloud.vpc.v1.NetworkService/List",
		"/kacho.cloud.vpc.v1.SubnetService/List",
		"/kacho.cloud.vpc.v1.AddressService/List",
		"/kacho.cloud.vpc.v1.RouteTableService/List",
		"/kacho.cloud.vpc.v1.SecurityGroupService/List",
		"/kacho.cloud.vpc.v1.GatewayService/List",
		"/kacho.cloud.vpc.v1.NetworkInterfaceService/List",
	}
	for _, rpc := range topLevelLists {
		e, ok := m.Lookup(rpc)
		require.Truef(t, ok, "%s должен быть в PermissionMap", rpc)
		require.Falsef(t, e.ScopeFiltered,
			"%s: top-level project List не должен деградировать object-scope authz до header-trusted пути", rpc)
		require.Equalf(t, "viewer", e.Relation, "%s: top-level List гейтится viewer на project", rpc)
	}
}
