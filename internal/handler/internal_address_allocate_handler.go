// Package handler — internal_address_allocate_handler.go реализует
// kacho.cloud.vpc.v1.InternalAddressService:
//   - AllocateInternalIP — atomic IPAM allocation для internal IP в subnet.
//   - AllocateExternalIP — atomic allocation из cascade-резолвленного pool.
//
// Legacy SetInternalIP RPC удалён (см. удалённые internal_address_handler.go
// + internal_address_composite_handler.go). Если старый stub'нутый proto
// generated code всё ещё содержит метод — он автоматически возвращает
// codes.Unimplemented через UnimplementedInternalAddressServiceServer
// embedding.
package handler

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	"github.com/PRO-Robotech/kacho-vpc/internal/service"
)

// InternalAddressAllocateHandler — InternalAddressService implementation.
type InternalAddressAllocateHandler struct {
	vpcv1.UnimplementedInternalAddressServiceServer
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
	// ResourceExhausted; pass through (status.FromError возвращает (status, true)
	// и для не-status err с Unknown code — поэтому проверяем code != Unknown).
	if st, ok := status.FromError(err); ok && st.Code() != codes.Unknown {
		return err
	}
	// Defensive: не leak'аем raw err.Error() (может содержать pgx-text с
	// hostname/db/query). Generic Internal без leak'а — info-leak vector
	// закрыт симметрично mapRepoErr.
	return status.Error(codes.Internal, "internal allocator error")
}
