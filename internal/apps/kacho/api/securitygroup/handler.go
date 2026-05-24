package securitygroup

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/PRO-Robotech/kacho-corelib/operations"
	operationpb "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/operation"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/dto"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"

	// Blank-import регистрирует SecurityGroup/time DTO трансферы (skill evgeniy §3 C.4).
	_ "github.com/PRO-Robotech/kacho-vpc/internal/dto/toproto"
	"github.com/PRO-Robotech/kacho-vpc/internal/handler"
)

// Handler — реализация vpcv1.SecurityGroupServiceServer на основе use-case'ов
// (skill evgeniy §2). Тонкий transport-слой: proto-request → domain → use-case
// → proto-response. Никакой бизнес-логики.
//
// SG-специфика: split-endpoint UpdateRules / UpdateRule — каждый идёт в свой
// use-case (UpdateRulesUseCase / UpdateRuleUseCase), а не в обычный
// UpdateSecurityGroupUseCase. Обычный Update — только name/description/labels
// (+ legacy `rule_specs` в mask для verbatim YC compat).
type Handler struct {
	vpcv1.UnimplementedSecurityGroupServiceServer

	create         *CreateSecurityGroupUseCase
	update         *UpdateSecurityGroupUseCase
	updateRules    *UpdateRulesUseCase
	updateRule     *UpdateRuleUseCase
	delete         *DeleteSecurityGroupUseCase
	move           *MoveSecurityGroupUseCase
	get            *GetSecurityGroupUseCase
	list           *ListSecurityGroupsUseCase
	listOperations *ListOperationsUseCase
}

// NewHandler собирает Handler из готовых use-case'ов. Конструктор намеренно
// принимает все use-case'ы — composition-root (cmd/vpc/main.go) собирает их
// с одинаковыми зависимостями (repo / networkReader / projectClient / opsRepo).
func NewHandler(
	create *CreateSecurityGroupUseCase,
	update *UpdateSecurityGroupUseCase,
	updateRules *UpdateRulesUseCase,
	updateRule *UpdateRuleUseCase,
	deleteUC *DeleteSecurityGroupUseCase,
	move *MoveSecurityGroupUseCase,
	get *GetSecurityGroupUseCase,
	list *ListSecurityGroupsUseCase,
	listOps *ListOperationsUseCase,
) *Handler {
	return &Handler{
		create:         create,
		update:         update,
		updateRules:    updateRules,
		updateRule:     updateRule,
		delete:         deleteUC,
		move:           move,
		get:            get,
		list:           list,
		listOperations: listOps,
	}
}

// Get — sync read + AuthZ.
func (h *Handler) Get(ctx context.Context, req *vpcv1.GetSecurityGroupRequest) (*vpcv1.SecurityGroup, error) {
	if req.SecurityGroupId == "" {
		return nil, status.Error(codes.InvalidArgument, "security_group_id required")
	}
	sg, err := h.get.Execute(ctx, req.SecurityGroupId)
	if err != nil {
		return nil, err
	}
	if err := handler.AssertFolderOwnership(ctx, sg.ProjectID); err != nil {
		return nil, err
	}
	return securityGroupToPb(sg)
}

// List — project_id required + AuthZ + FGA list-filter (KAC-127 Phase 4).
func (h *Handler) List(ctx context.Context, req *vpcv1.ListSecurityGroupsRequest) (*vpcv1.ListSecurityGroupsResponse, error) {
	if err := handler.AssertFolderOwnership(ctx, req.ProjectId); err != nil {
		return nil, err
	}
	subject := fgaSubjectFromCtx(ctx)
	sgs, nextToken, err := h.list.Execute(ctx, subject, SecurityGroupFilter{
		ProjectID: req.ProjectId,
		Filter:    req.Filter,
	}, Pagination{
		PageToken: req.PageToken,
		PageSize:  req.PageSize,
	})
	if err != nil {
		return nil, err
	}
	resp := &vpcv1.ListSecurityGroupsResponse{NextPageToken: nextToken}
	for _, sg := range sgs {
		pb, err := securityGroupToPb(sg)
		if err != nil {
			return nil, err
		}
		resp.SecurityGroups = append(resp.SecurityGroups, pb)
	}
	return resp, nil
}

