package service

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-corelib/operations"
	corevalidate "github.com/PRO-Robotech/kacho-corelib/validate"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/protoconv"
)

// SecurityGroupService — бизнес-логика SG.
type SecurityGroupService struct {
	repo         SecurityGroupRepo
	networkRepo  NetworkRepo
	folderClient FolderClient
	opsRepo      operations.Repo
}

// NewSecurityGroupService создаёт SecurityGroupService.
func NewSecurityGroupService(repo SecurityGroupRepo, networkRepo NetworkRepo, folderClient FolderClient, opsRepo operations.Repo) *SecurityGroupService {
	return &SecurityGroupService{
		repo:         repo,
		networkRepo:  networkRepo,
		folderClient: folderClient,
		opsRepo:      opsRepo,
	}
}

// CreateSecurityGroupReq — запрос на создание SG.
type CreateSecurityGroupReq struct {
	FolderID    string
	Name        string
	Description string
	Labels      map[string]string
	NetworkID   string
	RuleSpecs   []domain.SecurityGroupRule
}

// UpdateSecurityGroupReq — запрос на обновление SG.
type UpdateSecurityGroupReq struct {
	SecurityGroupID string
	Name            string
	Description     string
	Labels          map[string]string
	RuleSpecs       []domain.SecurityGroupRule
	UpdateMask      []string
}

// Get возвращает SG.
func (s *SecurityGroupService) Get(ctx context.Context, id string) (*domain.SecurityGroup, error) {
	sg, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	return sg, nil
}

// List возвращает список SG.
// folder_id обязателен (R10 #C1 closure).
func (s *SecurityGroupService) List(ctx context.Context, f SecurityGroupFilter, p Pagination) ([]*domain.SecurityGroup, string, error) {
	if f.FolderID == "" {
		return nil, "", status.Error(codes.InvalidArgument, "folder_id required")
	}
	return s.repo.List(ctx, f, p)
}

// Create создаёт SG (асинхронно через Operation).
func (s *SecurityGroupService) Create(ctx context.Context, req CreateSecurityGroupReq) (*operations.Operation, error) {
	if req.FolderID == "" {
		return nil, status.Error(codes.InvalidArgument, "folder_id required")
	}
	if req.NetworkID == "" {
		return nil, invalidArg("network_id", "network_id is required")
	}
	if err := corevalidate.NameVPC("name", req.Name); err != nil {
		return nil, err
	}
	if err := corevalidate.Description("description", req.Description); err != nil {
		return nil, err
	}
	if err := corevalidate.Labels("labels", req.Labels); err != nil {
		return nil, err
	}

	sgID := ids.NewID(ids.PrefixSecurityGroup)
	op, err := operations.New(ids.PrefixOperationVPC,
		fmt.Sprintf("Create security group %s", req.Name),
		&vpcv1.CreateSecurityGroupMetadata{SecurityGroupId: sgID},
	)
	if err != nil {
		return nil, err
	}
	if err := s.opsRepo.Create(ctx, op); err != nil {
		return nil, err
	}

	operations.Run(ctx, s.opsRepo, op.ID, func(ctx context.Context) (*anypb.Any, error) {
		exists, err := s.folderClient.Exists(ctx, req.FolderID)
		if err != nil {
			return nil, status.Errorf(codes.Unavailable, "folder check: %v", err)
		}
		if !exists {
			return nil, status.Errorf(codes.NotFound, "Folder with id %s not found", req.FolderID)
		}
		// Проверяем, что network существует
		if _, gerr := s.networkRepo.Get(ctx, req.NetworkID); gerr != nil {
			return nil, mapRepoErr(gerr)
		}

		sg := &domain.SecurityGroup{
			ID:          sgID,
			FolderID:    req.FolderID,
			NetworkID:   req.NetworkID,
			CreatedAt:   time.Now().UTC(),
			Name:        req.Name,
			Description: req.Description,
			Labels:      req.Labels,
			Status:      "ACTIVE",
			Rules:       assignRuleIDs(req.RuleSpecs),
		}
		created, err := s.repo.Insert(ctx, sg)
		if err != nil {
			return nil, mapRepoErr(err)
		}
		return anypb.New(protoconv.SecurityGroup(created))
	})

	return &op, nil
}

