// Package handler — internal_address_pool_handler.go реализует
// kacho.cloud.vpc.v1.InternalAddressPoolService — internal-only API для
// admin-управления AddressPool resources, bindings, и diagnostics.
package handler

import (
	"context"
	"errors"

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
		V4CIDRBlocks:     req.GetV4CidrBlocks(),
		V6CIDRBlocks:     req.GetV6CidrBlocks(),
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
	// KAC-71: replace выполняется ТОЛЬКО при выставленном явном bool-флаге.
	// Тело массива (включая пустой) значимо лишь при флаге true; иначе
	// игнорируется. Это даёт двум сценариям детерминированную семантику:
	// (1) очистить один family (replace_v6=true, v6=[]) превращает dual-stack
	// в v4-only; (2) обновить description без замены CIDR — оба массива в
	// запросе игнорируются. См. REQ-IPL-UPD-01..06 / B7..B12.
	if req.GetReplaceV4CidrBlocks() {
		in.ReplaceV4CIDR = true
		in.V4CIDRBlocks = req.GetV4CidrBlocks()
	}
	if req.GetReplaceV6CidrBlocks() {
		in.ReplaceV6CIDR = true
		in.V6CIDRBlocks = req.GetV6CidrBlocks()
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

// ExplainResolution — admin diagnostic. REQ-RESOLVE-04 / D4: при fall-through
// (никаких pool требуемой family) возвращаем HTTP 200 с `matched_via="none"`
// и пустым `selected_pool`, а НЕ FailedPrecondition (как делают
// AllocateExternalIPv4 / AllocateExternalIPv6 при той же ошибке). ExplainResolution —
// диагностический endpoint для admin-UI; «нет подходящего pool» — нормальный
// ответ, который должен рендериться без error-handling.
//
// Прочие ошибки (NotFound и т.п.) идут через стандартный mapPoolErr.
func (h *InternalAddressPoolHandler) ExplainResolution(ctx context.Context, req *vpcv1.ExplainResolutionRequest) (*vpcv1.ExplainResolutionResponse, error) {
	primary, runner, err := h.svc.ExplainResolution(ctx, req.GetAddressId(), req.GetNetworkId())
	if err != nil {
		if errors.Is(err, service.ErrPoolNotResolved) {
			return &vpcv1.ExplainResolutionResponse{MatchedVia: "none"}, nil
		}
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
			Name:      string(a.Name),
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
		V4CidrBlocks:     p.V4CIDRBlocks,
		V6CidrBlocks:     p.V6CIDRBlocks,
		Kind:             vpcv1.AddressPoolKind(p.Kind),
		ZoneId:           p.ZoneID,
		IsDefault:        p.IsDefault,
		SelectorLabels:   p.SelectorLabels,
		SelectorPriority: p.SelectorPriority,
	}
}

// mapPoolErr — admin-handler error mapping. Делегирует internalMapErr (R8 M1
// sibling closure: до этого возвращал err.Error() в Internal branch — leak
// pgx hostname/port/sslmode даже на cluster-internal listener'е).
func mapPoolErr(err error) error {
	return internalMapErr("address pool admin error", err)
}
