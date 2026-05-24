package privateendpoint

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/PRO-Robotech/kacho-corelib/operations"
	operationpb "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/operation"
	pepb "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1/privatelink"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/dto"

	// Blank-import регистрирует PrivateEndpoint/time DTO трансферы.
	_ "github.com/PRO-Robotech/kacho-vpc/internal/dto/toproto"
	"github.com/PRO-Robotech/kacho-vpc/internal/handler"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// Handler — реализация pepb.PrivateEndpointServiceServer на основе use-case'ов.
//
// NB: у PrivateEndpoint нет Move RPC в YC verbatim API.
type Handler struct {
	pepb.UnimplementedPrivateEndpointServiceServer

	create         *CreatePrivateEndpointUseCase
	update         *UpdatePrivateEndpointUseCase
	delete         *DeletePrivateEndpointUseCase
	get            *GetPrivateEndpointUseCase
	list           *ListPrivateEndpointsUseCase
	listOperations *ListOperationsUseCase
}

// NewHandler собирает Handler из готовых use-case'ов.
func NewHandler(
	create *CreatePrivateEndpointUseCase,
	update *UpdatePrivateEndpointUseCase,
	deleteUC *DeletePrivateEndpointUseCase,
	get *GetPrivateEndpointUseCase,
	list *ListPrivateEndpointsUseCase,
	listOps *ListOperationsUseCase,
) *Handler {
	return &Handler{
		create:         create,
		update:         update,
		delete:         deleteUC,
		get:            get,
		list:           list,
		listOperations: listOps,
	}
}

// Get — sync read + AuthZ.
func (h *Handler) Get(ctx context.Context, req *pepb.GetPrivateEndpointRequest) (*pepb.PrivateEndpoint, error) {
	if req.PrivateEndpointId == "" {
		return nil, status.Error(codes.InvalidArgument, "private_endpoint_id required")
	}
	got, err := h.get.Execute(ctx, req.PrivateEndpointId)
	if err != nil {
		return nil, err
	}
	if err := handler.AssertFolderOwnership(ctx, got.ProjectID); err != nil {
		return nil, err
	}
	return privateEndpointToPb(got)
}

// List — project_id required + AuthZ + FGA list-filter (KAC-127 Phase 4).
func (h *Handler) List(ctx context.Context, req *pepb.ListPrivateEndpointsRequest) (*pepb.ListPrivateEndpointsResponse, error) {
	folderID := ""
	if c, ok := req.Container.(*pepb.ListPrivateEndpointsRequest_ProjectId); ok {
		folderID = c.ProjectId
	}
	if err := handler.AssertFolderOwnership(ctx, folderID); err != nil {
		return nil, err
	}
	subject := fgaSubjectFromCtx(ctx)
	endpoints, nextToken, err := h.list.Execute(ctx, subject, PrivateEndpointFilter{
		ProjectID: folderID,
		Filter:    req.Filter,
	}, Pagination{
		PageToken: req.PageToken,
		PageSize:  req.PageSize,
	})
	if err != nil {
		return nil, err
	}
	resp := &pepb.ListPrivateEndpointsResponse{NextPageToken: nextToken}
	for _, p := range endpoints {
		pb, err := privateEndpointToPb(p)
		if err != nil {
			return nil, err
		}
		resp.PrivateEndpoints = append(resp.PrivateEndpoints, pb)
	}
	return resp, nil
}

