// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package securitygroup

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	operationpb "github.com/PRO-Robotech/kacho-corelib/proto/gen/go/kacho/cloud/operation"
	vpcv1 "github.com/PRO-Robotech/kacho-vpc/proto/gen/go/kacho/cloud/vpc/v1"

	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/shared/pbconv"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/dto"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"

	// Blank-import регистрирует SecurityGroup/time DTO-трансферы через init().
	_ "github.com/PRO-Robotech/kacho-vpc/internal/dto/toproto"
	"github.com/PRO-Robotech/kacho-vpc/internal/tenant"
)

// Handler — реализация vpcv1.SecurityGroupServiceServer на основе use-case'ов.
// Тонкий transport-слой: proto-request → domain → use-case → proto-response.
// Никакой бизнес-логики.
//
// SG-специфика: split-endpoint UpdateRules / UpdateRule — каждый идет в свой
// use-case (UpdateRulesUseCase / UpdateRuleUseCase), а не в обычный
// UpdateSecurityGroupUseCase. Обычный Update — name/description/labels, плюс
// full-replace всего набора правил через update_mask=rule_specs (альтернатива
// инкрементальным UpdateRules/UpdateRule).
type Handler struct {
	vpcv1.UnimplementedSecurityGroupServiceServer

	create         *CreateSecurityGroupUseCase
	update         *UpdateSecurityGroupUseCase
	updateRules    *UpdateRulesUseCase
	updateRule     *UpdateRuleUseCase
	delete         *DeleteSecurityGroupUseCase
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
		get:            get,
		list:           list,
		listOperations: listOps,
	}
}

// Get — sync read + AuthZ + per-object no-leak.
func (h *Handler) Get(ctx context.Context, req *vpcv1.GetSecurityGroupRequest) (*vpcv1.SecurityGroup, error) {
	if req.SecurityGroupId == "" {
		return nil, status.Error(codes.InvalidArgument, "security_group_id required")
	}
	subject := pbconv.SubjectFromContext(ctx)
	sg, err := h.get.Execute(ctx, subject, req.SecurityGroupId)
	if err != nil {
		return nil, err
	}
	if err := tenant.AssertProjectOwnership(ctx, sg.ProjectID); err != nil {
		return nil, err
	}
	return securityGroupToPb(sg)
}

// List — project_id required + AuthZ + FGA list-filter.
func (h *Handler) List(ctx context.Context, req *vpcv1.ListSecurityGroupsRequest) (*vpcv1.ListSecurityGroupsResponse, error) {
	if err := tenant.AssertProjectOwnership(ctx, req.ProjectId); err != nil {
		return nil, err
	}
	subject := pbconv.SubjectFromContext(ctx)
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
	if err := tenant.AssertProjectOwnership(ctx, req.ProjectId); err != nil {
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
	return pbconv.OperationToProto(op), nil
}

// Update — sync repo.Get + AuthZ + use-case. Весь набор правил можно заменить
// целиком (full-replace) через update_mask=rule_specs; инкрементальная правка —
// через split-endpoint UpdateRules.
func (h *Handler) Update(ctx context.Context, req *vpcv1.UpdateSecurityGroupRequest) (*operationpb.Operation, error) {
	if req.SecurityGroupId == "" {
		return nil, status.Error(codes.InvalidArgument, "security_group_id required")
	}
	sg, err := h.get.Execute(ctx, "", req.SecurityGroupId)
	if err != nil {
		return nil, err
	}
	if err := tenant.AssertProjectOwnership(ctx, sg.ProjectID); err != nil {
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
	return pbconv.OperationToProto(op), nil
}

// UpdateRules — split-endpoint: атомарно удалить deletion_rule_ids + добавить
// addition_rule_specs. Response — parent SG.
func (h *Handler) UpdateRules(ctx context.Context, req *vpcv1.UpdateSecurityGroupRulesRequest) (*operationpb.Operation, error) {
	if req.SecurityGroupId == "" {
		return nil, status.Error(codes.InvalidArgument, "security_group_id required")
	}
	sg, err := h.get.Execute(ctx, "", req.SecurityGroupId)
	if err != nil {
		return nil, err
	}
	if err := tenant.AssertProjectOwnership(ctx, sg.ProjectID); err != nil {
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
	return pbconv.OperationToProto(op), nil
}

// UpdateRule — изменение одного правила (description / labels). Response —
// parent SG.
func (h *Handler) UpdateRule(ctx context.Context, req *vpcv1.UpdateSecurityGroupRuleRequest) (*operationpb.Operation, error) {
	if req.SecurityGroupId == "" {
		return nil, status.Error(codes.InvalidArgument, "security_group_id required")
	}
	sg, err := h.get.Execute(ctx, "", req.SecurityGroupId)
	if err != nil {
		return nil, err
	}
	if err := tenant.AssertProjectOwnership(ctx, sg.ProjectID); err != nil {
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
	return pbconv.OperationToProto(op), nil
}

// Delete — sync repo.Get для AuthZ + default-SG-protected, затем use-case.
func (h *Handler) Delete(ctx context.Context, req *vpcv1.DeleteSecurityGroupRequest) (*operationpb.Operation, error) {
	if req.SecurityGroupId == "" {
		return nil, status.Error(codes.InvalidArgument, "security_group_id required")
	}
	sg, err := h.get.Execute(ctx, "", req.SecurityGroupId)
	if err != nil {
		return nil, err
	}
	if err := tenant.AssertProjectOwnership(ctx, sg.ProjectID); err != nil {
		return nil, err
	}
	op, err := h.delete.Execute(ctx, req.SecurityGroupId)
	if err != nil {
		return nil, err
	}
	return pbconv.OperationToProto(op), nil
}

// ListOperations — SG обязан существовать (Get для AuthZ) → list operations.
func (h *Handler) ListOperations(ctx context.Context, req *vpcv1.ListSecurityGroupOperationsRequest) (*vpcv1.ListSecurityGroupOperationsResponse, error) {
	if req.SecurityGroupId == "" {
		return nil, status.Error(codes.InvalidArgument, "security_group_id required")
	}
	sg, err := h.get.Execute(ctx, "", req.SecurityGroupId)
	if err != nil {
		return nil, err
	}
	if err := tenant.AssertProjectOwnership(ctx, sg.ProjectID); err != nil {
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
		resp.Operations = append(resp.Operations, pbconv.OperationToProto(&ops[i]))
	}
	return resp, nil
}

// securityGroupToPb — repo-entity SecurityGroup → proto SecurityGroup через
// DTO-реестр.
func securityGroupToPb(rec *kacho.SecurityGroupRecord) (*vpcv1.SecurityGroup, error) {
	var dst *vpcv1.SecurityGroup
	if err := dto.Transfer(dto.FromTo(*rec, &dst)); err != nil {
		return nil, status.Error(codes.Internal, "dto.Transfer SecurityGroup failed")
	}
	return dst, nil
}
