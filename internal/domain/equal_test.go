package domain_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// Wave 5 (KAC-94, skill evgeniy §4 D.10): unit-тесты Equal-методов 8 domain-
// типов и nested структур. Equal используется для noop-detection в Update-flow
// и для testing-equality в use-case-тестах.
//
// Контракт:
//   - identity equal — Equal с самим собой возвращает true
//   - one-field-diff → false для каждого domain-поля
//   - Labels (map-семантика, RcLabels) — order-insensitive: вставка в разном
//     порядке даёт equal=true
//   - Slices (reference-id, CIDR, StaticRoutes, Rules) — order-sensitive:
//     swap двух элементов даёт equal=false
//   - CreatedAt НЕ участвует (он в repo-leaf Record, не в domain — D.1)
//   - xmin НЕ участвует (runtime concurrency token, не domain identity)

// ---- LabelsEqual / stringSlicesEqual / labelsMapEqual --------------------

func TestLabelsEqual_OrderInsensitive(t *testing.T) {
	// Order-insensitive: один map вставляется как {a, b}, другой как {b, a} —
	// equal. RcLabels = dict.HDict с private map → итерация order-undefined,
	// но Equal — set-equality, поэтому equal независимо.
	a := domain.LabelsFromMap(map[string]string{"env": "prod", "tier": "premium"})
	b := domain.LabelsFromMap(map[string]string{"tier": "premium", "env": "prod"})
	assert.True(t, domain.LabelsEqual(a, b), "labels с одинаковыми парами в любом порядке — equal")

	// Diff value на одном ключе → not equal.
	c := domain.LabelsFromMap(map[string]string{"env": "dev", "tier": "premium"})
	assert.False(t, domain.LabelsEqual(a, c))

	// Diff cardinality → not equal.
	d := domain.LabelsFromMap(map[string]string{"env": "prod"})
	assert.False(t, domain.LabelsEqual(a, d))

	// Both empty → equal.
	assert.True(t, domain.LabelsEqual(domain.RcLabels{}, domain.RcLabels{}))
}

// ---- Network.Equal -------------------------------------------------------

func newNetwork() domain.Network {
	return domain.Network{
		ID:                     "enp1",
		ProjectID:               "fld1",
		Name:                   domain.RcNameVPC("net1"),
		Description:            domain.RcDescription("desc"),
		Labels:                 domain.LabelsFromMap(map[string]string{"env": "prod"}),
		DefaultSecurityGroupID: "enp2",
	}
}

func TestNetwork_Equal(t *testing.T) {
	base := newNetwork()
	t.Run("identity", func(t *testing.T) {
		assert.True(t, base.Equal(newNetwork()))
	})
	cases := []struct {
		name   string
		mutate func(*domain.Network)
	}{
		{"diff id", func(n *domain.Network) { n.ID = "enpX" }},
		{"diff folder", func(n *domain.Network) { n.ProjectID = "fldX" }},
		{"diff name", func(n *domain.Network) { n.Name = "netX" }},
		{"diff description", func(n *domain.Network) { n.Description = "descX" }},
		{"diff labels", func(n *domain.Network) {
			n.Labels = domain.LabelsFromMap(map[string]string{"env": "dev"})
		}},
		{"diff default_sg", func(n *domain.Network) { n.DefaultSecurityGroupID = "enpZ" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			modified := newNetwork()
			tc.mutate(&modified)
			assert.False(t, base.Equal(modified))
		})
	}
}

// ---- Subnet.Equal --------------------------------------------------------

func newSubnet() domain.Subnet {
	return domain.Subnet{
		ID:           "e9b1",
		ProjectID:     "fld1",
		Name:         "sub1",
		Description:  "desc",
		Labels:       domain.LabelsFromMap(map[string]string{"env": "prod"}),
		NetworkID:    "enp1",
		ZoneID:       "ru-central1-a",
		V4CidrBlocks: []string{"10.0.0.0/24", "10.0.1.0/24"},
		V6CidrBlocks: []string{"2001:db8::/64"},
		RouteTableID: "enp3",
		DhcpOptions: &domain.DhcpOptions{
			DomainName:        "example.com",
			DomainNameServers: []string{"8.8.8.8"},
			NtpServers:        []string{"pool.ntp.org"},
		},
	}
}

