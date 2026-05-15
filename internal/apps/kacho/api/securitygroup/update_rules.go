package securitygroup

import (
	"context"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-corelib/operations"
	corevalidate "github.com/PRO-Robotech/kacho-corelib/validate"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// UpdateRulesInput — split-endpoint: атомарно удалить deletion_rule_ids и
// добавить addition_rule_specs. Каждому новому правилу будет присвоен ID, если
// caller его не задал.
type UpdateRulesInput struct {
	SecurityGroupID   string
	DeletionRuleIDs   []string
	AdditionRuleSpecs []domain.SecurityGroupRule
}

// UpdateRulesUseCase атомарно заменяет набор правил SG (split-endpoint).
// Result — Operation, response — обновлённый SG (parent).
//
// OCC через `xmin::text` живёт в repo-слое (`security_group_repo.go`,
// `security_group_occ_integration_test.go`) — use-case просто вызывает
// repo.UpdateRules; concurrent UpdateRules с устаревшим snapshot отвергается
// ports.ErrFailedPrecondition → FailedPrecondition.
//
// Skill evgeniy §2 B.1: вынесено из SecurityGroupService в отдельный use-case
// (SG-специфика: split-endpoint требует собственный input-тип, не масштабируется
// через общий Update).
type UpdateRulesUseCase struct {
	repo    SecurityGroupRepo
	opsRepo operations.Repo
}

// NewUpdateRulesUseCase создаёт UpdateRulesUseCase.
func NewUpdateRulesUseCase(repo SecurityGroupRepo, opsRepo operations.Repo) *UpdateRulesUseCase {
	return &UpdateRulesUseCase{repo: repo, opsRepo: opsRepo}
}

// Execute — sync-валидация правил + Operation + async repo.UpdateRules.
func (u *UpdateRulesUseCase) Execute(ctx context.Context, in UpdateRulesInput) (*operations.Operation, error) {
	if err := corevalidate.ResourceID("security group", ids.PrefixSecurityGroup, in.SecurityGroupID); err != nil {
		return nil, err
	}
	if in.SecurityGroupID == "" {
		return nil, status.Error(codes.InvalidArgument, "security_group_id required")
	}
	for i, r := range in.AdditionRuleSpecs {
		if err := validateSGRule(fmt.Sprintf("addition_rule_specs[%d]", i), r); err != nil {
			return nil, err
		}
	}

	op, err := operations.New(
		ids.PrefixOperationVPC,
		fmt.Sprintf("Update rules of security group %s", in.SecurityGroupID),
		&vpcv1.UpdateSecurityGroupMetadata{SecurityGroupId: in.SecurityGroupID},
	)
	if err != nil {
		return nil, err
	}
	if err := u.opsRepo.Create(ctx, op); err != nil {
		return nil, err
	}

	operations.Run(ctx, u.opsRepo, op.ID, func(ctx context.Context) (*anypb.Any, error) {
		add := assignRuleIDs(in.AdditionRuleSpecs)
		updated, err := u.repo.UpdateRules(ctx, in.SecurityGroupID, in.DeletionRuleIDs, add)
		if err != nil {
			return nil, mapRepoErr(err)
		}
		return marshalSecurityGroupRecord(updated)
	})
	return &op, nil
}
