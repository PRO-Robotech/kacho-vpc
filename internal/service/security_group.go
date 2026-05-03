package service

import (
	"context"

	coreerrors "github.com/PRO-Robotech/kacho-corelib/errors"
	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-corelib/operations"
	pb "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"
)

// SecurityGroupService реализует use-cases для SecurityGroup.
type SecurityGroupService struct {
	repo         SecurityGroupRepo
	networkRepo  NetworkRepo
	opsRepo      operations.Repo
	folderClient FolderClient
}

// NewSecurityGroupService создаёт SecurityGroupService.
func NewSecurityGroupService(r SecurityGroupRepo, nr NetworkRepo, ops operations.Repo, fc FolderClient) *SecurityGroupService {
	return &SecurityGroupService{repo: r, networkRepo: nr, opsRepo: ops, folderClient: fc}
}

// Get возвращает SecurityGroup по ID (синхронный).
func (s *SecurityGroupService) Get(ctx context.Context, id string) (*domain.SecurityGroup, error) {
	if id == "" {
		return nil, coreerrors.InvalidArgument().AddFieldViolation("security_group_id", "security_group_id is required").Err()
	}
	sg, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if sg == nil {
		return nil, coreerrors.NotFound("SecurityGroup", id).Err()
	}
	return sg, nil
}

// List возвращает список SecurityGroup (синхронный).
func (s *SecurityGroupService) List(ctx context.Context, filter ListFilter) ([]domain.SecurityGroup, string, error) {
	return s.repo.List(ctx, filter)
}

// Create создаёт SecurityGroup асинхронно.
func (s *SecurityGroupService) Create(ctx context.Context, folderID, networkID, name, description string, labels map[string]string, rules []domain.SecurityGroupRule) (*operations.Operation, error) {
	if folderID == "" {
		return nil, coreerrors.InvalidArgument().AddFieldViolation("folder_id", "folder_id is required").Err()
	}
	if networkID == "" {
		return nil, coreerrors.InvalidArgument().AddFieldViolation("network_id", "network_id is required").Err()
	}
	if name == "" {
		return nil, coreerrors.InvalidArgument().AddFieldViolation("name", "name is required").Err()
	}
	if err := validateSGRules(rules); err != nil {
		return nil, err
	}

	resourceID := ids.NewUID()
	op, err := operations.New("Create SecurityGroup "+name, &pb.CreateSecurityGroupMetadata{SecurityGroupId: resourceID})
	if err != nil {
		return nil, err
	}
	if err := s.opsRepo.Create(ctx, op); err != nil {
		return nil, err
	}

	operations.Run(ctx, s.opsRepo, op.ID, func(ctx context.Context) (*anypb.Any, error) {
		exists, ferr := s.folderClient.Exists(ctx, folderID)
		if ferr != nil {
			return nil, ferr
		}
		if !exists {
			return nil, status.Errorf(codes.FailedPrecondition, "Folder %s not found", folderID)
		}

		net, nerr := s.networkRepo.Get(ctx, networkID)
		if nerr != nil {
			return nil, nerr
		}
		if net == nil {
			return nil, status.Errorf(codes.FailedPrecondition, "Network %s not found", networkID)
		}

		// Assign server-side UUIDs to rules
		assigned := assignRuleIDs(rules)

		sg := &domain.SecurityGroup{
			ID:          resourceID,
			FolderID:    folderID,
			NetworkID:   networkID,
			Name:        name,
			Description: description,
			Labels:      labels,
			Status:      domain.SecurityGroupStatusProvisioning,
			Generation:  1,
			Rules:       assigned,
		}
		if cerr := s.repo.Create(ctx, sg); cerr != nil {
			return nil, cerr
		}

		created, gerr := s.repo.Get(ctx, resourceID)
		if gerr != nil {
			return nil, gerr
		}
		created.Status = domain.SecurityGroupStatusActive
		if uerr := s.repo.Update(ctx, created); uerr != nil {
			return nil, uerr
		}
		final, gerr := s.repo.Get(ctx, resourceID)
		if gerr != nil {
			return nil, gerr
		}
		return anypb.New(domainSGToProto(final))
	})

	return &op, nil
}

