// Package handler — internal_address_pool_handler.go реализует
// kacho.cloud.vpc.v1.InternalAddressPoolService — internal-only API для
// admin-управления AddressPool resources, bindings, и diagnostics.
package handler

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/service"
)

// InternalAddressPoolHandler — gRPC server для InternalAddressPoolService.
type InternalAddressPoolHandler struct {
	vpcv1.UnimplementedInternalAddressPoolServiceServer
	svc *service.AddressPoolService
}

func NewInternalAddressPoolHandler(svc *service.AddressPoolService) *InternalAddressPoolHandler {
	return &InternalAddressPoolHandler{svc: svc}
}

// -- CRUD --

func (h *InternalAddressPoolHandler) Create(ctx context.Context, req *vpcv1.CreateAddressPoolRequest) (*vpcv1.AddressPool, error) {
	p, err := h.svc.Create(ctx, service.CreatePoolReq{
		Name:             req.GetName(),
		Description:      req.GetDescription(),
		Labels:           req.GetLabels(),
		CIDRBlocks:       req.GetCidrBlocks(),
		Kind:             domain.AddressPoolKind(req.GetKind()),
		ZoneID:           req.GetZoneId(),
		IsDefault:        req.GetIsDefault(),
		SelectorLabels:   req.GetSelectorLabels(),
		SelectorPriority: req.GetSelectorPriority(),
	})
	if err != nil {
		return nil, mapPoolErr(err)
	}
	return poolToProto(p), nil
}

func (h *InternalAddressPoolHandler) Get(ctx context.Context, req *vpcv1.GetAddressPoolRequest) (*vpcv1.AddressPool, error) {
	p, err := h.svc.Get(ctx, req.GetPoolId())
	if err != nil {
		return nil, mapPoolErr(err)
	}
	return poolToProto(p), nil
}

func (h *InternalAddressPoolHandler) List(ctx context.Context, req *vpcv1.ListAddressPoolsRequest) (*vpcv1.ListAddressPoolsResponse, error) {
	pools, next, err := h.svc.List(ctx, service.AddressPoolFilter{
		Kind:   domain.AddressPoolKind(req.GetKind()),
		ZoneID: req.GetZoneId(),
	}, service.Pagination{
		PageToken: req.GetPageToken(),
		PageSize:  req.GetPageSize(),
	})
	if err != nil {
		return nil, mapPoolErr(err)
	}
	out := make([]*vpcv1.AddressPool, 0, len(pools))
	for _, p := range pools {
		out = append(out, poolToProto(p))
	}
	return &vpcv1.ListAddressPoolsResponse{Pools: out, NextPageToken: next}, nil
}

func (h *InternalAddressPoolHandler) Update(ctx context.Context, req *vpcv1.UpdateAddressPoolRequest) (*vpcv1.AddressPool, error) {
	in := service.UpdatePoolReq{ID: req.GetPoolId()}
	if req.GetName() != "" {
		n := req.GetName()
		in.Name = &n
	}
	if req.GetDescription() != "" {
		d := req.GetDescription()
		in.Description = &d
	}
	if req.GetReplaceLabels() {
		in.ReplaceLabels = true
		in.Labels = req.GetLabels()
	}
	if len(req.GetCidrBlocks()) > 0 {
		in.ReplaceCIDR = true
		in.CIDRBlocks = req.GetCidrBlocks()
	}
	if req.GetUpdateIsDefault() {
		in.UpdateIsDefault = true
		in.IsDefault = req.GetIsDefault()
	}
	if req.GetReplaceSelectorLabels() {
		in.ReplaceSelectorLabels = true
		in.SelectorLabels = req.GetSelectorLabels()
	}
	if req.GetUpdateSelectorPriority() {
		in.UpdateSelectorPriority = true
		in.SelectorPriority = req.GetSelectorPriority()
	}
	p, err := h.svc.Update(ctx, in)
	if err != nil {
		return nil, mapPoolErr(err)
	}
	return poolToProto(p), nil
}

func (h *InternalAddressPoolHandler) Delete(ctx context.Context, req *vpcv1.DeleteAddressPoolRequest) (*vpcv1.DeleteAddressPoolResponse, error) {
	if err := h.svc.Delete(ctx, req.GetPoolId()); err != nil {
		return nil, mapPoolErr(err)
	}
	return &vpcv1.DeleteAddressPoolResponse{}, nil
}

// -- Bindings --

func (h *InternalAddressPoolHandler) BindAsNetworkDefault(ctx context.Context, req *vpcv1.BindAsNetworkDefaultRequest) (*vpcv1.BindResponse, error) {
	if err := h.svc.BindAsNetworkDefault(ctx, req.GetNetworkId(), req.GetPoolId()); err != nil {
		return nil, mapPoolErr(err)
	}
	return &vpcv1.BindResponse{}, nil
}

func (h *InternalAddressPoolHandler) UnbindNetworkDefault(ctx context.Context, req *vpcv1.UnbindNetworkDefaultRequest) (*vpcv1.BindResponse, error) {
	if err := h.svc.UnbindNetworkDefault(ctx, req.GetNetworkId()); err != nil {
		return nil, mapPoolErr(err)
	}
	return &vpcv1.BindResponse{}, nil
}

func (h *InternalAddressPoolHandler) BindAsAddressOverride(ctx context.Context, req *vpcv1.BindAsAddressOverrideRequest) (*vpcv1.BindResponse, error) {
	if err := h.svc.BindAsAddressOverride(ctx, req.GetAddressId(), req.GetPoolId()); err != nil {
		return nil, mapPoolErr(err)
	}
	return &vpcv1.BindResponse{}, nil
}

