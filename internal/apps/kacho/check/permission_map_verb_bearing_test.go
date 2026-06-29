// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package check_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/check"
)

// Эти таблицы фиксируют целевую (Design-B) карту enforcement-relation'ов для
// kacho-vpc: per-RPC Check резолвит action на verb-bearing relation (`v_get`/
// `v_update`/`v_delete`/`v_list`), а не на tier (`viewer`/`editor`). Дискриминатор
// object-self vs parent-scoped: object-self RPC (Extract возвращает id самого
// ресурса) флипается на v_*; parent-scoped Create (Extract возвращает project)
// остается tier `editor`. Top-level List остается `viewer` (visibility-фильтр —
// через iam ListObjects `viewer ∪ v_list`, см. authzfilter).
//
// Источник истины для cross-plane согласованности — per-RPC `required_relation`
// в permission_catalog (gateway форвардит его в Check); consumer-карта обязана
// совпадать на object-self.

// verbGetRPCs — object-self read (контент): Check → `v_get`.
var verbGetRPCs = []string{
	"/kacho.cloud.vpc.v1.NetworkService/Get",
	"/kacho.cloud.vpc.v1.SubnetService/Get",
	"/kacho.cloud.vpc.v1.AddressService/Get",
	"/kacho.cloud.vpc.v1.AddressService/GetByValue",
	"/kacho.cloud.vpc.v1.RouteTableService/Get",
	"/kacho.cloud.vpc.v1.SecurityGroupService/Get",
	"/kacho.cloud.vpc.v1.GatewayService/Get",
	"/kacho.cloud.vpc.v1.NetworkInterfaceService/Get",
}

// verbUpdateRPCs — object-self mutation (Update + domain-mutate): Check → `v_update`.
var verbUpdateRPCs = []string{
	"/kacho.cloud.vpc.v1.NetworkService/Update",
	"/kacho.cloud.vpc.v1.SubnetService/Update",
	"/kacho.cloud.vpc.v1.SubnetService/AddCidrBlocks",
	"/kacho.cloud.vpc.v1.SubnetService/RemoveCidrBlocks",
	"/kacho.cloud.vpc.v1.AddressService/Update",
	"/kacho.cloud.vpc.v1.RouteTableService/Update",
	"/kacho.cloud.vpc.v1.SecurityGroupService/Update",
	"/kacho.cloud.vpc.v1.SecurityGroupService/UpdateRules",
	"/kacho.cloud.vpc.v1.SecurityGroupService/UpdateRule",
	"/kacho.cloud.vpc.v1.GatewayService/Update",
	"/kacho.cloud.vpc.v1.NetworkInterfaceService/Update",
}

// verbDeleteRPCs — object-self delete: Check → `v_delete`.
var verbDeleteRPCs = []string{
	"/kacho.cloud.vpc.v1.NetworkService/Delete",
	"/kacho.cloud.vpc.v1.SubnetService/Delete",
	"/kacho.cloud.vpc.v1.AddressService/Delete",
	"/kacho.cloud.vpc.v1.RouteTableService/Delete",
	"/kacho.cloud.vpc.v1.SecurityGroupService/Delete",
	"/kacho.cloud.vpc.v1.GatewayService/Delete",
	"/kacho.cloud.vpc.v1.NetworkInterfaceService/Delete",
}

// verbListOnResourceRPCs — object-self read (видимость дочерних/операций на самом
// ресурсе): Check → `v_list`. Это НЕ top-level project-List (тот остается viewer).
var verbListOnResourceRPCs = []string{
	"/kacho.cloud.vpc.v1.NetworkService/ListSubnets",
	"/kacho.cloud.vpc.v1.NetworkService/ListSecurityGroups",
	"/kacho.cloud.vpc.v1.NetworkService/ListRouteTables",
	"/kacho.cloud.vpc.v1.NetworkService/ListOperations",
	"/kacho.cloud.vpc.v1.SubnetService/ListUsedAddresses",
	"/kacho.cloud.vpc.v1.SubnetService/ListOperations",
	"/kacho.cloud.vpc.v1.AddressService/ListBySubnet",
	"/kacho.cloud.vpc.v1.AddressService/ListOperations",
	"/kacho.cloud.vpc.v1.RouteTableService/ListOperations",
	"/kacho.cloud.vpc.v1.SecurityGroupService/ListOperations",
	"/kacho.cloud.vpc.v1.GatewayService/ListOperations",
	"/kacho.cloud.vpc.v1.NetworkInterfaceService/ListOperations",
}

// createChildRPCs — parent-scoped Create (Extract → project): остается tier
// `editor` на parent (F-7; create-self verb бессмыслен на еще-несуществующем
// объекте — create-authority = write-authz на parent).
var createChildRPCs = []string{
	"/kacho.cloud.vpc.v1.NetworkService/Create",
	"/kacho.cloud.vpc.v1.SubnetService/Create",
	"/kacho.cloud.vpc.v1.AddressService/Create",
	"/kacho.cloud.vpc.v1.RouteTableService/Create",
	"/kacho.cloud.vpc.v1.SecurityGroupService/Create",
	"/kacho.cloud.vpc.v1.GatewayService/Create",
	"/kacho.cloud.vpc.v1.NetworkInterfaceService/Create",
}

