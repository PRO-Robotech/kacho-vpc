package service

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-corelib/operations"
	corevalidate "github.com/PRO-Robotech/kacho-corelib/validate"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/dto"
	// type2pb регистрирует DTO-трансферы в init() — нужны для dto.Transfer.
	// Skill evgeniy §3 C.4.
	_ "github.com/PRO-Robotech/kacho-vpc/internal/dto/type2pb"
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
func (s *SecurityGroupService) Get(ctx context.Context, id string) (*domain.SecurityGroupRecord, error) {
	if err := corevalidate.ResourceID("security group", ids.PrefixSecurityGroup, id); err != nil {
		return nil, err
	}
	sg, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	return sg, nil
}

// List возвращает список SG.
// folder_id обязателен (R10 #C1 closure).
func (s *SecurityGroupService) List(ctx context.Context, f SecurityGroupFilter, p Pagination) ([]*domain.SecurityGroupRecord, string, error) {
	if f.FolderID == "" {
		return nil, "", status.Error(codes.InvalidArgument, "folder_id required")
	}
	return s.repo.List(ctx, f, p)
}

// Create создаёт SG (асинхронно через Operation).
//
// Wave 2 batch B (KAC-94): валидация name/description/labels — через
// domain.SecurityGroup.Validate() (skill evgeniy §4 D.5 / AP-1). Service-слой
// больше НЕ вызывает corevalidate.NameVPC/Description/Labels.
func (s *SecurityGroupService) Create(ctx context.Context, req CreateSecurityGroupReq) (*operations.Operation, error) {
	if err := corevalidate.ResourceID("network", ids.PrefixNetwork, req.NetworkID); err != nil {
		return nil, err
	}
	if req.FolderID == "" {
		return nil, status.Error(codes.InvalidArgument, "folder_id required")
	}
	// network_id больше НЕ обязателен (kacho-proto#8): пустой network_id →
	// "глобальная" (folder-level, unbound) security group. Если network_id
	// задан — существование сети проверяется (sync + async backstop в worker'е).

	// Domain self-validation: имя/описание/labels через newtype.Validate() +
	// каждое правило через r.Validate() (description/labels). Cross-cutting
	// rule-валидация (direction, CIDR, protocol) — отдельно через validateSGRule
	// ниже (это не newtype-level).
	sg := domain.SecurityGroup{
		FolderID:    req.FolderID,
		NetworkID:   req.NetworkID,
		Name:        domain.RcNameVPC(req.Name),
		Description: domain.RcDescription(req.Description),
		Labels:      domain.LabelsFromMap(req.Labels),
		Status:      domain.SecurityGroupStatusActive,
		Rules:       req.RuleSpecs,
	}
	if err := sg.Validate(); err != nil {
		return nil, err
	}
	for i, r := range req.RuleSpecs {
		if err := validateSGRule(fmt.Sprintf("rule_specs[%d]", i), r); err != nil {
			return nil, err
		}
	}

	// Verbatim YC: existence / uniqueness checks run synchronously, BEFORE the
	// Operation. The async copies in the worker stay as defensive backstops.
	// См. kacho-vpc#8.
	if err := checkFolderExists(ctx, s.folderClient, req.FolderID); err != nil {
		return nil, err
	}
	if req.NetworkID != "" {
		if _, err := s.networkRepo.Get(ctx, req.NetworkID); err != nil {
			if errors.Is(err, ErrNotFound) {
				return nil, status.Errorf(codes.NotFound, "Network %s not found", req.NetworkID)
			}
			return nil, mapRepoErr(err)
		}
	}
	if req.Name != "" {
		existing, _, lerr := s.repo.List(ctx, SecurityGroupFilter{FolderID: req.FolderID, Name: req.Name}, Pagination{})
		if lerr != nil {
			return nil, mapRepoErr(lerr)
		}
		if len(existing) > 0 {
			return nil, status.Errorf(codes.AlreadyExists, "SecurityGroup with name %s already exists", req.Name)
		}
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
		// Проверяем, что network существует (если задан — пустой network_id
		// означает folder-level / unbound SG, см. kacho-proto#8).
		if req.NetworkID != "" {
			if _, gerr := s.networkRepo.Get(ctx, req.NetworkID); gerr != nil {
				return nil, mapRepoErr(gerr)
			}
		}

		sgDomain := &domain.SecurityGroup{
			ID:          sgID,
			FolderID:    req.FolderID,
			NetworkID:   req.NetworkID,
			Name:        domain.RcNameVPC(req.Name),
			Description: domain.RcDescription(req.Description),
			Labels:      domain.LabelsFromMap(req.Labels),
			Status:      domain.SecurityGroupStatusActive,
			Rules:       assignRuleIDs(req.RuleSpecs),
		}
		created, err := s.repo.Insert(ctx, sgDomain)
		if err != nil {
			return nil, mapRepoErr(err)
		}
		return marshalSecurityGroupRecord(created)
	})

	return &op, nil
}

