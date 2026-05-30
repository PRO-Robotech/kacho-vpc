package securitygroup

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// KAC-239 S2 — SecurityGroup.used_by (потребители SG, derived-on-read) +
// safe-delete. used_by = к кому ПОДКЛЮЧЕНА SG: NIC.security_group_ids ∋ sg
// (тип "network_interface") + networks.default_security_group_id == sg (тип
// "network"). НЕ rule-references-another-SG.

const s2SGID = "enp" + "0000000000000usg2" // valid prefix + 17 chars

func seedPlainSG(r interface {
	SeedSecurityGroup(*kacho.SecurityGroupRecord)
}, id string) {
	r.SeedSecurityGroup(&kacho.SecurityGroupRecord{
		SecurityGroup: domain.SecurityGroup{ID: id, ProjectID: "f1", NetworkID: "net1"},
	})
}

// S2-1: SG без потребителей → used_by пуст.
func TestSG_UsedBy_Empty(t *testing.T) {
	r, _ := sgTestRepo(t)
	seedPlainSG(r, s2SGID)
	rec, err := NewGetSecurityGroupUseCase(r).Execute(context.Background(), s2SGID)
	require.NoError(t, err)
	assert.Empty(t, rec.UsedBy)
}

// S2-2: NIC с security_group_ids=[sg] → used_by содержит network_interface.
func TestSG_UsedBy_NIC(t *testing.T) {
	r, _ := sgTestRepo(t)
	seedPlainSG(r, s2SGID)
	r.SeedNetworkInterface(&kacho.NetworkInterfaceRecord{
		NetworkInterface: domain.NetworkInterface{ID: "e9bNIC1", SecurityGroupIDs: []string{s2SGID}},
	})
	rec, err := NewGetSecurityGroupUseCase(r).Execute(context.Background(), s2SGID)
	require.NoError(t, err)
	require.Len(t, rec.UsedBy, 1)
	assert.Equal(t, "network_interface", rec.UsedBy[0].ReferrerType)
	assert.Equal(t, "e9bNIC1", rec.UsedBy[0].ReferrerID)
}

// S2-3: сеть с default_security_group_id=sg → used_by содержит network.
func TestSG_UsedBy_NetworkDefault(t *testing.T) {
	r, _ := sgTestRepo(t)
	seedPlainSG(r, s2SGID)
	r.SeedNetwork(&kacho.NetworkRecord{
		Network: domain.Network{ID: "enpNET1", DefaultSecurityGroupID: s2SGID},
	})
	rec, err := NewGetSecurityGroupUseCase(r).Execute(context.Background(), s2SGID)
	require.NoError(t, err)
	require.Len(t, rec.UsedBy, 1)
	assert.Equal(t, "network", rec.UsedBy[0].ReferrerType)
	assert.Equal(t, "enpNET1", rec.UsedBy[0].ReferrerID)
}

// S2-4: SG с непустым used_by (подключена к NIC) → Delete = FailedPrecondition.
func TestSG_Delete_BlockedWhenUsed(t *testing.T) {
	r, opsRepo := sgTestRepo(t)
	seedPlainSG(r, s2SGID)
	r.SeedNetworkInterface(&kacho.NetworkInterfaceRecord{
		NetworkInterface: domain.NetworkInterface{ID: "e9bNIC2", SecurityGroupIDs: []string{s2SGID}},
	})
	_, err := NewDeleteSecurityGroupUseCase(r, opsRepo).Execute(context.Background(), s2SGID)
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
}

// S2-5: SG без потребителей → Delete проходит (Operation создан).
func TestSG_Delete_OkWhenUnused(t *testing.T) {
	r, opsRepo := sgTestRepo(t)
	seedPlainSG(r, s2SGID)
	op, err := NewDeleteSecurityGroupUseCase(r, opsRepo).Execute(context.Background(), s2SGID)
	require.NoError(t, err)
	require.NotNil(t, op)
}
