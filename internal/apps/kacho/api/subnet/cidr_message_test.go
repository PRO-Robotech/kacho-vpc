// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package subnet

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Сообщение об ошибке host-bits должно соответствовать family: для v6-поля —
// masked IPv6-адрес сети, а не зашитый v4-пример.
func TestValidateSubnetCIDR_HostBitsMessageFamilyCorrect(t *testing.T) {
	t.Run("v6_gives_v6_masked_not_v4_example", func(t *testing.T) {
		err := validateSubnetV6CIDR("v6_cidr_blocks[0]", "2001:4860:4860::8888/32")
		require.Error(t, err)
		require.Contains(t, err.Error(), "2001:4860::/32", "v6 host-bits error must suggest the masked v6 network")
		require.NotContains(t, err.Error(), "10.0.0", "v6 error must not show a v4 example")
	})
	t.Run("v4_gives_v4_masked", func(t *testing.T) {
		err := validateSubnetV4CIDR("v4_cidr_blocks[0]", "10.0.0.5/24")
		require.Error(t, err)
		require.Contains(t, err.Error(), "10.0.0.0/24", "v4 host-bits error must suggest the masked v4 network")
	})
}
