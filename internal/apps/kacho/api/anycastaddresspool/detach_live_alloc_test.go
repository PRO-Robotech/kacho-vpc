// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package anycastaddresspool

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/kachomock"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/repomock"
)

// GWT-26 (real path): живая anycast-Address-аллокация пула в сети блокирует
// detach. В отличие от TestDetach_LiveAllocationsAndIdempotent (seed-override),
// здесь сидится фактический anycast-Address — это упражняет реальный
// CountAllocationsInNetwork (COUNT по anycast.pool_id + anycast_network_id),
// заменивший прежний stub-0.
func TestDetach_LiveAnycastAddressBlocks(t *testing.T) {
	kr := kachomock.NewRepository()
	or := repomock.NewOpsRepo()
	netID := "net00000000000000001"
	created := createPool(t, kr, or, CreateInput{
		ProjectID: "prj-A", Scope: domain.AnycastScopeInternal,
		IPVersion: domain.IpVersionIPv4, CIDRBlocks: []string{"100.64.12.0/22"},
	})
	kr.SeedAnycastAttachment(created.Id, netID)
	// Фактическая anycast-аллокация из пула в сети (не seed-override).
	kr.SeedAddress(&kachorepo.AddressRecord{Address: domain.Address{
		ID:        ids.NewID(ids.PrefixAddress),
		ProjectID: "prj-A",
		Anycast:   &domain.AnycastSpec{NetworkID: netID, Address: "100.64.12.7", AnycastPoolID: created.Id},
	}})

	op, err := NewDetachNetworkUseCase(kr, or).Execute(context.Background(), created.Id, netID)
	require.NoError(t, err)
	saved := repomock.AwaitOpDone(t, or, op.ID)
	require.NotNil(t, saved.Error, "detach с живой anycast-аллокацией должен быть отклонён")
	assert.Equal(t, int32(codes.FailedPrecondition), saved.Error.Code)
	assert.Equal(t, "anycast address pool has allocated addresses in network", saved.Error.Message)
}