func TestSubnet_Equal(t *testing.T) {
	base := newSubnet()
	assert.True(t, base.Equal(newSubnet()))

	// reorder V4CidrBlocks — order-sensitive → not equal
	swapped := newSubnet()
	swapped.V4CidrBlocks = []string{"10.0.1.0/24", "10.0.0.0/24"}
	assert.False(t, base.Equal(swapped), "v4_cidr_blocks order-sensitive")

	// nil DhcpOptions vs non-nil
	noDhcp := newSubnet()
	noDhcp.DhcpOptions = nil
	assert.False(t, base.Equal(noDhcp))

	// diff DhcpOptions.DomainName
	diffDhcp := newSubnet()
	diffDhcp.DhcpOptions = &domain.DhcpOptions{
		DomainName:        "other.example.com",
		DomainNameServers: []string{"8.8.8.8"},
		NtpServers:        []string{"pool.ntp.org"},
	}
	assert.False(t, base.Equal(diffDhcp))

	// both DhcpOptions nil → equal
	a := newSubnet()
	b := newSubnet()
	a.DhcpOptions = nil
	b.DhcpOptions = nil
	assert.True(t, a.Equal(b))
}

func TestDhcpOptions_Equal(t *testing.T) {
	a := &domain.DhcpOptions{DomainName: "x", DomainNameServers: []string{"1.1.1.1"}}
	b := &domain.DhcpOptions{DomainName: "x", DomainNameServers: []string{"1.1.1.1"}}
	assert.True(t, a.Equal(b))

	var nilOpts *domain.DhcpOptions
	assert.True(t, nilOpts.Equal(nil))
	assert.False(t, a.Equal(nil))
	assert.False(t, nilOpts.Equal(a))
}

// ---- Address.Equal -------------------------------------------------------

func newAddress() domain.Address {
	t0 := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	return domain.Address{
		ID:          "e9b2",
		ProjectID:    "fld1",
		Name:        "addr1",
		Description: "desc",
		Labels:      domain.LabelsFromMap(map[string]string{"env": "prod"}),
		Type:        domain.AddressTypeExternal,
		IpVersion:   domain.IpVersionIPv4,
		Reserved:    true,
		Used:        false,
		ExternalIpv4: &domain.ExternalIpv4Spec{
			Address:       "203.0.113.5",
			ZoneID:        "ru-central1-a",
			AddressPoolID: "apl1",
			Requirements: &domain.AddressRequirements{
				DdosProtectionProvider: "qrator",
			},
		},
		UsedBy: []*domain.AddressReference{
			{AddressID: "e9b2", ReferrerType: "compute_instance", ReferrerID: "ci1", AttachedAt: t0},
		},
	}
}

func TestAddress_Equal(t *testing.T) {
	base := newAddress()
	assert.True(t, base.Equal(newAddress()))

	// diff ExternalIpv4.Address
	diffIp := newAddress()
	diffIp.ExternalIpv4.Address = "203.0.113.6"
	assert.False(t, base.Equal(diffIp))

	// diff Requirements
	diffReq := newAddress()
	diffReq.ExternalIpv4.Requirements = &domain.AddressRequirements{DdosProtectionProvider: "other"}
	assert.False(t, base.Equal(diffReq))

	// diff UsedBy referrer
	diffUsed := newAddress()
	diffUsed.UsedBy = []*domain.AddressReference{
		{AddressID: "e9b2", ReferrerType: "compute_instance", ReferrerID: "ciOTHER"},
	}
	assert.False(t, base.Equal(diffUsed))

	// empty UsedBy → not equal
	emptyUsed := newAddress()
	emptyUsed.UsedBy = nil
	assert.False(t, base.Equal(emptyUsed))

	// diff Reserved bool
	diffRes := newAddress()
	diffRes.Reserved = false
	assert.False(t, base.Equal(diffRes))
}

func TestExternalIpv4Spec_Equal_Nil(t *testing.T) {
	a := &domain.ExternalIpv4Spec{Address: "1.2.3.4"}
	var nilSpec *domain.ExternalIpv4Spec
	assert.True(t, nilSpec.Equal(nil))
	assert.False(t, a.Equal(nil))
	assert.False(t, nilSpec.Equal(a))
}

// ---- RouteTable.Equal ----------------------------------------------------

func newRouteTable() domain.RouteTable {
	return domain.RouteTable{
		ID:          "enp4",
		ProjectID:    "fld1",
		Name:        "rt1",
		Description: "desc",
		Labels:      domain.LabelsFromMap(map[string]string{"env": "prod"}),
		NetworkID:   "enp1",
		StaticRoutes: []domain.StaticRoute{
			{DestinationPrefix: "0.0.0.0/0", NextHopAddress: "10.0.0.1"},
			{DestinationPrefix: "10.1.0.0/16", NextHopAddress: "10.0.0.2", Labels: map[string]string{"k": "v"}},
		},
	}
}

