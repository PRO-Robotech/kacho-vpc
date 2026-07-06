// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package helpers

import (
	"errors"
	"strings"
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

// TestWrapPgErr_Unclassified_PreservesCauseForLogs — неклассифицированный
// SQLSTATE (напр. 40001 serialization_failure, 40P01 deadlock, 57014 statement
// timeout) уходит в fallback-ветку. Клиент по контракту получает фиксированный
// INTERNAL (no-leak — сохраняется через errors.Is(ErrInternal) для маппинга в
// serviceerr), НО root-cause обязан остаться в цепочке ошибки для server-side
// логов оператора (CWE-778: раньше fallback возвращал голый ErrInternal и терял
// SQLSTATE/detail безвозвратно). Зеркалит уже-существующий wrap-паттерн
// helpers/jsonb.go (`%w: … %v`).
func TestWrapPgErr_Unclassified_PreservesCauseForLogs(t *testing.T) {
	raw := &pgconn.PgError{
		Code:     "40001",
		Severity: "ERROR",
		Message:  "could not serialize access due to concurrent update",
	}

	got := WrapPgErr(raw, "Network", "e9bnet0000000000001")

	// (1) Маппинг: остаётся ErrInternal → serviceerr отдаст фиксированный
	// "internal database error" клиенту (no-leak сохранён).
	if !errors.Is(got, ErrInternal) {
		t.Fatalf("WrapPgErr on unclassified = %v; want errors.Is ErrInternal (client still maps to fixed INTERNAL)", got)
	}
	// (2) Observability: SQLSTATE и detail присутствуют в строке ошибки для логов.
	if !strings.Contains(got.Error(), "40001") {
		t.Fatalf("WrapPgErr on unclassified must keep the SQLSTATE in the error string for operator logs; got %q", got.Error())
	}
	if !strings.Contains(got.Error(), "serialize access") {
		t.Fatalf("WrapPgErr on unclassified must keep the raw pg detail in the error string for operator logs; got %q", got.Error())
	}
}
