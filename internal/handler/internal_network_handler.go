// Package handler — internal_network_handler.go реализует
// kacho.cloud.vpc.v1.InternalNetworkService:
//   - SetDefaultSecurityGroupId — admin-only computed-field setter.
//
// PoolSelector RPC переехали на InternalCloudService (см.
// internal_cloud_handler.go). Причина: external Address не имеет network_id.
//
// GetInternalNetwork RPC + InternalNetwork message + поле vpn_id удалены в
// KAC-79/KAC-36 (post-kube-ovn: data-plane проекции больше не нужны — kube-ovn
// управляет underlay сам). Раньше vpn_id выставлялся как 24-bit data-plane id и
// возвращался через InternalNetworkService.GetNetwork; сейчас этой информации в
// kacho-vpc нет.
package handler

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/services/networkinternal"
)

type InternalNetworkHandler struct {
	vpcv1.UnimplementedInternalNetworkServiceServer
	netInternal *networkinternal.Service
}

func NewInternalNetworkHandler(ni *networkinternal.Service) *InternalNetworkHandler {
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
		// Wave 5 batch 36 (KAC-94): после удаления internal_address_pool_handler.go
		// (миграция AP на use-case-структуру) reuse-mapping в виде `mapPoolErr`
		// больше нет — переключаемся на общий `internalMapErr` (та же no-leak
		// семантика, см. internal_maperr.go).
		return nil, internalMapErr("set default security group", err)
	}
	return &vpcv1.SetDefaultSecurityGroupIdResponse{}, nil
}
