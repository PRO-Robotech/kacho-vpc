// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

// Package addresspool — handler_toproto_test.go: locks the timestamp-convention
// on AddressPool proto projection. Kachō api-conventions: proto responses
// truncate created_at to whole seconds (DB stores microseconds, wire returns
// seconds) — parity with dto/toproto.timeObj and every other VPC resource.
package addresspool

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// TestPoolToProto_TruncatesCreatedAtToSeconds — sub-second precision in the
// DB-managed CreatedAt must NOT leak onto the wire; poolToProto truncates to
// whole seconds per the Kachō timestamp-convention.
func TestPoolToProto_TruncatesCreatedAtToSeconds(t *testing.T) {
	// created_at with microsecond precision (as read back from Postgres).
	created := time.Date(2026, 7, 6, 12, 34, 56, 789123000, time.UTC)
	rec := &kachorepo.AddressPoolRecord{
		AddressPool: domain.AddressPool{
			ID:   ids.NewID(ids.PrefixAddressPool),
			Name: domain.RcNameVPC("pool-a"),
		},
		CreatedAt: created,
	}

	pb := poolToProto(rec)

	require.NotNil(t, pb)
	require.NotNil(t, pb.GetCreatedAt())
	got := pb.GetCreatedAt().AsTime()
	assert.Equal(t, created.Truncate(time.Second), got, "created_at must be truncated to whole seconds")
	assert.Zero(t, got.Nanosecond(), "no sub-second precision may leak onto the wire")
}

// TestPoolToProto_NilRecord — defensive nil-record passthrough (no panic).
func TestPoolToProto_NilRecord(t *testing.T) {
	assert.Nil(t, poolToProto(nil))
}
