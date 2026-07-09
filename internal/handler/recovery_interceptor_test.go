// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package handler

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
)

// panicServerStream — минимальный grpc.ServerStream с подменяемым ctx для
// stream-recovery теста.
type panicServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *panicServerStream) Context() context.Context { return s.ctx }

// secret — «чувствительный» текст, который panic несёт в своём значении; фикс
// обязан НЕ протечь его наружу в gRPC-сообщении (leak-safe recovery,
// security.md hardening-инвариант #1).
const secret = "pgx: host=db-internal user=vpc password=s3cr3t dbname=kacho_vpc"

func TestUnaryRecoveryInterceptor_PanicMappedToOpaqueInternal(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	intr := UnaryRecoveryInterceptor(logger)

	panicking := func(context.Context, any) (any, error) {
		panic(secret)
	}

	resp, err := intr(context.Background(), "req",
		&grpc.UnaryServerInfo{FullMethod: "/kacho.cloud.vpc.v1.NetworkService/Create"}, panicking)

	require.Error(t, err)
	assert.Nil(t, resp)
	// Наблюдаемое поведение: код Internal И opaque-сообщение (не панический текст).
	st, ok := grpcstatus.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
	assert.Equal(t, "internal error", st.Message())
	assert.NotContains(t, st.Message(), secret, "panic value must not leak into gRPC message")
	// Диагностика падения обязана попасть в лог (stack + method), но не наружу.
	assert.Contains(t, buf.String(), "Create", "panic must be logged with method for diagnostics")
}

func TestUnaryRecoveryInterceptor_PassthroughNoPanic(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	intr := UnaryRecoveryInterceptor(logger)

	sentinel := errors.New("normal handler error")
	h := func(context.Context, any) (any, error) { return "ok", sentinel }

	resp, err := intr(context.Background(), "req", &grpc.UnaryServerInfo{FullMethod: "/m"}, h)
	assert.Equal(t, "ok", resp)
	assert.ErrorIs(t, err, sentinel, "non-panicking handler result must pass through verbatim")
}

func TestStreamRecoveryInterceptor_PanicMappedToOpaqueInternal(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	intr := StreamRecoveryInterceptor(logger)

	panicking := func(any, grpc.ServerStream) error { panic(secret) }
	ss := &panicServerStream{ctx: context.Background()}

	err := intr(nil, ss,
		&grpc.StreamServerInfo{FullMethod: "/kacho.cloud.vpc.v1.NetworkService/Watch"}, panicking)

	require.Error(t, err)
	st, ok := grpcstatus.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
	assert.Equal(t, "internal error", st.Message())
	assert.NotContains(t, st.Message(), secret)
	assert.True(t, strings.Contains(buf.String(), "Watch"))
}

func TestStreamRecoveryInterceptor_PassthroughNoPanic(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	intr := StreamRecoveryInterceptor(logger)

	sentinel := errors.New("normal stream error")
	h := func(any, grpc.ServerStream) error { return sentinel }
	ss := &panicServerStream{ctx: context.Background()}

	err := intr(nil, ss, &grpc.StreamServerInfo{FullMethod: "/m"}, h)
	assert.ErrorIs(t, err, sentinel)
}
