package check

import (
	"fmt"

	"github.com/PRO-Robotech/kacho-corelib/authz"
	operationv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/operation"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	privatelinkv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1/privatelink"
)

// FGA object types (FGA model E3 §4 acceptance).
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
//	"vpc_private_endpoint"— PrivateEndpoint
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
	objectTypePrivateEndpoint  = "vpc_private_endpoint"
	objectTypeNetworkInterface = "vpc_network_interface"
	objectTypeOperation        = "vpc_operation"
)

// FGA relations (FGA model E3 §4 acceptance). Дублирует константы из
// kacho-iam/internal/authzmap (там — source of truth); тут — backend
// view-only, чтобы не плодить cross-repo import просто ради двух строк.
const (
	relationViewer = "viewer"
	relationEditor = "editor"
)

// PermissionMap — карта RPC → required relation+extract.
//
// Семантика per-RPC:
//   - Create / List / *Operations            — на parent scope `project:<project_id>` (из request)
//   - Get/Update/Delete/Move/<verb>          — на самом ресурсе `<resource_type>:<resource_id>`
//   - OperationService.Get                   — на `vpc_operation:<operation_id>` (viewer)
//   - PrivateEndpoint.GetByEndpointAddress   — viewer на parent project (точечно резолвить
//                                              ресурс по эндпоинт-адресу слишком дорого; project-scope OK)
//
// Update/Delete/Move — relation=editor, всё read-only — relation=viewer.
//
// scope-guard (KAC-108): для Update/Delete/Move/<verb> мы НЕ резолвим project_id
// из БД заранее — это лишний DB-trip на каждый RPC. Проверяем relation на самом
// ресурсе (`vpc_network:enp_xxx`). FGA-модель E3 §4 настроена так, что
// `editor on vpc_network` → computed через `editor on project` → `member on group`
// (см. кascade в acceptance §4). Это эквивалентно проверке на project'е, но
// без лишнего DB-lookup'а.
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
		"/kacho.cloud.vpc.v1.NetworkService/Move": {
			Relation: relationEditor,
			Extract: authz.StaticExtractor(objectTypeNetwork, func(req any) (string, error) {
				return req.(*vpcv1.MoveNetworkRequest).GetNetworkId(), nil
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
		"/kacho.cloud.vpc.v1.SubnetService/Move": {
			Relation: relationEditor,
			Extract: authz.StaticExtractor(objectTypeSubnet, func(req any) (string, error) {
				return req.(*vpcv1.MoveSubnetRequest).GetSubnetId(), nil
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
		"/kacho.cloud.vpc.v1.SubnetService/Relocate": {
			Relation: relationEditor,
			Extract: authz.StaticExtractor(objectTypeSubnet, func(req any) (string, error) {
				return req.(*vpcv1.RelocateSubnetRequest).GetSubnetId(), nil
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
			// access ко всем его адресам). Если scope не задан — TODO(KAC-108):
			// резолвить project_id через secondary DB-lookup; пока default-deny
			// возвращаем пустой object_id → interceptor вернёт DENY.
			Extract: authz.StaticExtractor(objectTypeSubnet, func(req any) (string, error) {
				r := req.(*vpcv1.GetAddressByValueRequest)
				if sid := r.GetSubnetId(); sid != "" {
					return sid, nil
				}
				// TODO(KAC-108): resolve via secondary lookup. Сейчас — fail-closed.
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
		"/kacho.cloud.vpc.v1.AddressService/Move": {
			Relation: relationEditor,
			Extract: authz.StaticExtractor(objectTypeAddress, func(req any) (string, error) {
				return req.(*vpcv1.MoveAddressRequest).GetAddressId(), nil
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
		"/kacho.cloud.vpc.v1.RouteTableService/Move": {
			Relation: relationEditor,
			Extract: authz.StaticExtractor(objectTypeRouteTable, func(req any) (string, error) {
				return req.(*vpcv1.MoveRouteTableRequest).GetRouteTableId(), nil
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
		"/kacho.cloud.vpc.v1.SecurityGroupService/Move": {
			Relation: relationEditor,
			Extract: authz.StaticExtractor(objectTypeSecurityGroup, func(req any) (string, error) {
				return req.(*vpcv1.MoveSecurityGroupRequest).GetSecurityGroupId(), nil
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
		"/kacho.cloud.vpc.v1.GatewayService/Move": {
			Relation: relationEditor,
			Extract: authz.StaticExtractor(objectTypeGateway, func(req any) (string, error) {
				return req.(*vpcv1.MoveGatewayRequest).GetGatewayId(), nil
			}),
		},
		"/kacho.cloud.vpc.v1.GatewayService/ListOperations": {
			Relation: relationViewer,
			Extract: authz.StaticExtractor(objectTypeGateway, func(req any) (string, error) {
				return req.(*vpcv1.ListGatewayOperationsRequest).GetGatewayId(), nil
			}),
		},

		// =========================
		// PrivateEndpointService (privatelink package)
		// =========================
		"/kacho.cloud.vpc.v1.privatelink.PrivateEndpointService/Get": {
			Relation: relationViewer,
			Extract: authz.StaticExtractor(objectTypePrivateEndpoint, func(req any) (string, error) {
				return req.(*privatelinkv1.GetPrivateEndpointRequest).GetPrivateEndpointId(), nil
			}),
		},
		"/kacho.cloud.vpc.v1.privatelink.PrivateEndpointService/List": {
			Relation: relationViewer,
			Extract: authz.StaticExtractor(objectTypeProject, func(req any) (string, error) {
				return req.(*privatelinkv1.ListPrivateEndpointsRequest).GetProjectId(), nil
			}),
		},
		"/kacho.cloud.vpc.v1.privatelink.PrivateEndpointService/Create": {
			Relation: relationEditor,
			Extract: authz.StaticExtractor(objectTypeProject, func(req any) (string, error) {
				return req.(*privatelinkv1.CreatePrivateEndpointRequest).GetProjectId(), nil
			}),
		},
		"/kacho.cloud.vpc.v1.privatelink.PrivateEndpointService/Update": {
			Relation: relationEditor,
			Extract: authz.StaticExtractor(objectTypePrivateEndpoint, func(req any) (string, error) {
				return req.(*privatelinkv1.UpdatePrivateEndpointRequest).GetPrivateEndpointId(), nil
			}),
		},
		"/kacho.cloud.vpc.v1.privatelink.PrivateEndpointService/Delete": {
			Relation: relationEditor,
			Extract: authz.StaticExtractor(objectTypePrivateEndpoint, func(req any) (string, error) {
				return req.(*privatelinkv1.DeletePrivateEndpointRequest).GetPrivateEndpointId(), nil
			}),
		},
		"/kacho.cloud.vpc.v1.privatelink.PrivateEndpointService/ListOperations": {
			Relation: relationViewer,
			Extract: authz.StaticExtractor(objectTypePrivateEndpoint, func(req any) (string, error) {
				return req.(*privatelinkv1.ListPrivateEndpointOperationsRequest).GetPrivateEndpointId(), nil
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
		"/kacho.cloud.vpc.v1.NetworkInterfaceService/AttachToInstance": {
			Relation: relationEditor,
			Extract: authz.StaticExtractor(objectTypeNetworkInterface, func(req any) (string, error) {
				return req.(*vpcv1.AttachNetworkInterfaceRequest).GetNetworkInterfaceId(), nil
			}),
		},
		"/kacho.cloud.vpc.v1.NetworkInterfaceService/DetachFromInstance": {
			Relation: relationEditor,
			Extract: authz.StaticExtractor(objectTypeNetworkInterface, func(req any) (string, error) {
				return req.(*vpcv1.DetachNetworkInterfaceRequest).GetNetworkInterfaceId(), nil
			}),
		},
		"/kacho.cloud.vpc.v1.NetworkInterfaceService/ListOperations": {
			Relation: relationViewer,
			Extract: authz.StaticExtractor(objectTypeNetworkInterface, func(req any) (string, error) {
				return req.(*vpcv1.ListNetworkInterfaceOperationsRequest).GetNetworkInterfaceId(), nil
			}),
		},

		// =========================
		// OperationService (LRO; viewer на operation-id).
		//
		// Proto-пакет — `kacho.cloud.operation` (без `.v1`); gRPC fullMethod
		// соответственно `/kacho.cloud.operation.OperationService/*`.
		// =========================
		"/kacho.cloud.operation.OperationService/Get": {
			Relation: relationViewer,
			Extract: authz.StaticExtractor(objectTypeOperation, func(req any) (string, error) {
				r, ok := req.(*operationv1.GetOperationRequest)
				if !ok {
					return "", fmt.Errorf("authz: unexpected req type for OperationService.Get: %T", req)
				}
				return r.GetOperationId(), nil
			}),
		},
		"/kacho.cloud.operation.OperationService/Cancel": {
			Relation: relationEditor,
			Extract: authz.StaticExtractor(objectTypeOperation, func(req any) (string, error) {
				r, ok := req.(*operationv1.CancelOperationRequest)
				if !ok {
					return "", fmt.Errorf("authz: unexpected req type for OperationService.Cancel: %T", req)
				}
				return r.GetOperationId(), nil
			}),
		},
	}
}
