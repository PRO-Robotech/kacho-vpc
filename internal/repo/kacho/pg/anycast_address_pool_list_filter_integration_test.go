// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package pg_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coredb "github.com/PRO-Robotech/kacho-corelib/db"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
	kachopg "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/pg"
)

// List по network_id обязан сурфейсить платформенный is_default-пул (он
// авто-доступен каждой сети БЕЗ pivot-строки), иначе INTERNAL-anycast-селектор
// пуст. Плюс фильтры scope/ip_version отсекают несоответствующие пулы. Платформенный
// is_default IPv4 INTERNAL-пул засеян миграцией 0012 (project 'kacho-system').
func TestIntegration_AnycastPool_List_SurfacesDefaultAndFilters(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	pgPool, err := coredb.NewPool(ctx, setupTestDB(t))
	require.NoError(t, err)
	defer pgPool.Close()
	r := kachopg.New(pgPool, nil)
	defer r.Close()

	const project = "prj-listf"
	netID := seedNetwork(t, ctx, r, project, "net-listf")

	listByNetwork := func(f kacho.AnycastAddressPoolFilter) []*kacho.AnycastAddressPoolRecord {
		rd, e := r.Reader(ctx)
		require.NoError(t, e)
		defer func() { _ = rd.Close() }()
		out, _, le := rd.AnycastAddressPools().List(ctx, f, kacho.Pagination{})
		require.NoError(t, le)
		return out
	}
	hasDefault := func(pools []*kacho.AnycastAddressPoolRecord) bool {
		for _, p := range pools {
			if p.IsDefault {
				return true
			}
		}
		return false
	}
	hasID := func(pools []*kacho.AnycastAddressPoolRecord, id string) bool {
		for _, p := range pools {
			if p.ID == id {
				return true
			}
		}
		return false
	}

	// Сеть без собственных приаттаченных пулов: селектор обязан увидеть платформенный
	// is_default IPv4 INTERNAL-пул (раньше — пусто, т.к. JOIN на pivot его терял).
	def := listByNetwork(kacho.AnycastAddressPoolFilter{ProjectID: project, NetworkID: netID})
	assert.True(t, hasDefault(def),
		"List(network_id) обязан сурфейсить is_default-пул даже без attachment-строки")

	// is_default-пул засеян как IPv4 INTERNAL → фильтр scope=INTERNAL его сохраняет.
	internal := listByNetwork(kacho.AnycastAddressPoolFilter{
		ProjectID: project, NetworkID: netID, Scope: domain.AnycastScopeInternal,
	})
	assert.True(t, hasDefault(internal), "scope=INTERNAL не отсекает INTERNAL-дефолт")

	// Фильтр семейством IPv6 отсекает IPv4-дефолт (family mismatch).
	v6 := listByNetwork(kacho.AnycastAddressPoolFilter{
		ProjectID: project, NetworkID: netID, IPVersion: domain.IpVersionIPv6,
	})
	assert.False(t, hasDefault(v6),
		"ip_version=IPV6 отсекает IPv4-дефолт")

	// Tenant-пул, приаттаченный к сети, по-прежнему виден ∪ дефолт.
	tenantPool := seedAnycastPool(t, ctx, r, project, "aap-listf-mine", []string{"100.64.50.0/24"})
	require.NoError(t, anycastTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		return w.AnycastAddressPools().AttachNetwork(ctx, tenantPool, netID, []string{"100.64.50.0/24"})
	}))
	both := listByNetwork(kacho.AnycastAddressPoolFilter{ProjectID: project, NetworkID: netID})
	assert.True(t, hasID(both, tenantPool), "приаттаченный tenant-пул виден")
	assert.True(t, hasDefault(both), "дефолт сурфейсится вместе с tenant-пулом")

	// Не приаттаченный к этой сети tenant-пул в другом проекте не виден (изоляция).
	otherPool := seedAnycastPool(t, ctx, r, "prj-other", "aap-other", []string{"100.64.60.0/24"})
	none := listByNetwork(kacho.AnycastAddressPoolFilter{ProjectID: project, NetworkID: netID})
	assert.False(t, hasID(none, otherPool), "чужой не приаттаченный пул не сурфейсится")
}
