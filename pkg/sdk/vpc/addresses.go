package vpc

import (
	"context"

	"google.golang.org/grpc"

	operationv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/operation"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
)

// AddressServiceClient — alias на сгенерированный gRPC-клиент.
type AddressServiceClient = vpcv1.AddressServiceClient

// GetAddress — sync read.
func (c *Client) GetAddress(ctx context.Context, id string, opts ...grpc.CallOption) (*vpcv1.Address, error) {
	return c.Addresses.Get(ctx, &vpcv1.GetAddressRequest{AddressId: id}, opts...)
}

// GetAddressByValue — sync lookup по литералу IP (для cross-service ref-validation).
func (c *Client) GetAddressByValue(ctx context.Context, req *vpcv1.GetAddressByValueRequest, opts ...grpc.CallOption) (*vpcv1.Address, error) {
	return c.Addresses.GetByValue(ctx, req, opts...)
}

// ListAddresses — sync list по folder/page.
func (c *Client) ListAddresses(ctx context.Context, req *vpcv1.ListAddressesRequest, opts ...grpc.CallOption) (*vpcv1.ListAddressesResponse, error) {
	return c.Addresses.List(ctx, req, opts...)
}

// ListAddressesBySubnet — sync list internal-адресов одной подсети.
func (c *Client) ListAddressesBySubnet(ctx context.Context, req *vpcv1.ListAddressesBySubnetRequest, opts ...grpc.CallOption) (*vpcv1.ListAddressesBySubnetResponse, error) {
	return c.Addresses.ListBySubnet(ctx, req, opts...)
}

// CreateAddress — async; IPAM allocate происходит inline в worker (см. CLAUDE.md §16).
func (c *Client) CreateAddress(ctx context.Context, req *vpcv1.CreateAddressRequest, opts ...grpc.CallOption) (*operationv1.Operation, error) {
	return c.Addresses.Create(ctx, req, opts...)
}

// UpdateAddress — async. external_ipv4_spec / internal_ipv4_spec / folder_id immutable.
func (c *Client) UpdateAddress(ctx context.Context, req *vpcv1.UpdateAddressRequest, opts ...grpc.CallOption) (*operationv1.Operation, error) {
	return c.Addresses.Update(ctx, req, opts...)
}

// DeleteAddress — async hard-delete. Address в использовании у NIC нельзя удалить
// (FailedPrecondition, KAC-31) — сначала detach NIC.
func (c *Client) DeleteAddress(ctx context.Context, id string, opts ...grpc.CallOption) (*operationv1.Operation, error) {
	return c.Addresses.Delete(ctx, &vpcv1.DeleteAddressRequest{AddressId: id}, opts...)
}

// MoveAddress — async folder-перемещение.
func (c *Client) MoveAddress(ctx context.Context, req *vpcv1.MoveAddressRequest, opts ...grpc.CallOption) (*operationv1.Operation, error) {
	return c.Addresses.Move(ctx, req, opts...)
}
