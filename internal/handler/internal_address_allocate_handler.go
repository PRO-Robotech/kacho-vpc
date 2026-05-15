// Package handler — internal_address_allocate_handler.go реализует
// kacho.cloud.vpc.v1.InternalAddressService:
//   - AllocateInternalIP — atomic IPAM allocation для internal IP в subnet.
//   - AllocateExternalIP — atomic allocation из cascade-резолвленного pool.
//   - SetAddressReference / ClearAddressReference / GetAddressReference —
//     referrer-tracking (кто использует адрес; YC-like). Idempotent set,
//     no-op clear, NotFound get.
//
// Wave 3 (KAC-94): AddressService разобран на use-case-структуру в
// `internal/apps/kacho/api/address/`. Allocate-методы теперь — отдельный
// `address.AllocateUseCase`; Reference-методы — отдельный
// `addressref.Service`. Handler инжектирует оба и проксирует
// proto-запросы в их методы.
//
// Composition root (cmd/vpc/main.go) обновляется в той же ветке, что и
// этот пакет — там собирается AllocateUseCase + AddressReferenceService и
// передаётся в `NewInternalAddressAllocateHandler`. Импорт addressapp
// должен идти ЧЕРЕЗ cmd/, чтобы избежать import-cycle с use-case-пакетом
// (который импортирует `internal/handler` для AssertFolderOwnership).
//
// Для разрыва циклической зависимости handler не импортирует
// `internal/apps/kacho/api/address` напрямую — вместо этого определена
// узкая port-абстракция `AddressAllocator`, которой удовлетворяет
// `*address.AllocateUseCase`. Та же стратегия применена к
// `AddressReferenceService` (этот в `internal/apps/kacho/services/addressref` —
// нет цикла).
//
// Legacy SetInternalIP RPC удалён.
package handler

import (
	"context"
	"errors"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/services/addressref"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/ports"
)

// AddressAllocator — port для allocate-методов; реализуется
// `*addressapp.AllocateUseCase` в composition root (cmd/vpc/main.go).
// Возвращает `*domain.AllocateResult` — общий тип, живущий в domain leaf,
// чтобы избежать import-cycle между `internal/handler` и
// `internal/apps/kacho/api/address` (тот импортирует `internal/handler`
// для AssertFolderOwnership).
type AddressAllocator interface {
	AllocateInternalIP(ctx context.Context, addressID string) (*domain.AllocateResult, error)
	AllocateInternalIPv6(ctx context.Context, addressID string) (*domain.AllocateResult, error)
	AllocateExternalIP(ctx context.Context, addressID string) (*domain.AllocateResult, error)
}

// AddressReferenceManager — port для referrer-tracking; реализуется
// `*addressref.Service`.
type AddressReferenceManager interface {
	SetAddressReference(ctx context.Context, req addressref.SetAddressReferenceReq) (*domain.AddressReference, error)
	MarkAddressEphemeralInUse(ctx context.Context, req addressref.SetAddressReferenceReq) (*domain.AddressReference, error)
	ClearAddressReference(ctx context.Context, addressID string) error
	GetAddressReference(ctx context.Context, addressID string) (*domain.AddressReference, error)
}

// InternalAddressAllocateHandler — InternalAddressService implementation.
type InternalAddressAllocateHandler struct {
	vpcv1.UnimplementedInternalAddressServiceServer
	allocate AddressAllocator
	refs     AddressReferenceManager
}

// NewInternalAddressAllocateHandler собирает handler из двух port'ов —
// composition root (cmd/vpc/main.go) передаёт `*addressapp.AllocateUseCase`
// и `*addressref.Service`.
func NewInternalAddressAllocateHandler(allocate AddressAllocator, refs AddressReferenceManager) *InternalAddressAllocateHandler {
	return &InternalAddressAllocateHandler{allocate: allocate, refs: refs}
}

func (h *InternalAddressAllocateHandler) AllocateInternalIP(ctx context.Context, req *vpcv1.AllocateInternalIPRequest) (*vpcv1.AllocateIPResponse, error) {
	if req.GetAddressId() == "" {
		return nil, status.Error(codes.InvalidArgument, "address_id required")
	}
	res, err := h.allocate.AllocateInternalIP(ctx, req.GetAddressId())
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
	res, err := h.allocate.AllocateInternalIPv6(ctx, req.GetAddressId())
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
	res, err := h.allocate.AllocateExternalIP(ctx, req.GetAddressId())
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
	ref, err := h.refs.SetAddressReference(ctx, addressref.SetAddressReferenceReq{
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
	if err := h.refs.ClearAddressReference(ctx, req.GetAddressId()); err != nil {
		return nil, err
	}
	return &vpcv1.ClearAddressReferenceResponse{}, nil
}

func (h *InternalAddressAllocateHandler) GetAddressReference(ctx context.Context, req *vpcv1.GetAddressReferenceRequest) (*vpcv1.AddressReference, error) {
	if req.GetAddressId() == "" {
		return nil, status.Error(codes.InvalidArgument, "address_id required")
	}
	ref, err := h.refs.GetAddressReference(ctx, req.GetAddressId())
	if err != nil {
		return nil, err
	}
	return addressReferenceToProto(ref), nil
}

func (h *InternalAddressAllocateHandler) MarkAddressEphemeralInUse(ctx context.Context, req *vpcv1.MarkAddressEphemeralInUseRequest) (*vpcv1.MarkAddressEphemeralInUseResponse, error) {
	if req.GetAddressId() == "" {
		return nil, status.Error(codes.InvalidArgument, "address_id required")
	}
	if _, err := h.refs.MarkAddressEphemeralInUse(ctx, addressref.SetAddressReferenceReq{
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
	if errors.Is(err, ports.ErrNotFound) {
		return status.Error(codes.NotFound, err.Error())
	}
	if st, ok := status.FromError(err); ok && st.Code() != codes.Unknown {
		return err
	}
	return status.Error(codes.Internal, "internal allocator error")
}
