// Package handler — internal_network_interface_handler.go реализует
// kacho.cloud.vpc.v1.InternalNetworkInterfaceService (internal-only; data-plane-проекция
// NIC + write-back от kacho-vpc-implement). Регистрируется ТОЛЬКО на internal listener
// (:9091) — не на external TLS endpoint (workspace CLAUDE.md §запрет 6 / §«Инфра-чувствительные данные»).
package handler

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/protoconv"
	"github.com/PRO-Robotech/kacho-vpc/internal/service"
)

// InternalNetworkInterfaceHandler реализует vpcv1.InternalNetworkInterfaceServiceServer.
type InternalNetworkInterfaceHandler struct {
	vpcv1.UnimplementedInternalNetworkInterfaceServiceServer
	svc *service.NetworkInterfaceInternal
}

// NewInternalNetworkInterfaceHandler создаёт InternalNetworkInterfaceHandler.
func NewInternalNetworkInterfaceHandler(s *service.NetworkInterfaceInternal) *InternalNetworkInterfaceHandler {
	return &InternalNetworkInterfaceHandler{svc: s}
}

func (h *InternalNetworkInterfaceHandler) GetNetworkInterface(ctx context.Context, req *vpcv1.GetInternalNetworkInterfaceRequest) (*vpcv1.InternalNetworkInterface, error) {
	n, err := h.svc.Get(ctx, req.GetNetworkInterfaceId())
	if err != nil {
		return nil, err
	}
	return protoconv.InternalNetworkInterface(n), nil
}

func (h *InternalNetworkInterfaceHandler) ListByHypervisor(ctx context.Context, req *vpcv1.ListNetworkInterfacesByHypervisorRequest) (*vpcv1.ListInternalNetworkInterfacesResponse, error) {
	if req.GetHypervisorId() == "" {
		return nil, status.Error(codes.InvalidArgument, "hypervisor_id required")
	}
	out, err := h.svc.ListByHypervisor(ctx, req.GetHypervisorId())
	if err != nil {
		return nil, err
	}
	resp := &vpcv1.ListInternalNetworkInterfacesResponse{}
	for _, n := range out {
		resp.NetworkInterfaces = append(resp.NetworkInterfaces, protoconv.InternalNetworkInterface(n))
	}
	return resp, nil
}

func (h *InternalNetworkInterfaceHandler) ReportNiDataplane(ctx context.Context, req *vpcv1.ReportNiDataplaneRequest) (*vpcv1.ReportNiDataplaneResponse, error) {
	dp := domain.NICDataplane{
		HVID:        req.GetHypervisorId(),
		SID:         req.GetSid(),
		SIDSeq:      req.GetSidSeq(),
		HostIface:   req.GetHostIface(),
		Netns:       req.GetNetns(),
		GatewayIP:   req.GetGatewayIp(),
		ContainerID: req.GetContainerId(),
		StatusError: req.GetStatusError(),
		Revision:    req.GetDataplaneRevision(),
	}
	applied, err := h.svc.ReportNiDataplane(ctx, req.GetNetworkInterfaceId(), dp, int(req.GetStatus()))
	if err != nil {
		return nil, err
	}
	return &vpcv1.ReportNiDataplaneResponse{Applied: applied}, nil
}
