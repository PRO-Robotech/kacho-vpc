package vpc

import (
	"context"

	"google.golang.org/grpc"

	operationv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/operation"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
)

// GatewayServiceClient — alias на сгенерированный gRPC-клиент.
type GatewayServiceClient = vpcv1.GatewayServiceClient

// GetGateway — sync read.
func (c *Client) GetGateway(ctx context.Context, id string, opts ...grpc.CallOption) (*vpcv1.Gateway, error) {
	return c.Gateways.Get(ctx, &vpcv1.GetGatewayRequest{GatewayId: id}, opts...)
}

// ListGateways — sync list.
func (c *Client) ListGateways(ctx context.Context, req *vpcv1.ListGatewaysRequest, opts ...grpc.CallOption) (*vpcv1.ListGatewaysResponse, error) {
	return c.Gateways.List(ctx, req, opts...)
}

// CreateGateway — async create.
func (c *Client) CreateGateway(ctx context.Context, req *vpcv1.CreateGatewayRequest, opts ...grpc.CallOption) (*operationv1.Operation, error) {
	return c.Gateways.Create(ctx, req, opts...)
}

// UpdateGateway — async update. NameGateway — strict-контракт (lowercase, без uppercase/underscore).
func (c *Client) UpdateGateway(ctx context.Context, req *vpcv1.UpdateGatewayRequest, opts ...grpc.CallOption) (*operationv1.Operation, error) {
	return c.Gateways.Update(ctx, req, opts...)
}

// DeleteGateway — async hard-delete.
func (c *Client) DeleteGateway(ctx context.Context, id string, opts ...grpc.CallOption) (*operationv1.Operation, error) {
	return c.Gateways.Delete(ctx, &vpcv1.DeleteGatewayRequest{GatewayId: id}, opts...)
}

// MoveGateway — async folder-перемещение.
func (c *Client) MoveGateway(ctx context.Context, req *vpcv1.MoveGatewayRequest, opts ...grpc.CallOption) (*operationv1.Operation, error) {
	return c.Gateways.Move(ctx, req, opts...)
}
