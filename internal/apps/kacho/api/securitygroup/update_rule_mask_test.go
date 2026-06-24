// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package securitygroup

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/kachomock"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/repomock"
)

// UpdateRule с неизвестным полем в update_mask → InvalidArgument.
func TestUpdateRuleUseCase_UnknownMaskField_InvalidArgument(t *testing.T) {
	sgr := kachomock.NewRepository()
	netA := ids.NewID(ids.PrefixNetwork)
	sgID := seedMockSG(t, sgr, "P", netA, "sg-mask")

	uc := NewUpdateRuleUseCase(sgr, repomock.NewOpsRepo(), nil)
	_, err := uc.Execute(context.Background(), UpdateRuleInput{
		SecurityGroupID: sgID,
		RuleID:          ids.NewID(ids.PrefixSecurityGroup),
		UpdateMask:      []string{"description", "from_port"}, // from_port — unknown для UpdateRule
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

// Известные поля mask проходят валидацию (description/labels).
func TestUpdateRuleUseCase_KnownMaskField_OK(t *testing.T) {
	sgr := kachomock.NewRepository()
	netA := ids.NewID(ids.PrefixNetwork)
	sgID := seedMockSG(t, sgr, "P", netA, "sg-mask-ok")

	uc := NewUpdateRuleUseCase(sgr, repomock.NewOpsRepo(), nil)
	// Не зависим от существования rule (это async); проверяем только sync
	// mask-валидацию: known mask не должен дать InvalidArgument на этом этапе.
	_, err := uc.Execute(context.Background(), UpdateRuleInput{
		SecurityGroupID: sgID,
		RuleID:          ids.NewID(ids.PrefixSecurityGroup),
		Description:     "ok",
		UpdateMask:      []string{"description"},
	})
	require.NoError(t, err)
}

// Правило с малформированным/host-bits v6 CIDR → InvalidArgument.
func TestCreateSGUseCase_BadV6RuleCIDR_InvalidArgument(t *testing.T) {
	sgr := kachomock.NewRepository()
	netA := ids.NewID(ids.PrefixNetwork)
	seedMockSG(t, sgr, "P", netA, "owner") // network exists via SG seed

	rule := domain.SecurityGroupRule{
		Direction:    domain.SecurityGroupRuleDirectionIngress,
		FromPort:     -1,
		ToPort:       -1,
		V6CidrBlocks: []string{"2001:db8::1/64"}, // host-bits != 0 → invalid
	}
	err := validateSGRule("rule_specs[0]", rule)
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())

	// Корректный v6 CIDR (zero host-bits) проходит.
	ruleOK := rule
	ruleOK.V6CidrBlocks = []string{"2001:db8::/64"}
	require.NoError(t, validateSGRule("rule_specs[0]", ruleOK))
}
