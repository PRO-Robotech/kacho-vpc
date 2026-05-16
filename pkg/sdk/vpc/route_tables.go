package vpc

import (
	"context"

	"google.golang.org/grpc"

	operationv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/operation"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
)

// RouteTableServiceClient — alias на сгенерированный gRPC-клиент.
type RouteTableServiceClient = vpcv1.RouteTableServiceClient

// GetRouteTable — sync read.
func (c *Client) GetRouteTable(ctx context.Context, id string, opts ...grpc.CallOption) (*vpcv1.RouteTable, error) {
	return c.RouteTables.Get(ctx, &vpcv1.GetRouteTableRequest{RouteTableId: id}, opts...)
}

// ListRouteTables — sync list.
func (c *Client) ListRouteTables(ctx context.Context, req *vpcv1.ListRouteTablesRequest, opts ...grpc.CallOption) (*vpcv1.ListRouteTablesResponse, error) {
	return c.RouteTables.List(ctx, req, opts...)
}

// CreateRouteTable — async create.
func (c *Client) CreateRouteTable(ctx context.Context, req *vpcv1.CreateRouteTableRequest, opts ...grpc.CallOption) (*operationv1.Operation, error) {
	return c.RouteTables.Create(ctx, req, opts...)
}

// UpdateRouteTable — async update.
func (c *Client) UpdateRouteTable(ctx context.Context, req *vpcv1.UpdateRouteTableRequest, opts ...grpc.CallOption) (*operationv1.Operation, error) {
	return c.RouteTables.Update(ctx, req, opts...)
}

// DeleteRouteTable — async hard-delete. Subnet.route_table_id обнуляется
// автоматически (ON DELETE SET NULL + триггер `vpc_outbox` UPDATED, KAC-56).
func (c *Client) DeleteRouteTable(ctx context.Context, id string, opts ...grpc.CallOption) (*operationv1.Operation, error) {
	return c.RouteTables.Delete(ctx, &vpcv1.DeleteRouteTableRequest{RouteTableId: id}, opts...)
}

// MoveRouteTable — async folder-перемещение.
func (c *Client) MoveRouteTable(ctx context.Context, req *vpcv1.MoveRouteTableRequest, opts ...grpc.CallOption) (*operationv1.Operation, error) {
	return c.RouteTables.Move(ctx, req, opts...)
}