// CreateDefaultForNetwork создаёт default SG для сети (синхронно — вызывается из Network worker'а).
// Возвращает созданный SG.
//
// Стандартный default SG в YC: 2 правила (INGRESS и EGRESS), protocol ANY, cidr 0.0.0.0/0.
func (s *SecurityGroupService) CreateDefaultForNetwork(ctx context.Context, folderID, networkID string) (*domain.SecurityGroup, error) {
	sg := &domain.SecurityGroup{
		ID:                ids.NewID(ids.PrefixSecurityGroup),
		FolderID:          folderID,
		NetworkID:         networkID,
		CreatedAt:         time.Now().UTC(),
		Name:              "default-sg-" + networkID,
		Description:       "Default security group for network",
		Labels:            nil,
		Status:            "ACTIVE",
		DefaultForNetwork: true,
		Rules: []domain.SecurityGroupRule{
			{
				ID:             ids.NewID(ids.PrefixSecurityGroup),
				Direction:      "INGRESS",
				ProtocolName:   "ANY",
				ProtocolNumber: -1,
				V4CidrBlocks:   []string{"0.0.0.0/0"},
			},
			{
				ID:             ids.NewID(ids.PrefixSecurityGroup),
				Direction:      "EGRESS",
				ProtocolName:   "ANY",
				ProtocolNumber: -1,
				V4CidrBlocks:   []string{"0.0.0.0/0"},
			},
		},
	}
	created, err := s.repo.Insert(ctx, sg)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	return created, nil
}

// Update обновляет SG.
func (s *SecurityGroupService) Update(ctx context.Context, req UpdateSecurityGroupReq) (*operations.Operation, error) {
	if req.SecurityGroupID == "" {
		return nil, status.Error(codes.InvalidArgument, "security_group_id required")
	}
	if err := validateSGUpdate(req); err != nil {
		return nil, err
	}
	op, err := operations.New(ids.PrefixOperationVPC,
		fmt.Sprintf("Update security group %s", req.SecurityGroupID),
		&vpcv1.UpdateSecurityGroupMetadata{SecurityGroupId: req.SecurityGroupID},
	)
	if err != nil {
		return nil, err
	}
	if err := s.opsRepo.Create(ctx, op); err != nil {
		return nil, err
	}

	operations.Run(ctx, s.opsRepo, op.ID, func(ctx context.Context) (*anypb.Any, error) {
		sg, err := s.repo.Get(ctx, req.SecurityGroupID)
		if err != nil {
			return nil, mapRepoErr(err)
		}
		mask := req.UpdateMask
		if len(mask) == 0 {
			sg.Name = req.Name
			sg.Description = req.Description
			sg.Labels = req.Labels
			if req.RuleSpecs != nil {
				sg.Rules = assignRuleIDs(req.RuleSpecs)
			}
		} else {
			for _, f := range mask {
				switch f {
				case "name":
					sg.Name = req.Name
				case "description":
					sg.Description = req.Description
				case "labels":
					sg.Labels = req.Labels
				case "rule_specs":
					sg.Rules = assignRuleIDs(req.RuleSpecs)
				}
			}
		}
		updated, err := s.repo.Update(ctx, sg)
		if err != nil {
			return nil, mapRepoErr(err)
		}
		return anypb.New(protoconv.SecurityGroup(updated))
	})
	return &op, nil
}

// UpdateRulesReq — параметры UpdateRules: атомарно удалить правила deletionRuleIDs
// и добавить additionRuleSpecs (присвоит новые ID).
type UpdateRulesReq struct {
	SecurityGroupID   string
	DeletionRuleIDs   []string
	AdditionRuleSpecs []domain.SecurityGroupRule
}

