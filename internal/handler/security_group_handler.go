package handler

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	operationpb "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/operation"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/protoconv"
	svc "github.com/PRO-Robotech/kacho-vpc/internal/service"
)

// SecurityGroupHandler реализует vpcv1.SecurityGroupServiceServer.
type SecurityGroupHandler struct {
	vpcv1.UnimplementedSecurityGroupServiceServer
	svc *svc.SecurityGroupService
}

// NewSecurityGroupHandler создаёт SecurityGroupHandler.
func NewSecurityGroupHandler(s *svc.SecurityGroupService) *SecurityGroupHandler {
	return &SecurityGroupHandler{svc: s}
}

func (h *SecurityGroupHandler) Get(ctx context.Context, req *vpcv1.GetSecurityGroupRequest) (*vpcv1.SecurityGroup, error) {
	if req.SecurityGroupId == "" {
		return nil, status.Error(codes.InvalidArgument, "security_group_id required")
	}
	sg, err := h.svc.Get(ctx, req.SecurityGroupId)
	if err != nil {
		return nil, err
	}
	if err := AssertFolderOwnership(ctx, sg.FolderID); err != nil {
		return nil, err
	}
	return protoconv.SecurityGroup(sg), nil
}

func (h *SecurityGroupHandler) List(ctx context.Context, req *vpcv1.ListSecurityGroupsRequest) (*vpcv1.ListSecurityGroupsResponse, error) {
	if err := AssertFolderOwnership(ctx, req.FolderId); err != nil {
		return nil, err
	}
	sgs, nextToken, err := h.svc.List(ctx, svc.SecurityGroupFilter{
		FolderID: req.FolderId,
		Filter:   req.Filter,
	}, svc.Pagination{
		PageToken: req.PageToken,
		PageSize:  req.PageSize,
	})
	if err != nil {
		return nil, err
	}
	resp := &vpcv1.ListSecurityGroupsResponse{NextPageToken: nextToken}
	for _, sg := range sgs {
		resp.SecurityGroups = append(resp.SecurityGroups, protoconv.SecurityGroup(sg))
	}
	return resp, nil
}

