// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package repo_test

// FGARegister-эмиттер пишет labels owner-ресурса + parent_project_id в JSONB-поле
// `payload` таблицы fga_register_outbox и проставляет монотонный `source_version`
// из DB-часов (now()) в момент INSERT, в той же writer-tx. Новой колонки/миграции
// не требуется — payload это JSONB.

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coredb "github.com/PRO-Robotech/kacho-corelib/db"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/fgaregister"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
	kachopg "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/pg"
)

// payloadFor читает единственную строку fga_register_outbox для данного
// resource_id и декодирует ее payload в fgaregister.Payload.
func payloadFor(ctx context.Context, t *testing.T, pool *pgxpool.Pool, resourceID string) (string, fgaregister.Payload) {
	t.Helper()
	var eventType string
	var raw []byte
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT event_type, payload FROM kacho_vpc.fga_register_outbox
		   WHERE resource_id = $1 ORDER BY id DESC LIMIT 1`, resourceID).
		Scan(&eventType, &raw))
	var p fgaregister.Payload
	require.NoError(t, json.Unmarshal(raw, &p))
	return eventType, p
}

// Register-intent, эмитированный через реальный writer, несет labels+parent в
// JSONB-payload, а source_version проставлен ненулевым.
func Test_T3_01_FGARegisterEmit_PayloadCarriesLabelsParentAndSourceVersion(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	dsn := setupTestDB(t)
	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	r := kachopg.New(pool, nil)

	labels := map[string]string{"env": "prod", "team": "core"}
	err = legacyWithTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		return w.FGARegister().EmitRegister(ctx, fgaregister.Intent{Items: []fgaregister.Item{{
			Tuple:           fgaregister.ProjectHierarchy("prj-mir", "vpc_subnet", "sub-mir"),
			Labels:          labels,
			ParentProjectID: "prj-mir",
		}}})
	})
	require.NoError(t, err)

	evt, p := payloadFor(ctx, t, pool, "sub-mir")
	assert.Equal(t, "fga.register", evt)
	assert.Equal(t, "vpc_subnet:sub-mir", p.Tuple.Object)
	assert.Equal(t, labels, p.Labels, "payload carries the subnet labels")
	assert.Equal(t, "prj-mir", p.ParentProjectID, "payload carries parent_project_id")
	require.False(t, p.SourceVersion.IsZero(), "source_version stamped from DB clock at INSERT")
}

// source_version монотонен в пределах объекта: более поздний emit для того же
// ресурса проставляет строго-не-меньшую версию.
func Test_T3_01_FGARegisterEmit_SourceVersionMonotonic(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	dsn := setupTestDB(t)
	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	r := kachopg.New(pool, nil)

	emit := func(labels map[string]string) {
		require.NoError(t, legacyWithTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
			return w.FGARegister().EmitRegister(ctx, fgaregister.Intent{Items: []fgaregister.Item{{
				Tuple:           fgaregister.ProjectHierarchy("prj-mon", "vpc_subnet", "sub-mon"),
				Labels:          labels,
				ParentProjectID: "prj-mon",
			}}})
		}))
	}
	emit(map[string]string{"env": "prod"})
	emit(map[string]string{"env": "dev"})

	var versions []string
	rows, err := pool.Query(ctx,
		`SELECT payload->>'source_version' FROM kacho_vpc.fga_register_outbox
		   WHERE resource_id = 'sub-mon' ORDER BY id ASC`)
	require.NoError(t, err)
	defer rows.Close()
	for rows.Next() {
		var v string
		require.NoError(t, rows.Scan(&v))
		versions = append(versions, v)
	}
	require.NoError(t, rows.Err())
	require.Len(t, versions, 2)
	require.NotEmpty(t, versions[0])
	require.NotEmpty(t, versions[1])
	assert.LessOrEqual(t, versions[0], versions[1], "source_version monotonic per object")
}
