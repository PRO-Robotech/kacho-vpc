// Package handler — internal_cloud_handler.go реализует
// kacho.cloud.vpc.v1.InternalCloudService.SetPoolSelector / Unset / Get.
//
// PoolSelector привязан к Cloud (не Network): cloud_id берётся из folder
// при cascade-resolve, что покрывает external IP (у которых нет network_id).
package handler

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/services/addresspool"
)

type InternalCloudHandler struct {
	vpcv1.UnimplementedInternalCloudServiceServer
	pools *addresspool.AddressPoolService
}

func NewInternalCloudHandler(pools *addresspool.AddressPoolService) *InternalCloudHandler {
	return &InternalCloudHandler{pools: pools}
}

func (h *InternalCloudHandler) SetPoolSelector(ctx context.Context, req *vpcv1.SetCloudPoolSelectorRequest) (*vpcv1.SetCloudPoolSelectorResponse, error) {
	if req.GetCloudId() == "" {
		return nil, status.Error(codes.InvalidArgument, "cloud_id required")
	}
	if err := h.pools.SetCloudPoolSelector(ctx, req.GetCloudId(), req.GetSelector(), req.GetSetBy()); err != nil {
		return nil, mapPoolErr(err)
	}
	return &vpcv1.SetCloudPoolSelectorResponse{}, nil
}

func (h *InternalCloudHandler) UnsetPoolSelector(ctx context.Context, req *vpcv1.UnsetCloudPoolSelectorRequest) (*vpcv1.UnsetCloudPoolSelectorResponse, error) {
	if req.GetCloudId() == "" {
		return nil, status.Error(codes.InvalidArgument, "cloud_id required")
	}
	if err := h.pools.UnsetCloudPoolSelector(ctx, req.GetCloudId()); err != nil {
		return nil, mapPoolErr(err)
	}
	return &vpcv1.UnsetCloudPoolSelectorResponse{}, nil
}

func (h *InternalCloudHandler) GetPoolSelector(ctx context.Context, req *vpcv1.GetCloudPoolSelectorRequest) (*vpcv1.GetCloudPoolSelectorResponse, error) {
	if req.GetCloudId() == "" {
		return nil, status.Error(codes.InvalidArgument, "cloud_id required")
	}
	sel, err := h.pools.GetCloudPoolSelector(ctx, req.GetCloudId())
	if err != nil {
		return &vpcv1.GetCloudPoolSelectorResponse{Present: false}, nil
	}
	return &vpcv1.GetCloudPoolSelectorResponse{
		Selector:     sel.Selector,
		Present:      true,
		SetAtRfc3339: sel.SetAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
		SetBy:        sel.SetBy,
	}, nil
}