// UpdateRules заменяет набор правил SG атомарно через Operation.
//
// YC verbatim: result — Operation, response — обновлённый SG.
// Sync-валидация: каждое правило (direction, protocol, ports, cidr или sgRef).
func (s *SecurityGroupService) UpdateRules(ctx context.Context, req UpdateRulesReq) (*operations.Operation, error) {
	if req.SecurityGroupID == "" {
		return nil, status.Error(codes.InvalidArgument, "security_group_id required")
	}
	for i, r := range req.AdditionRuleSpecs {
		if err := validateSGRule(fmt.Sprintf("addition_rule_specs[%d]", i), r); err != nil {
			return nil, err
		}
	}
	op, err := operations.New(ids.PrefixOperationVPC,
		fmt.Sprintf("Update rules of security group %s", req.SecurityGroupID),
		&vpcv1.UpdateSecurityGroupMetadata{SecurityGroupId: req.SecurityGroupID},
	)
	if err != nil {
		return nil, err
	}
	if err := s.opsRepo.Create(ctx, op); err != nil {
		return nil, err
	}
	operations.Run(ctx, s.opsRepo, op.ID, func(ctx context.Context) (*anypb.Any, error) {
		add := assignRuleIDs(req.AdditionRuleSpecs)
		updated, err := s.repo.UpdateRules(ctx, req.SecurityGroupID, req.DeletionRuleIDs, add)
		if err != nil {
			return nil, mapRepoErr(err)
		}
		return anypb.New(protoconv.SecurityGroup(updated))
	})
	return &op, nil
}

// UpdateRuleReq — параметры UpdateRule: обновить description/labels единичного rule.
type UpdateRuleReq struct {
	SecurityGroupID string
	RuleID          string
	Description     string
	Labels          map[string]string
	UpdateMask      []string
}

// UpdateRule обновляет description/labels единичного правила.
func (s *SecurityGroupService) UpdateRule(ctx context.Context, req UpdateRuleReq) (*operations.Operation, error) {
	if req.SecurityGroupID == "" {
		return nil, status.Error(codes.InvalidArgument, "security_group_id required")
	}
	if req.RuleID == "" {
		return nil, status.Error(codes.InvalidArgument, "rule_id required")
	}
	if err := corevalidate.Description("description", req.Description); err != nil {
		return nil, err
	}
	if err := corevalidate.Labels("labels", req.Labels); err != nil {
		return nil, err
	}
	op, err := operations.New(ids.PrefixOperationVPC,
		fmt.Sprintf("Update rule %s of security group %s", req.RuleID, req.SecurityGroupID),
		&vpcv1.UpdateSecurityGroupRuleMetadata{
			SecurityGroupId: req.SecurityGroupID,
			RuleId:          req.RuleID,
		},
	)
	if err != nil {
		return nil, err
	}
	if err := s.opsRepo.Create(ctx, op); err != nil {
		return nil, err
	}
	operations.Run(ctx, s.opsRepo, op.ID, func(ctx context.Context) (*anypb.Any, error) {
		updated, err := s.repo.UpdateRule(ctx, req.SecurityGroupID, req.RuleID, req.Description, req.Labels, req.UpdateMask)
		if err != nil {
			return nil, mapRepoErr(err)
		}
		// Response — parent SecurityGroup (verbatim YC CLI 1.x compat).
		// CLI hardcodes expectation на SecurityGroup, не SecurityGroupRule.
		// См. finding SG-UPDATERULE-RESPONSE-TYPE-MISMATCH.md.
		return anypb.New(protoconv.SecurityGroup(updated))
	})
	return &op, nil
}

