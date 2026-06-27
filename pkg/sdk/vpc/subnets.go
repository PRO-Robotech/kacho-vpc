// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package vpc

import (
	"context"

	"google.golang.org/grpc"

	operationv1 "github.com/PRO-Robotech/kacho-corelib/proto/gen/go/kacho/cloud/operation"
	vpcv1 "github.com/PRO-Robotech/kacho-vpc/proto/gen/go/kacho/cloud/vpc/v1"
)

// SubnetServiceClient — alias на сгенерированный gRPC-клиент (см. networks.go).
type SubnetServiceClient = vpcv1.SubnetServiceClient

// GetSubnet — sync read одной подсети по id.
func (c *Client) GetSubnet(ctx context.Context, id string, opts ...grpc.CallOption) (*vpcv1.Subnet, error) {
	return c.Subnets.Get(ctx, &vpcv1.GetSubnetRequest{SubnetId: id}, opts...)
}

// ListSubnets — sync list по project/page.
func (c *Client) ListSubnets(ctx context.Context, req *vpcv1.ListSubnetsRequest, opts ...grpc.CallOption) (*vpcv1.ListSubnetsResponse, error) {
	return c.Subnets.List(ctx, req, opts...)
}

// CreateSubnet — async create; возвращает Operation.
func (c *Client) CreateSubnet(ctx context.Context, req *vpcv1.CreateSubnetRequest, opts ...grpc.CallOption) (*operationv1.Operation, error) {
	return c.Subnets.Create(ctx, req, opts...)
}

// UpdateSubnet — async update. network_id и zone_id immutable после Create.
func (c *Client) UpdateSubnet(ctx context.Context, req *vpcv1.UpdateSubnetRequest, opts ...grpc.CallOption) (*operationv1.Operation, error) {
	return c.Subnets.Update(ctx, req, opts...)
}

// DeleteSubnet — async hard-delete. FK RESTRICT к Address/NIC: сначала удалить
// NIC и Address.
func (c *Client) DeleteSubnet(ctx context.Context, id string, opts ...grpc.CallOption) (*operationv1.Operation, error) {
	return c.Subnets.Delete(ctx, &vpcv1.DeleteSubnetRequest{SubnetId: id}, opts...)
}

// AddSubnetCidrBlocks — async :add-cidr-blocks; реальное изменение CIDR подсети
// (CIDR-поля в Update — no-op по soft-immutable правилу, меняются только здесь).
func (c *Client) AddSubnetCidrBlocks(ctx context.Context, req *vpcv1.AddSubnetCidrBlocksRequest, opts ...grpc.CallOption) (*operationv1.Operation, error) {
	return c.Subnets.AddCidrBlocks(ctx, req, opts...)
}

// RemoveSubnetCidrBlocks — async :remove-cidr-blocks.
func (c *Client) RemoveSubnetCidrBlocks(ctx context.Context, req *vpcv1.RemoveSubnetCidrBlocksRequest, opts ...grpc.CallOption) (*operationv1.Operation, error) {
	return c.Subnets.RemoveCidrBlocks(ctx, req, opts...)
}
