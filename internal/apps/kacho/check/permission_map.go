// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package check

import (
	"fmt"

	"github.com/PRO-Robotech/kacho-corelib/authz"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
)

// FGA object types.
//
// Naming convention для kacho-vpc:
//
//	"project"             — parent scope, на котором висят RBAC bindings;
//	                        используется для Create/List (caller должен иметь
//	                        editor/viewer на project'е).
//	"vpc_network"         — Network ресурс
//	"vpc_subnet"          — Subnet
//	"vpc_address"         — Address
//	"vpc_route_table"     — RouteTable
//	"vpc_security_group"  — SecurityGroup
//	"vpc_gateway"         — Gateway
//	"vpc_network_interface" — NetworkInterface
//	"vpc_operation"       — Operation (LRO; для ListOperations / OperationService.Get)
const (
	objectTypeProject          = "project"
	objectTypeNetwork          = "vpc_network"
	objectTypeSubnet           = "vpc_subnet"
	objectTypeAddress          = "vpc_address"
	objectTypeRouteTable       = "vpc_route_table"
	objectTypeSecurityGroup    = "vpc_security_group"
	objectTypeGateway          = "vpc_gateway"
	objectTypeNetworkInterface = "vpc_network_interface"

	// objectTypeCluster — cluster singleton scope для internal admin/cluster-RPC
	// (InternalNetworkService / InternalAddressPoolService). Proto-аннотация:
	// object_type="cluster", from_request_field="*" — объект не зависит от
	// request'а, всегда singleton cluster:<clusterRootID>.
	objectTypeCluster = "cluster"
)

// clusterRootID — singleton id для object'а `cluster:cluster_kacho_root`.
// Source of truth — kacho-iam (cluster-таблица, единственная строка); тут —
// backend view-only для cluster-scoped Check'ов internal RPC.
const clusterRootID = "cluster_kacho_root"

// FGA relations. Дублирует константы из kacho-iam/internal/authzmap (там —
// source of truth); тут — backend view-only, чтобы не плодить cross-repo import
// просто ради двух строк.
const (
	// relationViewer / relationEditor — tier-relations. Сохраняются для
	// create-child (на parent project, F-7) и top-level project-List (visibility
	// per-object идет через iam ListObjects `viewer ∪ v_list`, не через per-RPC
	// Check). Для object-self CRUD энфорс — verb-bearing relations ниже.
	relationViewer = "viewer"
	relationEditor = "editor"

	// verb-bearing relations (v_*) — enforcement резолвит object-self action на
	// verb, а не на tier (см. anchor-эпик «Explicit RBAC model 2026», D-6/D-6a:
	// доступ по verb развязан с tier). Материализуются per-object reconciler'ом
	// kacho-iam; consumer гейтит ими object-self RPC. Дублируют relation-имена из
	// kacho-iam/internal/authzmap (там — source of truth); тут — backend view-only.
	//
	//	v_get    — чтение содержимого самого ресурса (Get / GetByValue);
	//	v_list   — видимость дочерних/операций на самом ресурсе (ListSubnets,
	//	           ListOperations, …) — НЕ top-level project-List;
	//	v_update — мутация самого ресурса (Update + domain-mutate verb'ы);
	//	v_delete — удаление самого ресурса.
	relationVGet    = "v_get"
	relationVList   = "v_list"
	relationVUpdate = "v_update"
	relationVDelete = "v_delete"

	// system_* — cluster-tier relations для internal admin/cluster-RPC.
	// `system_viewer` — read-tier (инфра-чувствительный read, напр. vrf_id);
	// `system_admin` — write-tier (admin-мутации, AddressPool CRUD).
	// Source of truth — kacho-iam/internal/authzmap.
	relationSystemViewer = "system_viewer"
	relationSystemAdmin  = "system_admin"
)

