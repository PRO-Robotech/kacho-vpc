// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package serviceerr_test

import (
	"errors"
	"fmt"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/shared/serviceerr"
)

// TestMapRepoErr_SentinelClassification — единый classifier покрывает весь
// superset repo-sentinel'ов, включая ErrPoolNotResolved (документирован как
// FailedPrecondition, но старый MapRepoErr его не классифицировал и слал в
// Internal — это дрейф, который консолидация закрывает).
func TestMapRepoErr_SentinelClassification(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want codes.Code
	}{
		{"not_found", serviceerr.ErrNotFound, codes.NotFound},
		{"already_exists", serviceerr.ErrAlreadyExists, codes.AlreadyExists},
		{"failed_precondition", serviceerr.ErrFailedPrecondition, codes.FailedPrecondition},
		{"pool_not_resolved", serviceerr.ErrPoolNotResolved, codes.FailedPrecondition},
		{"invalid_arg", serviceerr.ErrInvalidArg, codes.InvalidArgument},
		{"internal", serviceerr.ErrInternal, codes.Internal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := serviceerr.MapRepoErr(tc.err)
			if status.Code(got) != tc.want {
				t.Fatalf("MapRepoErr(%v) code = %v, want %v", tc.err, status.Code(got), tc.want)
			}
		})
	}
}

// TestMapRepoErr_WrappedContextStripped — контекстная политика: обёрнутый
// sentinel отдаёт clean-текст без sentinel-префикса.
func TestMapRepoErr_WrappedContextStripped(t *testing.T) {
	err := fmt.Errorf("%w: network is not empty", serviceerr.ErrFailedPrecondition)
	got := serviceerr.MapRepoErr(err)
	if status.Code(got) != codes.FailedPrecondition {
		t.Fatalf("code = %v, want FailedPrecondition", status.Code(got))
	}
	if msg := status.Convert(got).Message(); msg != "network is not empty" {
		t.Fatalf("message = %q, want stripped %q", msg, "network is not empty")
	}
}

// TestMapRepoErr_UnknownFallbackNoLeak — неклассифицированный raw err не течёт
// наружу: фиксированный "internal database error".
func TestMapRepoErr_UnknownFallbackNoLeak(t *testing.T) {
	raw := errors.New("pgx: host=db-primary.internal query=SELECT ...")
	got := serviceerr.MapRepoErr(raw)
	if status.Code(got) != codes.Internal {
		t.Fatalf("code = %v, want Internal", status.Code(got))
	}
	if msg := status.Convert(got).Message(); msg != "internal database error" {
		t.Fatalf("message = %q leaks or wrong, want fixed %q", msg, "internal database error")
	}
}

// TestMapRepoErrLeakSafe_StrictMessages — Internal/admin-вариант: sentinel-текст
// фиксирован (strict leak-suppression), обёрнутый tail НЕ протаскивается наружу.
func TestMapRepoErrLeakSafe_StrictMessages(t *testing.T) {
	// Обёрнутый ErrNotFound с pgx-detail в tail — strict-режим отдаёт голый
	// sentinel-текст, не tail.
	wrapped := fmt.Errorf("%w: row id=abc host=db-primary.internal", serviceerr.ErrNotFound)
	got := serviceerr.MapRepoErrLeakSafe(wrapped, "fallback tag")
	if status.Code(got) != codes.NotFound {
		t.Fatalf("code = %v, want NotFound", status.Code(got))
	}
	if msg := status.Convert(got).Message(); msg != serviceerr.ErrNotFound.Error() {
		t.Fatalf("message = %q, want strict sentinel %q", msg, serviceerr.ErrNotFound.Error())
	}
}

// TestMapRepoErrLeakSafe_Superset — leak-safe вариант классифицирует весь
// superset (включая PoolNotResolved и Internal).
func TestMapRepoErrLeakSafe_Superset(t *testing.T) {
	cases := []struct {
		err  error
		want codes.Code
	}{
		{serviceerr.ErrNotFound, codes.NotFound},
		{serviceerr.ErrAlreadyExists, codes.AlreadyExists},
		{serviceerr.ErrFailedPrecondition, codes.FailedPrecondition},
		{serviceerr.ErrPoolNotResolved, codes.FailedPrecondition},
		{serviceerr.ErrInvalidArg, codes.InvalidArgument},
		{serviceerr.ErrInternal, codes.Internal},
	}
	for _, tc := range cases {
		if got := serviceerr.MapRepoErrLeakSafe(tc.err, "tag"); status.Code(got) != tc.want {
			t.Fatalf("MapRepoErrLeakSafe(%v) = %v, want %v", tc.err, status.Code(got), tc.want)
		}
	}
}

// TestMapRepoErrLeakSafe_FallbackTag — неклассифицированный err → Internal с
// переданным tag'ом (не leak raw-текста).
func TestMapRepoErrLeakSafe_FallbackTag(t *testing.T) {
	raw := errors.New("pgx: host=db-primary.internal")
	got := serviceerr.MapRepoErrLeakSafe(raw, "address pool admin error")
	if status.Code(got) != codes.Internal {
		t.Fatalf("code = %v, want Internal", status.Code(got))
	}
	if msg := status.Convert(got).Message(); msg != "address pool admin error" {
		t.Fatalf("message = %q, want tag %q", msg, "address pool admin error")
	}
}

// TestMapRepoErrLeakSafe_PassthroughStatus — уже-сформированный gRPC status
// (не Unknown) пробрасывается как есть.
func TestMapRepoErrLeakSafe_PassthroughStatus(t *testing.T) {
	in := status.Error(codes.InvalidArgument, "invalid network id 'garbage'")
	got := serviceerr.MapRepoErrLeakSafe(in, "tag")
	if status.Code(got) != codes.InvalidArgument {
		t.Fatalf("code = %v, want InvalidArgument", status.Code(got))
	}
	if msg := status.Convert(got).Message(); msg != "invalid network id 'garbage'" {
		t.Fatalf("message = %q, want passthrough", msg)
	}
}

// TestMapRepoErr_Nil — nil → nil для обоих.
func TestMapRepoErr_Nil(t *testing.T) {
	if serviceerr.MapRepoErr(nil) != nil {
		t.Fatal("MapRepoErr(nil) != nil")
	}
	if serviceerr.MapRepoErrLeakSafe(nil, "tag") != nil {
		t.Fatal("MapRepoErrLeakSafe(nil) != nil")
	}
}
