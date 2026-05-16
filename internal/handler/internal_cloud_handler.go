// Package handler — internal_cloud_handler.go реализует
// kacho.cloud.vpc.v1.InternalCloudService.SetPoolSelector / Unset / Get.
//
// PoolSelector привязан к Cloud (не Network): cloud_id берётся из folder
// при cascade-resolve, что покрывает external IP (у которых нет network_id).
//
// Wave 5 batch 36 (KAC-94, skill `evgeniy` §2 B.1): после переезда
// AddressPool на use-case-структуру (`internal/apps/kacho/api/addresspool/`)
// этот handler принимает три узких use-case'а вместо «толстого»
// `*addresspool.AddressPoolService`. Логика сохранена verbatim.
package handler

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/api/addresspool"
)

// InternalCloudHandler — admin-only handler для Cloud-pool-selector RPC.
type InternalCloudHandler struct {
	vpcv1.UnimplementedInternalCloudServiceServer
	set   *addresspool.SetCloudPoolSelectorUseCase
	unset *addresspool.UnsetCloudPoolSelectorUseCase
	get   *addresspool.GetCloudPoolSelectorUseCase
}

// NewInternalCloudHandler собирает handler из готовых use-case'ов.
func NewInternalCloudHandler(
	set *addresspool.SetCloudPoolSelectorUseCase,
	unset *addresspool.UnsetCloudPoolSelectorUseCase,
	get *addresspool.GetCloudPoolSelectorUseCase,
) *InternalCloudHandler {
	return &InternalCloudHandler{set: set, unset: unset, get: get}
}

func (h *InternalCloudHandler) SetPoolSelector(ctx context.Context, req *vpcv1.SetCloudPoolSelectorRequest) (*vpcv1.SetCloudPoolSelectorResponse, error) {
	if req.GetCloudId() == "" {
		return nil, status.Error(codes.InvalidArgument, "cloud_id required")
	}
	if err := h.set.Execute(ctx, req.GetCloudId(), req.GetSelector(), req.GetSetBy()); err != nil {
		return nil, internalMapErr("cloud pool selector set", err)
	}
	return &vpcv1.SetCloudPoolSelectorResponse{}, nil
}

func (h *InternalCloudHandler) UnsetPoolSelector(ctx context.Context, req *vpcv1.UnsetCloudPoolSelectorRequest) (*vpcv1.UnsetCloudPoolSelectorResponse, error) {
	if req.GetCloudId() == "" {
		return nil, status.Error(codes.InvalidArgument, "cloud_id required")
	}
	if err := h.unset.Execute(ctx, req.GetCloudId()); err != nil {
		return nil, internalMapErr("cloud pool selector unset", err)
	}
	return &vpcv1.UnsetCloudPoolSelectorResponse{}, nil
}

func (h *InternalCloudHandler) GetPoolSelector(ctx context.Context, req *vpcv1.GetCloudPoolSelectorRequest) (*vpcv1.GetCloudPoolSelectorResponse, error) {
	if req.GetCloudId() == "" {
		return nil, status.Error(codes.InvalidArgument, "cloud_id required")
	}
	sel, err := h.get.Execute(ctx, req.GetCloudId())
	if err != nil {
		// Verbatim legacy: NotFound (binding не задан) превращается в
		// Present=false-ответ без error'а.
		return &vpcv1.GetCloudPoolSelectorResponse{Present: false}, nil
	}
	return &vpcv1.GetCloudPoolSelectorResponse{
		Selector:     sel.Selector,
		Present:      true,
		SetAtRfc3339: sel.SetAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
		SetBy:        sel.SetBy,
	}, nil
}
