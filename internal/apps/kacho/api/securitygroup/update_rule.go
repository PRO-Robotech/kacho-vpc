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
	repo     Repo
	opsRepo  operations.Repo
	sgReader SecurityGroupReader
}

// NewUpdateRuleUseCase создаёт UpdateRuleUseCase.
//
// sgReader (KAC-243 §C / D4 net-new wiring) резолвит network_id редактируемой SG
// + target-SG её SG-target-правила для same-network-валидации (сценарий 10).
// Composition-root инжектит `cqrsadapter.SecurityGroupAdapter`; nil = пропуск.
func NewUpdateRuleUseCase(r Repo, opsRepo operations.Repo, sgReader SecurityGroupReader) *UpdateRuleUseCase {
	return &UpdateRuleUseCase{repo: r, opsRepo: opsRepo, sgReader: sgReader}
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
	rd, err := u.repo.Reader(ctx)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	cur, gerr := rd.SecurityGroups().Get(ctx, in.SecurityGroupID)
	if gerr != nil {
		_ = rd.Close()
		return nil, mapRepoErr(gerr)
	}
	_ = rd.Close()

	// Same-network-валидация SG-target правила, которое редактируется (KAC-243
	// §C / сценарий 10, D3/D4): если у целевого rule выбран SG-target
	// (`security_group_id`), он обязан указывать на SG из той же Network, что и
	// редактируемая SG. UpdateRule (proto) меняет только description/labels, так
	// что target не переписывается — проверка валидирует итоговое (= текущее)
	// правило как defense-in-depth и ловит унаследованный cross-network target.
	if u.sgReader != nil {
		for _, r := range cur.Rules {
			if r.ID != in.RuleID || r.SecurityGroupID == "" {
				continue
			}
			if verr := validateSGTargetSameNetwork(ctx, u.sgReader, cur.NetworkID,
				[]domain.SecurityGroupRule{r},
				func(int) string { return "security_group_id" }); verr != nil {
				return nil, verr
			}
		}
	}

	op, err := operations.NewFromContext(
		ctx,
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
		w, werr := u.repo.Writer(ctx)
		if werr != nil {
			return nil, mapRepoErr(werr)
		}
		defer w.Abort()
		updated, uerr := w.SecurityGroups().UpdateRule(ctx, in.SecurityGroupID, in.RuleID, in.Description, in.Labels, in.UpdateMask)
		if uerr != nil {
			return nil, mapRepoErr(uerr)
		}
		if oerr := w.Outbox().Emit(ctx, "SecurityGroup", updated.ID, "UPDATED", securityGroupPayloadMap(updated)); oerr != nil {
			return nil, mapRepoErr(fmt.Errorf("%w: outbox emit: %v", repo.ErrInternal, oerr))
		}
		if cerr := w.Commit(); cerr != nil {
			return nil, mapRepoErr(cerr)
		}
		// Response — parent SecurityGroup (verbatim YC CLI 1.x compat).
		// CLI hardcodes expectation на SecurityGroup, не SecurityGroupRule.
		return marshalSecurityGroupRecord(updated)
	})
	return &op, nil
}
