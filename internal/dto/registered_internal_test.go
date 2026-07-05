// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package dto

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestMustBeRegistered_PanicsOnMissing — если реестр пуст (init dto/toproto не
// отработал: потерян blank-import `_ "internal/dto/toproto"`), MustBeRegistered
// обязан паниковать на старте (fail-fast в composition-root), а не отдавать
// codes.Internal на первом же Get/List в рантайме.
func TestMustBeRegistered_PanicsOnMissing(t *testing.T) {
	regMu.Lock()
	saved := transfersReg
	transfersReg = map[reflect.Type]any{}
	regMu.Unlock()
	t.Cleanup(func() {
		regMu.Lock()
		transfersReg = saved
		regMu.Unlock()
	})
	require.Panics(t, func() { MustBeRegistered() })
}