func (h *InternalAddressPoolHandler) UnbindAddressOverride(ctx context.Context, req *vpcv1.UnbindAddressOverrideRequest) (*vpcv1.BindResponse, error) {
	if err := h.svc.UnbindAddressOverride(ctx, req.GetAddressId()); err != nil {
		return nil, mapPoolErr(err)
	}
	return &vpcv1.BindResponse{}, nil
}

// -- Diagnostics --

func (h *InternalAddressPoolHandler) Check(ctx context.Context, req *vpcv1.CheckRequest) (*vpcv1.CheckResponse, error) {
	warnings, err := h.svc.Check(ctx, req.GetZoneId())
	if err != nil {
		return nil, mapPoolErr(err)
	}
	return &vpcv1.CheckResponse{Warnings: warnings}, nil
}

func (h *InternalAddressPoolHandler) ExplainResolution(ctx context.Context, req *vpcv1.ExplainResolutionRequest) (*vpcv1.ExplainResolutionResponse, error) {
	primary, runner, err := h.svc.ExplainResolution(ctx, req.GetAddressId(), req.GetNetworkId())
	if err != nil {
		return nil, mapPoolErr(err)
	}
	out := &vpcv1.ExplainResolutionResponse{}
	if primary != nil {
		out.SelectedPool = poolToProto(primary.Pool)
		out.MatchedVia = primary.MatchedVia
		out.MatchedSelector = primary.MatchedSelector
	}
	if runner != nil {
		out.RunnerUpPool = poolToProto(runner.Pool)
		out.RunnerUpMatchedVia = runner.MatchedVia
	}
	return out, nil
}

// -- Admin observability --

func (h *InternalAddressPoolHandler) ListAddresses(ctx context.Context, req *vpcv1.ListAddressPoolAddressesRequest) (*vpcv1.ListAddressPoolAddressesResponse, error) {
	addrs, next, err := h.svc.ListPoolAddresses(ctx, req.GetPoolId(), req.GetFolderId(), service.Pagination{
		PageToken: req.GetPageToken(),
		PageSize:  req.GetPageSize(),
	})
	if err != nil {
		return nil, mapPoolErr(err)
	}
	out := make([]*vpcv1.AddressPoolAddressEntry, 0, len(addrs))
	for _, a := range addrs {
		ip := ""
		zone := ""
		if a.ExternalIpv4 != nil {
			ip = a.ExternalIpv4.Address
			zone = a.ExternalIpv4.ZoneID
		}
		out = append(out, &vpcv1.AddressPoolAddressEntry{
			Id:        a.ID,
			FolderId:  a.FolderID,
			Name:      a.Name,
			Ipv4:      ip,
			ZoneId:    zone,
			Reserved:  a.Reserved,
			Used:      a.Used,
			CreatedAt: timestamppb.New(a.CreatedAt),
		})
	}
	return &vpcv1.ListAddressPoolAddressesResponse{Addresses: out, NextPageToken: next}, nil
}

func (h *InternalAddressPoolHandler) GetUtilization(ctx context.Context, req *vpcv1.GetAddressPoolUtilizationRequest) (*vpcv1.AddressPoolUtilization, error) {
	u, err := h.svc.GetPoolUtilization(ctx, req.GetPoolId())
	if err != nil {
		return nil, mapPoolErr(err)
	}
	cidrs := make([]*vpcv1.CIDRUtilization, 0, len(u.CIDRs))
	for _, c := range u.CIDRs {
		cidrs = append(cidrs, &vpcv1.CIDRUtilization{Cidr: c.CIDR, Total: c.Total, Used: c.Used})
	}
	return &vpcv1.AddressPoolUtilization{
		PoolId:      u.PoolID,
		TotalIps:    u.TotalIPs,
		UsedIps:     u.UsedIPs,
		FreeIps:     u.FreeIPs,
		UsedPercent: u.UsedPercent,
		Cidrs:       cidrs,
	}, nil
}

// -- helpers --

func poolToProto(p *domain.AddressPool) *vpcv1.AddressPool {
	if p == nil {
		return nil
	}
	return &vpcv1.AddressPool{
		Id:               p.ID,
		CreatedAt:        timestamppb.New(p.CreatedAt),
		Name:             p.Name,
		Description:      p.Description,
		Labels:           p.Labels,
		CidrBlocks:       p.CIDRBlocks,
		Kind:             vpcv1.AddressPoolKind(p.Kind),
		ZoneId:           p.ZoneID,
		IsDefault:        p.IsDefault,
		SelectorLabels:   p.SelectorLabels,
		SelectorPriority: p.SelectorPriority,
	}
}

func mapPoolErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, service.ErrNotFound) {
		return status.Error(codes.NotFound, err.Error())
	}
	if errors.Is(err, service.ErrAlreadyExists) {
		return status.Error(codes.AlreadyExists, err.Error())
	}
	if errors.Is(err, service.ErrFailedPrecondition) {
		return status.Error(codes.FailedPrecondition, err.Error())
	}
	if errors.Is(err, service.ErrInvalidArg) {
		return status.Error(codes.InvalidArgument, err.Error())
	}
	if _, ok := status.FromError(err); ok {
		return err
	}
	return status.Error(codes.Internal, err.Error())
}
