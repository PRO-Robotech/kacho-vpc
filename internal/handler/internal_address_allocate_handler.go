// Package handler — internal_address_allocate_handler.go реализует
// расширения kacho.cloud.vpc.v1.InternalAddressService:
//   - AllocateInternalIP — atomic IPAM allocation для internal IP в subnet.
//   - AllocateExternalIP — atomic allocation из cascade-резолвленного pool.
//
// Старый SetInternalIP остаётся в internal_address_handler.go. Этот файл
// разделяет concerns между legacy direct-update (SetInternalIP) и новой
// IPAM-allocation (Allocate*).
package handler

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	"github.com/PRO-Robotech/kacho-vpc/internal/service"
)

// InternalAddressAllocateHandler — расширения InternalAddressService.
// SetInternalIP остаётся в InternalAddressHandler (legacy). Новые методы
// AllocateInternal/External — здесь, через service-layer (AddressAllocator).
type InternalAddressAllocateHandler struct {
	allocator *service.AddressAllocator
}

func NewInternalAddressAllocateHandler(a *service.AddressAllocator) *InternalAddressAllocateHandler {
	return &InternalAddressAllocateHandler{allocator: a}
}

func (h *InternalAddressAllocateHandler) AllocateInternalIP(ctx context.Context, req *vpcv1.AllocateInternalIPRequest) (*vpcv1.AllocateIPResponse, error) {
	if req.GetAddressId() == "" {
		return nil, status.Error(codes.InvalidArgument, "address_id required")
	}
	res, err := h.allocator.AllocateInternalIP(ctx, req.GetAddressId())
	if err != nil {
		return nil, mapAllocErr(err)
	}
	return &vpcv1.AllocateIPResponse{
		Ip:               res.IP,
		AlreadyAllocated: res.AlreadyAllocated,
	}, nil
}

func (h *InternalAddressAllocateHandler) AllocateExternalIP(ctx context.Context, req *vpcv1.AllocateExternalIPRequest) (*vpcv1.AllocateIPResponse, error) {
	if req.GetAddressId() == "" {
		return nil, status.Error(codes.InvalidArgument, "address_id required")
	}
	res, err := h.allocator.AllocateExternalIP(ctx, req.GetAddressId())
	if err != nil {
		return nil, mapAllocErr(err)
	}
	return &vpcv1.AllocateIPResponse{
		Ip:               res.IP,
		PoolId:           res.PoolID,
		AlreadyAllocated: res.AlreadyAllocated,
	}, nil
}

func mapAllocErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, service.ErrNotFound) {
		return status.Error(codes.NotFound, err.Error())
	}
	// allocator already returns gRPC status errors for FailedPrecondition /
	// ResourceExhausted; pass through.
	if _, ok := status.FromError(err); ok {
		return err
	}
	return status.Error(codes.Internal, err.Error())
}
