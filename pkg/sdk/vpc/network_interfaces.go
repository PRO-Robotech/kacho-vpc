// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package vpc

import (
	"context"

	"google.golang.org/grpc"

	operationv1 "github.com/PRO-Robotech/kacho-corelib/proto/gen/go/kacho/cloud/operation"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
)

// NetworkInterfaceServiceClient — alias на сгенерированный gRPC-клиент.
// NetworkInterface (NIC) — самостоятельный ресурс VPC, отвязанный от Instance:
// живет в kacho-vpc, не в Compute.
type NetworkInterfaceServiceClient = vpcv1.NetworkInterfaceServiceClient

// GetNetworkInterface — sync read.
func (c *Client) GetNetworkInterface(ctx context.Context, id string, opts ...grpc.CallOption) (*vpcv1.NetworkInterface, error) {
	return c.NetworkInterfaces.Get(ctx, &vpcv1.GetNetworkInterfaceRequest{NetworkInterfaceId: id}, opts...)
}

// ListNetworkInterfaces — sync list по project/page.
func (c *Client) ListNetworkInterfaces(ctx context.Context, req *vpcv1.ListNetworkInterfacesRequest, opts ...grpc.CallOption) (*vpcv1.ListNetworkInterfacesResponse, error) {
	return c.NetworkInterfaces.List(ctx, req, opts...)
}

// CreateNetworkInterface — async create. mac_address аллоцируется сервером
// (output-only, клиент задать не может).
func (c *Client) CreateNetworkInterface(ctx context.Context, req *vpcv1.CreateNetworkInterfaceRequest, opts ...grpc.CallOption) (*operationv1.Operation, error) {
	return c.NetworkInterfaces.Create(ctx, req, opts...)
}

// UpdateNetworkInterface — async update. На NIC допустимо ≤ 1 v4 + ≤ 1 v6 адреса.
func (c *Client) UpdateNetworkInterface(ctx context.Context, req *vpcv1.UpdateNetworkInterfaceRequest, opts ...grpc.CallOption) (*operationv1.Operation, error) {
	return c.NetworkInterfaces.Update(ctx, req, opts...)
}

// DeleteNetworkInterface — async hard-delete. NIC блокирует свою подсеть
// (FK RESTRICT) — удалять снизу вверх: NIC → Address → Subnet → Network.
func (c *Client) DeleteNetworkInterface(ctx context.Context, id string, opts ...grpc.CallOption) (*operationv1.Operation, error) {
	return c.NetworkInterfaces.Delete(ctx, &vpcv1.DeleteNetworkInterfaceRequest{NetworkInterfaceId: id}, opts...)
}