// marshalSecurityGroupRecord конвертирует repo-entity SG в *anypb.Any через
// DTO-реестр (skill evgeniy §3 C.3 / C.4: protoconv.SecurityGroup → dto.Transfer).
func marshalSecurityGroupRecord(rec *domain.SecurityGroupRecord) (*anypb.Any, error) {
	var dst *vpcv1.SecurityGroup
	if err := dto.Transfer(dto.FromTo(*rec, &dst)); err != nil {
		return nil, fmt.Errorf("dto.Transfer SecurityGroup: %w", err)
	}
	return anypb.New(dst)
}

// CreateDefaultForNetwork создаёт default SG для сети (синхронно — вызывается из Network worker'а).
// Возвращает созданный SG.
//
// Стандартный default SG в YC: 2 правила (INGRESS и EGRESS), protocol ANY, cidr 0.0.0.0/0.
func (s *SecurityGroupService) CreateDefaultForNetwork(ctx context.Context, folderID, networkID string) (*domain.SecurityGroupRecord, error) {
	sg := &domain.SecurityGroup{
		ID:                ids.NewID(ids.PrefixSecurityGroup),
		FolderID:          folderID,
		NetworkID:         networkID,
		Name:              domain.RcNameVPC("default-sg-" + networkID),
		Description:       domain.RcDescription("Default security group for network"),
		Labels:            domain.RcLabels{},
		Status:            domain.SecurityGroupStatusActive,
		DefaultForNetwork: true,
		Rules: []domain.SecurityGroupRule{
			{
				ID:             ids.NewID(ids.PrefixSecurityGroup),
				Direction:      domain.SecurityGroupRuleDirectionIngress,
				ProtocolName:   "ANY",
				ProtocolNumber: -1,
				V4CidrBlocks:   []string{"0.0.0.0/0"},
			},
			{
				ID:             ids.NewID(ids.PrefixSecurityGroup),
				Direction:      domain.SecurityGroupRuleDirectionEgress,
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
	if err := corevalidate.ResourceID("security group", ids.PrefixSecurityGroup, req.SecurityGroupID); err != nil {
		return nil, err
	}
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
		rec, err := s.repo.Get(ctx, req.SecurityGroupID)
		if err != nil {
			return nil, mapRepoErr(err)
		}
		mask := req.UpdateMask
		sg := &rec.SecurityGroup
		if len(mask) == 0 {
			sg.Name = domain.RcNameVPC(req.Name)
			sg.Description = domain.RcDescription(req.Description)
			sg.Labels = domain.LabelsFromMap(req.Labels)
			if req.RuleSpecs != nil {
				sg.Rules = assignRuleIDs(req.RuleSpecs)
			}
		} else {
			for _, f := range mask {
				switch f {
				case "name":
					sg.Name = domain.RcNameVPC(req.Name)
				case "description":
					sg.Description = domain.RcDescription(req.Description)
				case "labels":
					sg.Labels = domain.LabelsFromMap(req.Labels)
				case "rule_specs":
					sg.Rules = assignRuleIDs(req.RuleSpecs)
				}
			}
		}
		updated, err := s.repo.Update(ctx, sg)
		if err != nil {
			return nil, mapRepoErr(err)
		}
		return marshalSecurityGroupRecord(updated)
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
	if err := corevalidate.ResourceID("security group", ids.PrefixSecurityGroup, req.SecurityGroupID); err != nil {
		return nil, err
	}
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
		return marshalSecurityGroupRecord(updated)
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
	if err := corevalidate.ResourceID("security group", ids.PrefixSecurityGroup, req.SecurityGroupID); err != nil {
		return nil, err
	}
	if req.SecurityGroupID == "" {
		return nil, status.Error(codes.InvalidArgument, "security_group_id required")
	}
	if req.RuleID == "" {
		return nil, status.Error(codes.InvalidArgument, "rule_id required")
	}
	// Verbatim YC (probe 2026-05-11, kacho-vpc#10): малформированный rule_id →
	// sync InvalidArgument "Invalid rule id <ruleId>"; несуществующий SG → sync
	// NotFound "Security group SecurityGroup.Id(value=<id>) not found".
	if corevalidate.ResourceID("rule", "", req.RuleID) != nil {
		return nil, status.Errorf(codes.InvalidArgument, "Invalid rule id %s", req.RuleID)
	}
	// Domain self-validation для description/labels.
	if err := domain.RcDescription(req.Description).Validate(); err != nil {
		return nil, err
	}
	if err := domain.ValidateLabels(domain.LabelsFromMap(req.Labels)); err != nil {
		return nil, err
	}
	if _, err := s.repo.Get(ctx, req.SecurityGroupID); err != nil {
		return nil, mapRepoErr(err)
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
		return marshalSecurityGroupRecord(updated)
	})
	return &op, nil
}

// validateSGUpdate — sync-проверка update_mask и значений (как в Network/Subnet).
//
// Wave 2 batch B (KAC-94): name/description/labels — через newtype.Validate()
// (skill evgeniy §4 D.5 / AP-1). Mask-семантика — через corevalidate.UpdateMask.
//
// Decision table (для каждого поля в mask):
//   - name        → RcNameVPC.Validate() (verbatim YC permissive name regex).
//   - description → RcDescription.Validate() (≤256 chars utf-8).
//   - labels      → domain.ValidateLabels() (≤64 пары, key/value verbatim YC).
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
			if err := domain.RcNameVPC(req.Name).Validate(); err != nil {
				return err
			}
		case "description":
			if err := domain.RcDescription(req.Description).Validate(); err != nil {
				return err
			}
		case "labels":
			if err := domain.ValidateLabels(domain.LabelsFromMap(req.Labels)); err != nil {
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
//
// Direction-семантика и CIDR host-bits — это cross-field invariants, не
// newtype-level (description/labels валидируются через r.Validate() внутри
// SecurityGroup.Validate()).
func validateSGRule(field string, r domain.SecurityGroupRule) error {
	if r.Direction != domain.SecurityGroupRuleDirectionIngress && r.Direction != domain.SecurityGroupRuleDirectionEgress {
		return invalidArg(field+".direction", "direction must be INGRESS or EGRESS")
	}
	if err := r.Description.Validate(); err != nil {
		return err
	}
	if err := domain.ValidateLabels(domain.LabelsFromMap(r.Labels)); err != nil {
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
	if err := corevalidate.ResourceID("security group", ids.PrefixSecurityGroup, id); err != nil {
		return nil, err
	}
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
	if err := corevalidate.ResourceID("security group", ids.PrefixSecurityGroup, id); err != nil {
		return nil, err
	}
	if id == "" {
		return nil, status.Error(codes.InvalidArgument, "security_group_id required")
	}
	if destFolderID == "" {
		return nil, invalidArg("destination_folder_id", "destination_folder_id is required")
	}
	cur, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	if err := checkMoveDestination(ctx, s.folderClient, cur.FolderID, destFolderID); err != nil {
		return nil, err
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
		return marshalSecurityGroupRecord(updated)
	})
	return &op, nil
}

// ListOperations возвращает операции для конкретного SG.
func (s *SecurityGroupService) ListOperations(ctx context.Context, id string, p Pagination) ([]operations.Operation, string, error) {
	if err := corevalidate.ResourceID("security group", ids.PrefixSecurityGroup, id); err != nil {
		return nil, "", err
	}
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