func TestRouteTable_Equal(t *testing.T) {
	base := newRouteTable()
	assert.True(t, base.Equal(newRouteTable()))

	// reorder StaticRoutes — order-sensitive → not equal
	swapped := newRouteTable()
	swapped.StaticRoutes[0], swapped.StaticRoutes[1] = swapped.StaticRoutes[1], swapped.StaticRoutes[0]
	assert.False(t, base.Equal(swapped))

	// diff next_hop
	diffHop := newRouteTable()
	diffHop.StaticRoutes[0].NextHopAddress = "10.0.0.99"
	assert.False(t, base.Equal(diffHop))

	// diff rule-label value
	diffRtLab := newRouteTable()
	diffRtLab.StaticRoutes[1].Labels = map[string]string{"k": "OTHER"}
	assert.False(t, base.Equal(diffRtLab))

	// rule labels — order-insensitive (map-семантика)
	r1 := domain.StaticRoute{DestinationPrefix: "0.0.0.0/0", NextHopAddress: "x", Labels: map[string]string{"a": "1", "b": "2"}}
	r2 := domain.StaticRoute{DestinationPrefix: "0.0.0.0/0", NextHopAddress: "x", Labels: map[string]string{"b": "2", "a": "1"}}
	assert.True(t, r1.Equal(r2))
}

// ---- SecurityGroup.Equal -------------------------------------------------

func newSecurityGroup() domain.SecurityGroup {
	return domain.SecurityGroup{
		ID:                "enp5",
		ProjectID:          "fld1",
		NetworkID:         "enp1",
		Name:              "sg1",
		Description:       "desc",
		Labels:            domain.LabelsFromMap(map[string]string{"env": "prod"}),
		Status:            domain.SecurityGroupStatusActive,
		DefaultForNetwork: true,
		Rules: []domain.SecurityGroupRule{
			{
				ID:           "r1",
				Direction:    domain.SecurityGroupRuleDirectionIngress,
				FromPort:     22,
				ToPort:       22,
				ProtocolName: "tcp",
				V4CidrBlocks: []string{"0.0.0.0/0"},
			},
			{
				ID:           "r2",
				Direction:    domain.SecurityGroupRuleDirectionEgress,
				FromPort:     -1,
				ToPort:       -1,
				ProtocolName: "any",
				V4CidrBlocks: []string{"0.0.0.0/0"},
				Labels:       map[string]string{"k": "v"},
			},
		},
	}
}

func TestSecurityGroup_Equal(t *testing.T) {
	base := newSecurityGroup()
	assert.True(t, base.Equal(newSecurityGroup()))

	// reorder Rules — order-sensitive → not equal
	swapped := newSecurityGroup()
	swapped.Rules[0], swapped.Rules[1] = swapped.Rules[1], swapped.Rules[0]
	assert.False(t, base.Equal(swapped))

	// diff status
	diffStatus := newSecurityGroup()
	diffStatus.Status = domain.SecurityGroupStatusUpdating
	assert.False(t, base.Equal(diffStatus))

	// diff rule port
	diffPort := newSecurityGroup()
	diffPort.Rules[0].FromPort = 80
	assert.False(t, base.Equal(diffPort))

	// rule labels order-insensitive
	r1 := domain.SecurityGroupRule{ID: "r", Labels: map[string]string{"a": "1", "b": "2"}}
	r2 := domain.SecurityGroupRule{ID: "r", Labels: map[string]string{"b": "2", "a": "1"}}
	assert.True(t, r1.Equal(r2))

	// reorder V4CidrBlocks внутри rule → not equal (order-sensitive)
	r3 := domain.SecurityGroupRule{ID: "r", V4CidrBlocks: []string{"10.0.0.0/24", "10.0.1.0/24"}}
	r4 := domain.SecurityGroupRule{ID: "r", V4CidrBlocks: []string{"10.0.1.0/24", "10.0.0.0/24"}}
	assert.False(t, r3.Equal(r4))
}

// ---- Gateway.Equal -------------------------------------------------------

func TestGateway_Equal(t *testing.T) {
	base := domain.Gateway{
		ID: "enp6", ProjectID: "fld1", Name: "gw1", Description: "d",
		Labels:      domain.LabelsFromMap(map[string]string{"env": "prod"}),
		GatewayType: domain.GatewayTypeSharedEgress,
	}
	assert.True(t, base.Equal(base))

	diffName := base
	diffName.Name = "gwX"
	assert.False(t, base.Equal(diffName))

	diffLabels := base
	diffLabels.Labels = domain.LabelsFromMap(map[string]string{"env": "dev"})
	assert.False(t, base.Equal(diffLabels))

	// labels reorder → equal (map-семантика)
	a := domain.Gateway{Labels: domain.LabelsFromMap(map[string]string{"a": "1", "b": "2"})}
	b := domain.Gateway{Labels: domain.LabelsFromMap(map[string]string{"b": "2", "a": "1"})}
	assert.True(t, a.Equal(b))
}

