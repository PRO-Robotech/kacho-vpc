// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

// Package handler — internal_network_handler.go реализует
// kacho.cloud.vpc.v1.InternalNetworkService:
//   - GetNetwork — admin/data-plane read с инфра-полем vrf_id (SRv6 VRF id).
//   - SetDefaultSecurityGroupId — admin-only computed-field setter.
//
// vrf_id — инфра-чувствительное поле (data-plane tenancy): отдается ТОЛЬКО на
// cluster-internal listener (:9091) через GetNetwork отдельным полем
// GetInternalNetworkResponse.vrf_id, никогда на публичной поверхности Network
// (security.md — две проекции ресурса).
package handler

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	corevalidate "github.com/PRO-Robotech/kacho-corelib/validate"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/services/networkinternal"
	"github.com/PRO-Robotech/kacho-vpc/internal/dto"
	vpcv1 "github.com/PRO-Robotech/kacho-vpc/proto/gen/go/kacho/cloud/vpc/v1"

	// blank-import регистрирует трансфер kachorepo.NetworkRecord → *vpcv1.Network
	// в DTO-реестре (тот же шов, что публичный NetworkService.Get).
	_ "github.com/PRO-Robotech/kacho-vpc/internal/dto/toproto"
)

type InternalNetworkHandler struct {
	vpcv1.UnimplementedInternalNetworkServiceServer
	netInternal *networkinternal.Service
}

func NewInternalNetworkHandler(ni *networkinternal.Service) *InternalNetworkHandler {
	return &InternalNetworkHandler{netInternal: ni}
}

// GetNetwork — internal read: тот же Network-объект, что публичный
// NetworkService.Get, плюс инфра-поле vrf_id отдельным полем response.
// Тонкий transport: id-валидация → service.GetNetwork → DTO-маппинг.
//
// vrf_id берется из NetworkRecord.VRFID (миграция 0007) и кладется в
// GetInternalNetworkResponse.vrf_id, а НЕ в public Network message —
// разделение публичной и internal проекций (security.md).
func (h *InternalNetworkHandler) GetNetwork(ctx context.Context, req *vpcv1.GetInternalNetworkRequest) (*vpcv1.GetInternalNetworkResponse, error) {
	// Первым стейтментом: malformed id → InvalidArgument "invalid network id '<X>'"
	// (тот же helper/prefix, что публичный GetNetworkUseCase).
	if err := corevalidate.ResourceID("network", ids.PrefixNetwork, req.GetNetworkId()); err != nil {
		return nil, err
	}
	rec, err := h.netInternal.GetNetwork(ctx, req.GetNetworkId())
	if err != nil {
		return nil, internalMapErr("get internal network", err)
	}
	var pb *vpcv1.Network
	if err := dto.Transfer(dto.FromTo(*rec, &pb)); err != nil {
		return nil, status.Error(codes.Internal, "dto.Transfer Network failed")
	}
	return &vpcv1.GetInternalNetworkResponse{Network: pb, VrfId: rec.VRFID}, nil
}

func (h *InternalNetworkHandler) SetDefaultSecurityGroupId(ctx context.Context, req *vpcv1.SetDefaultSecurityGroupIdRequest) (*vpcv1.SetDefaultSecurityGroupIdResponse, error) {
	if req.GetNetworkId() == "" {
		return nil, status.Error(codes.InvalidArgument, "network_id required")
	}
	if req.GetSecurityGroupId() == "" {
		return nil, status.Error(codes.InvalidArgument, "security_group_id required")
	}
	if err := h.netInternal.SetDefaultSecurityGroupId(ctx, req.GetNetworkId(), req.GetSecurityGroupId()); err != nil {
		// Общий no-leak error-mapper для Internal-handler'ов (см. internal_maperr.go).
		return nil, internalMapErr("set default security group", err)
	}
	return &vpcv1.SetDefaultSecurityGroupIdResponse{}, nil
}
