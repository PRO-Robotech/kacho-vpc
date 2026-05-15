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

// UpdateRuleInput — параметры UpdateRule: обновить description/labels единичного
// rule. UpdateMask разрешает частичный апдейт; пустой mask = full PATCH (только
// description+labels, других mutable полей у rule нет).
type UpdateRuleInput struct {
	SecurityGroupID string
	RuleID          string
	Description     string
	Labels          map[string]string
	UpdateMask      []string
}

// UpdateRuleUseCase обновляет description/labels единичного правила в SG.
//
// Verbatim YC (probe 2026-05-11, kacho-vpc#10): результат RPC — Operation,
// response — **parent SG** (не SecurityGroupRule). YC CLI 1.x hardcodes
// expectation на SecurityGroup, поэтому worker возвращает marshalled SG. См.
// finding SG-UPDATERULE-RESPONSE-TYPE-MISMATCH.md.
//
// Skill evgeniy §2 B.1: SG-специфика — отдельный use-case рядом с handler'ом
// (response-type расходится с обычным Update, и input-тип тоже свой).
type UpdateRuleUseCase struct {
	repo    SecurityGroupRepo
	opsRepo operations.Repo
}

// NewUpdateRuleUseCase создаёт UpdateRuleUseCase.
func NewUpdateRuleUseCase(repo SecurityGroupRepo, opsRepo operations.Repo) *UpdateRuleUseCase {
	return &UpdateRuleUseCase{repo: repo, opsRepo: opsRepo}
}

// Execute — sync-валидация id и domain self-validation
// description/labels + Operation + async repo.UpdateRule.
func (u *UpdateRuleUseCase) Execute(ctx context.Context, in UpdateRuleInput) (*operations.Operation, error) {
	if err := corevalidate.ResourceID("security group", ids.PrefixSecurityGroup, in.SecurityGroupID); err != nil {
		return nil, err
	}
	if in.SecurityGroupID == "" {
		return nil, status.Error(codes.InvalidArgument, "security_group_id required")
	}
	if in.RuleID == "" {
		return nil, status.Error(codes.InvalidArgument, "rule_id required")
	}
	// Verbatim YC (probe 2026-05-11, kacho-vpc#10): малформированный rule_id →
	// sync InvalidArgument "Invalid rule id <ruleId>"; несуществующий SG → sync
	// NotFound через repo.Get ниже.
	if corevalidate.ResourceID("rule", "", in.RuleID) != nil {
		return nil, status.Errorf(codes.InvalidArgument, "Invalid rule id %s", in.RuleID)
	}
	// Domain self-validation для description/labels (skill evgeniy §4 D.5 / AP-1).
	if err := domain.RcDescription(in.Description).Validate(); err != nil {
		return nil, err
	}
	if err := domain.ValidateLabels(domain.LabelsFromMap(in.Labels)); err != nil {
		return nil, err
	}
	if _, err := u.repo.Get(ctx, in.SecurityGroupID); err != nil {
		return nil, mapRepoErr(err)
	}

	op, err := operations.New(
		ids.PrefixOperationVPC,
		fmt.Sprintf("Update rule %s of security group %s", in.RuleID, in.SecurityGroupID),
		&vpcv1.UpdateSecurityGroupRuleMetadata{
			SecurityGroupId: in.SecurityGroupID,
			RuleId:          in.RuleID,
		},
	)
	if err != nil {
		return nil, err
	}
	if err := u.opsRepo.Create(ctx, op); err != nil {
		return nil, err
	}

	operations.Run(ctx, u.opsRepo, op.ID, func(ctx context.Context) (*anypb.Any, error) {
		updated, err := u.repo.UpdateRule(ctx, in.SecurityGroupID, in.RuleID, in.Description, in.Labels, in.UpdateMask)
		if err != nil {
			return nil, mapRepoErr(err)
		}
		// Response — parent SecurityGroup (verbatim YC CLI 1.x compat).
		// CLI hardcodes expectation на SecurityGroup, не SecurityGroupRule.
		return marshalSecurityGroupRecord(updated)
	})
	return &op, nil
}
