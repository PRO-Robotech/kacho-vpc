// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package handler

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

// TestUnaryTimeoutInterceptor_InjectsDeadline — без входящего deadline handler
// получает ctx с deadline ~= now+timeout (server-side граница обработки).
func TestUnaryTimeoutInterceptor_InjectsDeadline(t *testing.T) {
	intr := UnaryTimeoutInterceptor(50 * time.Millisecond)
	var got time.Time
	var ok bool
	_, err := intr(context.Background(), nil, &grpc.UnaryServerInfo{},
		func(ctx context.Context, _ any) (any, error) {
			got, ok = ctx.Deadline()
			return nil, nil
		})
	require.NoError(t, err)
	require.True(t, ok, "handler must observe an injected deadline")
	require.WithinDuration(t, time.Now().Add(50*time.Millisecond), got, 40*time.Millisecond)
}

// TestUnaryTimeoutInterceptor_RespectsTighterClientDeadline — если у клиента уже
// более строгий deadline, он сохраняется (не расширяем окно).
func TestUnaryTimeoutInterceptor_RespectsTighterClientDeadline(t *testing.T) {
	intr := UnaryTimeoutInterceptor(1 * time.Hour)
	tight := time.Now().Add(20 * time.Millisecond)
	ctx, cancel := context.WithDeadline(context.Background(), tight)
	defer cancel()

	var got time.Time
	_, err := intr(ctx, nil, &grpc.UnaryServerInfo{},
		func(ctx context.Context, _ any) (any, error) {
			got, _ = ctx.Deadline()
			return nil, nil
		})
	require.NoError(t, err)
	require.WithinDuration(t, tight, got, time.Millisecond, "tighter client deadline must be preserved")
}

// TestUnaryTimeoutInterceptor_CapsLooserClientDeadline — deadline дальше лимита
// подрезается до now+timeout (deadline-less-эффект bounded).
func TestUnaryTimeoutInterceptor_CapsLooserClientDeadline(t *testing.T) {
	intr := UnaryTimeoutInterceptor(30 * time.Millisecond)
	loose := time.Now().Add(1 * time.Hour)
	ctx, cancel := context.WithDeadline(context.Background(), loose)
	defer cancel()

	var got time.Time
	_, err := intr(ctx, nil, &grpc.UnaryServerInfo{},
		func(ctx context.Context, _ any) (any, error) {
			got, _ = ctx.Deadline()
			return nil, nil
		})
	require.NoError(t, err)
	require.WithinDuration(t, time.Now().Add(30*time.Millisecond), got, 25*time.Millisecond)
	require.True(t, got.Before(loose), "loose client deadline must be capped down")
}

// TestUnaryTimeoutInterceptor_ZeroDisabled — timeout<=0 → passthrough, deadline
// не навязывается.
func TestUnaryTimeoutInterceptor_ZeroDisabled(t *testing.T) {
	intr := UnaryTimeoutInterceptor(0)
	var ok bool
	_, err := intr(context.Background(), nil, &grpc.UnaryServerInfo{},
		func(ctx context.Context, _ any) (any, error) {
			_, ok = ctx.Deadline()
			return nil, nil
		})
	require.NoError(t, err)
	require.False(t, ok, "timeout<=0 must not inject a deadline")
}

// TestStreamTimeoutInterceptor_InjectsDeadline — stream-ветка: ss.Context()
// получает injected deadline.
func TestStreamTimeoutInterceptor_InjectsDeadline(t *testing.T) {
	intr := StreamTimeoutInterceptor(50 * time.Millisecond)
	var got time.Time
	var ok bool
	err := intr(nil, &fakeServerStream{ctx: context.Background()}, &grpc.StreamServerInfo{},
		func(_ any, ss grpc.ServerStream) error {
			got, ok = ss.Context().Deadline()
			return nil
		})
	require.NoError(t, err)
	require.True(t, ok)
	require.WithinDuration(t, time.Now().Add(50*time.Millisecond), got, 40*time.Millisecond)
}

// TestStreamTimeoutInterceptor_ZeroDisabled — timeout<=0 → passthrough.
func TestStreamTimeoutInterceptor_ZeroDisabled(t *testing.T) {
	intr := StreamTimeoutInterceptor(0)
	var ok bool
	err := intr(nil, &fakeServerStream{ctx: context.Background()}, &grpc.StreamServerInfo{},
		func(_ any, ss grpc.ServerStream) error {
			_, ok = ss.Context().Deadline()
			return nil
		})
	require.NoError(t, err)
	require.False(t, ok)
}

type fakeServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (f *fakeServerStream) Context() context.Context { return f.ctx }
