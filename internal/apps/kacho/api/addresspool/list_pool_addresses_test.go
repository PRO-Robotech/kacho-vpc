// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package addresspool

// Unit-тест sync-guard'а ListPoolAddressesUseCase. Ветка «пустой pool_id →
// InvalidArgument» отбивается ДО открытия Reader-TX и e2e её не покрывал (newman
// всегда передаёт валидный pool_id из предыдущего шага). Репозиторий не должен
// затрагиваться — поэтому mock-Repo без сидов достаточно.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/kachomock"
)

func TestListPoolAddresses_EmptyPoolID_InvalidArgument(t *testing.T) {
	uc := NewListPoolAddressesUseCase(kachomock.NewRepository())

	recs, next, err := uc.Execute(context.Background(), "", "", Pagination{})

	require.Error(t, err, "empty pool_id must be rejected")
	assert.Equal(t, codes.InvalidArgument, status.Code(err), "empty pool_id → InvalidArgument")
	assert.Nil(t, recs)
	assert.Empty(t, next)
}
