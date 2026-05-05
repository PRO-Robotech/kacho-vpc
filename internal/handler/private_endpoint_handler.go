package handler

import (
	"context"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	operationpb "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/operation"
	pe "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1/privatelink"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	svc "github.com/PRO-Robotech/kacho-vpc/internal/service"
)

// PrivateEndpointHandler реализует pe.PrivateEndpointServiceServer.
type PrivateEndpointHandler struct {
	pe.UnimplementedPrivateEndpointServiceServer
	svc *svc.PrivateEndpointService
}

// NewPrivateEndpointHandler создаёт PrivateEndpointHandler.
func NewPrivateEndpointHandler(s *svc.PrivateEndpointService) *PrivateEndpointHandler {
	return &PrivateEndpointHandler{svc: s}
}

func (h *PrivateEndpointHandler) Get(ctx context.Context, req *pe.GetPrivateEndpointRequest) (*pe.PrivateEndpoint, error) {
	if req.PrivateEndpointId == "" {
		return nil, status.Error(codes.InvalidArgument, "private_endpoint_id required")
	}
	got, err := h.svc.Get(ctx, req.PrivateEndpointId)
	if err != nil {
		return nil, err
	}
	return privateEndpointToProto(got), nil
}

func (h *PrivateEndpointHandler) List(ctx context.Context, req *pe.ListPrivateEndpointsRequest) (*pe.ListPrivateEndpointsResponse, error) {
	folderID := ""
	if c, ok := req.Container.(*pe.ListPrivateEndpointsRequest_FolderId); ok {
		folderID = c.FolderId
	}
	endpoints, nextToken, err := h.svc.List(ctx, svc.PrivateEndpointFilter{
		FolderID: folderID,
		Filter:   req.Filter,
	}, svc.Pagination{
		PageToken: req.PageToken,
		PageSize:  req.PageSize,
	})
	if err != nil {
		return nil, err
	}
	resp := &pe.ListPrivateEndpointsResponse{NextPageToken: nextToken}
	for _, p := range endpoints {
		resp.PrivateEndpoints = append(resp.PrivateEndpoints, privateEndpointToProto(p))
	}
	return resp, nil
}

func (h *PrivateEndpointHandler) Create(ctx context.Context, req *pe.CreatePrivateEndpointRequest) (*operationpb.Operation, error) {
	r := svc.CreatePrivateEndpointReq{
		FolderID:    req.FolderId,
		Name:        req.Name,
		Description: req.Description,
		Labels:      req.Labels,
		NetworkID:   req.NetworkId,
	}
	// AddressSpec oneof — internal_ipv4 или address_id.
	if req.AddressSpec != nil {
		switch a := req.AddressSpec.Address.(type) {
		case *pe.AddressSpec_AddressId:
			r.AddressID = a.AddressId
		case *pe.AddressSpec_InternalIpv4AddressSpec:
			r.SubnetID = a.InternalIpv4AddressSpec.SubnetId
			r.IPAddress = a.InternalIpv4AddressSpec.Address
		}
	}
	if _, ok := req.Service.(*pe.CreatePrivateEndpointRequest_ObjectStorage); ok {
		r.ServiceType = "object_storage"
	}
	if req.DnsOptions != nil {
		r.DnsOptions = map[string]any{
			"private_dns_records_enabled": req.DnsOptions.PrivateDnsRecordsEnabled,
		}
	}
	op, err := h.svc.Create(ctx, r)
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

func (h *PrivateEndpointHandler) Update(ctx context.Context, req *pe.UpdatePrivateEndpointRequest) (*operationpb.Operation, error) {
	var mask []string
	if req.UpdateMask != nil {
		mask = req.UpdateMask.Paths
	}
	r := svc.UpdatePrivateEndpointReq{
		PrivateEndpointID: req.PrivateEndpointId,
		Name:              req.Name,
		Description:       req.Description,
		Labels:            req.Labels,
		UpdateMask:        mask,
	}
	if req.DnsOptions != nil {
		r.DnsOptions = map[string]any{
			"private_dns_records_enabled": req.DnsOptions.PrivateDnsRecordsEnabled,
		}
	}
	op, err := h.svc.Update(ctx, r)
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

func (h *PrivateEndpointHandler) Delete(ctx context.Context, req *pe.DeletePrivateEndpointRequest) (*operationpb.Operation, error) {
	if req.PrivateEndpointId == "" {
		return nil, status.Error(codes.InvalidArgument, "private_endpoint_id required")
	}
	op, err := h.svc.Delete(ctx, req.PrivateEndpointId)
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

func (h *PrivateEndpointHandler) ListOperations(ctx context.Context, req *pe.ListPrivateEndpointOperationsRequest) (*pe.ListPrivateEndpointOperationsResponse, error) {
	if req.PrivateEndpointId == "" {
		return nil, status.Error(codes.InvalidArgument, "private_endpoint_id required")
	}
	ops, nextToken, err := h.svc.ListOperations(ctx, req.PrivateEndpointId, svc.Pagination{
		PageToken: req.PageToken,
		PageSize:  req.PageSize,
	})
	if err != nil {
		return nil, err
	}
	resp := &pe.ListPrivateEndpointOperationsResponse{NextPageToken: nextToken}
	for i := range ops {
		resp.Operations = append(resp.Operations, operationToProto(&ops[i]))
	}
	return resp, nil
}

// privateEndpointToProto конвертирует domain → proto PrivateEndpoint.
func privateEndpointToProto(p *domain.PrivateEndpoint) *pe.PrivateEndpoint {
	out := &pe.PrivateEndpoint{
		Id:          p.ID,
		FolderId:    p.FolderID,
		CreatedAt:   timestamppb.New(p.CreatedAt.Truncate(time.Second)),
		Name:        p.Name,
		Description: p.Description,
		Labels:      p.Labels,
		NetworkId:   p.NetworkID,
	}
	switch p.Status {
	case "PENDING":
		out.Status = pe.PrivateEndpoint_PENDING
	case "AVAILABLE":
		out.Status = pe.PrivateEndpoint_AVAILABLE
	case "DELETING":
		out.Status = pe.PrivateEndpoint_DELETING
	default:
		out.Status = pe.PrivateEndpoint_STATUS_UNSPECIFIED
	}
	if p.SubnetID != "" || p.IPAddress != "" || p.AddressID != "" {
		out.Address = &pe.PrivateEndpoint_EndpointAddress{
			SubnetId:  p.SubnetID,
			Address:   p.IPAddress,
			AddressId: p.AddressID,
		}
	}
	if v, ok := p.DnsOptions["private_dns_records_enabled"]; ok {
		if b, ok := v.(bool); ok {
			out.DnsOptions = &pe.PrivateEndpoint_DnsOptions{PrivateDnsRecordsEnabled: b}
		}
	}
	if p.ServiceType == "object_storage" || p.ServiceType == "" {
		out.Service = &pe.PrivateEndpoint_ObjectStorage_{
			ObjectStorage: &pe.PrivateEndpoint_ObjectStorage{},
		}
	}
	return out
}
