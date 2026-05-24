package subnet

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/PRO-Robotech/kacho-corelib/operations"
	operationpb "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/operation"
	reference "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/reference"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/dto"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"

	// Blank-import регистрирует Subnet/Address/time DTO трансферы (skill evgeniy §3 C.4).
	_ "github.com/PRO-Robotech/kacho-vpc/internal/dto/toproto"
	"github.com/PRO-Robotech/kacho-vpc/internal/handler"
)

// Handler — реализация vpcv1.SubnetServiceServer на основе use-case'ов
// (skill evgeniy §2). Тонкий transport-слой: proto-request → domain → use-case
// → proto-response. Никакой бизнес-логики.
type Handler struct {
	vpcv1.UnimplementedSubnetServiceServer

	create            *CreateSubnetUseCase
	update            *UpdateSubnetUseCase
	delete            *DeleteSubnetUseCase
	move              *MoveSubnetUseCase
	get               *GetSubnetUseCase
	list              *ListSubnetsUseCase
	addCidrBlocks     *AddCidrBlocksUseCase
	removeCidrBlocks  *RemoveCidrBlocksUseCase
	relocate          *RelocateUseCase
	listUsedAddresses *ListUsedAddressesUseCase
	listOperations    *ListOperationsUseCase
}

// NewHandler собирает Handler из готовых use-case'ов. Конструктор намеренно
// принимает все use-case'ы — composition-root (cmd/vpc/main.go) собирает их
// с одинаковыми зависимостями (repo / networkReader / projectClient / zoneReg /
// opsRepo / addrRefRepo / nicRepo).
func NewHandler(
	create *CreateSubnetUseCase,
	update *UpdateSubnetUseCase,
	deleteUC *DeleteSubnetUseCase,
	move *MoveSubnetUseCase,
	get *GetSubnetUseCase,
	list *ListSubnetsUseCase,
	addCidr *AddCidrBlocksUseCase,
	removeCidr *RemoveCidrBlocksUseCase,
	relocate *RelocateUseCase,
	listUsedAddrs *ListUsedAddressesUseCase,
	listOps *ListOperationsUseCase,
) *Handler {
	return &Handler{
		create:            create,
		update:            update,
		delete:            deleteUC,
		move:              move,
		get:               get,
		list:              list,
		addCidrBlocks:     addCidr,
		removeCidrBlocks:  removeCidr,
		relocate:          relocate,
		listUsedAddresses: listUsedAddrs,
		listOperations:    listOps,
	}
}

// Get — sync read + AuthZ.
func (h *Handler) Get(ctx context.Context, req *vpcv1.GetSubnetRequest) (*vpcv1.Subnet, error) {
	if req.SubnetId == "" {
		return nil, status.Error(codes.InvalidArgument, "subnet_id required")
	}
	s, err := h.get.Execute(ctx, req.SubnetId)
	if err != nil {
		return nil, err
	}
	if err := handler.AssertFolderOwnership(ctx, s.ProjectID); err != nil {
		return nil, err
	}
	return subnetToPb(s)
}

// List — project_id required + AuthZ + FGA list-filter (KAC-127 Phase 4).
func (h *Handler) List(ctx context.Context, req *vpcv1.ListSubnetsRequest) (*vpcv1.ListSubnetsResponse, error) {
	if err := handler.AssertFolderOwnership(ctx, req.ProjectId); err != nil {
		return nil, err
	}
	subject := fgaSubjectFromCtx(ctx)
	subs, nextToken, err := h.list.Execute(ctx, subject, SubnetFilter{
		ProjectID: req.ProjectId,
		Filter:    req.Filter,
	}, Pagination{
		PageToken: req.PageToken,
		PageSize:  req.PageSize,
	})
	if err != nil {
		return nil, err
	}
	resp := &vpcv1.ListSubnetsResponse{NextPageToken: nextToken}
	for _, s := range subs {
		pb, err := subnetToPb(s)
		if err != nil {
			return nil, err
		}
		resp.Subnets = append(resp.Subnets, pb)
	}
	return resp, nil
}