// Create — AuthZ → proto → domain → use-case.
func (h *Handler) Create(ctx context.Context, req *pepb.CreatePrivateEndpointRequest) (*operationpb.Operation, error) {
	if err := handler.AssertFolderOwnership(ctx, req.ProjectId); err != nil {
		return nil, err
	}
	p := domain.PrivateEndpoint{
		ProjectID:   req.ProjectId,
		Name:        domain.RcNameVPC(req.Name),
		Description: domain.RcDescription(req.Description),
		Labels:      domain.LabelsFromMap(req.Labels),
		NetworkID:   req.NetworkId,
	}
	// AddressSpec oneof — internal_ipv4 или address_id.
	if req.AddressSpec != nil {
		switch a := req.AddressSpec.Address.(type) {
		case *pepb.AddressSpec_AddressId:
			p.AddressID = a.AddressId
		case *pepb.AddressSpec_InternalIpv4AddressSpec:
			p.SubnetID = a.InternalIpv4AddressSpec.SubnetId
			p.IPAddress = a.InternalIpv4AddressSpec.Address
		}
	}
	if _, ok := req.Service.(*pepb.CreatePrivateEndpointRequest_ObjectStorage); ok {
		p.ServiceType = domain.PrivateEndpointServiceTypeObjectStorage
	}
	if req.DnsOptions != nil {
		p.DnsOptions = map[string]any{
			"private_dns_records_enabled": req.DnsOptions.PrivateDnsRecordsEnabled,
		}
	}
	op, err := h.create.Execute(ctx, p)
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

// Update — sync repo.Get + AuthZ + use-case.
func (h *Handler) Update(ctx context.Context, req *pepb.UpdatePrivateEndpointRequest) (*operationpb.Operation, error) {
	if req.PrivateEndpointId == "" {
		return nil, status.Error(codes.InvalidArgument, "private_endpoint_id required")
	}
	got, err := h.get.Execute(ctx, req.PrivateEndpointId)
	if err != nil {
		return nil, err
	}
	if err := handler.AssertFolderOwnership(ctx, got.ProjectID); err != nil {
		return nil, err
	}
	var mask []string
	if req.UpdateMask != nil {
		mask = req.UpdateMask.Paths
	}
	in := UpdateInput{
		PrivateEndpointID: req.PrivateEndpointId,
		PrivateEndpoint: domain.PrivateEndpoint{
			Name:        domain.RcNameVPC(req.Name),
			Description: domain.RcDescription(req.Description),
			Labels:      domain.LabelsFromMap(req.Labels),
		},
		UpdateMask: mask,
	}
	if req.DnsOptions != nil {
		in.PrivateEndpoint.DnsOptions = map[string]any{
			"private_dns_records_enabled": req.DnsOptions.PrivateDnsRecordsEnabled,
		}
	}
	op, err := h.update.Execute(ctx, in)
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

// Delete — sync repo.Get для AuthZ, затем use-case.
func (h *Handler) Delete(ctx context.Context, req *pepb.DeletePrivateEndpointRequest) (*operationpb.Operation, error) {
	if req.PrivateEndpointId == "" {
		return nil, status.Error(codes.InvalidArgument, "private_endpoint_id required")
	}
	got, err := h.get.Execute(ctx, req.PrivateEndpointId)
	if err != nil {
		return nil, err
	}
	if err := handler.AssertFolderOwnership(ctx, got.ProjectID); err != nil {
		return nil, err
	}
	op, err := h.delete.Execute(ctx, req.PrivateEndpointId)
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

// ListOperations — best-effort AuthZ.
func (h *Handler) ListOperations(ctx context.Context, req *pepb.ListPrivateEndpointOperationsRequest) (*pepb.ListPrivateEndpointOperationsResponse, error) {
	if req.PrivateEndpointId == "" {
		return nil, status.Error(codes.InvalidArgument, "private_endpoint_id required")
	}
	if got, gerr := h.get.Execute(ctx, req.PrivateEndpointId); gerr == nil {
		if err := handler.AssertFolderOwnership(ctx, got.ProjectID); err != nil {
			return nil, err
		}
	} else if status.Code(gerr) != codes.NotFound {
		return nil, gerr
	}
	ops, nextToken, err := h.listOperations.Execute(ctx, req.PrivateEndpointId, Pagination{
		PageToken: req.PageToken,
		PageSize:  req.PageSize,
	})
	if err != nil {
		return nil, err
	}
	resp := &pepb.ListPrivateEndpointOperationsResponse{NextPageToken: nextToken}
	for i := range ops {
		resp.Operations = append(resp.Operations, operationToProto(&ops[i]))
	}
	return resp, nil
}

// privateEndpointToPb — repo-entity → proto через DTO-реестр.
// Wave 5 replicate (KAC-94): принимает kacho.PrivateEndpointRecord (repo-leaf).
func privateEndpointToPb(rec *kacho.PrivateEndpointRecord) (*pepb.PrivateEndpoint, error) {
	var dst *pepb.PrivateEndpoint
	if err := dto.Transfer(dto.FromTo(*rec, &dst)); err != nil {
		return nil, status.Error(codes.Internal, "dto.Transfer PrivateEndpoint failed")
	}
	return dst, nil
}

// operationToProto — локальная копия `handler.operationToProto`.
func operationToProto(op *operations.Operation) *operationpb.Operation {
	p := &operationpb.Operation{
		Id:          op.ID,
		Description: op.Description,
		CreatedAt:   timestamppb.New(op.CreatedAt),
		CreatedBy:   op.CreatedBy,
		ModifiedAt:  timestamppb.New(op.ModifiedAt),
		Done:        op.Done,
		Metadata:    op.Metadata,
		PrincipalType:        op.Principal.Type,
		PrincipalId:          op.Principal.ID,
		PrincipalDisplayName: op.Principal.DisplayName,
	}
	if op.Error != nil {
		p.Result = &operationpb.Operation_Error{Error: op.Error}
	} else if op.Response != nil {
		p.Result = &operationpb.Operation_Response{Response: op.Response}
	}
	return p
}

// fgaSubjectFromCtx — KAC-127 Phase 4: extract FGA subject из ctx-Principal.
func fgaSubjectFromCtx(ctx context.Context) string {
	p := operations.PrincipalFromContext(ctx)
	if p.Type == "" || p.ID == "" || p.Type == "system" {
		return ""
	}
	return p.Type + ":" + p.ID
}
