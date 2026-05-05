package handler

import (
	"context"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	operationpb "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/operation"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
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
	return sgToProto(sg), nil
}

func (h *SecurityGroupHandler) List(ctx context.Context, req *vpcv1.ListSecurityGroupsRequest) (*vpcv1.ListSecurityGroupsResponse, error) {
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
		resp.SecurityGroups = append(resp.SecurityGroups, sgToProto(sg))
	}
	return resp, nil
}

func (h *SecurityGroupHandler) Create(ctx context.Context, req *vpcv1.CreateSecurityGroupRequest) (*operationpb.Operation, error) {
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
	op, err := h.svc.Delete(ctx, req.SecurityGroupId)
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

func (h *SecurityGroupHandler) Move(ctx context.Context, req *vpcv1.MoveSecurityGroupRequest) (*operationpb.Operation, error) {
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
func sgToProto(sg *domain.SecurityGroup) *vpcv1.SecurityGroup {
	p := &vpcv1.SecurityGroup{
		Id:                sg.ID,
		FolderId:          sg.FolderID,
		NetworkId:         sg.NetworkID,
		CreatedAt:         timestamppb.New(sg.CreatedAt.Truncate(time.Second)),
		Name:              sg.Name,
		Description:       sg.Description,
		Labels:            sg.Labels,
		Status:            sgStatusToProtoH(sg.Status),
		DefaultForNetwork: sg.DefaultForNetwork,
	}
	for _, r := range sg.Rules {
		pr := &vpcv1.SecurityGroupRule{
			Id:             r.ID,
			Description:    r.Description,
			Labels:         r.Labels,
			Direction:      sgDirectionToProtoH(r.Direction),
			ProtocolName:   r.ProtocolName,
			ProtocolNumber: r.ProtocolNumber,
		}
		if r.FromPort != 0 || r.ToPort != 0 {
			pr.Ports = &vpcv1.PortRange{FromPort: r.FromPort, ToPort: r.ToPort}
		}
		if len(r.V4CidrBlocks) > 0 || len(r.V6CidrBlocks) > 0 {
			pr.Target = &vpcv1.SecurityGroupRule_CidrBlocks{
				CidrBlocks: &vpcv1.CidrBlocks{
					V4CidrBlocks: r.V4CidrBlocks,
					V6CidrBlocks: r.V6CidrBlocks,
				},
			}
		}
		p.Rules = append(p.Rules, pr)
	}
	return p
}

func sgStatusToProtoH(s string) vpcv1.SecurityGroup_Status {
	switch s {
	case "CREATING":
		return vpcv1.SecurityGroup_CREATING
	case "ACTIVE":
		return vpcv1.SecurityGroup_ACTIVE
	case "UPDATING":
		return vpcv1.SecurityGroup_UPDATING
	case "DELETING":
		return vpcv1.SecurityGroup_DELETING
	}
	return vpcv1.SecurityGroup_STATUS_UNSPECIFIED
}

func sgDirectionToProtoH(d string) vpcv1.SecurityGroupRule_Direction {
	switch d {
	case "INGRESS":
		return vpcv1.SecurityGroupRule_INGRESS
	case "EGRESS":
		return vpcv1.SecurityGroupRule_EGRESS
	}
	return vpcv1.SecurityGroupRule_DIRECTION_UNSPECIFIED
}

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