// ---- PrivateEndpoint.Equal -----------------------------------------------

func newPrivateEndpoint() domain.PrivateEndpoint {
	return domain.PrivateEndpoint{
		ID:          "enp7",
		ProjectID:    "fld1",
		Name:        "pe1",
		Description: "desc",
		Labels:      domain.LabelsFromMap(map[string]string{"env": "prod"}),
		NetworkID:   "enp1",
		SubnetID:    "e9b1",
		AddressID:   "e9b2",
		IPAddress:   "10.0.0.5",
		ServiceType: domain.PrivateEndpointServiceTypeObjectStorage,
		DnsOptions:  map[string]any{"private_dns_records_enabled": true},
		Status:      domain.PrivateEndpointStatusAvailable,
	}
}

func TestPrivateEndpoint_Equal(t *testing.T) {
	base := newPrivateEndpoint()
	assert.True(t, base.Equal(newPrivateEndpoint()))

	diffStatus := newPrivateEndpoint()
	diffStatus.Status = domain.PrivateEndpointStatusPending
	assert.False(t, base.Equal(diffStatus))

	diffDns := newPrivateEndpoint()
	diffDns.DnsOptions = map[string]any{"private_dns_records_enabled": false}
	assert.False(t, base.Equal(diffDns))

	// nil DnsOptions on both → equal
	a := newPrivateEndpoint()
	b := newPrivateEndpoint()
	a.DnsOptions = nil
	b.DnsOptions = nil
	assert.True(t, a.Equal(b))
}

// ---- NetworkInterface.Equal ----------------------------------------------

func newNetworkInterface() domain.NetworkInterface {
	return domain.NetworkInterface{
		ID:               "e9b9",
		ProjectID:         "fld1",
		Name:             "nic1",
		Description:      "desc",
		Labels:           domain.LabelsFromMap(map[string]string{"env": "prod"}),
		SubnetID:         "e9b1",
		V4AddressIDs:     []string{"e9b2", "e9b3"},
		V6AddressIDs:     []string{"e9b4"},
		SecurityGroupIDs: []string{"enp5"},
		UsedByType:       "compute_instance",
		UsedByID:         "ci1",
		UsedByName:       "vm-alpha",
		MAC:              "0e:11:22:33:44:55",
		Status:           domain.NIStatusActive,
	}
}

func TestNetworkInterface_Equal(t *testing.T) {
	base := newNetworkInterface()
	assert.True(t, base.Equal(newNetworkInterface()))

	// slices reorder → not equal (order-sensitive)
	swapped := newNetworkInterface()
	swapped.V4AddressIDs[0], swapped.V4AddressIDs[1] = swapped.V4AddressIDs[1], swapped.V4AddressIDs[0]
	assert.False(t, base.Equal(swapped), "v4_address_ids order-sensitive")

	cases := []struct {
		name   string
		mutate func(*domain.NetworkInterface)
	}{
		{"diff subnet", func(n *domain.NetworkInterface) { n.SubnetID = "e9bX" }},
		{"diff sg ids", func(n *domain.NetworkInterface) { n.SecurityGroupIDs = []string{"enpX"} }},
		{"diff used_by_type", func(n *domain.NetworkInterface) { n.UsedByType = "other" }},
		{"diff used_by_id", func(n *domain.NetworkInterface) { n.UsedByID = "ciX" }},
		{"diff mac", func(n *domain.NetworkInterface) { n.MAC = "0e:aa:bb:cc:dd:ee" }},
		{"diff status", func(n *domain.NetworkInterface) { n.Status = domain.NIStatusProvisioning }},
		{"empty v6", func(n *domain.NetworkInterface) { n.V6AddressIDs = nil }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newNetworkInterface()
			tc.mutate(&m)
			assert.False(t, base.Equal(m))
		})
	}

	// labels reorder → equal
	a := newNetworkInterface()
	a.Labels = domain.LabelsFromMap(map[string]string{"a": "1", "b": "2"})
	b := newNetworkInterface()
	b.Labels = domain.LabelsFromMap(map[string]string{"b": "2", "a": "1"})
	assert.True(t, a.Equal(b))
}
