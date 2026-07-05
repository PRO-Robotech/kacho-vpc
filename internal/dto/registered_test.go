// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package dto_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/PRO-Robotech/kacho-vpc/internal/dto"
	_ "github.com/PRO-Robotech/kacho-vpc/internal/dto/toproto" // регистрирует все трансферы через init()
)

// TestMustBeRegistered_AllPresent — с подключенным dto/toproto (его init()
// регистрирует все VPC-ресурсы + time.Time) boot-check обязан проходить без
// паники. Регрессионный guard: если кто-то удалит регистрацию члена union'а,
// MustBeRegistered начнёт паниковать и тест это поймает.
func TestMustBeRegistered_AllPresent(t *testing.T) {
	require.NotPanics(t, dto.MustBeRegistered)
}
