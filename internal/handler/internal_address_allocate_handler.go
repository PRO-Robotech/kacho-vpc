// Package handler — internal_address_allocate_handler.go реализует
// kacho.cloud.vpc.v1.InternalAddressService:
//   - AllocateInternalIP — atomic IPAM allocation для internal IP в subnet.
//   - AllocateExternalIP — atomic allocation из cascade-резолвленного pool.
//   - SetAddressReference / ClearAddressReference / GetAddressReference —
//     referrer-tracking (кто использует адрес; YC-like). Idempotent set,
//     no-op clear, NotFound get.
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
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/service"
)

// InternalAddressAllocateHandler — InternalAddressService implementation.
type InternalAddressAllocateHandler struct {
	vpcv1.UnimplementedInternalAddressServiceServer
	addressSvc *service.AddressService
}

func NewInternalAddressAllocateHandler(s *service.AddressService) *InternalAddressAllocateHandler {
	return &InternalAddressAllocateHandler{addressSvc: s}
}

func (h *InternalAddressAllocateHandler) AllocateInternalIP(ctx context.Context, req *vpcv1.AllocateInternalIPRequest) (*vpcv1.AllocateIPResponse, error) {
	if req.GetAddressId() == "" {
		return nil, status.Error(codes.InvalidArgument, "address_id required")
	}
	res, err := h.addressSvc.AllocateInternalIP(ctx, req.GetAddressId())
	if err != nil {
		return nil, mapAllocErr(err)
	}
	return &vpcv1.AllocateIPResponse{
		Ip:               res.IP,
		AlreadyAllocated: res.AlreadyAllocated,
	}, nil
}

func (h *InternalAddressAllocateHandler) AllocateInternalIPv6(ctx context.Context, req *vpcv1.AllocateInternalIPRequest) (*vpcv1.AllocateIPResponse, error) {
	if req.GetAddressId() == "" {
		return nil, status.Error(codes.InvalidArgument, "address_id required")
	}
	res, err := h.addressSvc.AllocateInternalIPv6(ctx, req.GetAddressId())
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
	res, err := h.addressSvc.AllocateExternalIP(ctx, req.GetAddressId())
	if err != nil {
		return nil, mapAllocErr(err)
	}
	return &vpcv1.AllocateIPResponse{
		Ip:               res.IP,
		PoolId:           res.PoolID,
		AlreadyAllocated: res.AlreadyAllocated,
	}, nil
}

func (h *InternalAddressAllocateHandler) SetAddressReference(ctx context.Context, req *vpcv1.SetAddressReferenceRequest) (*vpcv1.AddressReference, error) {
	if req.GetAddressId() == "" {
		return nil, status.Error(codes.InvalidArgument, "address_id required")
	}
	ref, err := h.addressSvc.SetAddressReference(ctx, service.SetAddressReferenceReq{
		AddressID:    req.GetAddressId(),
		ReferrerType: req.GetReferrerType(),
		ReferrerID:   req.GetReferrerId(),
		ReferrerName: req.GetReferrerName(),
	})
	if err != nil {
		return nil, err
	}
	return addressReferenceToProto(ref), nil
}

func (h *InternalAddressAllocateHandler) ClearAddressReference(ctx context.Context, req *vpcv1.ClearAddressReferenceRequest) (*vpcv1.ClearAddressReferenceResponse, error) {
	if req.GetAddressId() == "" {
		return nil, status.Error(codes.InvalidArgument, "address_id required")
	}
	if err := h.addressSvc.ClearAddressReference(ctx, req.GetAddressId()); err != nil {
		return nil, err
	}
	return &vpcv1.ClearAddressReferenceResponse{}, nil
}

func (h *InternalAddressAllocateHandler) GetAddressReference(ctx context.Context, req *vpcv1.GetAddressReferenceRequest) (*vpcv1.AddressReference, error) {
	if req.GetAddressId() == "" {
		return nil, status.Error(codes.InvalidArgument, "address_id required")
	}
	ref, err := h.addressSvc.GetAddressReference(ctx, req.GetAddressId())
	if err != nil {
		return nil, err
	}
	return addressReferenceToProto(ref), nil
}

func (h *InternalAddressAllocateHandler) MarkAddressEphemeralInUse(ctx context.Context, req *vpcv1.MarkAddressEphemeralInUseRequest) (*vpcv1.MarkAddressEphemeralInUseResponse, error) {
	if req.GetAddressId() == "" {
		return nil, status.Error(codes.InvalidArgument, "address_id required")
	}
	if _, err := h.addressSvc.MarkAddressEphemeralInUse(ctx, service.SetAddressReferenceReq{
		AddressID:    req.GetAddressId(),
		ReferrerType: req.GetReferrerType(),
		ReferrerID:   req.GetReferrerId(),
		ReferrerName: req.GetReferrerName(),
	}); err != nil {
		return nil, err
	}
	return &vpcv1.MarkAddressEphemeralInUseResponse{}, nil
}

func addressReferenceToProto(r *domain.AddressReference) *vpcv1.AddressReference {
	if r == nil {
		return nil
	}
	return &vpcv1.AddressReference{
		AddressId:    r.AddressID,
		ReferrerType: r.ReferrerType,
		ReferrerId:   r.ReferrerID,
		ReferrerName: r.ReferrerName,
		AttachedAt:   timestamppb.New(r.AttachedAt.Truncate(time.Second)),
	}
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
