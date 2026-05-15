package handler

import (
	"testing"

	"github.com/stretchr/testify/assert"

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

// Wave 3 (KAC-94): TestAddressToProto_External / TestAddressToProto_Internal
// переехали в `internal/apps/kacho/api/address/usecase_test.go::TestAddressToPb_External`
// (addressToPb теперь живёт в новом пакете).

// TestRouteTableToProto_StaticRoutes — moved to internal/apps/kacho/api/routetable/usecase_test.go (Wave 3b).

// TestSubnetToProto_CidrBlocks — moved to internal/apps/kacho/api/subnet/usecase_test.go
// (Wave 3, KAC-94). subnetToPb теперь живёт в новом пакете.
