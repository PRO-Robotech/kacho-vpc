package protoconv

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// TestCreatedAt_TruncatedToSeconds — регрессия на FINDING/drift: раньше service-копии
// конвертеров не ставили created_at вообще (Operation.response отдавал created_at == null),
// а handler-копии ставили с truncate до секунд. Теперь конвертер один и всегда truncate.
//
// Wave 2 batch A (KAC-94): Subnet/Address/RouteTable конверторы переехали в
// `internal/dto/type2pb/` — тут остаются только SecurityGroup/Gateway/PrivateEndpoint/
// NetworkInterface/Network (Network — pilot KAC-99, остальные мигрируют в Wave 2 batch B/C).
func TestCreatedAt_TruncatedToSeconds(t *testing.T) {
	at := time.Date(2026, 5, 11, 12, 34, 56, 789_000_000, time.UTC)

	require.NotNil(t, Network(&domain.NetworkRecord{Network: domain.Network{ID: "enp1"}, CreatedAt: at}).CreatedAt)
	assert.Equal(t, at.Truncate(time.Second), Network(&domain.NetworkRecord{CreatedAt: at}).CreatedAt.AsTime())
	assert.Equal(t, at.Truncate(time.Second), SecurityGroup(&domain.SecurityGroup{CreatedAt: at}).CreatedAt.AsTime())
	assert.Equal(t, at.Truncate(time.Second), Gateway(&domain.Gateway{CreatedAt: at}).CreatedAt.AsTime())
	assert.Equal(t, at.Truncate(time.Second), PrivateEndpoint(&domain.PrivateEndpoint{CreatedAt: at}).CreatedAt.AsTime())
}

func TestSGStatus_AllStates(t *testing.T) {
	cases := map[string]vpcv1.SecurityGroup_Status{
		"CREATING": vpcv1.SecurityGroup_CREATING,
		"ACTIVE":   vpcv1.SecurityGroup_ACTIVE,
		"UPDATING": vpcv1.SecurityGroup_UPDATING,
		"DELETING": vpcv1.SecurityGroup_DELETING,
		"":         vpcv1.SecurityGroup_STATUS_UNSPECIFIED,
		"weird":    vpcv1.SecurityGroup_STATUS_UNSPECIFIED,
	}
	for in, want := range cases {
		assert.Equal(t, want, sgStatus(in), "status=%q", in)
	}
}

func TestSGDirection_All(t *testing.T) {
	assert.Equal(t, vpcv1.SecurityGroupRule_INGRESS, sgDirection("INGRESS"))
	assert.Equal(t, vpcv1.SecurityGroupRule_EGRESS, sgDirection("EGRESS"))
	assert.Equal(t, vpcv1.SecurityGroupRule_DIRECTION_UNSPECIFIED, sgDirection(""))
}

func TestSecurityGroup_RulesAndTarget(t *testing.T) {
	p := SecurityGroup(&domain.SecurityGroup{
		ID: "enpsg", NetworkID: "enpnet", Status: "ACTIVE", DefaultForNetwork: true,
		Rules: []domain.SecurityGroupRule{
			{ID: "r1", Direction: "INGRESS", ProtocolName: "tcp", FromPort: 22, ToPort: 22, V4CidrBlocks: []string{"10.0.0.0/8"}},
			{ID: "r2", Direction: "EGRESS"}, // no ports, no cidr → Ports/Target nil
		},
	})
	assert.Equal(t, vpcv1.SecurityGroup_ACTIVE, p.Status)
	assert.True(t, p.DefaultForNetwork)
	require.Len(t, p.Rules, 2)
	assert.Equal(t, vpcv1.SecurityGroupRule_INGRESS, p.Rules[0].Direction)
	require.NotNil(t, p.Rules[0].Ports)
	assert.Equal(t, int64(22), p.Rules[0].Ports.FromPort)
	assert.NotNil(t, p.Rules[0].GetCidrBlocks())
	assert.Nil(t, p.Rules[1].Ports)
	assert.Nil(t, p.Rules[1].GetCidrBlocks())
}

func TestNetworkInterface_MacAddressSurfaced(t *testing.T) {
	// KAC-48: mac_address присутствует на публичной проекции NetworkInterface.
	n := &domain.NetworkInterface{ID: "e9bnic", SubnetID: "e9bsub", MAC: "0e:1a:2b:3c:4d:5e", Status: domain.NIStatusAvailable}
	pub := NetworkInterface(n)
	assert.Equal(t, "0e:1a:2b:3c:4d:5e", pub.MacAddress)
}
