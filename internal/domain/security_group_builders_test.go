// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package domain_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// Тесты domain-builder'ов для default-SG.

func TestDefaultSGName(t *testing.T) {
	assert.Equal(t, "default-sg-enp12345", domain.DefaultSGName("enp12345abcdefghij"))
	assert.Equal(t, "default-sg-short", domain.DefaultSGName("short"))
}

func TestTruncateID_ShortIDLen(t *testing.T) {
	assert.Equal(t, 8, domain.ShortIDLen)
	assert.Equal(t, "abcdefgh", domain.TruncateID("abcdefghij"))
	assert.Equal(t, "abc", domain.TruncateID("abc"))
	assert.Equal(t, "", domain.TruncateID(""))
}

func TestNewDefaultSecurityGroupRules(t *testing.T) {
	rules := domain.NewDefaultSecurityGroupRules()
	require.Len(t, rules, 2)
	assert.Equal(t, domain.SecurityGroupRuleDirectionIngress, rules[0].Direction)
	assert.Equal(t, "ANY", rules[0].ProtocolName)
	assert.Equal(t, int64(-1), rules[0].ProtocolNumber)
	assert.Equal(t, []string{"0.0.0.0/0"}, rules[0].V4CidrBlocks)
	assert.Equal(t, domain.SecurityGroupRuleDirectionEgress, rules[1].Direction)

	// Каждый вызов отдает fresh slice (caller может мутировать).
	rules2 := domain.NewDefaultSecurityGroupRules()
	rules[0].Direction = "MUTATED"
	assert.Equal(t, domain.SecurityGroupRuleDirectionIngress, rules2[0].Direction)
}

func TestNewDefaultSecurityGroup(t *testing.T) {
	net := domain.Network{
		ID:        "enpabcdefghij",
		ProjectID: "project-1",
	}
	sg := domain.NewDefaultSecurityGroup(net)
	assert.NotEmpty(t, sg.ID, "ID generated")
	assert.Equal(t, "project-1", sg.ProjectID)
	assert.Equal(t, "enpabcdefghij", sg.NetworkID)
	assert.Equal(t, domain.RcNameVPC("default-sg-enpabcde"), sg.Name)
	assert.True(t, sg.DefaultForNetwork)
	assert.Equal(t, domain.RcDescription(domain.DefaultSGDescription), sg.Description)
	assert.Len(t, sg.Rules, 2)
}
