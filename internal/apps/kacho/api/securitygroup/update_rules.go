// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

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
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/shared/serviceerr"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/helpers"
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
// Result — Operation, response — обновленный SG (parent).
//
// OCC через `xmin::text` живет в repo-слое — use-case просто вызывает
// repo.UpdateRules; concurrent UpdateRules с устаревшим snapshot отвергается
// repo.ErrFailedPrecondition → FailedPrecondition.
//
// SG-специфика: split-endpoint требует собственный input-тип и не укладывается
// в общий Update, потому вынесен в отдельный use-case.
type UpdateRulesUseCase struct {
	repo     Repo
	opsRepo  operations.Repo
	sgReader SecurityGroupReader
}

// NewUpdateRulesUseCase создает UpdateRulesUseCase.
//
// sgReader резолвит network_id редактируемой SG + каждой target-SG для
// same-network-валидации SG-target-правил. Composition-root инжектит
// `cqrsadapter.SecurityGroupAdapter`; nil = валидация пропускается.
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

	// Same-network-валидация SG-target-правил: sync fast-fail.
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
		// Async backstop для same-network SG-target-правил.
		if verr := u.validateAdditionsSameNetwork(ctx, in.SecurityGroupID, in.AdditionRuleSpecs, addFieldFor); verr != nil {
			return nil, verr
		}
		add := assignRuleIDs(in.AdditionRuleSpecs)
		w, werr := u.repo.Writer(ctx)
		if werr != nil {
			return nil, serviceerr.MapRepoErr(werr)
		}
		defer w.Abort()
		updated, uerr := w.SecurityGroups().UpdateRules(ctx, in.SecurityGroupID, in.DeletionRuleIDs, add)
		if uerr != nil {
			return nil, serviceerr.MapRepoErr(uerr)
		}
		if oerr := w.Outbox().Emit(ctx, "SecurityGroup", updated.ID, "UPDATED", helpers.DomainToMap(updated)); oerr != nil {
			return nil, serviceerr.MapRepoErr(fmt.Errorf("%w: outbox emit: %v", repo.ErrInternal, oerr))
		}
		if cerr := w.Commit(); cerr != nil {
			return nil, serviceerr.MapRepoErr(cerr)
		}
		return marshalSecurityGroupRecord(updated)
	})
	return &op, nil
}

// validateAdditionsSameNetwork — резолвит network_id редактируемой SG и
// проверяет, что каждое добавляемое SG-target-правило ссылается на SG из той же
// сети. Если у addition'ов нет SG-target-правил — no-op (никакого lookup). Если
// редактируемая SG не найдена — НЕ ошибка здесь: основной flow вернет NotFound
// из repo.UpdateRules в worker'е.
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
			return nil // редактируемой SG нет → NotFound отдаст worker.
		}
		return serviceerr.MapRepoErr(err)
	}
	return validateSGTargetSameNetwork(ctx, u.sgReader, owner.NetworkID, additions, fieldFor)
}
