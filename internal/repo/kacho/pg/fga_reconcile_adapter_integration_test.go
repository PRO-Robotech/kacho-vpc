// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package pg_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coredb "github.com/PRO-Robotech/kacho-corelib/db"
	kachopg "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/pg"
)

// FGAReconcileAdapter.ListResources обязан перечислять anycast-пулы как
// vpc_anycast_address_pool — иначе derive-from-state backfill не восстановит
// owner-hierarchy-tuple пула после починки FGA-модели (Create эмитит именно этот
// kind). Покрывает: реальная пуловая строка попадает в enumeration с верным
// (kind, id, project_id).
func TestIntegration_FGAReconcile_EnumeratesAnycastPool(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	pgPool, err := coredb.NewPool(ctx, setupTestDB(t))
	require.NoError(t, err)
	defer pgPool.Close()
	r := kachopg.New(pgPool, nil)
	defer r.Close()

	poolID := seedAnycastPool(t, ctx, r, "prj-recon", "aap-recon", []string{"100.64.40.0/24"})

	adapter := kachopg.NewFGAReconcileAdapter(pgPool)
	rows, err := adapter.ListResources(ctx)
	require.NoError(t, err)

	var found bool
	for _, row := range rows {
		if row.Kind == "vpc_anycast_address_pool" && row.ID == poolID {
			found = true
			assert.Equal(t, "prj-recon", row.ProjectID, "project_id перечислен верно")
		}
	}
	assert.True(t, found,
		"reconciler обязан перечислить vpc_anycast_address_pool (иначе backfill не восстановит owner-tuple)")

	// ResourceExists по тому же kind подтверждает существование (нужно inverse-orphan GC).
	exists, err := adapter.ResourceExists(ctx, "vpc_anycast_address_pool", poolID)
	require.NoError(t, err)
	assert.True(t, exists, "ResourceExists для anycast-пула → true")
}