// Create — AuthZ → proto → domain → use-case.
func (h *Handler) Create(ctx context.Context, req *vpcv1.CreateSubnetRequest) (*operationpb.Operation, error) {
	if err := handler.AssertFolderOwnership(ctx, req.ProjectId); err != nil {
		return nil, err
	}
	s := domain.Subnet{
		ProjectID:    req.ProjectId,
		Name:         domain.RcNameVPC(req.Name),
		Description:  domain.RcDescription(req.Description),
		Labels:       domain.LabelsFromMap(req.Labels),
		NetworkID:    req.NetworkId,
		ZoneID:       req.ZoneId,
		V4CidrBlocks: req.V4CidrBlocks,
		V6CidrBlocks: req.V6CidrBlocks,
		RouteTableID: req.RouteTableId,
	}
	if req.DhcpOptions != nil {
		s.DhcpOptions = &domain.DhcpOptions{
			DomainNameServers: req.DhcpOptions.DomainNameServers,
			DomainName:        req.DhcpOptions.DomainName,
			NtpServers:        req.DhcpOptions.NtpServers,
		}
	}
	op, err := h.create.Execute(ctx, s)
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

// Update — sync repo.Get + AuthZ + use-case.
func (h *Handler) Update(ctx context.Context, req *vpcv1.UpdateSubnetRequest) (*operationpb.Operation, error) {
	if req.SubnetId == "" {
		return nil, status.Error(codes.InvalidArgument, "subnet_id required")
	}
	s, err := h.get.Execute(ctx, req.SubnetId)
	if err != nil {
		return nil, err
	}
	if err := handler.AssertFolderOwnership(ctx, s.ProjectID); err != nil {
		return nil, err
	}
	var mask []string
	if req.UpdateMask != nil {
		mask = req.UpdateMask.Paths
	}
	in := UpdateInput{
		SubnetID: req.SubnetId,
		Subnet: domain.Subnet{
			Name:         domain.RcNameVPC(req.Name),
			Description:  domain.RcDescription(req.Description),
			Labels:       domain.LabelsFromMap(req.Labels),
			RouteTableID: req.RouteTableId,
		},
		V4CidrBlocks: req.V4CidrBlocks,
		UpdateMask:   mask,
	}
	if req.DhcpOptions != nil {
		in.Subnet.DhcpOptions = &domain.DhcpOptions{
			DomainNameServers: req.DhcpOptions.DomainNameServers,
			DomainName:        req.DhcpOptions.DomainName,
			NtpServers:        req.DhcpOptions.NtpServers,
		}
	}
	op, err := h.update.Execute(ctx, in)
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

// Delete — sync repo.Get для AuthZ, затем use-case.
func (h *Handler) Delete(ctx context.Context, req *vpcv1.DeleteSubnetRequest) (*operationpb.Operation, error) {
	if req.SubnetId == "" {
		return nil, status.Error(codes.InvalidArgument, "subnet_id required")
	}
	s, err := h.get.Execute(ctx, req.SubnetId)
	if err != nil {
		return nil, err
	}
	if err := handler.AssertFolderOwnership(ctx, s.ProjectID); err != nil {
		return nil, err
	}
	op, err := h.delete.Execute(ctx, req.SubnetId)
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

// Move — sync repo.Get для AuthZ источника, AssertFolderOwnership на dest, затем
// use-case.
func (h *Handler) Move(ctx context.Context, req *vpcv1.MoveSubnetRequest) (*operationpb.Operation, error) {
	if req.SubnetId == "" {
		return nil, status.Error(codes.InvalidArgument, "subnet_id required")
	}
	s, err := h.get.Execute(ctx, req.SubnetId)
	if err != nil {
		return nil, err
	}
	if err := handler.AssertFolderOwnership(ctx, s.ProjectID); err != nil {
		return nil, err
	}
	if err := handler.AssertFolderOwnership(ctx, req.DestinationProjectId); err != nil {
		return nil, err
	}
	op, err := h.move.Execute(ctx, req.SubnetId, req.DestinationProjectId)
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

// AddCidrBlocks — sync repo.Get для AuthZ, затем use-case.
func (h *Handler) AddCidrBlocks(ctx context.Context, req *vpcv1.AddSubnetCidrBlocksRequest) (*operationpb.Operation, error) {
	if req.SubnetId == "" {
		return nil, status.Error(codes.InvalidArgument, "subnet_id required")
	}
	s, err := h.get.Execute(ctx, req.SubnetId)
	if err != nil {
		return nil, err
	}
	if err := handler.AssertFolderOwnership(ctx, s.ProjectID); err != nil {
		return nil, err
	}
	op, err := h.addCidrBlocks.Execute(ctx, req.SubnetId, req.GetV4CidrBlocks(), req.GetV6CidrBlocks())
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

// RemoveCidrBlocks — sync repo.Get для AuthZ, затем use-case.
func (h *Handler) RemoveCidrBlocks(ctx context.Context, req *vpcv1.RemoveSubnetCidrBlocksRequest) (*operationpb.Operation, error) {
	if req.SubnetId == "" {
		return nil, status.Error(codes.InvalidArgument, "subnet_id required")
	}
	s, err := h.get.Execute(ctx, req.SubnetId)
	if err != nil {
		return nil, err
	}
	if err := handler.AssertFolderOwnership(ctx, s.ProjectID); err != nil {
		return nil, err
	}
	op, err := h.removeCidrBlocks.Execute(ctx, req.SubnetId, req.GetV4CidrBlocks(), req.GetV6CidrBlocks())
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

// Relocate — sync repo.Get для AuthZ, затем use-case (всегда FailedPrecondition).
func (h *Handler) Relocate(ctx context.Context, req *vpcv1.RelocateSubnetRequest) (*operationpb.Operation, error) {
	if req.SubnetId == "" {
		return nil, status.Error(codes.InvalidArgument, "subnet_id required")
	}
	s, err := h.get.Execute(ctx, req.SubnetId)
	if err != nil {
		return nil, err
	}
	if err := handler.AssertFolderOwnership(ctx, s.ProjectID); err != nil {
		return nil, err
	}
	op, err := h.relocate.Execute(ctx, req.SubnetId, req.DestinationZoneId)
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

// ListUsedAddresses — sync read; AuthZ через parent Subnet.
func (h *Handler) ListUsedAddresses(ctx context.Context, req *vpcv1.ListUsedAddressesRequest) (*vpcv1.ListUsedAddressesResponse, error) {
	if req.SubnetId == "" {
		return nil, status.Error(codes.InvalidArgument, "subnet_id required")
	}
	s, err := h.get.Execute(ctx, req.SubnetId)
	if err != nil {
		return nil, err
	}
	if err := handler.AssertFolderOwnership(ctx, s.ProjectID); err != nil {
		return nil, err
	}
	addrs, refs, nextToken, err := h.listUsedAddresses.Execute(ctx, req.SubnetId, Pagination{
		PageToken: req.PageToken,
		PageSize:  req.PageSize,
	})
	if err != nil {
		return nil, err
	}
	resp := &vpcv1.ListUsedAddressesResponse{NextPageToken: nextToken}
	for _, a := range addrs {
		ua := &vpcv1.UsedAddress{
			IpVersion: vpcv1.IpVersion(a.IpVersion),
		}
		if a.InternalIpv4 != nil {
			ua.Address = a.InternalIpv4.Address
		} else if a.ExternalIpv4 != nil {
			ua.Address = a.ExternalIpv4.Address
		}
		// references[] — кто использует адрес (referrer-tracking; YC-like).
		if ref, ok := refs[a.ID]; ok && ref != nil {
			ua.References = []*reference.Reference{{
				Referrer: &reference.Referrer{Type: ref.ReferrerType, Id: ref.ReferrerID},
				Type:     reference.Reference_USED_BY,
			}}
		}
		resp.Addresses = append(resp.Addresses, ua)
	}
	return resp, nil
}

// ListOperations — best-effort AuthZ: ресурс жив → folder-ownership проверяем;
// удалён (NotFound от get) → пропускаем (история операций должна оставаться
// доступной).
func (h *Handler) ListOperations(ctx context.Context, req *vpcv1.ListSubnetOperationsRequest) (*vpcv1.ListSubnetOperationsResponse, error) {
	if req.SubnetId == "" {
		return nil, status.Error(codes.InvalidArgument, "subnet_id required")
	}
	if s, gerr := h.get.Execute(ctx, req.SubnetId); gerr == nil {
		if err := handler.AssertFolderOwnership(ctx, s.ProjectID); err != nil {
			return nil, err
		}
	} else if status.Code(gerr) != codes.NotFound {
		return nil, gerr
	}
	ops, nextToken, err := h.listOperations.Execute(ctx, req.SubnetId, Pagination{
		PageToken: req.PageToken,
		PageSize:  req.PageSize,
	})
	if err != nil {
		return nil, err
	}
	resp := &vpcv1.ListSubnetOperationsResponse{NextPageToken: nextToken}
	for i := range ops {
		resp.Operations = append(resp.Operations, operationToProto(&ops[i]))
	}
	return resp, nil
}

// subnetToPb — repo-entity Subnet → proto Subnet через DTO-реестр (skill
// evgeniy §3 C.3).
func subnetToPb(rec *kachorepo.SubnetRecord) (*vpcv1.Subnet, error) {
	var dst *vpcv1.Subnet
	if err := dto.Transfer(dto.FromTo(*rec, &dst)); err != nil {
		return nil, status.Error(codes.Internal, "dto.Transfer Subnet failed")
	}
	return dst, nil
}

// operationToProto — локальная копия `handler.operationToProto` (та lowercase).
// При полном переезде use-case'ов из `internal/service` извлечём общий helper в
// shared-leaf.
func operationToProto(op *operations.Operation) *operationpb.Operation {
	if op == nil {
		return nil
	}
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
// Empty subject (system / no auth) → use-case fallback на legacy unfiltered.
func fgaSubjectFromCtx(ctx context.Context) string {
	p := operations.PrincipalFromContext(ctx)
	if p.Type == "" || p.ID == "" || p.Type == "system" {
		return ""
	}
	return p.Type + ":" + p.ID
}
