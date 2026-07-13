// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package nicinternal

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
)

// Behaviour-level regression-lock (testing.md «на уровне обсёрвабла»): repo-sentinel
// → gRPC-status с ТОЧНЫМ contract-текстом (не только код). Тексты S4-03/04 —
// часть контракта; их дрейф ловится здесь.
func TestMapAttachErr_ContractTexts(t *testing.T) {
	s := &Service{}
	const nicID = "nic_victim"

	t.Run("in-use → FailedPrecondition NetworkInterface is in use", func(t *testing.T) {
		err := s.mapAttachErr(repo.ErrNICInUse, nicID)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.FailedPrecondition, st.Code())
		assert.Equal(t, "NetworkInterface is in use", st.Message())
	})

	t.Run("zone-mismatch → FailedPrecondition с обеими зонами", func(t *testing.T) {
		err := s.mapAttachErr(&repo.NICZoneMismatchError{SubnetZone: "zone-2", InstanceZone: "zone-1"}, nicID)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.FailedPrecondition, st.Code())
		assert.Equal(t, "NetworkInterface subnet is in zone zone-2, instance zone is zone-1", st.Message())
	})

	t.Run("not-found → NotFound Network interface <id> not found", func(t *testing.T) {
		err := s.mapAttachErr(fmt.Errorf("%w: Network interface %s not found", repo.ErrNotFound, nicID), nicID)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.NotFound, st.Code())
		assert.Equal(t, "Network interface nic_victim not found", st.Message())
	})

	t.Run("unmapped raw pgx-error → Internal, no leak (leak-safe fallback)", func(t *testing.T) {
		raw := errors.New("pq: connection to host db.internal:5432 user vpc failed")
		err := s.mapAttachErr(raw, nicID)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.Internal, st.Code())
		assert.NotContains(t, st.Message(), "db.internal", "raw pgx/connection текст не должен утекать (INV/hardening #1)")
		assert.NotContains(t, st.Message(), "5432")
	})
}
