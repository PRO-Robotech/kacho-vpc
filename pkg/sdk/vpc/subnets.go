package vpc

import (
	"context"

	"google.golang.org/grpc"

	operationv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/operation"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
)

// SubnetServiceClient — alias на сгенерированный gRPC-клиент (см. networks.go).
type SubnetServiceClient = vpcv1.SubnetServiceClient

// GetSubnet — sync read одной подсети по id.
func (c *Client) GetSubnet(ctx context.Context, id string, opts ...grpc.CallOption) (*vpcv1.Subnet, error) {
	return c.Subnets.Get(ctx, &vpcv1.GetSubnetRequest{SubnetId: id}, opts...)
}

// ListSubnets — sync list по folder/page.
func (c *Client) ListSubnets(ctx context.Context, req *vpcv1.ListSubnetsRequest, opts ...grpc.CallOption) (*vpcv1.ListSubnetsResponse, error) {
	return c.Subnets.List(ctx, req, opts...)
}

// CreateSubnet — async create; возвращает Operation.
func (c *Client) CreateSubnet(ctx context.Context, req *vpcv1.CreateSubnetRequest, opts ...grpc.CallOption) (*operationv1.Operation, error) {
	return c.Subnets.Create(ctx, req, opts...)
}

// UpdateSubnet — async update. network_id и zone_id immutable (см. CLAUDE.md §4.4).
func (c *Client) UpdateSubnet(ctx context.Context, req *vpcv1.UpdateSubnetRequest, opts ...grpc.CallOption) (*operationv1.Operation, error) {
	return c.Subnets.Update(ctx, req, opts...)
}

// DeleteSubnet — async hard-delete. FK RESTRICT к Address/NIC: сначала удалить
// NIC и Address (см. CLAUDE.md §2.1 порядок удаления).
func (c *Client) DeleteSubnet(ctx context.Context, id string, opts ...grpc.CallOption) (*operationv1.Operation, error) {
	return c.Subnets.Delete(ctx, &vpcv1.DeleteSubnetRequest{SubnetId: id}, opts...)
}

// MoveSubnet — async folder-перемещение.
func (c *Client) MoveSubnet(ctx context.Context, req *vpcv1.MoveSubnetRequest, opts ...grpc.CallOption) (*operationv1.Operation, error) {
	return c.Subnets.Move(ctx, req, opts...)
}

// AddSubnetCidrBlocks — async :add-cidr-blocks; реальное изменение CIDR подсети
// (Update со старым CIDR — no-op по soft-immutable правилу, см. §4.4).
func (c *Client) AddSubnetCidrBlocks(ctx context.Context, req *vpcv1.AddSubnetCidrBlocksRequest, opts ...grpc.CallOption) (*operationv1.Operation, error) {
	return c.Subnets.AddCidrBlocks(ctx, req, opts...)
}

// RemoveSubnetCidrBlocks — async :remove-cidr-blocks.
func (c *Client) RemoveSubnetCidrBlocks(ctx context.Context, req *vpcv1.RemoveSubnetCidrBlocksRequest, opts ...grpc.CallOption) (*operationv1.Operation, error) {
	return c.Subnets.RemoveCidrBlocks(ctx, req, opts...)
}

// RelocateSubnet — async :relocate. У kacho-vpc всегда отвергается синхронно
// (FailedPrecondition "Invalid subnet state", verbatim YC) — см. CLAUDE.md §8.4.
func (c *Client) RelocateSubnet(ctx context.Context, req *vpcv1.RelocateSubnetRequest, opts ...grpc.CallOption) (*operationv1.Operation, error) {
	return c.Subnets.Relocate(ctx, req, opts...)
}
