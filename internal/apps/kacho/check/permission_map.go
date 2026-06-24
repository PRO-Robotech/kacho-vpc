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
	relationViewer = "viewer"
	relationEditor = "editor"

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
// Семантика per-RPC:
//   - Create / List / *Operations            — на parent scope `project:<project_id>` (из request)
//   - Get/Update/Delete/Move/<verb>          — на самом ресурсе `<resource_type>:<resource_id>`
//   - OperationService.Get                   — на `vpc_operation:<operation_id>` (viewer)
//
// Update/Delete/Move — relation=editor, все read-only — relation=viewer.
//
// Для Update/Delete/Move/<verb> мы НЕ резолвим project_id из БД заранее — это
// лишний DB-trip на каждый RPC. Проверяем relation на самом ресурсе
// (`vpc_network:enp_xxx`). FGA-модель настроена так, что `editor on vpc_network`
// → computed через `editor on project` → `member on group`. Это эквивалентно
// проверке на project'е, но без лишнего DB-lookup'а.
func PermissionMap() authz.RPCMap {
	return authz.RPCMap{
		// =========================
		// NetworkService
		// =========================
		"/kacho.cloud.vpc.v1.NetworkService/Get": {
			Relation: relationViewer,
			Extract: authz.StaticExtractor(objectTypeNetwork, func(req any) (string, error) {
				return req.(*vpcv1.GetNetworkRequest).GetNetworkId(), nil
			}),
		},
		// NetworkService/List — scope-filtered List RPC: handler
		// (ListNetworksUseCase) резолвит набор разрешенных FGA id Network через
		// ListObjects и возвращает 200 + отфильтрованный список (пустой, если у
		// caller'а нет грантов в запрошенном project'е). Один per-RPC Check здесь
		// отклонил бы весь вызов `no path` 403 еще до scope-filter'а.
		// ScopeFiltered → interceptor пропускает Check; authn по-прежнему
		// энфорсится выше (api-gateway JWT). Extract оставлен для parity с каталогом/tooling.
		"/kacho.cloud.vpc.v1.NetworkService/List": {
			Relation:      relationViewer,
			ScopeFiltered: true,
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
			Relation: relationEditor,
			Extract: authz.StaticExtractor(objectTypeNetwork, func(req any) (string, error) {
				return req.(*vpcv1.UpdateNetworkRequest).GetNetworkId(), nil
			}),
		},
		"/kacho.cloud.vpc.v1.NetworkService/Delete": {
			Relation: relationEditor,
			Extract: authz.StaticExtractor(objectTypeNetwork, func(req any) (string, error) {
				return req.(*vpcv1.DeleteNetworkRequest).GetNetworkId(), nil
			}),
		},
		"/kacho.cloud.vpc.v1.NetworkService/ListSubnets": {
			Relation: relationViewer,
			Extract: authz.StaticExtractor(objectTypeNetwork, func(req any) (string, error) {
				return req.(*vpcv1.ListNetworkSubnetsRequest).GetNetworkId(), nil
			}),
		},
		"/kacho.cloud.vpc.v1.NetworkService/ListSecurityGroups": {
			Relation: relationViewer,
			Extract: authz.StaticExtractor(objectTypeNetwork, func(req any) (string, error) {
				return req.(*vpcv1.ListNetworkSecurityGroupsRequest).GetNetworkId(), nil
			}),
		},
		"/kacho.cloud.vpc.v1.NetworkService/ListRouteTables": {
			Relation: relationViewer,
			Extract: authz.StaticExtractor(objectTypeNetwork, func(req any) (string, error) {
				return req.(*vpcv1.ListNetworkRouteTablesRequest).GetNetworkId(), nil
			}),
		},
		"/kacho.cloud.vpc.v1.NetworkService/ListOperations": {
			Relation: relationViewer,
			Extract: authz.StaticExtractor(objectTypeNetwork, func(req any) (string, error) {
				return req.(*vpcv1.ListNetworkOperationsRequest).GetNetworkId(), nil
			}),
		},

		// =========================
		// SubnetService
		// =========================
		"/kacho.cloud.vpc.v1.SubnetService/Get": {
			Relation: relationViewer,
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
			Relation: relationEditor,
			Extract: authz.StaticExtractor(objectTypeSubnet, func(req any) (string, error) {
				return req.(*vpcv1.UpdateSubnetRequest).GetSubnetId(), nil
			}),
		},
		"/kacho.cloud.vpc.v1.SubnetService/Delete": {
			Relation: relationEditor,
			Extract: authz.StaticExtractor(objectTypeSubnet, func(req any) (string, error) {
				return req.(*vpcv1.DeleteSubnetRequest).GetSubnetId(), nil
			}),
		},
		"/kacho.cloud.vpc.v1.SubnetService/AddCidrBlocks": {
			Relation: relationEditor,
			Extract: authz.StaticExtractor(objectTypeSubnet, func(req any) (string, error) {
				return req.(*vpcv1.AddSubnetCidrBlocksRequest).GetSubnetId(), nil
			}),
		},
		"/kacho.cloud.vpc.v1.SubnetService/RemoveCidrBlocks": {
			Relation: relationEditor,
			Extract: authz.StaticExtractor(objectTypeSubnet, func(req any) (string, error) {
				return req.(*vpcv1.RemoveSubnetCidrBlocksRequest).GetSubnetId(), nil
			}),
		},
		"/kacho.cloud.vpc.v1.SubnetService/ListUsedAddresses": {
			Relation: relationViewer,
			Extract: authz.StaticExtractor(objectTypeSubnet, func(req any) (string, error) {
				return req.(*vpcv1.ListUsedAddressesRequest).GetSubnetId(), nil
			}),
		},
		"/kacho.cloud.vpc.v1.SubnetService/ListOperations": {
			Relation: relationViewer,
			Extract: authz.StaticExtractor(objectTypeSubnet, func(req any) (string, error) {
				return req.(*vpcv1.ListSubnetOperationsRequest).GetSubnetId(), nil
			}),
		},

		// =========================
		// AddressService
		// =========================
		"/kacho.cloud.vpc.v1.AddressService/Get": {
			Relation: relationViewer,
			Extract: authz.StaticExtractor(objectTypeAddress, func(req any) (string, error) {
				return req.(*vpcv1.GetAddressRequest).GetAddressId(), nil
			}),
		},
		"/kacho.cloud.vpc.v1.AddressService/GetByValue": {
			Relation: relationViewer,
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
			Relation: relationViewer,
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
			Relation: relationEditor,
			Extract: authz.StaticExtractor(objectTypeAddress, func(req any) (string, error) {
				return req.(*vpcv1.UpdateAddressRequest).GetAddressId(), nil
			}),
		},
		"/kacho.cloud.vpc.v1.AddressService/Delete": {
			Relation: relationEditor,
			Extract: authz.StaticExtractor(objectTypeAddress, func(req any) (string, error) {
				return req.(*vpcv1.DeleteAddressRequest).GetAddressId(), nil
			}),
		},
		"/kacho.cloud.vpc.v1.AddressService/ListOperations": {
			Relation: relationViewer,
			Extract: authz.StaticExtractor(objectTypeAddress, func(req any) (string, error) {
				return req.(*vpcv1.ListAddressOperationsRequest).GetAddressId(), nil
			}),
		},

		// =========================
		// RouteTableService
		// =========================
		"/kacho.cloud.vpc.v1.RouteTableService/Get": {
			Relation: relationViewer,
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
			Relation: relationEditor,
			Extract: authz.StaticExtractor(objectTypeRouteTable, func(req any) (string, error) {
				return req.(*vpcv1.UpdateRouteTableRequest).GetRouteTableId(), nil
			}),
		},
		"/kacho.cloud.vpc.v1.RouteTableService/Delete": {
			Relation: relationEditor,
			Extract: authz.StaticExtractor(objectTypeRouteTable, func(req any) (string, error) {
				return req.(*vpcv1.DeleteRouteTableRequest).GetRouteTableId(), nil
			}),
		},
		"/kacho.cloud.vpc.v1.RouteTableService/ListOperations": {
			Relation: relationViewer,
			Extract: authz.StaticExtractor(objectTypeRouteTable, func(req any) (string, error) {
				return req.(*vpcv1.ListRouteTableOperationsRequest).GetRouteTableId(), nil
			}),
		},

		// =========================
		// SecurityGroupService
		// =========================
		"/kacho.cloud.vpc.v1.SecurityGroupService/Get": {
			Relation: relationViewer,
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
			Relation: relationEditor,
			Extract: authz.StaticExtractor(objectTypeSecurityGroup, func(req any) (string, error) {
				return req.(*vpcv1.UpdateSecurityGroupRequest).GetSecurityGroupId(), nil
			}),
		},
		"/kacho.cloud.vpc.v1.SecurityGroupService/UpdateRules": {
			Relation: relationEditor,
			Extract: authz.StaticExtractor(objectTypeSecurityGroup, func(req any) (string, error) {
				return req.(*vpcv1.UpdateSecurityGroupRulesRequest).GetSecurityGroupId(), nil
			}),
		},
		"/kacho.cloud.vpc.v1.SecurityGroupService/UpdateRule": {
			Relation: relationEditor,
			Extract: authz.StaticExtractor(objectTypeSecurityGroup, func(req any) (string, error) {
				return req.(*vpcv1.UpdateSecurityGroupRuleRequest).GetSecurityGroupId(), nil
			}),
		},
		"/kacho.cloud.vpc.v1.SecurityGroupService/Delete": {
			Relation: relationEditor,
			Extract: authz.StaticExtractor(objectTypeSecurityGroup, func(req any) (string, error) {
				return req.(*vpcv1.DeleteSecurityGroupRequest).GetSecurityGroupId(), nil
			}),
		},
		"/kacho.cloud.vpc.v1.SecurityGroupService/ListOperations": {
			Relation: relationViewer,
			Extract: authz.StaticExtractor(objectTypeSecurityGroup, func(req any) (string, error) {
				return req.(*vpcv1.ListSecurityGroupOperationsRequest).GetSecurityGroupId(), nil
			}),
		},

		// =========================
		// GatewayService
		// =========================
		"/kacho.cloud.vpc.v1.GatewayService/Get": {
			Relation: relationViewer,
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
			Relation: relationEditor,
			Extract: authz.StaticExtractor(objectTypeGateway, func(req any) (string, error) {
				return req.(*vpcv1.UpdateGatewayRequest).GetGatewayId(), nil
			}),
		},
		"/kacho.cloud.vpc.v1.GatewayService/Delete": {
			Relation: relationEditor,
			Extract: authz.StaticExtractor(objectTypeGateway, func(req any) (string, error) {
				return req.(*vpcv1.DeleteGatewayRequest).GetGatewayId(), nil
			}),
		},
		"/kacho.cloud.vpc.v1.GatewayService/ListOperations": {
			Relation: relationViewer,
			Extract: authz.StaticExtractor(objectTypeGateway, func(req any) (string, error) {
				return req.(*vpcv1.ListGatewayOperationsRequest).GetGatewayId(), nil
			}),
		},

		// =========================
		// NetworkInterfaceService
		// =========================
		"/kacho.cloud.vpc.v1.NetworkInterfaceService/Get": {
			Relation: relationViewer,
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
			Relation: relationEditor,
			Extract: authz.StaticExtractor(objectTypeNetworkInterface, func(req any) (string, error) {
				return req.(*vpcv1.UpdateNetworkInterfaceRequest).GetNetworkInterfaceId(), nil
			}),
		},
		"/kacho.cloud.vpc.v1.NetworkInterfaceService/Delete": {
			Relation: relationEditor,
			Extract: authz.StaticExtractor(objectTypeNetworkInterface, func(req any) (string, error) {
				return req.(*vpcv1.DeleteNetworkInterfaceRequest).GetNetworkInterfaceId(), nil
			}),
		},
		"/kacho.cloud.vpc.v1.NetworkInterfaceService/ListOperations": {
			Relation: relationViewer,
			Extract: authz.StaticExtractor(objectTypeNetworkInterface, func(req any) (string, error) {
				return req.(*vpcv1.ListNetworkInterfaceOperationsRequest).GetNetworkInterfaceId(), nil
			}),
		},

		// =========================
		// Internal cluster-scoped RPC (cluster-internal listener :9091).
		//
		// FGA-гейт на internal listener'е (security-инвариант: authN+authZ и на
		// internal'е тоже). object — singleton `cluster:cluster_kacho_root`,
		// relation из proto-аннотации required_relation. IPAM-примитивы
		// InternalAddressService.* сюда НЕ добавляются — они остаются exempt
		// (skip через methodIsInternal), авторизуются in-handler.
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

		// =========================
		// OperationService (LRO poll RPC).
		//
		// Proto-пакет — `kacho.cloud.operation` (без `.v1`); gRPC fullMethod
		// соответственно `/kacho.cloud.operation.OperationService/*`.
		//
		// Operation poll НЕ гейтится per-RPC. В FGA-модели нет object type
		// `vpc_operation` и per-operation tuple'ы не эмитятся, поэтому Check
		// `viewer on vpc_operation:<id>` не имеет пути и любой poll — включая
		// тот, что создавший клиент шлет сразу после успешной мутации — был бы
		// отклонен. Operation id'шники opaque и неугадываемы; api-gateway уже
		// помечает `OperationService/Get` и `/Cancel` как `<exempt>`. Пометка
		// Public здесь делает interceptor vpc-сервиса согласованным с gateway
		// (map-miss дал бы fail-closed ErrUnmapped, поэтому записи оставлены, но
		// помечены Public).
		"/kacho.cloud.operation.OperationService/Get":    {Public: true},
		"/kacho.cloud.operation.OperationService/Cancel": {Public: true},
	}
}
