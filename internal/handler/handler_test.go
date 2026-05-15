package handler

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/protoconv"
)

// Fake-реализации port-ов и await-helper'ы — в `internal/ports/portmock`
// (shim с прежними именами — в mock_test.go). См. TODO #12.
//
// Wave 3a pilot (KAC-94): Network-handler-тесты переехали в
// `internal/apps/kacho/api/network/usecase_test.go` (NetworkHandler удалён,
// Handler теперь живёт в use-case-пакете).

// ---- tests ----

func TestNetworkToProto_Fields(t *testing.T) {
	// Wave 2 pilot (KAC-99/KAC-94): Network теперь — repo-entity (NetworkRecord)
	// + domain newtypes для Name/Description/Labels.
	rec := &domain.NetworkRecord{
		Network: domain.Network{
			ID:          "net-123",
			FolderID:    "folder-1",
			Name:        domain.RcNameVPC("my-net"),
			Description: domain.RcDescription("desc"),
			Labels:      domain.LabelsFromMap(map[string]string{"env": "test"}),
		},
	}
	p := protoconv.Network(rec)
	assert.Equal(t, "net-123", p.Id)
	assert.Equal(t, "folder-1", p.FolderId)
	assert.Equal(t, "my-net", p.Name)
	assert.Equal(t, "desc", p.Description)
	assert.Equal(t, "test", p.Labels["env"])
}

// Wave 2 batch A (KAC-94): тесты Address/RouteTable/Subnet → proto перешли
// на DTO type2pb. Конверсия через handler-local helper'ы (subnetToPb /
// addressToPb / routeTableToPb), которые внутри зовут dto.Transfer.
func TestAddressToProto_External(t *testing.T) {
	rec := &domain.AddressRecord{
		Address: domain.Address{
			ID:       "addr-1",
			FolderID: "f1",
			Type:     domain.AddressTypeExternal,
			ExternalIpv4: &domain.ExternalIpv4Spec{
				Address: "203.0.113.10",
				ZoneID:  "ru-central1-a",
			},
		},
	}
	p, err := addressToPb(rec)
	require.NoError(t, err)
	assert.Equal(t, "addr-1", p.Id)
	ext := p.GetExternalIpv4Address()
	require.NotNil(t, ext)
	assert.Equal(t, "203.0.113.10", ext.Address)
	assert.Equal(t, "ru-central1-a", ext.ZoneId)
}

func TestAddressToProto_Internal(t *testing.T) {
	rec := &domain.AddressRecord{
		Address: domain.Address{
			ID:       "addr-2",
			FolderID: "f1",
			Type:     domain.AddressTypeInternal,
			InternalIpv4: &domain.InternalIpv4Spec{
				Address:  "10.0.0.5",
				SubnetID: "subnet-1",
			},
		},
	}
	p, err := addressToPb(rec)
	require.NoError(t, err)
	intAddr := p.GetInternalIpv4Address()
	require.NotNil(t, intAddr)
	assert.Equal(t, "10.0.0.5", intAddr.Address)
	assert.Equal(t, "subnet-1", intAddr.GetSubnetId())
}

func TestRouteTableToProto_StaticRoutes(t *testing.T) {
	rec := &domain.RouteTableRecord{
		RouteTable: domain.RouteTable{
			ID:        "rt-1",
			FolderID:  "f1",
			NetworkID: "net-1",
			StaticRoutes: []domain.StaticRoute{
				{DestinationPrefix: "0.0.0.0/0", NextHopAddress: "192.168.0.1"},
			},
		},
	}
	p, err := routeTableToPb(rec)
	require.NoError(t, err)
	require.Len(t, p.StaticRoutes, 1)
	assert.Equal(t, "0.0.0.0/0", p.StaticRoutes[0].GetDestinationPrefix())
	assert.Equal(t, "192.168.0.1", p.StaticRoutes[0].GetNextHopAddress())
}

func TestSubnetToProto_CidrBlocks(t *testing.T) {
	rec := &domain.SubnetRecord{
		Subnet: domain.Subnet{
			ID:           "sub-1",
			FolderID:     "f1",
			V4CidrBlocks: []string{"10.0.0.0/24", "10.1.0.0/24"},
		},
	}
	p, err := subnetToPb(rec)
	require.NoError(t, err)
	assert.Equal(t, []string{"10.0.0.0/24", "10.1.0.0/24"}, p.V4CidrBlocks)
}
