package handler

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/dto"

	// blank-import регистрирует трансфер kachorepo.NetworkRecord → *vpcv1.Network
	// в DTO-реестре.
	_ "github.com/PRO-Robotech/kacho-vpc/internal/dto/toproto"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// Fake-реализации port-ов и await-helper'ы — в `internal/repo/repomock`
// (shim с прежними именами — в mock_test.go). См. TODO #12.
//
// Wave 3a pilot (KAC-94): Network-handler-тесты переехали в
// `internal/apps/kacho/api/network/usecase_test.go` (NetworkHandler удалён,
// Handler теперь живёт в use-case-пакете).

// ---- tests ----

func TestNetworkToProto_Fields(t *testing.T) {
	// Wave 5 (KAC-94, skill evgeniy §11 AP-11): legacy `protoconv.Network()`
	// helper удалён; production-call идёт через DTO-реестр. Тест валидирует тот
	// же контракт через `dto.Transfer(dto.FromTo(rec, &dst))`. NetworkRecord
	// уехал из `domain` в `internal/repo/kacho/` (skill §4 D.1).
	rec := kachorepo.NetworkRecord{
		Network: domain.Network{
			ID:          "net-123",
			ProjectID:    "folder-1",
			Name:        domain.RcNameVPC("my-net"),
			Description: domain.RcDescription("desc"),
			Labels:      domain.LabelsFromMap(map[string]string{"env": "test"}),
		},
	}
	var p *vpcv1.Network
	require.NoError(t, dto.Transfer(dto.FromTo(rec, &p)))
	require.NotNil(t, p)
	assert.Equal(t, "net-123", p.Id)
	assert.Equal(t, "folder-1", p.ProjectId)
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
