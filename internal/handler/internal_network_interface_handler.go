// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

// Package handler — internal_network_interface_handler.go реализует
// kacho.cloud.vpc.v1.InternalNetworkInterfaceService (cluster-internal :9091, ban #6):
// координация NIC↔Instance attach (§3a). Оживляет отложенную в KAC-266 явную привязку.
//
//   - Attach          — атомарный CAS на used_by_id (zone-coherence + anycast), sync.
//   - Detach          — идемпотентное снятие привязки, sync.
//   - ListByInstance  — batched read NIC-привязок для compute-side зеркала, sync.
//
// Тонкий transport: malformed-id-first (apiconv) → service → DTO. Бизнес-логика и
// error-контракт (in-use / zone-mismatch / not-found) живут в services/nicinternal.
// AuthN(mTLS)+AuthZ(per-RPC Check) энфорсятся цепочкой интерсепторов :9091
// (cmd/vpc/main.go internalUnary + check.PermissionMap) — не в этом handler'е.
package handler

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	corevalidate "github.com/PRO-Robotech/kacho-corelib/validate"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"

	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/services/nicinternal"
	"github.com/PRO-Robotech/kacho-vpc/internal/dto"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"

	// blank-import регистрирует DTO-трансфер kachorepo.NetworkInterfaceRecord →
	// *vpcv1.NetworkInterface (тот же шов, что публичный NetworkInterfaceService).
	_ "github.com/PRO-Robotech/kacho-vpc/internal/dto/toproto"
)

// niInternalResource — имя ресурса для corevalidate.ResourceID (malformed nic-id →
// "invalid network interface id '<X>'", verbatim контракт S4-06).
const niInternalResource = "network interface"

type InternalNetworkInterfaceHandler struct {
	vpcv1.UnimplementedInternalNetworkInterfaceServiceServer
	svc *nicinternal.Service
}

func NewInternalNetworkInterfaceHandler(svc *nicinternal.Service) *InternalNetworkInterfaceHandler {
	return &InternalNetworkInterfaceHandler{svc: svc}
}

// Attach — атомарный CAS NIC↔Instance. malformed nic_id → sync InvalidArgument
// (первым стейтментом). index<=0 → авто-назначение первого свободного слота;
// index>0 → явный слот.
func (h *InternalNetworkInterfaceHandler) Attach(ctx context.Context, req *vpcv1.AttachNetworkInterfaceRequest) (*vpcv1.AttachNetworkInterfaceResponse, error) {
	if err := corevalidate.ResourceID(niInternalResource, ids.PrefixNetworkInterface, req.GetNicId()); err != nil {
		return nil, err
	}
	if req.GetInstanceId() == "" {
		return nil, status.Error(codes.InvalidArgument, "instance_id required")
	}
	idx := kachorepo.AutoIndex
	if req.GetIndex() > 0 {
		idx = req.GetIndex()
	}
	rec, err := h.svc.Attach(ctx, kachorepo.AttachNICParams{
		NICID:          req.GetNicId(),
		InstanceID:     req.GetInstanceId(),
		InstanceName:   req.GetInstanceName(),
		InstanceZoneID: req.GetInstanceZoneId(),
		ProjectID:      req.GetProjectId(),
		Index:          idx,
	})
	if err != nil {
		return nil, err
	}
	pb, err := toNetworkInterfacePB(rec)
	if err != nil {
		return nil, err
	}
	return &vpcv1.AttachNetworkInterfaceResponse{NetworkInterface: pb}, nil
}

// Detach — идемпотентное снятие привязки. malformed nic_id → sync InvalidArgument.
func (h *InternalNetworkInterfaceHandler) Detach(ctx context.Context, req *vpcv1.DetachNetworkInterfaceRequest) (*vpcv1.DetachNetworkInterfaceResponse, error) {
	if err := corevalidate.ResourceID(niInternalResource, ids.PrefixNetworkInterface, req.GetNicId()); err != nil {
		return nil, err
	}
	if req.GetInstanceId() == "" {
		return nil, status.Error(codes.InvalidArgument, "instance_id required")
	}
	rec, err := h.svc.Detach(ctx, req.GetNicId(), req.GetInstanceId())
	if err != nil {
		return nil, err
	}
	pb, err := toNetworkInterfacePB(rec)
	if err != nil {
		return nil, err
	}
	return &vpcv1.DetachNetworkInterfaceResponse{NetworkInterface: pb}, nil
}

// ListByInstance — batched read NIC-привязок для compute-side зеркала.
func (h *InternalNetworkInterfaceHandler) ListByInstance(ctx context.Context, req *vpcv1.ListNetworkInterfacesByInstanceRequest) (*vpcv1.ListNetworkInterfacesByInstanceResponse, error) {
	att, err := h.svc.ListByInstance(ctx, req.GetInstanceIds())
	if err != nil {
		return nil, err
	}
	out := make([]*vpcv1.NetworkInterfaceAttachmentInfo, 0, len(att))
	for _, a := range att {
		out = append(out, &vpcv1.NetworkInterfaceAttachmentInfo{
			NicId:            a.NICID,
			InstanceId:       a.InstanceID,
			Index:            a.Index,
			SubnetId:         a.SubnetID,
			PrimaryV4Address: a.PrimaryV4Address,
			PrimaryV6Address: a.PrimaryV6Address,
			SecurityGroupIds: a.SecurityGroupIDs,
			MacAddress:       a.MAC,
		})
	}
	return &vpcv1.ListNetworkInterfacesByInstanceResponse{NetworkInterfaces: out}, nil
}

// toNetworkInterfacePB — repo-запись NIC → публичный *vpcv1.NetworkInterface через
// DTO-реестр (тот же шов, что публичный NetworkInterfaceService.Get).
func toNetworkInterfacePB(rec *kachorepo.NetworkInterfaceRecord) (*vpcv1.NetworkInterface, error) {
	var pb *vpcv1.NetworkInterface
	if err := dto.Transfer(dto.FromTo(*rec, &pb)); err != nil {
		return nil, status.Error(codes.Internal, "dto.Transfer NetworkInterface failed")
	}
	return pb, nil
}