// Create — AuthZ → proto → domain → use-case.
func (h *Handler) Create(ctx context.Context, req *vpcv1.CreateSecurityGroupRequest) (*operationpb.Operation, error) {
	if err := handler.AssertFolderOwnership(ctx, req.ProjectId); err != nil {
		return nil, err
	}
	sg := domain.SecurityGroup{
		ProjectID:   req.ProjectId,
		NetworkID:   req.NetworkId,
		Name:        domain.RcNameVPC(req.Name),
		Description: domain.RcDescription(req.Description),
		Labels:      domain.LabelsFromMap(req.Labels),
	}
	for _, rs := range req.RuleSpecs {
		sg.Rules = append(sg.Rules, ruleSpecFromProto(rs))
	}
	op, err := h.create.Execute(ctx, sg)
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

// Update — sync repo.Get + AuthZ + use-case. Legacy verbatim YC: rule_specs
// можно передать через update_mask=rule_specs (split-endpoint UpdateRules —
// предпочтительнее, но старый путь поддерживается).
func (h *Handler) Update(ctx context.Context, req *vpcv1.UpdateSecurityGroupRequest) (*operationpb.Operation, error) {
	if req.SecurityGroupId == "" {
		return nil, status.Error(codes.InvalidArgument, "security_group_id required")
	}
	sg, err := h.get.Execute(ctx, req.SecurityGroupId)
	if err != nil {
		return nil, err
	}
	if err := handler.AssertFolderOwnership(ctx, sg.ProjectID); err != nil {
		return nil, err
	}
	var mask []string
	if req.UpdateMask != nil {
		mask = req.UpdateMask.Paths
	}
	dsg := domain.SecurityGroup{
		Name:        domain.RcNameVPC(req.Name),
		Description: domain.RcDescription(req.Description),
		Labels:      domain.LabelsFromMap(req.Labels),
	}
	for _, rs := range req.RuleSpecs {
		dsg.Rules = append(dsg.Rules, ruleSpecFromProto(rs))
	}
	op, err := h.update.Execute(ctx, UpdateInput{
		SecurityGroupID: req.SecurityGroupId,
		SecurityGroup:   dsg,
		UpdateMask:      mask,
	})
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

// UpdateRules — split-endpoint: атомарно удалить deletion_rule_ids + добавить
// addition_rule_specs. Response — parent SG.
func (h *Handler) UpdateRules(ctx context.Context, req *vpcv1.UpdateSecurityGroupRulesRequest) (*operationpb.Operation, error) {
	if req.SecurityGroupId == "" {
		return nil, status.Error(codes.InvalidArgument, "security_group_id required")
	}
	sg, err := h.get.Execute(ctx, req.SecurityGroupId)
	if err != nil {
		return nil, err
	}
	if err := handler.AssertFolderOwnership(ctx, sg.ProjectID); err != nil {
		return nil, err
	}
	in := UpdateRulesInput{
		SecurityGroupID: req.SecurityGroupId,
		DeletionRuleIDs: req.DeletionRuleIds,
	}
	for _, rs := range req.AdditionRuleSpecs {
		in.AdditionRuleSpecs = append(in.AdditionRuleSpecs, ruleSpecFromProto(rs))
	}
	op, err := h.updateRules.Execute(ctx, in)
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

// UpdateRule — single rule modify (description / labels). Response — parent SG
// (verbatim YC CLI 1.x compat).
func (h *Handler) UpdateRule(ctx context.Context, req *vpcv1.UpdateSecurityGroupRuleRequest) (*operationpb.Operation, error) {
	if req.SecurityGroupId == "" {
		return nil, status.Error(codes.InvalidArgument, "security_group_id required")
	}
	sg, err := h.get.Execute(ctx, req.SecurityGroupId)
	if err != nil {
		return nil, err
	}
	if err := handler.AssertFolderOwnership(ctx, sg.ProjectID); err != nil {
		return nil, err
	}
	var mask []string
	if req.UpdateMask != nil {
		mask = req.UpdateMask.Paths
	}
	op, err := h.updateRule.Execute(ctx, UpdateRuleInput{
		SecurityGroupID: req.SecurityGroupId,
		RuleID:          req.RuleId,
		Description:     req.Description,
		Labels:          req.Labels,
		UpdateMask:      mask,
	})
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

// Delete — sync repo.Get для AuthZ + default-SG-protected, затем use-case.
func (h *Handler) Delete(ctx context.Context, req *vpcv1.DeleteSecurityGroupRequest) (*operationpb.Operation, error) {
	if req.SecurityGroupId == "" {
		return nil, status.Error(codes.InvalidArgument, "security_group_id required")
	}
	sg, err := h.get.Execute(ctx, req.SecurityGroupId)
	if err != nil {
		return nil, err
	}
	if err := handler.AssertFolderOwnership(ctx, sg.ProjectID); err != nil {
		return nil, err
	}
	op, err := h.delete.Execute(ctx, req.SecurityGroupId)
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

// Move — sync repo.Get для AuthZ источника, AssertFolderOwnership на dest, затем
// use-case.
func (h *Handler) Move(ctx context.Context, req *vpcv1.MoveSecurityGroupRequest) (*operationpb.Operation, error) {
	if req.SecurityGroupId == "" {
		return nil, status.Error(codes.InvalidArgument, "security_group_id required")
	}
	sg, err := h.get.Execute(ctx, req.SecurityGroupId)
	if err != nil {
		return nil, err
	}
	if err := handler.AssertFolderOwnership(ctx, sg.ProjectID); err != nil {
		return nil, err
	}
	if err := handler.AssertFolderOwnership(ctx, req.DestinationProjectId); err != nil {
		return nil, err
	}
	op, err := h.move.Execute(ctx, req.SecurityGroupId, req.DestinationProjectId)
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

// ListOperations — verbatim YC behaviour: SG обязан существовать (Get для
// AuthZ) → list operations.
func (h *Handler) ListOperations(ctx context.Context, req *vpcv1.ListSecurityGroupOperationsRequest) (*vpcv1.ListSecurityGroupOperationsResponse, error) {
	if req.SecurityGroupId == "" {
		return nil, status.Error(codes.InvalidArgument, "security_group_id required")
	}
	sg, err := h.get.Execute(ctx, req.SecurityGroupId)
	if err != nil {
		return nil, err
	}
	if err := handler.AssertFolderOwnership(ctx, sg.ProjectID); err != nil {
		return nil, err
	}
	ops, nextToken, err := h.listOperations.Execute(ctx, req.SecurityGroupId, Pagination{
		PageToken: req.PageToken,
		PageSize:  req.PageSize,
	})
	if err != nil {
		return nil, err
	}
	resp := &vpcv1.ListSecurityGroupOperationsResponse{NextPageToken: nextToken}
	for i := range ops {
		resp.Operations = append(resp.Operations, operationToProto(&ops[i]))
	}
	return resp, nil
}

// securityGroupToPb — repo-entity SecurityGroup → proto SecurityGroup через
// DTO-реестр (skill evgeniy §3 C.3).
func securityGroupToPb(rec *kacho.SecurityGroupRecord) (*vpcv1.SecurityGroup, error) {
	var dst *vpcv1.SecurityGroup
	if err := dto.Transfer(dto.FromTo(*rec, &dst)); err != nil {
		return nil, status.Error(codes.Internal, "dto.Transfer SecurityGroup failed")
	}
	return dst, nil
}

// operationToProto — локальная копия `handler.operationToProto`. При полном
// переезде use-case'ов из `internal/service` (Wave 3b) вынесем общий helper в
// shared-leaf.
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
