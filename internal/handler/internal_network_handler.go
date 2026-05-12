// Package handler — internal_network_handler.go реализует
// kacho.cloud.vpc.v1.InternalNetworkService:
//   - SetDefaultSecurityGroupId — admin-only computed-field setter.
//   - GetNetwork — internal-проекция Network (включая инфра-чувствительный vpn_id).
//
// PoolSelector RPC переехали на InternalCloudService (см.
// internal_cloud_handler.go). Причина: external Address не имеет network_id.
package handler

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	"github.com/PRO-Robotech/kacho-vpc/internal/protoconv"
	"github.com/PRO-Robotech/kacho-vpc/internal/service"
)

type InternalNetworkHandler struct {
	vpcv1.UnimplementedInternalNetworkServiceServer
	netInternal *service.NetworkInternal
}

func NewInternalNetworkHandler(ni *service.NetworkInternal) *InternalNetworkHandler {
	return &InternalNetworkHandler{netInternal: ni}
}

func (h *InternalNetworkHandler) SetDefaultSecurityGroupId(ctx context.Context, req *vpcv1.SetDefaultSecurityGroupIdRequest) (*vpcv1.SetDefaultSecurityGroupIdResponse, error) {
	if req.GetNetworkId() == "" {
		return nil, status.Error(codes.InvalidArgument, "network_id required")
	}
	if req.GetSecurityGroupId() == "" {
		return nil, status.Error(codes.InvalidArgument, "security_group_id required")
	}
	if err := h.netInternal.SetDefaultSecurityGroupId(ctx, req.GetNetworkId(), req.GetSecurityGroupId()); err != nil {
		return nil, mapPoolErr(err) // reuse mapping
	}
	return &vpcv1.SetDefaultSecurityGroupIdResponse{}, nil
}

// GetNetwork возвращает internal-проекцию сети (public-поля + vpn_id).
func (h *InternalNetworkHandler) GetNetwork(ctx context.Context, req *vpcv1.GetInternalNetworkRequest) (*vpcv1.InternalNetwork, error) {
	if req.GetNetworkId() == "" {
		return nil, status.Error(codes.InvalidArgument, "network_id required")
	}
	n, err := h.netInternal.GetNetwork(ctx, req.GetNetworkId())
	if err != nil {
		return nil, mapPoolErr(err)
	}
	return protoconv.InternalNetwork(n), nil
}
