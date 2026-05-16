package vpc

import (
	"context"

	"google.golang.org/grpc"

	operationv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/operation"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
)

// NetworkInterfaceServiceClient — alias на сгенерированный gRPC-клиент.
// NetworkInterface (NIC) — first-class AWS-ENI-подобный ресурс kacho-vpc
// (вариант А эпика KAC-2): живёт в kacho-vpc, не в Compute.
type NetworkInterfaceServiceClient = vpcv1.NetworkInterfaceServiceClient

// GetNetworkInterface — sync read.
func (c *Client) GetNetworkInterface(ctx context.Context, id string, opts ...grpc.CallOption) (*vpcv1.NetworkInterface, error) {
	return c.NetworkInterfaces.Get(ctx, &vpcv1.GetNetworkInterfaceRequest{NetworkInterfaceId: id}, opts...)
}

// ListNetworkInterfaces — sync list по folder/page.
func (c *Client) ListNetworkInterfaces(ctx context.Context, req *vpcv1.ListNetworkInterfacesRequest, opts ...grpc.CallOption) (*vpcv1.ListNetworkInterfacesResponse, error) {
	return c.NetworkInterfaces.List(ctx, req, opts...)
}

// CreateNetworkInterface — async create. mac_address аллоцируется сервером
// (нельзя задать клиенту — AWS-ENI semantics, KAC-48).
func (c *Client) CreateNetworkInterface(ctx context.Context, req *vpcv1.CreateNetworkInterfaceRequest, opts ...grpc.CallOption) (*operationv1.Operation, error) {
	return c.NetworkInterfaces.Create(ctx, req, opts...)
}

// UpdateNetworkInterface — async update. На NIC ≤ 1 v4 + ≤ 1 v6 (KAC-55).
func (c *Client) UpdateNetworkInterface(ctx context.Context, req *vpcv1.UpdateNetworkInterfaceRequest, opts ...grpc.CallOption) (*operationv1.Operation, error) {
	return c.NetworkInterfaces.Update(ctx, req, opts...)
}

// DeleteNetworkInterface — async hard-delete. NIC блокирует свою подсеть
// (FK RESTRICT) — удалять снизу вверх: NIC → Address → Subnet → Network.
func (c *Client) DeleteNetworkInterface(ctx context.Context, id string, opts ...grpc.CallOption) (*operationv1.Operation, error) {
	return c.NetworkInterfaces.Delete(ctx, &vpcv1.DeleteNetworkInterfaceRequest{NetworkInterfaceId: id}, opts...)
}

// AttachNetworkInterfaceToInstance — async; выставляет used_by=
// {compute_instance, <instance_id>} (CAS, без race-window).
func (c *Client) AttachNetworkInterfaceToInstance(ctx context.Context, req *vpcv1.AttachNetworkInterfaceRequest, opts ...grpc.CallOption) (*operationv1.Operation, error) {
	return c.NetworkInterfaces.AttachToInstance(ctx, req, opts...)
}

// DetachNetworkInterfaceFromInstance — async; очищает used_by.
func (c *Client) DetachNetworkInterfaceFromInstance(ctx context.Context, req *vpcv1.DetachNetworkInterfaceRequest, opts ...grpc.CallOption) (*operationv1.Operation, error) {
	return c.NetworkInterfaces.DetachFromInstance(ctx, req, opts...)
}