func (h *SecurityGroupHandler) Create(ctx context.Context, req *vpcv1.CreateSecurityGroupRequest) (*operationpb.Operation, error) {
	if err := AssertFolderOwnership(ctx, req.FolderId); err != nil {
		return nil, err
	}
	createReq := svc.CreateSecurityGroupReq{
		FolderID:    req.FolderId,
		Name:        req.Name,
		Description: req.Description,
		Labels:      req.Labels,
		NetworkID:   req.NetworkId,
	}
	for _, rs := range req.RuleSpecs {
		createReq.RuleSpecs = append(createReq.RuleSpecs, ruleSpecFromProto(rs))
	}
	op, err := h.svc.Create(ctx, createReq)
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

func (h *SecurityGroupHandler) Update(ctx context.Context, req *vpcv1.UpdateSecurityGroupRequest) (*operationpb.Operation, error) {
	if req.SecurityGroupId == "" {
		return nil, status.Error(codes.InvalidArgument, "security_group_id required")
	}
	sg, err := h.svc.Get(ctx, req.SecurityGroupId)
	if err != nil {
		return nil, err
	}
	if err := AssertFolderOwnership(ctx, sg.FolderID); err != nil {
		return nil, err
	}
	var mask []string
	if req.UpdateMask != nil {
		mask = req.UpdateMask.Paths
	}
	updReq := svc.UpdateSecurityGroupReq{
		SecurityGroupID: req.SecurityGroupId,
		Name:            req.Name,
		Description:     req.Description,
		Labels:          req.Labels,
		UpdateMask:      mask,
	}
	for _, rs := range req.RuleSpecs {
		updReq.RuleSpecs = append(updReq.RuleSpecs, ruleSpecFromProto(rs))
	}
	op, err := h.svc.Update(ctx, updReq)
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

func (h *SecurityGroupHandler) UpdateRules(ctx context.Context, req *vpcv1.UpdateSecurityGroupRulesRequest) (*operationpb.Operation, error) {
	if req.SecurityGroupId == "" {
		return nil, status.Error(codes.InvalidArgument, "security_group_id required")
	}
	sg, err := h.svc.Get(ctx, req.SecurityGroupId)
	if err != nil {
		return nil, err
	}
	if err := AssertFolderOwnership(ctx, sg.FolderID); err != nil {
		return nil, err
	}
	updReq := svc.UpdateRulesReq{
		SecurityGroupID: req.SecurityGroupId,
		DeletionRuleIDs: req.DeletionRuleIds,
	}
	for _, rs := range req.AdditionRuleSpecs {
		updReq.AdditionRuleSpecs = append(updReq.AdditionRuleSpecs, ruleSpecFromProto(rs))
	}
	op, err := h.svc.UpdateRules(ctx, updReq)
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

func (h *SecurityGroupHandler) UpdateRule(ctx context.Context, req *vpcv1.UpdateSecurityGroupRuleRequest) (*operationpb.Operation, error) {
	if req.SecurityGroupId == "" {
		return nil, status.Error(codes.InvalidArgument, "security_group_id required")
	}
	sg, err := h.svc.Get(ctx, req.SecurityGroupId)
	if err != nil {
		return nil, err
	}
	if err := AssertFolderOwnership(ctx, sg.FolderID); err != nil {
		return nil, err
	}
	var mask []string
	if req.UpdateMask != nil {
		mask = req.UpdateMask.Paths
	}
	op, err := h.svc.UpdateRule(ctx, svc.UpdateRuleReq{
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

func (h *SecurityGroupHandler) Delete(ctx context.Context, req *vpcv1.DeleteSecurityGroupRequest) (*operationpb.Operation, error) {
	if req.SecurityGroupId == "" {
		return nil, status.Error(codes.InvalidArgument, "security_group_id required")
	}
	sg, err := h.svc.Get(ctx, req.SecurityGroupId)
	if err != nil {
		return nil, err
	}
	if err := AssertFolderOwnership(ctx, sg.FolderID); err != nil {
		return nil, err
	}
	op, err := h.svc.Delete(ctx, req.SecurityGroupId)
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

func (h *SecurityGroupHandler) Move(ctx context.Context, req *vpcv1.MoveSecurityGroupRequest) (*operationpb.Operation, error) {
	if req.SecurityGroupId == "" {
		return nil, status.Error(codes.InvalidArgument, "security_group_id required")
	}
	sg, err := h.svc.Get(ctx, req.SecurityGroupId)
	if err != nil {
		return nil, err
	}
	if err := AssertFolderOwnership(ctx, sg.FolderID); err != nil {
		return nil, err
	}
	if err := AssertFolderOwnership(ctx, req.DestinationFolderId); err != nil {
		return nil, err
	}
	op, err := h.svc.Move(ctx, req.SecurityGroupId, req.DestinationFolderId)
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

func (h *SecurityGroupHandler) ListOperations(ctx context.Context, req *vpcv1.ListSecurityGroupOperationsRequest) (*vpcv1.ListSecurityGroupOperationsResponse, error) {
	if req.SecurityGroupId == "" {
		return nil, status.Error(codes.InvalidArgument, "security_group_id required")
	}
	sg, err := h.svc.Get(ctx, req.SecurityGroupId)
	if err != nil {
		return nil, err
	}
	if err := AssertFolderOwnership(ctx, sg.FolderID); err != nil {
		return nil, err
	}
	ops, nextToken, err := h.svc.ListOperations(ctx, req.SecurityGroupId, svc.Pagination{
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

// sgToProto конвертирует domain SG → proto SG (с timestamp truncation).

// ruleSpecFromProto конвертирует proto SecurityGroupRuleSpec → domain SecurityGroupRule.
func ruleSpecFromProto(rs *vpcv1.SecurityGroupRuleSpec) domain.SecurityGroupRule {
	r := domain.SecurityGroupRule{
		Description: rs.Description,
		Labels:      rs.Labels,
	}
	switch rs.Direction {
	case vpcv1.SecurityGroupRule_INGRESS:
		r.Direction = "INGRESS"
	case vpcv1.SecurityGroupRule_EGRESS:
		r.Direction = "EGRESS"
	}
	if rs.Ports != nil {
		r.FromPort = rs.Ports.FromPort
		r.ToPort = rs.Ports.ToPort
	}
	if name := rs.GetProtocolName(); name != "" {
		r.ProtocolName = name
	}
	if num := rs.GetProtocolNumber(); num != 0 {
		r.ProtocolNumber = num
	}
	if cb := rs.GetCidrBlocks(); cb != nil {
		r.V4CidrBlocks = cb.V4CidrBlocks
		r.V6CidrBlocks = cb.V6CidrBlocks
	}
	if sgID := rs.GetSecurityGroupId(); sgID != "" {
		r.SecurityGroupID = sgID
	}
	if pred := rs.GetPredefinedTarget(); pred != "" {
		r.PredefinedTarget = pred
	}
	return r
}