// projectListRPCs — top-level project-scoped List (Extract → project): остается
// `viewer`. Visibility per-object идет через iam ListObjects `viewer ∪ v_list`
// (authzfilter), не через per-RPC Check relation.
var projectListRPCs = []string{
	"/kacho.cloud.vpc.v1.SubnetService/List",
	"/kacho.cloud.vpc.v1.AddressService/List",
	"/kacho.cloud.vpc.v1.RouteTableService/List",
	"/kacho.cloud.vpc.v1.SecurityGroupService/List",
	"/kacho.cloud.vpc.v1.GatewayService/List",
	"/kacho.cloud.vpc.v1.NetworkInterfaceService/List",
}

func TestPermissionMap_VerbBearing_Get_VGet(t *testing.T) {
	m := check.PermissionMap()
	for _, rpc := range verbGetRPCs {
		e, ok := m.Lookup(rpc)
		require.Truef(t, ok, "%s must be mapped", rpc)
		require.Equalf(t, "v_get", e.Relation, "%s: object-self read must enforce v_get (Design B)", rpc)
		require.Falsef(t, e.ScopeFiltered, "%s: object-self Get is not scope-filtered", rpc)
	}
}

func TestPermissionMap_VerbBearing_Update_VUpdate(t *testing.T) {
	m := check.PermissionMap()
	for _, rpc := range verbUpdateRPCs {
		e, ok := m.Lookup(rpc)
		require.Truef(t, ok, "%s must be mapped", rpc)
		require.Equalf(t, "v_update", e.Relation, "%s: object-self mutation must enforce v_update (Design B)", rpc)
	}
}

func TestPermissionMap_VerbBearing_Delete_VDelete(t *testing.T) {
	m := check.PermissionMap()
	for _, rpc := range verbDeleteRPCs {
		e, ok := m.Lookup(rpc)
		require.Truef(t, ok, "%s must be mapped", rpc)
		require.Equalf(t, "v_delete", e.Relation, "%s: object-self delete must enforce v_delete (Design B)", rpc)
	}
}

func TestPermissionMap_VerbBearing_ListOnResource_VList(t *testing.T) {
	m := check.PermissionMap()
	for _, rpc := range verbListOnResourceRPCs {
		e, ok := m.Lookup(rpc)
		require.Truef(t, ok, "%s must be mapped", rpc)
		require.Equalf(t, "v_list", e.Relation, "%s: object-self list-on-resource must enforce v_list (Design B)", rpc)
	}
}

func TestPermissionMap_VerbBearing_CreateChild_StaysEditor(t *testing.T) {
	m := check.PermissionMap()
	for _, rpc := range createChildRPCs {
		e, ok := m.Lookup(rpc)
		require.Truef(t, ok, "%s must be mapped", rpc)
		require.Equalf(t, "editor", e.Relation, "%s: create-child stays tier editor on parent project (F-7)", rpc)
	}
}

func TestPermissionMap_VerbBearing_ProjectList_StaysViewer(t *testing.T) {
	m := check.PermissionMap()
	for _, rpc := range projectListRPCs {
		e, ok := m.Lookup(rpc)
		require.Truef(t, ok, "%s must be mapped", rpc)
		require.Equalf(t, "viewer", e.Relation, "%s: top-level project List stays viewer (visibility via iam ListObjects union)", rpc)
	}
}

// TestPermissionMap_VerbBearing_InternalUnchanged — internal cluster-scoped RPC
// НЕ verb-bearing (cluster — не verb-bearing тип, F-8); остаются system_viewer/
// system_admin на cluster singleton, не флипаются.
func TestPermissionMap_VerbBearing_InternalUnchanged(t *testing.T) {
	m := check.PermissionMap()
	cases := map[string]string{
		"/kacho.cloud.vpc.v1.InternalNetworkService/GetNetwork":                "system_viewer",
		"/kacho.cloud.vpc.v1.InternalNetworkService/SetDefaultSecurityGroupId": "system_admin",
		"/kacho.cloud.vpc.v1.InternalAddressPoolService/Get":                   "system_admin",
	}
	for rpc, want := range cases {
		e, ok := m.Lookup(rpc)
		require.Truef(t, ok, "%s must be mapped", rpc)
		require.Equalf(t, want, e.Relation, "%s: internal cluster-scoped relation unchanged (F-8)", rpc)
	}
}

// TestPermissionMap_VerbBearing_NoTierLeftOnObjectSelf — guard: ни один object-self
// CRUD-RPC не должен остаться на tier `viewer`/`editor` (анти-регресс Design A).
func TestPermissionMap_VerbBearing_NoTierLeftOnObjectSelf(t *testing.T) {
	m := check.PermissionMap()
	objectSelf := append(append(append(append([]string{}, verbGetRPCs...), verbUpdateRPCs...), verbDeleteRPCs...), verbListOnResourceRPCs...)
	for _, rpc := range objectSelf {
		e, ok := m.Lookup(rpc)
		require.Truef(t, ok, "%s must be mapped", rpc)
		require.NotEqualf(t, "viewer", e.Relation, "%s: object-self must not stay on tier viewer", rpc)
		require.NotEqualf(t, "editor", e.Relation, "%s: object-self must not stay on tier editor", rpc)
	}
}
