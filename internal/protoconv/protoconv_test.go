package protoconv

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// TestCreatedAt_TruncatedToSeconds — регрессия на FINDING/drift: раньше service-копии
// конвертеров не ставили created_at вообще (Operation.response отдавал created_at == null),
// а handler-копии ставили с truncate до секунд. Теперь конвертер один и всегда truncate.
//
// Wave 2 batch A (KAC-94): Subnet/Address/RouteTable конверторы переехали в
// `internal/dto/type2pb/`.
// Wave 2 batch B (KAC-94): SecurityGroup/Gateway/PrivateEndpoint конверторы тоже
// переехали в `internal/dto/type2pb/` — соответствующие тесты живут в `dto/type2pb/*_test.go`.
// Тут остаётся проверка только для тех конверторов, что ещё в protoconv (Network legacy
// helper + NetworkInterface; ожидается миграция в Wave 2/3).
func TestCreatedAt_TruncatedToSeconds(t *testing.T) {
	at := time.Date(2026, 5, 11, 12, 34, 56, 789_000_000, time.UTC)

	require.NotNil(t, Network(&domain.NetworkRecord{Network: domain.Network{ID: "enp1"}, CreatedAt: at}).CreatedAt)
	assert.Equal(t, at.Truncate(time.Second), Network(&domain.NetworkRecord{CreatedAt: at}).CreatedAt.AsTime())
}

func TestNetworkInterface_MacAddressSurfaced(t *testing.T) {
	// KAC-48: mac_address присутствует на публичной проекции NetworkInterface.
	n := &domain.NetworkInterface{ID: "e9bnic", SubnetID: "e9bsub", MAC: "0e:1a:2b:3c:4d:5e", Status: domain.NIStatusAvailable}
	pub := NetworkInterface(n)
	assert.Equal(t, "0e:1a:2b:3c:4d:5e", pub.MacAddress)
}
