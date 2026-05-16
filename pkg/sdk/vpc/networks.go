package vpc

import (
	"context"

	"google.golang.org/grpc"

	operationv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/operation"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
)

// NetworkServiceClient — alias на сгенерированный gRPC-клиент. Дублирующий тип
// нужен только для документации: интеграторы видят «vpc.NetworkServiceClient»
// в годокке SDK, а не «vpcv1.NetworkServiceClient» из соседнего модуля.
type NetworkServiceClient = vpcv1.NetworkServiceClient

// Сокращения над c.Networks — для удобства и читаемости в integrator-коде:
// `c.GetNetwork(ctx, id)` короче, чем `c.Networks.Get(ctx, &vpcv1.GetNetworkRequest{NetworkId: id})`.

// GetNetwork — sync read одной сети по id.
func (c *Client) GetNetwork(ctx context.Context, id string, opts ...grpc.CallOption) (*vpcv1.Network, error) {
	return c.Networks.Get(ctx, &vpcv1.GetNetworkRequest{NetworkId: id}, opts...)
}

// ListNetworks — sync list (см. proto для page_size/page_token/filter).
func (c *Client) ListNetworks(ctx context.Context, req *vpcv1.ListNetworksRequest, opts ...grpc.CallOption) (*vpcv1.ListNetworksResponse, error) {
	return c.Networks.List(ctx, req, opts...)
}

// CreateNetwork — async create; возвращает long-running Operation. Опрашивать
// до завершения через Client.WaitForOperation.
func (c *Client) CreateNetwork(ctx context.Context, req *vpcv1.CreateNetworkRequest, opts ...grpc.CallOption) (*operationv1.Operation, error) {
	return c.Networks.Create(ctx, req, opts...)
}

// UpdateNetwork — async update (см. UpdateMask discipline в VPC CLAUDE.md §4.4).
func (c *Client) UpdateNetwork(ctx context.Context, req *vpcv1.UpdateNetworkRequest, opts ...grpc.CallOption) (*operationv1.Operation, error) {
	return c.Networks.Update(ctx, req, opts...)
}

// DeleteNetwork — async hard-delete. RESTRICT-FK к Subnet/SG/RT/PE: удалить
// можно только пустую сеть (см. workspace-CLAUDE.md «within-service refs»).
func (c *Client) DeleteNetwork(ctx context.Context, id string, opts ...grpc.CallOption) (*operationv1.Operation, error) {
	return c.Networks.Delete(ctx, &vpcv1.DeleteNetworkRequest{NetworkId: id}, opts...)
}

// MoveNetwork — async folder-перемещение (`folder_id` immutable, мутация только
// через :move; verbatim YC).
func (c *Client) MoveNetwork(ctx context.Context, req *vpcv1.MoveNetworkRequest, opts ...grpc.CallOption) (*operationv1.Operation, error) {
	return c.Networks.Move(ctx, req, opts...)
}
