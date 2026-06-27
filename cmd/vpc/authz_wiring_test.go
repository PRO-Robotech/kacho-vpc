// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package main

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/PRO-Robotech/kacho-corelib/authz"

	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/check"
)

// S3 (defense-in-depth): даже если S1-гард обойдён регрессией, wiring-слой не
// должен поднимать production-инстанс без authz-interceptor'а. fail-fast в
// production, graceful Warn+continue в dev.

// vpc8-C-12: production + interceptor отсутствует → фатальная ошибка.
func TestAuthzWiringDecision_Production_Absent_Fatal(t *testing.T) {
	intr, err := authzWiringDecision(true, nil, check.ErrIAMConnNotConfigured)
	require.Error(t, err)
	require.Nil(t, intr)
	require.Contains(t, err.Error(), "production mode requires authz interceptor")
}

// vpc8-C-13: dev + interceptor отсутствует → (nil, nil) continue.
func TestAuthzWiringDecision_Dev_Absent_Continue(t *testing.T) {
	intr, err := authzWiringDecision(false, nil, check.ErrIAMConnNotConfigured)
	require.NoError(t, err)
	require.Nil(t, intr, "dev продолжает без authz-interceptor'а")
}

// happy: interceptor собран → возвращается для навешивания.
func TestAuthzWiringDecision_Present_Attach(t *testing.T) {
	got := &authz.Interceptor{}
	intr, err := authzWiringDecision(true, got, nil)
	require.NoError(t, err)
	require.Same(t, got, intr)
}

// прочая build-ошибка пробрасывается как есть (не маскируется).
func TestAuthzWiringDecision_OtherError_Propagated(t *testing.T) {
	sentinel := errors.New("boom")
	intr, err := authzWiringDecision(true, nil, sentinel)
	require.ErrorIs(t, err, sentinel)
	require.Nil(t, intr)
}
