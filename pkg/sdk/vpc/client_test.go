// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

// Package vpc tests — smoke-уровень: убеждаемся, что Client собирается, accessors
// зарегистрированы и Close идемпотентен. Полноценная end-to-end проверка с реальным
// gRPC-сервером — задача интеграционных newman-тестов (`tests/newman/`).
package vpc

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestNewClient_EmptyAddr — пустой target должен дать explicit error до dial'а.
func TestNewClient_EmptyAddr(t *testing.T) {
	_, err := NewClient("")
	require.Error(t, err)
	require.Contains(t, err.Error(), "empty addr")
}

// TestNewClient_LazyDial — grpc.NewClient ленив: dial случится только при
// первом RPC, поэтому валидный синтаксис target'а проходит даже без backend'а.
// Все накшированные accessors должны быть не-nil.
func TestNewClient_LazyDial(t *testing.T) {
	c, err := NewClient("localhost:0")
	require.NoError(t, err)
	require.NotNil(t, c)
	t.Cleanup(func() { _ = c.Close() })

	require.NotNil(t, c.Conn())
	require.NotNil(t, c.Networks)
	require.NotNil(t, c.Subnets)
	require.NotNil(t, c.Addresses)
	require.NotNil(t, c.RouteTables)
	require.NotNil(t, c.SecurityGroups)
	require.NotNil(t, c.Gateways)
	require.NotNil(t, c.NetworkInterfaces)
	require.NotNil(t, c.Operations)
}

// TestClient_CloseIdempotent — повторный Close после успешного первого — не panic
// и возвращает ошибку grpc.ClientConn ("connection is closing"); главное — SDK
// сам не теряет state.
func TestClient_CloseIdempotent(t *testing.T) {
	c, err := NewClient("localhost:0")
	require.NoError(t, err)
	require.NoError(t, c.Close())
	// Второй Close — grpc.ClientConn вернет sentinel; нам важно отсутствие panic.
	_ = c.Close()
}

// TestWaitForOperation_EmptyID — guard rail: пустой operationID = программная
// ошибка вызывающего, fail-fast без сетевого вызова.
func TestWaitForOperation_EmptyID(t *testing.T) {
	c, err := NewClient("localhost:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })

	_, err = c.WaitForOperation(t.Context(), "", 0)
	require.Error(t, err)
	require.Contains(t, err.Error(), "empty operationID")
}
