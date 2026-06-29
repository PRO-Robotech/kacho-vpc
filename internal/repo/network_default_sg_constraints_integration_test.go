// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package repo_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	coredb "github.com/PRO-Robotech/kacho-corelib/db"
	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
	kachopg "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/pg"
)

// Миграция 0005: FK networks.default_security_group_id → security_groups(id)
// ON DELETE SET NULL — прямое удаление default-SG обнуляет ссылку сети (без
// dangling). Плюс partial UNIQUE — не более одного default-SG на сеть.

// FK ON DELETE SET NULL: удалили default-SG напрямую → networks.default_sg → NULL ("" в выводе).
func TestIntegration_DefaultSG_FKSetsNullOnDelete(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	pgPool, err := coredb.NewPool(ctx, setupTestDB(t))
	require.NoError(t, err)
	defer pgPool.Close()
	r := kachopg.New(pgPool, nil)
	defer r.Close()

	netID := ids.NewID(ids.PrefixNetwork)
	sgID := ids.NewID(ids.PrefixSecurityGroup)
	require.NoError(t, legacyWithTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		if _, e := w.Networks().Insert(ctx, &domain.Network{ID: netID, ProjectID: "f-dsg", Name: domain.RcNameVPC("n-dsg")}); e != nil {
			return e
		}
		if _, e := w.SecurityGroups().Insert(ctx, &domain.SecurityGroup{ID: sgID, ProjectID: "f-dsg", NetworkID: netID, DefaultForNetwork: true}); e != nil {
			return e
		}
		_, e := w.Networks().SetDefaultSGID(ctx, netID, sgID)
		return e
	}))

	// Sanity: ссылка проставлена.
	require.Equal(t, sgID, getNetRec(t, ctx, r, netID).DefaultSecurityGroupID)

	// Прямое удаление SG → FK ON DELETE SET NULL обнуляет ссылку.
	_, err = pgPool.Exec(ctx, `DELETE FROM security_groups WHERE id = $1`, sgID)
	require.NoError(t, err)
	require.Equal(t, "", getNetRec(t, ctx, r, netID).DefaultSecurityGroupID,
		"default_security_group_id must be reset to NULL/empty after the SG is deleted (FK ON DELETE SET NULL)")
}

// FK reject: нельзя выставить default_sg на несуществующую SG.
func TestIntegration_DefaultSG_FKRejectsMissingSG(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	pgPool, err := coredb.NewPool(ctx, setupTestDB(t))
	require.NoError(t, err)
	defer pgPool.Close()
	r := kachopg.New(pgPool, nil)
	defer r.Close()

	netID := ids.NewID(ids.PrefixNetwork)
	require.NoError(t, legacyWithTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		_, e := w.Networks().Insert(ctx, &domain.Network{ID: netID, ProjectID: "f-dsg", Name: domain.RcNameVPC("n-dsg2")})
		return e
	}))
	// Прямой UPDATE на несуществующую SG → FK violation.
	_, err = pgPool.Exec(ctx, `UPDATE networks SET default_security_group_id = $2 WHERE id = $1`, netID, "sgrdoesnotexist01")
	require.Error(t, err, "FK must reject a default_security_group_id with no matching security_group")
}

// partial UNIQUE: вторая default-SG в той же сети → ошибка.
func TestIntegration_OneDefaultSGPerNetwork(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	pgPool, err := coredb.NewPool(ctx, setupTestDB(t))
	require.NoError(t, err)
	defer pgPool.Close()
	r := kachopg.New(pgPool, nil)
	defer r.Close()

	netID := ids.NewID(ids.PrefixNetwork)
	require.NoError(t, legacyWithTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		if _, e := w.Networks().Insert(ctx, &domain.Network{ID: netID, ProjectID: "f-dsg", Name: domain.RcNameVPC("n-dsg3")}); e != nil {
			return e
		}
		_, e := w.SecurityGroups().Insert(ctx, &domain.SecurityGroup{ID: ids.NewID(ids.PrefixSecurityGroup), ProjectID: "f-dsg", NetworkID: netID, DefaultForNetwork: true})
		return e
	}))
	// Вторая default-SG в той же сети → partial UNIQUE violation.
	_, err = pgPool.Exec(ctx,
		`INSERT INTO security_groups (id, project_id, network_id, default_for_network) VALUES ($1, 'f-dsg', $2, true)`,
		ids.NewID(ids.PrefixSecurityGroup), netID)
	require.Error(t, err, "a network must have at most one default security group")
}

func getNetRec(t *testing.T, ctx context.Context, r kacho.Repository, id string) *kacho.NetworkRecord {
	t.Helper()
	rd, err := r.Reader(ctx)
	require.NoError(t, err)
	defer func() { _ = rd.Close() }()
	n, err := rd.Networks().Get(ctx, id)
	require.NoError(t, err)
	return n
}
