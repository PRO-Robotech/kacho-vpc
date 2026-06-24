// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package authzfilter

// FGA object types VPC-домена (передаются в AuthorizeService.ListObjects как
// resource_type). Должны совпадать с closed-table objectTypes в kacho-iam
// (например "vpc.subnet" → "vpc_subnet").
const (
	ResourceTypeNetwork          = "vpc_network"
	ResourceTypeSubnet           = "vpc_subnet"
	ResourceTypeSecurityGroup    = "vpc_security_group"
	ResourceTypeRouteTable       = "vpc_route_table"
	ResourceTypeAddress          = "vpc_address"
	ResourceTypeGateway          = "vpc_gateway"
	ResourceTypeNetworkInterface = "vpc_network_interface"
)

// Action-строки VPC-домена. На стороне kacho-iam последний `.`-сегмент (verb)
// резолвится в FGA relation: `list` → `viewer` — та же tier-relation, что
// энфорсит per-RPC Check для чтения (read==enforce). Формат —
// `<domain>.<resource>.<verb>` из IAM permission catalog.
const (
	ActionNetworkList          = "vpc.networks.list"
	ActionSubnetList           = "vpc.subnets.list"
	ActionSecurityGroupList    = "vpc.securityGroups.list"
	ActionRouteTableList       = "vpc.routeTables.list"
	ActionAddressList          = "vpc.addresses.list"
	ActionGatewayList          = "vpc.gateways.list"
	ActionNetworkInterfaceList = "vpc.networkInterfaces.list"
)
