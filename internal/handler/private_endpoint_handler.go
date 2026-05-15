package handler

import (
	"context"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	operationpb "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/operation"
	pe "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1/privatelink"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/dto"
	// type2pb регистрирует DTO-трансферы в init() — нужны для dto.Transfer.
	// Skill evgeniy §3 C.4.
	_ "github.com/PRO-Robotech/kacho-vpc/internal/dto/type2pb"
	svc "github.com/PRO-Robotech/kacho-vpc/internal/service"
)

// privateEndpointToPb конвертирует repo-entity PE → proto PE через DTO-реестр.
// Wave 2 batch B (KAC-94): protoconv.PrivateEndpoint удалён (skill evgeniy §3 C.6 / AP-11).
func privateEndpointToPb(rec *domain.PrivateEndpointRecord) (*pe.PrivateEndpoint, error) {
	var dst *pe.PrivateEndpoint
	if err := dto.Transfer(dto.FromTo(*rec, &dst)); err != nil {
		return nil, fmt.Errorf("dto.Transfer PrivateEndpoint: %w", err)
	}
	return dst, nil
}

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
	if err := AssertFolderOwnership(ctx, got.FolderID); err != nil {
		return nil, err
	}
	return privateEndpointToPb(got)
}

func (h *PrivateEndpointHandler) List(ctx context.Context, req *pe.ListPrivateEndpointsRequest) (*pe.ListPrivateEndpointsResponse, error) {
	folderID := ""
	if c, ok := req.Container.(*pe.ListPrivateEndpointsRequest_FolderId); ok {
		folderID = c.FolderId
	}
	if err := AssertFolderOwnership(ctx, folderID); err != nil {
		return nil, err
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
		pb, err := privateEndpointToPb(p)
		if err != nil {
			return nil, err
		}
		resp.PrivateEndpoints = append(resp.PrivateEndpoints, pb)
	}
	return resp, nil
}

func (h *PrivateEndpointHandler) Create(ctx context.Context, req *pe.CreatePrivateEndpointRequest) (*operationpb.Operation, error) {
	if err := AssertFolderOwnership(ctx, req.FolderId); err != nil {
		return nil, err
	}
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
	if req.PrivateEndpointId == "" {
		return nil, status.Error(codes.InvalidArgument, "private_endpoint_id required")
	}
	got, err := h.svc.Get(ctx, req.PrivateEndpointId)
	if err != nil {
		return nil, err
	}
	if err := AssertFolderOwnership(ctx, got.FolderID); err != nil {
		return nil, err
	}
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
	got, err := h.svc.Get(ctx, req.PrivateEndpointId)
	if err != nil {
		return nil, err
	}
	if err := AssertFolderOwnership(ctx, got.FolderID); err != nil {
		return nil, err
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
	got, err := h.svc.Get(ctx, req.PrivateEndpointId)
	if err != nil {
		return nil, err
	}
	if err := AssertFolderOwnership(ctx, got.FolderID); err != nil {
		return nil, err
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
