package domain_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// Wave 2 pilot (KAC-99/KAC-94): тесты domain builders для default-SG.
// Skill evgeniy §4 D.7 / AP-2.

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
	assert.Equal(t, "INGRESS", rules[0].Direction)
	assert.Equal(t, "ANY", rules[0].ProtocolName)
	assert.Equal(t, int64(-1), rules[0].ProtocolNumber)
	assert.Equal(t, []string{"0.0.0.0/0"}, rules[0].V4CidrBlocks)
	assert.Equal(t, "EGRESS", rules[1].Direction)

	// Каждый вызов отдаёт fresh slice (caller может мутировать).
	rules2 := domain.NewDefaultSecurityGroupRules()
	rules[0].Direction = "MUTATED"
	assert.Equal(t, "INGRESS", rules2[0].Direction)
}

func TestNewDefaultSecurityGroup(t *testing.T) {
	net := domain.Network{
		ID:       "enpabcdefghij",
		FolderID: "folder-1",
	}
	sg := domain.NewDefaultSecurityGroup(net)
	assert.NotEmpty(t, sg.ID, "ID generated")
	assert.Equal(t, "folder-1", sg.FolderID)
	assert.Equal(t, "enpabcdefghij", sg.NetworkID)
	assert.Equal(t, "default-sg-enpabcde", sg.Name)
	assert.Equal(t, string(domain.SecurityGroupStatusActive), sg.Status)
	assert.True(t, sg.DefaultForNetwork)
	assert.Equal(t, domain.DefaultSGDescription, sg.Description)
	assert.Len(t, sg.Rules, 2)
}