// Update обновляет SecurityGroup асинхронно (rules — full-replace).
func (s *SecurityGroupService) Update(ctx context.Context, sgID, resourceVersion, name, description string, labels map[string]string, rules []domain.SecurityGroupRule, updateMask []string) (*operations.Operation, error) {
	if sgID == "" {
		return nil, coreerrors.InvalidArgument().AddFieldViolation("security_group_id", "security_group_id is required").Err()
	}
	if err := validateSGRules(rules); err != nil {
		return nil, err
	}

	existing, err := s.repo.Get(ctx, sgID)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, coreerrors.NotFound("SecurityGroup", sgID).Err()
	}

	op, err := operations.New("Update SecurityGroup "+sgID, &pb.UpdateSecurityGroupMetadata{SecurityGroupId: sgID})
	if err != nil {
		return nil, err
	}
	if err := s.opsRepo.Create(ctx, op); err != nil {
		return nil, err
	}

	operations.Run(ctx, s.opsRepo, op.ID, func(ctx context.Context) (*anypb.Any, error) {
		cur, gerr := s.repo.Get(ctx, sgID)
		if gerr != nil {
			return nil, gerr
		}
		if cur == nil {
			return nil, status.Errorf(codes.NotFound, "SecurityGroup %s not found", sgID)
		}
		if resourceVersion != "" && cur.ResourceVersion != resourceVersion {
			return nil, status.Errorf(codes.Aborted, "resource_version mismatch: expected %s, got %s", resourceVersion, cur.ResourceVersion)
		}

		applySGUpdateMask(cur, name, description, labels, rules, updateMask)
		cur.Generation++
		if uerr := s.repo.Update(ctx, cur); uerr != nil {
			return nil, uerr
		}
		final, gerr := s.repo.Get(ctx, sgID)
		if gerr != nil {
			return nil, gerr
		}
		return anypb.New(domainSGToProto(final))
	})

	return &op, nil
}

// Delete удаляет SecurityGroup асинхронно.
func (s *SecurityGroupService) Delete(ctx context.Context, sgID string) (*operations.Operation, error) {
	if sgID == "" {
		return nil, coreerrors.InvalidArgument().AddFieldViolation("security_group_id", "security_group_id is required").Err()
	}

	existing, err := s.repo.Get(ctx, sgID)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, coreerrors.NotFound("SecurityGroup", sgID).Err()
	}

	op, err := operations.New("Delete SecurityGroup "+sgID, &pb.DeleteSecurityGroupMetadata{SecurityGroupId: sgID})
	if err != nil {
		return nil, err
	}
	if err := s.opsRepo.Create(ctx, op); err != nil {
		return nil, err
	}

	operations.Run(ctx, s.opsRepo, op.ID, func(ctx context.Context) (*anypb.Any, error) {
		if derr := s.repo.SoftDelete(ctx, sgID); derr != nil {
			return nil, derr
		}
		return anypb.New(&pb.DeleteSecurityGroupMetadata{SecurityGroupId: sgID})
	})

	return &op, nil
}

// assignRuleIDs назначает server-side UUID каждому правилу.
func assignRuleIDs(rules []domain.SecurityGroupRule) []domain.SecurityGroupRule {
	result := make([]domain.SecurityGroupRule, len(rules))
	for i, r := range rules {
		r.ID = ids.NewUID()
		result[i] = r
	}
	return result
}

// validateSGRules проверяет CIDR-блоки в правилах.
func validateSGRules(rules []domain.SecurityGroupRule) error {
	for i, r := range rules {
		for _, cidr := range r.CIDRBlocks {
			if err := validateCIDR(cidr); err != nil {
				return status.Errorf(codes.InvalidArgument, "rules[%d].cidr_blocks: %v", i, err)
			}
		}
	}
	return nil
}

func applySGUpdateMask(sg *domain.SecurityGroup, name, description string, labels map[string]string, rules []domain.SecurityGroupRule, mask []string) {
	if len(mask) == 0 {
		sg.Name = name
		sg.Description = description
		sg.Labels = labels
		sg.Rules = assignRuleIDs(rules)
		return
	}
	for _, f := range mask {
		switch f {
		case "name":
			sg.Name = name
		case "description":
			sg.Description = description
		case "labels":
			sg.Labels = labels
		case "rules":
			sg.Rules = assignRuleIDs(rules)
		}
	}
}

func domainSGToProto(sg *domain.SecurityGroup) *pb.SecurityGroup {
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
		proto.CreatedAt = timestampProto(sg.CreatedAt)
	}
	return proto
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
