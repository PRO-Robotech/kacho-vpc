// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

// Package handler — internal_address_allocate_handler.go реализует
// kacho.cloud.vpc.v1.InternalAddressService:
//   - AllocateInternalIP — atomic IPAM allocation для internal IP в subnet.
//   - AllocateExternalIP — atomic allocation из cascade-резолвленного pool.
//   - SetAddressReference / ClearAddressReference / GetAddressReference —
//     referrer-tracking (кто использует адрес). Idempotent set, no-op clear,
//     NotFound get.
//
// Allocate-методы реализует `address.AllocateUseCase`, reference-методы —
// `addressref.Service`. Handler инжектирует оба и проксирует proto-запросы в
// их методы; оба собираются в composition root (cmd/vpc/main.go) и передаются
// в `NewInternalAddressAllocateHandler`.
//
// По dependency-rule (architecture.md) transport-слой не импортирует use-case-
// конкреты: handler НЕ тянет пакет `internal/apps/kacho/api/address` напрямую, а
// определяет узкие port-абстракции `AddressAllocator` (ее удовлетворяет
// `*address.AllocateUseCase`) и `AddressReferenceManager`, которые связываются в
// composition root. (AssertProjectOwnership живет в `internal/tenant`; use-case
// address `internal/handler` не импортирует.)
//
// AuthZ: per-RPC FGA-Check (object-scoped на `vpc_address:<address_id>`,
// v_update для мутаций / v_get для чтения referrer'а) выполняет authz-interceptor
// на internal listener'е :9091 — см. check.PermissionMap. Handler сам
// авторизацию НЕ делает и НЕ дублирует.
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
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
)

// AddressAllocator — port для allocate-методов; реализуется
// `*address.AllocateUseCase` в composition root (cmd/vpc/main.go).
// Возвращает `*domain.AllocateResult` — общий тип в domain leaf, чтобы и этот
// port, и use-case address ссылались на него, не импортируя пакеты друг друга
// (transport ↔ use-case развязаны через domain-тип и port-абстракцию).
type AddressAllocator interface {
	AllocateInternalIP(ctx context.Context, addressID string) (*domain.AllocateResult, error)
	AllocateInternalIPv6(ctx context.Context, addressID string) (*domain.AllocateResult, error)
	AllocateExternalIP(ctx context.Context, addressID string) (*domain.AllocateResult, error)
	AllocateExternalIPv6(ctx context.Context, addressID string) (*domain.AllocateResult, error)
}

// AddressReferenceManager — port для referrer-tracking; реализуется
// `*addressref.Service`.
type AddressReferenceManager interface {
	SetAddressReference(ctx context.Context, req addressref.SetAddressReferenceReq) (*domain.AddressReference, error)
	MarkAddressEphemeralInUse(ctx context.Context, req addressref.SetAddressReferenceReq) (*domain.AddressReference, error)
	ClearAddressReference(ctx context.Context, addressID string) error
	GetAddressReference(ctx context.Context, addressID string) (*domain.AddressReference, error)
}

// InternalAddressAllocateHandler — реализация InternalAddressService.
type InternalAddressAllocateHandler struct {
	vpcv1.UnimplementedInternalAddressServiceServer
	allocate AddressAllocator
	refs     AddressReferenceManager
}

// NewInternalAddressAllocateHandler собирает handler из двух port'ов —
// composition root (cmd/vpc/main.go) передает `*address.AllocateUseCase`
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

func (h *InternalAddressAllocateHandler) AllocateExternalIPv6(ctx context.Context, req *vpcv1.AllocateExternalIPRequest) (*vpcv1.AllocateIPResponse, error) {
	if req.GetAddressId() == "" {
		return nil, status.Error(codes.InvalidArgument, "address_id required")
	}
	res, err := h.allocate.AllocateExternalIPv6(ctx, req.GetAddressId())
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
		Owned:        req.GetOwned(),
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
		Owned:        r.Owned,
		AttachedAt:   timestamppb.New(r.AttachedAt.Truncate(time.Second)),
	}
}

func mapAllocErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, repo.ErrNotFound) {
		return status.Error(codes.NotFound, err.Error())
	}
	if st, ok := status.FromError(err); ok && st.Code() != codes.Unknown {
		return err
	}
	return status.Error(codes.Internal, "internal allocator error")
}
