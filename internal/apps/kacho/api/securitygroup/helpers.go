// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package securitygroup

import (
	"context"
	"errors"
	"fmt"
	"net/netip"

	"google.golang.org/protobuf/types/known/anypb"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/shared/serviceerr"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/dto"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"

	// Blank-import регистрирует трансферы SecurityGroup/time через init().
	_ "github.com/PRO-Robotech/kacho-vpc/internal/dto/toproto"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
)

// validateCIDRPrefix — host-bits = 0; используется в правилах SG (sync-валидация
// каждого V4/V6 CIDR-блока). Параллельный к `service.validateCIDRPrefix`.
func validateCIDRPrefix(field, value string) error {
	prefix, err := netip.ParsePrefix(value)
	if err != nil {
		return serviceerr.InvalidArg(field, field+" must be a valid CIDR (e.g. 10.0.0.0/24)")
	}
	if prefix.Masked() != prefix {
		return serviceerr.InvalidArg(field,
			field+" must have zero host-bits (use the network address "+prefix.Masked().String()+")")
	}
	return nil
}

// validateSGRule — sync-валидация правила.
//
// Direction-семантика и CIDR host-bits — cross-field invariants, не newtype-level
// (description/labels валидируются через r.Validate() внутри SecurityGroup.Validate()).
func validateSGRule(field string, r domain.SecurityGroupRule) error {
	if r.Direction != domain.SecurityGroupRuleDirectionIngress && r.Direction != domain.SecurityGroupRuleDirectionEgress {
		return serviceerr.InvalidArg(field+".direction", "direction must be INGRESS or EGRESS")
	}
	if err := serviceerr.FromValidation(r.Description.Validate()); err != nil {
		return err
	}
	if err := serviceerr.FromValidation(domain.ValidateLabels(domain.LabelsFromMap(r.Labels))); err != nil {
		return err
	}
	for i, c := range r.V4CidrBlocks {
		if err := validateCIDRPrefix(fmt.Sprintf("%s.cidr_blocks.v4_cidr_blocks[%d]", field, i), c); err != nil {
			return err
		}
	}
	// v6 CIDR-блоки правила валидируются симметрично v4 — иначе
	// малформированный/host-bits v6 CIDR попадал бы в БД как мусор.
	for i, c := range r.V6CidrBlocks {
		if err := validateCIDRPrefix(fmt.Sprintf("%s.cidr_blocks.v6_cidr_blocks[%d]", field, i), c); err != nil {
			return err
		}
	}
	return nil
}

// Тексты ошибок для same-network-валидации SG-target-правил.
const (
	errRuleCrossNetwork  = "security group rule can only reference a security group in the same network"
	errRuleTargetMissing = "security group rule references a non-existent security group"
)

// validateSGTargetSameNetwork проверяет, что каждое SG-target-правило (`oneof
// target = security_group_id`) ссылается на SecurityGroup из той же Network,
// что и редактируемая SG.
//
// `ownerNetworkID` — network_id редактируемой SG (на Create приходит из
// request'а; на UpdateRules/UpdateRule резолвится use-case'ом из самой SG).
// `fieldFor(i)` строит имя поля для BadRequest.field_violations (напр.
// `rule_specs[0].security_group_id` / `addition_rule_specs[0].security_group_id`).
//
// CIDR / predefined правила не затрагиваются (нет `SecurityGroupID`). Cross-network
// → InvalidArgument; несуществующая target-SG (`repo.ErrNotFound`) →
// InvalidArgument (единый класс с cross-network) — НЕ NotFound, НЕ wrapSGErr.
// Проверка не TOCTOU-prone (network_id immutable); удаление target-SG —
// грациозный dangling-ref либо negative InvalidArgument.
func validateSGTargetSameNetwork(
	ctx context.Context,
	reader SecurityGroupReader,
	ownerNetworkID string,
	rules []domain.SecurityGroupRule,
	fieldFor func(i int) string,
) error {
	if reader == nil {
		return nil
	}
	for i, r := range rules {
		if r.SecurityGroupID == "" {
			continue // CIDR / predefined / no target — not an SG-target rule.
		}
		target, err := reader.Get(ctx, r.SecurityGroupID)
		if err != nil {
			if errors.Is(err, repo.ErrNotFound) {
				return serviceerr.InvalidArg(fieldFor(i), errRuleTargetMissing)
			}
			return serviceerr.MapRepoErr(err)
		}
		if target.NetworkID != ownerNetworkID {
			return serviceerr.InvalidArg(fieldFor(i), errRuleCrossNetwork)
		}
	}
	return nil
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

// marshalSecurityGroupRecord конвертирует repo-entity SG в *anypb.Any через
// DTO-реестр. Используется worker'ами Create/Update/UpdateRules/UpdateRule/Move
// для упаковки результата в Operation.response.
func marshalSecurityGroupRecord(rec *kacho.SecurityGroupRecord) (*anypb.Any, error) {
	var dst *vpcv1.SecurityGroup
	if err := dto.Transfer(dto.FromTo(*rec, &dst)); err != nil {
		return nil, fmt.Errorf("dto.Transfer SecurityGroup: %w", err)
	}
	return anypb.New(dst)
}

// ruleSpecFromProto конвертирует proto SecurityGroupRuleSpec → domain SecurityGroupRule.
// Description — newtype RcDescription, Direction — enum SecurityGroupRuleDirection.
// Labels на rule-уровне остаются map[string]string (JSONB-friendly, см.
// domain/security_group.go).
func ruleSpecFromProto(rs *vpcv1.SecurityGroupRuleSpec) domain.SecurityGroupRule {
	r := domain.SecurityGroupRule{
		Description: domain.RcDescription(rs.Description),
		Labels:      rs.Labels,
	}
	switch rs.Direction {
	case vpcv1.SecurityGroupRule_INGRESS:
		r.Direction = domain.SecurityGroupRuleDirectionIngress
	case vpcv1.SecurityGroupRule_EGRESS:
		r.Direction = domain.SecurityGroupRuleDirectionEgress
	}
	if rs.Ports != nil {
		r.FromPort = rs.Ports.FromPort
		r.ToPort = rs.Ports.ToPort
	}
	if name := rs.GetProtocolName(); name != "" {
		r.ProtocolName = name
	}
	if num := rs.GetProtocolNumber(); num != 0 {
		r.ProtocolNumber = num
	}
	if cb := rs.GetCidrBlocks(); cb != nil {
		r.V4CidrBlocks = cb.V4CidrBlocks
		r.V6CidrBlocks = cb.V6CidrBlocks
	}
	if sgID := rs.GetSecurityGroupId(); sgID != "" {
		r.SecurityGroupID = sgID
	}
	if pred := rs.GetPredefinedTarget(); pred != "" {
		r.PredefinedTarget = pred
	}
	return r
}
