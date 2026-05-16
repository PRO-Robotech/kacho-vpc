package vpc

import (
	"context"

	"google.golang.org/grpc"

	operationv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/operation"
	privatelinkv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1/privatelink"
)

// PrivateEndpointServiceClient — alias на сгенерированный gRPC-клиент.
// Лежит в proto-подпакете privatelink/ (как и в YC privatelink API).
type PrivateEndpointServiceClient = privatelinkv1.PrivateEndpointServiceClient

// GetPrivateEndpoint — sync read.
func (c *Client) GetPrivateEndpoint(ctx context.Context, id string, opts ...grpc.CallOption) (*privatelinkv1.PrivateEndpoint, error) {
	return c.PrivateEndpoints.Get(ctx, &privatelinkv1.GetPrivateEndpointRequest{PrivateEndpointId: id}, opts...)
}

// ListPrivateEndpoints — sync list.
func (c *Client) ListPrivateEndpoints(ctx context.Context, req *privatelinkv1.ListPrivateEndpointsRequest, opts ...grpc.CallOption) (*privatelinkv1.ListPrivateEndpointsResponse, error) {
	return c.PrivateEndpoints.List(ctx, req, opts...)
}

// CreatePrivateEndpoint — async create.
func (c *Client) CreatePrivateEndpoint(ctx context.Context, req *privatelinkv1.CreatePrivateEndpointRequest, opts ...grpc.CallOption) (*operationv1.Operation, error) {
	return c.PrivateEndpoints.Create(ctx, req, opts...)
}

// UpdatePrivateEndpoint — async update.
func (c *Client) UpdatePrivateEndpoint(ctx context.Context, req *privatelinkv1.UpdatePrivateEndpointRequest, opts ...grpc.CallOption) (*operationv1.Operation, error) {
	return c.PrivateEndpoints.Update(ctx, req, opts...)
}

// DeletePrivateEndpoint — async hard-delete.
func (c *Client) DeletePrivateEndpoint(ctx context.Context, id string, opts ...grpc.CallOption) (*operationv1.Operation, error) {
	return c.PrivateEndpoints.Delete(ctx, &privatelinkv1.DeletePrivateEndpointRequest{PrivateEndpointId: id}, opts...)
}