// validateSGUpdate — sync-проверка update_mask и значений (как в Network/Subnet).
//
// Decision table (для каждого поля в mask):
//   - name        → corevalidate.NameVPC (permissive: empty + uppercase + underscore).
//   - description → ≤256 chars utf-8.
//   - labels      → ≤64 пары, key/value verbatim YC.
//   - rule_specs  → каждое правило проходит validateSGRule.
//
// Поле, не упомянутое в mask, не валидируется (= unchanged). Unknown field в
// update_mask → InvalidArgument (corevalidate.UpdateMask).
func validateSGUpdate(req UpdateSecurityGroupReq) error {
	known := map[string]struct{}{
		"name": {}, "description": {}, "labels": {}, "rule_specs": {},
	}
	if err := corevalidate.UpdateMask("update_mask", req.UpdateMask, known); err != nil {
		return err
	}
	updates := req.UpdateMask
	if len(updates) == 0 {
		updates = []string{"name", "description", "labels"}
	}
	for _, f := range updates {
		switch f {
		case "name":
			if err := corevalidate.NameVPC("name", req.Name); err != nil {
				return err
			}
		case "description":
			if err := corevalidate.Description("description", req.Description); err != nil {
				return err
			}
		case "labels":
			if err := corevalidate.Labels("labels", req.Labels); err != nil {
				return err
			}
		case "rule_specs":
			for i, r := range req.RuleSpecs {
				if err := validateSGRule(fmt.Sprintf("rule_specs[%d]", i), r); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// validateSGRule — sync-валидация правила.
func validateSGRule(field string, r domain.SecurityGroupRule) error {
	if r.Direction != "INGRESS" && r.Direction != "EGRESS" {
		return invalidArg(field+".direction", "direction must be INGRESS or EGRESS")
	}
	if err := corevalidate.Description(field+".description", r.Description); err != nil {
		return err
	}
	for i, c := range r.V4CidrBlocks {
		if err := validateCIDRPrefix(fmt.Sprintf("%s.cidr_blocks.v4_cidr_blocks[%d]", field, i), c); err != nil {
			return err
		}
	}
	return nil
}

// Delete удаляет SG. Default SG нельзя удалить (вернёт FAILED_PRECONDITION).
func (s *SecurityGroupService) Delete(ctx context.Context, id string) (*operations.Operation, error) {
	if id == "" {
		return nil, status.Error(codes.InvalidArgument, "security_group_id required")
	}
	// pre-flight check: default SG защищён
	existing, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	if existing.DefaultForNetwork {
		return nil, status.Errorf(codes.FailedPrecondition, "default security group cannot be deleted")
	}

	op, err := operations.New(ids.PrefixOperationVPC,
		fmt.Sprintf("Delete security group %s", id),
		&vpcv1.DeleteSecurityGroupMetadata{SecurityGroupId: id},
	)
	if err != nil {
		return nil, err
	}
	if err := s.opsRepo.Create(ctx, op); err != nil {
		return nil, err
	}
	operations.Run(ctx, s.opsRepo, op.ID, func(ctx context.Context) (*anypb.Any, error) {
		if err := s.repo.Delete(ctx, id); err != nil {
			return nil, mapRepoErr(err)
		}
		return anypb.New(&emptypb.Empty{})
	})
	return &op, nil
}

// Move инициирует перенос SG в другой folder.
func (s *SecurityGroupService) Move(ctx context.Context, id, destFolderID string) (*operations.Operation, error) {
	if id == "" {
		return nil, status.Error(codes.InvalidArgument, "security_group_id required")
	}
	if destFolderID == "" {
		return nil, invalidArg("destination_folder_id", "destination_folder_id is required")
	}
	op, err := operations.New(ids.PrefixOperationVPC, fmt.Sprintf("Move security group %s", id),
		&vpcv1.MoveSecurityGroupMetadata{SecurityGroupId: id})
	if err != nil {
		return nil, err
	}
	if err := s.opsRepo.Create(ctx, op); err != nil {
		return nil, err
	}
	operations.Run(ctx, s.opsRepo, op.ID, func(ctx context.Context) (*anypb.Any, error) {
		exists, err := s.folderClient.Exists(ctx, destFolderID)
		if err != nil {
			return nil, status.Errorf(codes.Unavailable, "folder check: %v", err)
		}
		if !exists {
			return nil, status.Errorf(codes.NotFound, "Folder with id %s not found", destFolderID)
		}
		updated, err := s.repo.SetFolderID(ctx, id, destFolderID)
		if err != nil {
			return nil, mapRepoErr(err)
		}
		return anypb.New(protoconv.SecurityGroup(updated))
	})
	return &op, nil
}

// ListOperations возвращает операции для конкретного SG.
func (s *SecurityGroupService) ListOperations(ctx context.Context, id string, p Pagination) ([]operations.Operation, string, error) {
	if _, err := s.repo.Get(ctx, id); err != nil {
		return nil, "", mapRepoErr(err)
	}
	return s.opsRepo.List(ctx, operations.ListFilter{
		ResourceID: id,
		PageSize:   p.PageSize,
		PageToken:  p.PageToken,
	})
}

// assignRuleIDs присваивает каждому rule UID если он пустой.
func assignRuleIDs(rules []domain.SecurityGroupRule) []domain.SecurityGroupRule {
	out := make([]domain.SecurityGroupRule, len(rules))
	for i, r := range rules {
		if r.ID == "" {
			r.ID = ids.NewID(ids.PrefixSecurityGroup)
		}
		out[i] = r
	}
	return out
}
