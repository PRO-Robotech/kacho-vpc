// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package repo_test

// FGARegister-эмиттер заполняет колонки resource_kind/resource_id из tuple
// Object ("<kind>:<id>"), чтобы corelib-reconciler адресовал intent'ы поресурсно.
// Здесь проверяется writer-путь (форма колонок проверяется в пакете clients).

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coredb "github.com/PRO-Robotech/kacho-corelib/db"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/fgaregister"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
	kachopg "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/pg"
)

// TestIntegration_FGARegisterEmit_PopulatesResourceColumns — эмиттер пишет
// resource_kind/resource_id, распарсенные из tuple Object, в той же writer-tx.
func TestIntegration_FGARegisterEmit_PopulatesResourceColumns(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	dsn := setupTestDB(t)
	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	r := kachopg.New(pool, nil)

	// Эмитим register-intent для vpc_network через реальный writer-путь.
	err = legacyWithTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		return w.FGARegister().EmitRegister(ctx, fgaregister.RegisterIntent(
			fgaregister.ProjectHierarchy("prj-emit", "vpc_network", "net-emit"),
		))
	})
	require.NoError(t, err)

	var eventType, kind, id string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT event_type, resource_kind, resource_id
		   FROM kacho_vpc.fga_register_outbox WHERE resource_id = 'net-emit'`).
		Scan(&eventType, &kind, &id))
	assert.Equal(t, "fga.register", eventType)
	assert.Equal(t, "vpc_network", kind, "resource_kind parsed from tuple Object")
	assert.Equal(t, "net-emit", id, "resource_id parsed from tuple Object")

	// Unregister-intent несет те же колонки (симметрично).
	err = legacyWithTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		return w.FGARegister().EmitUnregister(ctx, fgaregister.RegisterIntent(
			fgaregister.ProjectHierarchy("prj-emit", "vpc_subnet", "sub-emit"),
		))
	})
	require.NoError(t, err)
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT event_type, resource_kind, resource_id
		   FROM kacho_vpc.fga_register_outbox WHERE resource_id = 'sub-emit'`).
		Scan(&eventType, &kind, &id))
	assert.Equal(t, "fga.unregister", eventType)
	assert.Equal(t, "vpc_subnet", kind)
	assert.Equal(t, "sub-emit", id)
}