// clusterScoped — helper для cluster-scoped internal RPC: object всегда
// singleton `cluster:cluster_kacho_root` (proto from_request_field="*" —
// объект не извлекается из request'а), варьируется только required relation.
func clusterScoped(relation string) authz.RPCEntry {
	return authz.RPCEntry{
		Relation: relation,
		Extract: authz.StaticExtractor(objectTypeCluster, func(any) (string, error) {
			return clusterRootID, nil
		}),
	}
}

// PermissionMap — карта RPC → required relation+extract.
//
// Семантика per-RPC (enforcement по verb, развязан с tier — D-6/D-6a):
//   - Create                          — parent scope `project:<project_id>`, tier `editor` (F-7:
//     create-authority = write-authz на parent, не v_create)
//   - top-level List                  — parent scope `project:<project_id>`, tier `viewer`;
//     visibility per-object — через iam ListObjects `viewer ∪ v_list`
//   - Get / GetByValue                — на самом ресурсе, `v_get`
//   - ListSubnets/ListOps/… (on res)  — на самом ресурсе, `v_list`
//   - Update + domain-mutate verb'ы   — на самом ресурсе, `v_update`
//   - Delete                          — на самом ресурсе, `v_delete`
//   - OperationService.Get            — Public (op-id opaque, поллится creator'ом)
//
// Для object-self RPC мы НЕ резолвим project_id из БД заранее — это лишний DB-trip
// на каждый RPC. Проверяем v_*-relation на самом ресурсе (`vpc_network:enp_xxx`):
// reconciler kacho-iam материализует per-object `v_get/v_list/v_update/v_delete`
// для grant'а соответствующего verb'а; владелец/grantee аккаунта получает их
// forward-материализацией. cluster-admin резолвится через iam short-circuit
// (consumer short-circuit не держит).
func PermissionMap() authz.RPCMap {
	return authz.RPCMap{
		// =========================
		// NetworkService
		// =========================
		"/kacho.cloud.vpc.v1.NetworkService/Get": {
			Relation: relationVGet,
			Extract: authz.StaticExtractor(objectTypeNetwork, func(req any) (string, error) {
				return req.(*vpcv1.GetNetworkRequest).GetNetworkId(), nil
			}),
		},
		// NetworkService/List — top-level project List: server-side FGA-Check
		// `viewer` на `project:<project_id>` (parity с остальными 6 top-level
		// List RPC). data-level list-filter (ListNetworksUseCase → ListObjects)
		// сужает результат per-object ПОВЕРХ Check'а, но НЕ заменяет его: при
		// выключенном фильтре (helm-default) единственным гейтом остался бы
		// header-trusted handler-side AssertProjectOwnership → cross-project
		// enumeration. Поэтому Check обязателен (НЕ ScopeFiltered) — object-scope
		// авторизация не деградирует до client-заголовка (SEC audit 2026-07-05,
		// CWE-862/CWE-639). visibility per-object — через iam ListObjects
		// `viewer ∪ v_list`, см. authzfilter.
		"/kacho.cloud.vpc.v1.NetworkService/List": {
			Relation: relationViewer,
			Extract: authz.StaticExtractor(objectTypeProject, func(req any) (string, error) {
				return req.(*vpcv1.ListNetworksRequest).GetProjectId(), nil
			}),
		},
		"/kacho.cloud.vpc.v1.NetworkService/Create": {
			Relation: relationEditor,
			Extract: authz.StaticExtractor(objectTypeProject, func(req any) (string, error) {
				return req.(*vpcv1.CreateNetworkRequest).GetProjectId(), nil
			}),
		},
		"/kacho.cloud.vpc.v1.NetworkService/Update": {
			Relation: relationVUpdate,
			Extract: authz.StaticExtractor(objectTypeNetwork, func(req any) (string, error) {
				return req.(*vpcv1.UpdateNetworkRequest).GetNetworkId(), nil
			}),
		},
		"/kacho.cloud.vpc.v1.NetworkService/Delete": {
			Relation: relationVDelete,
			Extract: authz.StaticExtractor(objectTypeNetwork, func(req any) (string, error) {
				return req.(*vpcv1.DeleteNetworkRequest).GetNetworkId(), nil
			}),
		},
		"/kacho.cloud.vpc.v1.NetworkService/ListSubnets": {
			Relation: relationVList,
			Extract: authz.StaticExtractor(objectTypeNetwork, func(req any) (string, error) {
				return req.(*vpcv1.ListNetworkSubnetsRequest).GetNetworkId(), nil
			}),
		},
		"/kacho.cloud.vpc.v1.NetworkService/ListSecurityGroups": {
			Relation: relationVList,
			Extract: authz.StaticExtractor(objectTypeNetwork, func(req any) (string, error) {
				return req.(*vpcv1.ListNetworkSecurityGroupsRequest).GetNetworkId(), nil
			}),
		},
		"/kacho.cloud.vpc.v1.NetworkService/ListRouteTables": {
			Relation: relationVList,
			Extract: authz.StaticExtractor(objectTypeNetwork, func(req any) (string, error) {
				return req.(*vpcv1.ListNetworkRouteTablesRequest).GetNetworkId(), nil
			}),
		},
		"/kacho.cloud.vpc.v1.NetworkService/ListOperations": {
			Relation: relationVList,
			Extract: authz.StaticExtractor(objectTypeNetwork, func(req any) (string, error) {
				return req.(*vpcv1.ListNetworkOperationsRequest).GetNetworkId(), nil
			}),
		},

		// =========================
		// SubnetService
		// =========================
		"/kacho.cloud.vpc.v1.SubnetService/Get": {
			Relation: relationVGet,
			Extract: authz.StaticExtractor(objectTypeSubnet, func(req any) (string, error) {
				return req.(*vpcv1.GetSubnetRequest).GetSubnetId(), nil
			}),
		},
		"/kacho.cloud.vpc.v1.SubnetService/List": {
			Relation: relationViewer,
			Extract: authz.StaticExtractor(objectTypeProject, func(req any) (string, error) {
				return req.(*vpcv1.ListSubnetsRequest).GetProjectId(), nil
			}),
		},
		"/kacho.cloud.vpc.v1.SubnetService/Create": {
			Relation: relationEditor,
			Extract: authz.StaticExtractor(objectTypeProject, func(req any) (string, error) {
				return req.(*vpcv1.CreateSubnetRequest).GetProjectId(), nil
			}),
		},
		"/kacho.cloud.vpc.v1.SubnetService/Update": {
			Relation: relationVUpdate,
			Extract: authz.StaticExtractor(objectTypeSubnet, func(req any) (string, error) {
				return req.(*vpcv1.UpdateSubnetRequest).GetSubnetId(), nil
			}),
		},
		"/kacho.cloud.vpc.v1.SubnetService/Delete": {
			Relation: relationVDelete,
			Extract: authz.StaticExtractor(objectTypeSubnet, func(req any) (string, error) {
				return req.(*vpcv1.DeleteSubnetRequest).GetSubnetId(), nil
			}),
		},
		"/kacho.cloud.vpc.v1.SubnetService/AddCidrBlocks": {
			Relation: relationVUpdate,
			Extract: authz.StaticExtractor(objectTypeSubnet, func(req any) (string, error) {
				return req.(*vpcv1.AddSubnetCidrBlocksRequest).GetSubnetId(), nil
			}),
		},
		"/kacho.cloud.vpc.v1.SubnetService/RemoveCidrBlocks": {
			Relation: relationVUpdate,
			Extract: authz.StaticExtractor(objectTypeSubnet, func(req any) (string, error) {
				return req.(*vpcv1.RemoveSubnetCidrBlocksRequest).GetSubnetId(), nil
			}),
		},
		"/kacho.cloud.vpc.v1.SubnetService/ListUsedAddresses": {
			Relation: relationVList,
			Extract: authz.StaticExtractor(objectTypeSubnet, func(req any) (string, error) {
				return req.(*vpcv1.ListUsedAddressesRequest).GetSubnetId(), nil
			}),
		},
		"/kacho.cloud.vpc.v1.SubnetService/ListOperations": {
			Relation: relationVList,
			Extract: authz.StaticExtractor(objectTypeSubnet, func(req any) (string, error) {
				return req.(*vpcv1.ListSubnetOperationsRequest).GetSubnetId(), nil
			}),
		},

		// =========================
		// AddressService
		// =========================
		"/kacho.cloud.vpc.v1.AddressService/Get": {
			Relation: relationVGet,
			Extract: authz.StaticExtractor(objectTypeAddress, func(req any) (string, error) {
				return req.(*vpcv1.GetAddressRequest).GetAddressId(), nil
			}),
		},
		"/kacho.cloud.vpc.v1.AddressService/GetByValue": {
			Relation: relationVGet,
			// GetByValue lookup'ит Address по значению IP (без address_id заранее).
			// В request'е есть oneof scope { subnet_id } — если subnet_id передан,
			// проверяем viewer на subnet'е (caller с access на subnet получает
			// access ко всем его адресам). Без scope.subnet_id authz-объект
			// неопределим (адрес еще не резолвлен) → fail-closed DENY: безопасный
			// дефолт, scope.subnet_id обязателен для авторизованного GetByValue.
			Extract: authz.StaticExtractor(objectTypeSubnet, func(req any) (string, error) {
				r := req.(*vpcv1.GetAddressByValueRequest)
				if sid := r.GetSubnetId(); sid != "" {
					return sid, nil
				}
				return "", fmt.Errorf("authz: GetAddressByValue без scope.subnet_id — fail-closed")
			}),
		},
		"/kacho.cloud.vpc.v1.AddressService/List": {
			Relation: relationViewer,
			Extract: authz.StaticExtractor(objectTypeProject, func(req any) (string, error) {
				return req.(*vpcv1.ListAddressesRequest).GetProjectId(), nil
			}),
		},
		"/kacho.cloud.vpc.v1.AddressService/ListBySubnet": {
			Relation: relationVList,
			Extract: authz.StaticExtractor(objectTypeSubnet, func(req any) (string, error) {
				return req.(*vpcv1.ListAddressesBySubnetRequest).GetSubnetId(), nil
			}),
		},
		"/kacho.cloud.vpc.v1.AddressService/Create": {
			Relation: relationEditor,
			Extract: authz.StaticExtractor(objectTypeProject, func(req any) (string, error) {
				return req.(*vpcv1.CreateAddressRequest).GetProjectId(), nil
			}),
		},
		"/kacho.cloud.vpc.v1.AddressService/Update": {
			Relation: relationVUpdate,
			Extract: authz.StaticExtractor(objectTypeAddress, func(req any) (string, error) {
				return req.(*vpcv1.UpdateAddressRequest).GetAddressId(), nil
			}),
		},
		"/kacho.cloud.vpc.v1.AddressService/Delete": {
			Relation: relationVDelete,
			Extract: authz.StaticExtractor(objectTypeAddress, func(req any) (string, error) {
				return req.(*vpcv1.DeleteAddressRequest).GetAddressId(), nil
			}),
		},
		"/kacho.cloud.vpc.v1.AddressService/ListOperations": {
			Relation: relationVList,
			Extract: authz.StaticExtractor(objectTypeAddress, func(req any) (string, error) {
				return req.(*vpcv1.ListAddressOperationsRequest).GetAddressId(), nil
			}),
		},

		// =========================
		// RouteTableService
		// =========================
		"/kacho.cloud.vpc.v1.RouteTableService/Get": {
			Relation: relationVGet,
			Extract: authz.StaticExtractor(objectTypeRouteTable, func(req any) (string, error) {
				return req.(*vpcv1.GetRouteTableRequest).GetRouteTableId(), nil
			}),
		},
		"/kacho.cloud.vpc.v1.RouteTableService/List": {
			Relation: relationViewer,
			Extract: authz.StaticExtractor(objectTypeProject, func(req any) (string, error) {
				return req.(*vpcv1.ListRouteTablesRequest).GetProjectId(), nil
			}),
		},
		"/kacho.cloud.vpc.v1.RouteTableService/Create": {
			Relation: relationEditor,
			Extract: authz.StaticExtractor(objectTypeProject, func(req any) (string, error) {
				return req.(*vpcv1.CreateRouteTableRequest).GetProjectId(), nil
			}),
		},
		"/kacho.cloud.vpc.v1.RouteTableService/Update": {
			Relation: relationVUpdate,
			Extract: authz.StaticExtractor(objectTypeRouteTable, func(req any) (string, error) {
				return req.(*vpcv1.UpdateRouteTableRequest).GetRouteTableId(), nil
			}),
		},
		"/kacho.cloud.vpc.v1.RouteTableService/Delete": {
			Relation: relationVDelete,
			Extract: authz.StaticExtractor(objectTypeRouteTable, func(req any) (string, error) {
				return req.(*vpcv1.DeleteRouteTableRequest).GetRouteTableId(), nil
			}),
		},
		"/kacho.cloud.vpc.v1.RouteTableService/ListOperations": {
			Relation: relationVList,
			Extract: authz.StaticExtractor(objectTypeRouteTable, func(req any) (string, error) {
				return req.(*vpcv1.ListRouteTableOperationsRequest).GetRouteTableId(), nil
			}),
		},

		// =========================
		// SecurityGroupService
		// =========================
		"/kacho.cloud.vpc.v1.SecurityGroupService/Get": {
			Relation: relationVGet,
			Extract: authz.StaticExtractor(objectTypeSecurityGroup, func(req any) (string, error) {
				return req.(*vpcv1.GetSecurityGroupRequest).GetSecurityGroupId(), nil
			}),
		},
		"/kacho.cloud.vpc.v1.SecurityGroupService/List": {
			Relation: relationViewer,
			Extract: authz.StaticExtractor(objectTypeProject, func(req any) (string, error) {
				return req.(*vpcv1.ListSecurityGroupsRequest).GetProjectId(), nil
			}),
		},
		"/kacho.cloud.vpc.v1.SecurityGroupService/Create": {
			Relation: relationEditor,
			Extract: authz.StaticExtractor(objectTypeProject, func(req any) (string, error) {
				return req.(*vpcv1.CreateSecurityGroupRequest).GetProjectId(), nil
			}),
		},
		"/kacho.cloud.vpc.v1.SecurityGroupService/Update": {
			Relation: relationVUpdate,
			Extract: authz.StaticExtractor(objectTypeSecurityGroup, func(req any) (string, error) {
				return req.(*vpcv1.UpdateSecurityGroupRequest).GetSecurityGroupId(), nil
			}),
		},
		"/kacho.cloud.vpc.v1.SecurityGroupService/UpdateRules": {
			Relation: relationVUpdate,
			Extract: authz.StaticExtractor(objectTypeSecurityGroup, func(req any) (string, error) {
				return req.(*vpcv1.UpdateSecurityGroupRulesRequest).GetSecurityGroupId(), nil
			}),
		},
		"/kacho.cloud.vpc.v1.SecurityGroupService/UpdateRule": {
			Relation: relationVUpdate,
			Extract: authz.StaticExtractor(objectTypeSecurityGroup, func(req any) (string, error) {
				return req.(*vpcv1.UpdateSecurityGroupRuleRequest).GetSecurityGroupId(), nil
			}),
		},
		"/kacho.cloud.vpc.v1.SecurityGroupService/Delete": {
			Relation: relationVDelete,
			Extract: authz.StaticExtractor(objectTypeSecurityGroup, func(req any) (string, error) {
				return req.(*vpcv1.DeleteSecurityGroupRequest).GetSecurityGroupId(), nil
			}),
		},
		"/kacho.cloud.vpc.v1.SecurityGroupService/ListOperations": {
			Relation: relationVList,
			Extract: authz.StaticExtractor(objectTypeSecurityGroup, func(req any) (string, error) {
				return req.(*vpcv1.ListSecurityGroupOperationsRequest).GetSecurityGroupId(), nil
			}),
		},

		// =========================
		// GatewayService
		// =========================
		"/kacho.cloud.vpc.v1.GatewayService/Get": {
			Relation: relationVGet,
			Extract: authz.StaticExtractor(objectTypeGateway, func(req any) (string, error) {
				return req.(*vpcv1.GetGatewayRequest).GetGatewayId(), nil
			}),
		},
		"/kacho.cloud.vpc.v1.GatewayService/List": {
			Relation: relationViewer,
			Extract: authz.StaticExtractor(objectTypeProject, func(req any) (string, error) {
				return req.(*vpcv1.ListGatewaysRequest).GetProjectId(), nil
			}),
		},
		"/kacho.cloud.vpc.v1.GatewayService/Create": {
			Relation: relationEditor,
			Extract: authz.StaticExtractor(objectTypeProject, func(req any) (string, error) {
				return req.(*vpcv1.CreateGatewayRequest).GetProjectId(), nil
			}),
		},
		"/kacho.cloud.vpc.v1.GatewayService/Update": {
			Relation: relationVUpdate,
			Extract: authz.StaticExtractor(objectTypeGateway, func(req any) (string, error) {
				return req.(*vpcv1.UpdateGatewayRequest).GetGatewayId(), nil
			}),
		},
		"/kacho.cloud.vpc.v1.GatewayService/Delete": {
			Relation: relationVDelete,
			Extract: authz.StaticExtractor(objectTypeGateway, func(req any) (string, error) {
				return req.(*vpcv1.DeleteGatewayRequest).GetGatewayId(), nil
			}),
		},
		"/kacho.cloud.vpc.v1.GatewayService/ListOperations": {
			Relation: relationVList,
			Extract: authz.StaticExtractor(objectTypeGateway, func(req any) (string, error) {
				return req.(*vpcv1.ListGatewayOperationsRequest).GetGatewayId(), nil
			}),
		},

		// =========================
		// NetworkInterfaceService
		// =========================
		"/kacho.cloud.vpc.v1.NetworkInterfaceService/Get": {
			Relation: relationVGet,
			Extract: authz.StaticExtractor(objectTypeNetworkInterface, func(req any) (string, error) {
				return req.(*vpcv1.GetNetworkInterfaceRequest).GetNetworkInterfaceId(), nil
			}),
		},
		"/kacho.cloud.vpc.v1.NetworkInterfaceService/List": {
			Relation: relationViewer,
			Extract: authz.StaticExtractor(objectTypeProject, func(req any) (string, error) {
				return req.(*vpcv1.ListNetworkInterfacesRequest).GetProjectId(), nil
			}),
		},
		"/kacho.cloud.vpc.v1.NetworkInterfaceService/Create": {
			Relation: relationEditor,
			Extract: authz.StaticExtractor(objectTypeProject, func(req any) (string, error) {
				return req.(*vpcv1.CreateNetworkInterfaceRequest).GetProjectId(), nil
			}),
		},
		"/kacho.cloud.vpc.v1.NetworkInterfaceService/Update": {
			Relation: relationVUpdate,
			Extract: authz.StaticExtractor(objectTypeNetworkInterface, func(req any) (string, error) {
				return req.(*vpcv1.UpdateNetworkInterfaceRequest).GetNetworkInterfaceId(), nil
			}),
		},
		"/kacho.cloud.vpc.v1.NetworkInterfaceService/Delete": {
			Relation: relationVDelete,
			Extract: authz.StaticExtractor(objectTypeNetworkInterface, func(req any) (string, error) {
				return req.(*vpcv1.DeleteNetworkInterfaceRequest).GetNetworkInterfaceId(), nil
			}),
		},
		"/kacho.cloud.vpc.v1.NetworkInterfaceService/ListOperations": {
			Relation: relationVList,
			Extract: authz.StaticExtractor(objectTypeNetworkInterface, func(req any) (string, error) {
				return req.(*vpcv1.ListNetworkInterfaceOperationsRequest).GetNetworkInterfaceId(), nil
			}),
		},

		// =========================
		// Internal RPC (cluster-internal listener :9091).
		//
		// FGA-гейт на internal listener'е (security-инвариант: authN+authZ и на
		// internal'е тоже — internal не освобожден). Admin/cluster-RPC
		// (InternalNetworkService / InternalAddressPoolService) гейтятся на
		// singleton `cluster:cluster_kacho_root` (system_viewer/system_admin).
		// IPAM-примитивы InternalAddressService.* гейтятся object-scoped на самом
		// ресурсе Address (vpc_address:<address_id>, verb-bearing v_update/v_get —
		// зеркало публичного AddressService.{Update,Get}): они обслуживают мутацию/
		// чтение tenant-ресурса Address от имени конечного пользователя (Instance
		// NAT, Listener VIP), а не admin-операцию над платформой, поэтому
		// cluster-scoped system_admin отклонил бы каждого нормального владельца
		// Address. Наличие в Map снимает их с methodIsInternal-bypass'а.
		// =========================

		// InternalNetworkService — GetNetwork (read инфра-чувствительного vrf_id,
		// read-tier system_viewer; потребитель — vpc-оператор) +
		// SetDefaultSecurityGroupId (admin-мутация computed-поля, system_admin).
		"/kacho.cloud.vpc.v1.InternalNetworkService/GetNetwork":                clusterScoped(relationSystemViewer),
		"/kacho.cloud.vpc.v1.InternalNetworkService/SetDefaultSecurityGroupId": clusterScoped(relationSystemAdmin),

		// InternalAddressPoolService — admin-only ресурс (не на external endpoint);
		// все 11 RPC гейтятся system_admin@cluster.
		"/kacho.cloud.vpc.v1.InternalAddressPoolService/Create":               clusterScoped(relationSystemAdmin),
		"/kacho.cloud.vpc.v1.InternalAddressPoolService/Get":                  clusterScoped(relationSystemAdmin),
		"/kacho.cloud.vpc.v1.InternalAddressPoolService/List":                 clusterScoped(relationSystemAdmin),
		"/kacho.cloud.vpc.v1.InternalAddressPoolService/Update":               clusterScoped(relationSystemAdmin),
		"/kacho.cloud.vpc.v1.InternalAddressPoolService/Delete":               clusterScoped(relationSystemAdmin),
		"/kacho.cloud.vpc.v1.InternalAddressPoolService/AddCidrBlocks":        clusterScoped(relationSystemAdmin),
		"/kacho.cloud.vpc.v1.InternalAddressPoolService/RemoveCidrBlocks":     clusterScoped(relationSystemAdmin),
		"/kacho.cloud.vpc.v1.InternalAddressPoolService/BindAsNetworkDefault": clusterScoped(relationSystemAdmin),
		"/kacho.cloud.vpc.v1.InternalAddressPoolService/UnbindNetworkDefault": clusterScoped(relationSystemAdmin),
		"/kacho.cloud.vpc.v1.InternalAddressPoolService/ListAddresses":        clusterScoped(relationSystemAdmin),
		"/kacho.cloud.vpc.v1.InternalAddressPoolService/GetUtilization":       clusterScoped(relationSystemAdmin),

		// InternalAddressService — IPAM-примитивы на конкретном Address
		// (object-scoped, не cluster-scoped): мутации (atomic IP-allocate +
		// referrer-tracking) → v_update, чтение referrer'а → v_get. object —
		// `vpc_address:<address_id>` из request-поля address_id; пустой id →
		// FormatObject-ошибка → DecisionDenied (как у публичного AddressService.Get).
		"/kacho.cloud.vpc.v1.InternalAddressService/AllocateInternalIP": {
			Relation: relationVUpdate,
			Extract: authz.StaticExtractor(objectTypeAddress, func(req any) (string, error) {
				return req.(*vpcv1.AllocateInternalIPRequest).GetAddressId(), nil
			}),
		},
		"/kacho.cloud.vpc.v1.InternalAddressService/AllocateInternalIPv6": {
			Relation: relationVUpdate,
			Extract: authz.StaticExtractor(objectTypeAddress, func(req any) (string, error) {
				return req.(*vpcv1.AllocateInternalIPRequest).GetAddressId(), nil
			}),
		},
		"/kacho.cloud.vpc.v1.InternalAddressService/AllocateExternalIP": {
			Relation: relationVUpdate,
			Extract: authz.StaticExtractor(objectTypeAddress, func(req any) (string, error) {
				return req.(*vpcv1.AllocateExternalIPRequest).GetAddressId(), nil
			}),
		},
		"/kacho.cloud.vpc.v1.InternalAddressService/AllocateExternalIPv6": {
			Relation: relationVUpdate,
			Extract: authz.StaticExtractor(objectTypeAddress, func(req any) (string, error) {
				return req.(*vpcv1.AllocateExternalIPRequest).GetAddressId(), nil
			}),
		},
		"/kacho.cloud.vpc.v1.InternalAddressService/SetAddressReference": {
			Relation: relationVUpdate,
			Extract: authz.StaticExtractor(objectTypeAddress, func(req any) (string, error) {
				return req.(*vpcv1.SetAddressReferenceRequest).GetAddressId(), nil
			}),
		},
		"/kacho.cloud.vpc.v1.InternalAddressService/ClearAddressReference": {
			Relation: relationVUpdate,
			Extract: authz.StaticExtractor(objectTypeAddress, func(req any) (string, error) {
				return req.(*vpcv1.ClearAddressReferenceRequest).GetAddressId(), nil
			}),
		},
		"/kacho.cloud.vpc.v1.InternalAddressService/MarkAddressEphemeralInUse": {
			Relation: relationVUpdate,
			Extract: authz.StaticExtractor(objectTypeAddress, func(req any) (string, error) {
				return req.(*vpcv1.MarkAddressEphemeralInUseRequest).GetAddressId(), nil
			}),
		},
		"/kacho.cloud.vpc.v1.InternalAddressService/GetAddressReference": {
			Relation: relationVGet,
			Extract: authz.StaticExtractor(objectTypeAddress, func(req any) (string, error) {
				return req.(*vpcv1.GetAddressReferenceRequest).GetAddressId(), nil
			}),
		},

		// =========================
		// OperationService (LRO poll RPC).
		//
		// Proto-пакет — `kacho.cloud.operation` (без `.v1`); gRPC fullMethod
		// соответственно `/kacho.cloud.operation.OperationService/*`.
		//
		// Operation Get/Cancel НЕ гейтятся per-RPC ReBAC-Check'ом: в FGA-модели нет
		// object type `vpc_operation` и per-operation tuple'ы не эмитятся, поэтому
		// `Check viewer on vpc_operation:<id>` не имеет пути и отверг бы даже
		// owner-poll. Здесь `Public` означает «ReBAC-exempt», а НЕ «unauthenticated»:
		// anti-anon (TenantUnaryInterceptor в production-mode) сохраняется, а
		// ownership энфорсится В HANDLER'е (OperationHandler.Get/Cancel через
		// ownership-scoped GetOwned/CancelOwned: owner — creator-principal из
		// доверенного ctx; чужой id → NotFound, no-leak). Анти-регресс: пометка
		// Public тут НЕ освобождает Operation RPC ни от anti-anon, ни от
		// ownership — она лишь снимает их с ReBAC-Check'а (которого для них нет).
		"/kacho.cloud.operation.OperationService/Get":    {Public: true},
		"/kacho.cloud.operation.OperationService/Cancel": {Public: true},
	}
}
