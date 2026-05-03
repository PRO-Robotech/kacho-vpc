package handler

import (
	"context"

	operationv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/operation/v1"
	pb "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/service"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// SecurityGroupHandler реализует pb.SecurityGroupServiceServer.
type SecurityGroupHandler struct {
	pb.UnimplementedSecurityGroupServiceServer
	svc *service.SecurityGroupService
}

// NewSecurityGroupHandler создаёт SecurityGroupHandler.
func NewSecurityGroupHandler(svc *service.SecurityGroupService) *SecurityGroupHandler {
	return &SecurityGroupHandler{svc: svc}
}

func (h *SecurityGroupHandler) Get(ctx context.Context, req *pb.GetSecurityGroupRequest) (*pb.SecurityGroup, error) {
	sg, err := h.svc.Get(ctx, req.GetSecurityGroupId())
	if err != nil {
		return nil, err
	}
	return sgDomainToProto(sg), nil
}

func (h *SecurityGroupHandler) List(ctx context.Context, req *pb.ListSecurityGroupsRequest) (*pb.ListSecurityGroupsResponse, error) {
	filter := service.ListFilter{
		FolderID:  req.GetFolderId(),
		PageSize:  req.GetPageSize(),
		PageToken: req.GetPageToken(),
		Filter:    req.GetFilter(),
		OrderBy:   req.GetOrderBy(),
	}
	sgs, nextToken, err := h.svc.List(ctx, filter)
	if err != nil {
		return nil, err
	}
	resp := &pb.ListSecurityGroupsResponse{NextPageToken: nextToken}
	for i := range sgs {
		resp.SecurityGroups = append(resp.SecurityGroups, sgDomainToProto(&sgs[i]))
	}
	return resp, nil
}

func (h *SecurityGroupHandler) Create(ctx context.Context, req *pb.CreateSecurityGroupRequest) (*operationv1.Operation, error) {
	rules := protoRulesToDomain(req.GetRules())
	op, err := h.svc.Create(ctx,
		req.GetFolderId(), req.GetNetworkId(), req.GetName(), req.GetDescription(), req.GetLabels(), rules,
	)
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

func (h *SecurityGroupHandler) Update(ctx context.Context, req *pb.UpdateSecurityGroupRequest) (*operationv1.Operation, error) {
	rules := protoRulesToDomain(req.GetRules())
	op, err := h.svc.Update(ctx,
		req.GetSecurityGroupId(), req.GetResourceVersion(),
		req.GetName(), req.GetDescription(), req.GetLabels(), rules,
		maskFields(req.GetUpdateMask()),
	)
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

func (h *SecurityGroupHandler) Delete(ctx context.Context, req *pb.DeleteSecurityGroupRequest) (*operationv1.Operation, error) {
	op, err := h.svc.Delete(ctx, req.GetSecurityGroupId())
	if err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}

func sgDomainToProto(sg *domain.SecurityGroup) *pb.SecurityGroup {
	rules := make([]*pb.SecurityGroupRule, len(sg.Rules))
	for i, r := range sg.Rules {
		rules[i] = &pb.SecurityGroupRule{
			Id:           r.ID,
			Direction:    directionProto(r.Direction),
			Protocol:     protocolProto(r.Protocol),
			PortRangeMin: r.PortRangeMin,
			PortRangeMax: r.PortRangeMax,
			CidrBlocks:   r.CIDRBlocks,
			Description:  r.Description,
		}
	}
	proto := &pb.SecurityGroup{
		Id:              sg.ID,
		FolderId:        sg.FolderID,
		NetworkId:       sg.NetworkID,
		Name:            sg.Name,
		Description:     sg.Description,
		Labels:          sg.Labels,
		Status:          pb.SecurityGroupStatus(sg.Status),
		Generation:      sg.Generation,
		ResourceVersion: sg.ResourceVersion,
		Rules:           rules,
	}
	if !sg.CreatedAt.IsZero() {
		proto.CreatedAt = timestamppb.New(sg.CreatedAt)
	}
	return proto
}

func protoRulesToDomain(protoRules []*pb.SecurityGroupRule) []domain.SecurityGroupRule {
	rules := make([]domain.SecurityGroupRule, len(protoRules))
	for i, r := range protoRules {
		rules[i] = domain.SecurityGroupRule{
			Direction:    directionString(r.GetDirection()),
			Protocol:     protocolString(r.GetProtocol()),
			PortRangeMin: r.GetPortRangeMin(),
			PortRangeMax: r.GetPortRangeMax(),
			CIDRBlocks:   r.GetCidrBlocks(),
			Description:  r.GetDescription(),
		}
	}
	return rules
}

func directionProto(d string) pb.Direction {
	switch d {
	case "INGRESS", "DIRECTION_INGRESS":
		return pb.Direction_DIRECTION_INGRESS
	case "EGRESS", "DIRECTION_EGRESS":
		return pb.Direction_DIRECTION_EGRESS
	}
	return pb.Direction_DIRECTION_UNSPECIFIED
}

func directionString(d pb.Direction) string {
	switch d {
	case pb.Direction_DIRECTION_INGRESS:
		return "INGRESS"
	case pb.Direction_DIRECTION_EGRESS:
		return "EGRESS"
	}
	return ""
}

func protocolProto(p string) pb.Protocol {
	switch p {
	case "TCP", "PROTOCOL_TCP":
		return pb.Protocol_PROTOCOL_TCP
	case "UDP", "PROTOCOL_UDP":
		return pb.Protocol_PROTOCOL_UDP
	case "ICMP", "PROTOCOL_ICMP":
		return pb.Protocol_PROTOCOL_ICMP
	}
	return pb.Protocol_PROTOCOL_UNSPECIFIED
}

func protocolString(p pb.Protocol) string {
	switch p {
	case pb.Protocol_PROTOCOL_TCP:
		return "TCP"
	case pb.Protocol_PROTOCOL_UDP:
		return "UDP"
	case pb.Protocol_PROTOCOL_ICMP:
		return "ICMP"
	}
	return ""
}
