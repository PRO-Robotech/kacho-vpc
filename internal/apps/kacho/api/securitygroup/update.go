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
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
)

// UpdateInput — параметры для UpdateSecurityGroupUseCase.Execute. Несёт
// SecurityGroupID (target) + domain.SecurityGroup (с заявленными полями) +
// UpdateMask. Skill evgeniy §7 I.1 — допустимо иметь собственный input-тип,
// когда есть orthogonal mask и target id.
//
// rule_specs в этом use-case'е тоже принимаются (через mask `"rule_specs"`) —
// это legacy verbatim YC behaviour. Split-endpoint `UpdateRules` (новый
// контракт) — отдельный use-case.
type UpdateInput struct {
	SecurityGroupID string
	SecurityGroup   domain.SecurityGroup // несёт Name/Description/Labels/Rules
	UpdateMask      []string
}

// UpdateSecurityGroupUseCase — sync-валидация update_mask + значений, затем
// создание Operation + async update в worker'е.
//
// Skill evgeniy §2 B.1: распилили SecurityGroupService.Update на отдельный
// use-case рядом с handler'ом. Wave 5 replicate (KAC-94 §6 G.5): Get + Update +
// outbox-emit в одной writer-TX (G.2 — writer видит свои writes для Get).
type UpdateSecurityGroupUseCase struct {
	repo    Repo
	opsRepo operations.Repo
}

// NewUpdateSecurityGroupUseCase создаёт UpdateSecurityGroupUseCase.
func NewUpdateSecurityGroupUseCase(r Repo, opsRepo operations.Repo) *UpdateSecurityGroupUseCase {
	return &UpdateSecurityGroupUseCase{repo: r, opsRepo: opsRepo}
}

// Execute — sync-проверки и запуск Update в worker'е.
func (u *UpdateSecurityGroupUseCase) Execute(ctx context.Context, in UpdateInput) (*operations.Operation, error) {
	if err := corevalidate.ResourceID("security group", ids.PrefixSecurityGroup, in.SecurityGroupID); err != nil {
		return nil, err
	}
	if in.SecurityGroupID == "" {
		return nil, status.Error(codes.InvalidArgument, "security_group_id required")
	}
	if err := validateSGUpdate(in); err != nil {
		return nil, err
	}

	op, err := operations.New(
		ids.PrefixOperationVPC,
		fmt.Sprintf("Update security group %s", in.SecurityGroupID),
		&vpcv1.UpdateSecurityGroupMetadata{SecurityGroupId: in.SecurityGroupID},
	)
	if err != nil {
		return nil, err
	}
	if err := u.opsRepo.Create(ctx, op); err != nil {
		return nil, err
	}

	operations.Run(ctx, u.opsRepo, op.ID, func(ctx context.Context) (*anypb.Any, error) {
		return u.doUpdate(ctx, in)
	})

	return &op, nil
}

func (u *UpdateSecurityGroupUseCase) doUpdate(ctx context.Context, in UpdateInput) (*anypb.Any, error) {
	w, err := u.repo.Writer(ctx)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	defer w.Abort()
	rec, err := w.SecurityGroups().Get(ctx, in.SecurityGroupID)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	applySGMask(&rec.SecurityGroup, in)
	updated, err := w.SecurityGroups().Update(ctx, &rec.SecurityGroup)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	if oerr := w.Outbox().Emit(ctx, "SecurityGroup", updated.ID, "UPDATED", securityGroupPayloadMap(updated)); oerr != nil {
		return nil, mapRepoErr(fmt.Errorf("%w: outbox emit: %v", repo.ErrInternal, oerr))
	}
	if cerr := w.Commit(); cerr != nil {
		return nil, mapRepoErr(cerr)
	}
	return marshalSecurityGroupRecord(updated)
}

// validateSGUpdate — sync-проверка update_mask и значений (parity с
// Network/Subnet). Skill evgeniy §4 D.5 / AP-1: name/description/labels —
// через newtype.Validate(). Mask-семантика — через corevalidate.UpdateMask.
//
// Decision table (для каждого поля в mask):
//
//	name        → RcNameVPC.Validate() (verbatim YC permissive name regex).
//	description → RcDescription.Validate() (≤256 chars utf-8).
//	labels      → domain.ValidateLabels() (≤64 пары, key/value verbatim YC).
//	rule_specs  → каждое правило проходит validateSGRule.
//
// Поле, не упомянутое в mask, не валидируется (= unchanged). Unknown field в
// update_mask → InvalidArgument (corevalidate.UpdateMask).
func validateSGUpdate(in UpdateInput) error {
	known := map[string]struct{}{
		"name": {}, "description": {}, "labels": {}, "rule_specs": {},
	}
	if err := corevalidate.UpdateMask("update_mask", in.UpdateMask, known); err != nil {
		return err
	}
	updates := in.UpdateMask
	if len(updates) == 0 {
		updates = []string{"name", "description", "labels"}
	}
	for _, f := range updates {
		switch f {
		case "name":
			if err := in.SecurityGroup.Name.Validate(); err != nil {
				return err
			}
		case "description":
			if err := in.SecurityGroup.Description.Validate(); err != nil {
				return err
			}
		case "labels":
			if err := domain.ValidateLabels(in.SecurityGroup.Labels); err != nil {
				return err
			}
		case "rule_specs":
			for i, r := range in.SecurityGroup.Rules {
				if err := validateSGRule(fmt.Sprintf("rule_specs[%d]", i), r); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// applySGMask — применяет subset полей к существующему domain.SecurityGroup.
// no-mask = full PATCH (verbatim YC).
func applySGMask(sg *domain.SecurityGroup, in UpdateInput) {
	if len(in.UpdateMask) == 0 {
		sg.Name = in.SecurityGroup.Name
		sg.Description = in.SecurityGroup.Description
		sg.Labels = in.SecurityGroup.Labels
		if in.SecurityGroup.Rules != nil {
			sg.Rules = assignRuleIDs(in.SecurityGroup.Rules)
		}
		return
	}
	for _, field := range in.UpdateMask {
		switch field {
		case "name":
			sg.Name = in.SecurityGroup.Name
		case "description":
			sg.Description = in.SecurityGroup.Description
		case "labels":
			sg.Labels = in.SecurityGroup.Labels
		case "rule_specs":
			sg.Rules = assignRuleIDs(in.SecurityGroup.Rules)
		}
	}
}
