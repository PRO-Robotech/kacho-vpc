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
// переехали в `internal/dto/type2pb/`.
// Wave 2 batch C (KAC-94): NetworkInterface конвертер тоже переехал в
// `internal/dto/type2pb/network_interface.go` — соответствующий MAC-тест живёт там.
// Тут остаётся проверка только для Network legacy helper (handler_test ещё использует
// `protoconv.Network` для unit-теста; будет удалён вместе с handler-рефакторингом
// в следующей фазе).
func TestCreatedAt_TruncatedToSeconds(t *testing.T) {
	at := time.Date(2026, 5, 11, 12, 34, 56, 789_000_000, time.UTC)

	require.NotNil(t, Network(&domain.NetworkRecord{Network: domain.Network{ID: "enp1"}, CreatedAt: at}).CreatedAt)
	assert.Equal(t, at.Truncate(time.Second), Network(&domain.NetworkRecord{CreatedAt: at}).CreatedAt.AsTime())
}
