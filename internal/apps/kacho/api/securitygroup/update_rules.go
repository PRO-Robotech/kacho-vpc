package securitygroup

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-corelib/operations"
	corevalidate "github.com/PRO-Robotech/kacho-corelib/validate"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
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
// repo.ErrFailedPrecondition → FailedPrecondition.
//
// Skill evgeniy §2 B.1: вынесено из SecurityGroupService в отдельный use-case
// (SG-специфика: split-endpoint требует собственный input-тип, не масштабируется
// через общий Update).
type UpdateRulesUseCase struct {
	repo     Repo
	opsRepo  operations.Repo
	sgReader SecurityGroupReader
}

// NewUpdateRulesUseCase создаёт UpdateRulesUseCase.
//
// sgReader (KAC-243 §C / D4 net-new wiring) резолвит network_id редактируемой SG
// + каждой target-SG для same-network-валидации SG-target-правил. Composition-root
// инжектит `cqrsadapter.SecurityGroupAdapter`; nil = валидация пропускается.
func NewUpdateRulesUseCase(r Repo, opsRepo operations.Repo, sgReader SecurityGroupReader) *UpdateRulesUseCase {
	return &UpdateRulesUseCase{repo: r, opsRepo: opsRepo, sgReader: sgReader}
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

	// Same-network-валидация SG-target-правил (KAC-243 §C, D3/D4): sync fast-fail.
	addFieldFor := func(i int) string { return fmt.Sprintf("addition_rule_specs[%d].security_group_id", i) }
	if err := u.validateAdditionsSameNetwork(ctx, in.SecurityGroupID, in.AdditionRuleSpecs, addFieldFor); err != nil {
		return nil, err
	}

	op, err := operations.NewFromContext(
		ctx,
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
		// Async backstop для same-network SG-target-правил (KAC-243 §C, D4).
		if verr := u.validateAdditionsSameNetwork(ctx, in.SecurityGroupID, in.AdditionRuleSpecs, addFieldFor); verr != nil {
			return nil, verr
		}
		add := assignRuleIDs(in.AdditionRuleSpecs)
		w, werr := u.repo.Writer(ctx)
		if werr != nil {
			return nil, mapRepoErr(werr)
		}
		defer w.Abort()
		updated, uerr := w.SecurityGroups().UpdateRules(ctx, in.SecurityGroupID, in.DeletionRuleIDs, add)
		if uerr != nil {
			return nil, mapRepoErr(uerr)
		}
		if oerr := w.Outbox().Emit(ctx, "SecurityGroup", updated.ID, "UPDATED", securityGroupPayloadMap(updated)); oerr != nil {
			return nil, mapRepoErr(fmt.Errorf("%w: outbox emit: %v", repo.ErrInternal, oerr))
		}
		if cerr := w.Commit(); cerr != nil {
			return nil, mapRepoErr(cerr)
		}
		return marshalSecurityGroupRecord(updated)
	})
	return &op, nil
}

// validateAdditionsSameNetwork — резолвит network_id редактируемой SG и
// проверяет, что каждое добавляемое SG-target-правило ссылается на SG из той же
// сети (KAC-243 §C, D3). Если у addition'ов нет SG-target-правил — no-op
// (никакого lookup). Если редактируемая SG не найдена — НЕ ошибка здесь:
// existing flow вернёт NotFound из repo.UpdateRules в worker'е (verbatim YC).
func (u *UpdateRulesUseCase) validateAdditionsSameNetwork(ctx context.Context, sgID string, additions []domain.SecurityGroupRule, fieldFor func(i int) string) error {
	if u.sgReader == nil {
		return nil
	}
	hasTarget := false
	for _, r := range additions {
		if r.SecurityGroupID != "" {
			hasTarget = true
			break
		}
	}
	if !hasTarget {
		return nil
	}
	owner, err := u.sgReader.Get(ctx, sgID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return nil // editing SG missing → let worker surface verbatim NotFound.
		}
		return mapRepoErr(err)
	}
	return validateSGTargetSameNetwork(ctx, u.sgReader, owner.NetworkID, additions, fieldFor)
}
