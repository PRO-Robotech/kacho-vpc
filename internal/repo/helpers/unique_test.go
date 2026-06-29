// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package helpers

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// TestWrapPgErr_ExclusionViolation_FailedPrecondition проверяет, что
// exclusion-violation (SQLSTATE 23P01) классифицируется как
// ErrFailedPrecondition (23P01 → FailedPrecondition), а не как ErrInvalidArg.
func TestWrapPgErr_ExclusionViolation_FailedPrecondition(t *testing.T) {
	pgErr := &pgconn.PgError{Code: "23P01"}

	got := WrapPgErr(pgErr, "Subnet", "sub-123")

	if !errors.Is(got, ErrFailedPrecondition) {
		t.Fatalf("WrapPgErr on 23P01 = %v; want wrap of ErrFailedPrecondition", got)
	}
	if errors.Is(got, ErrInvalidArg) {
		t.Fatalf("WrapPgErr on 23P01 = %v; must NOT wrap ErrInvalidArg", got)
	}
}
