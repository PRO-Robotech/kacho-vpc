// Package handler — composite InternalAddressService implementation.
//
// gRPC требует одну имплементацию на сервис, но у нас два логически
// разделённых handler'а (legacy SetInternalIP + новые Allocate*).
// Composite-adapter объединяет их за единым интерфейсом.
package handler

import (
	"context"

	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
)

// InternalAddressCompositeHandler — diamond-style composite, делегирующий
// каждый RPC соответствующему backend handler'у.
type InternalAddressCompositeHandler struct {
	vpcv1.UnimplementedInternalAddressServiceServer
	legacy   *InternalAddressHandler
	allocate *InternalAddressAllocateHandler
}

func NewInternalAddressCompositeHandler(legacy *InternalAddressHandler, alloc *InternalAddressAllocateHandler) *InternalAddressCompositeHandler {
	return &InternalAddressCompositeHandler{legacy: legacy, allocate: alloc}
}

func (h *InternalAddressCompositeHandler) SetInternalIP(ctx context.Context, req *vpcv1.SetInternalIPRequest) (*vpcv1.SetInternalIPResponse, error) {
	return h.legacy.SetInternalIP(ctx, req)
}

func (h *InternalAddressCompositeHandler) AllocateInternalIP(ctx context.Context, req *vpcv1.AllocateInternalIPRequest) (*vpcv1.AllocateIPResponse, error) {
	return h.allocate.AllocateInternalIP(ctx, req)
}

func (h *InternalAddressCompositeHandler) AllocateExternalIP(ctx context.Context, req *vpcv1.AllocateExternalIPRequest) (*vpcv1.AllocateIPResponse, error) {
	return h.allocate.AllocateExternalIP(ctx, req)
}
