// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package pg_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coredb "github.com/PRO-Robotech/kacho-corelib/db"
	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	kachopg "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/pg"
)

// Integration-тесты CQRS NIC-репо: Insert + Commit, Reader видит запись.

// helper — создать parent Subnet (NIC требует FK).
func insertSubnetForNIC(t *testing.T, ctx context.Context, dsn string) (projectID, subnetID string) {
	t.Helper()
	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	projectID = "project-nic-cqrs"
	subnetID = ids.NewID(ids.PrefixSubnet)
	// network parent для Subnet FK
	netID := ids.NewID(ids.PrefixNetwork)
	_, err = pool.Exec(ctx, `INSERT INTO networks(id, project_id, name, description, labels) VALUES ($1,$2,$3,$4,'{}'::jsonb)`,
		netID, projectID, "net-nic", "")
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO subnets(id, project_id, network_id, zone_id, placement_type, name, description, labels, v4_cidr_blocks, v6_cidr_blocks) VALUES ($1,$2,$3,$4,'ZONAL',$5,$6,'{}'::jsonb, ARRAY['10.0.0.0/24']::text[], ARRAY[]::text[])`,
		subnetID, projectID, netID, "zone-a", "sn-nic", "")
	require.NoError(t, err)
	return projectID, subnetID
}

// TestCQRS_NIC_InsertCommit_ReaderSees — sanity: Writer.Insert + Commit, Reader видит.
func TestCQRS_NIC_InsertCommit_ReaderSees(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	dsn := setupTestDB(t)
	projectID, subnetID := insertSubnetForNIC(t, ctx, dsn)

	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()
	r := kachopg.New(pool, nil)

	w, err := r.Writer(ctx)
	require.NoError(t, err)
	nic := &domain.NetworkInterface{
		ID:          ids.NewID(ids.PrefixSubnet),
		ProjectID:   projectID,
		Name:        domain.RcNameVPC("nic-cqrs"),
		Description: domain.RcDescription(""),
		Labels:      domain.LabelsFromMap(nil),
		SubnetID:    subnetID,
		MAC:         "0e:11:22:33:44:55",
		Status:      domain.NIStatusAvailable,
	}
	created, err := w.NetworkInterfaces().Insert(ctx, nic)
	require.NoError(t, err)
	require.NoError(t, w.Outbox().Emit(ctx, "NetworkInterface", created.ID, "CREATED", map[string]any{"id": created.ID}))
	require.NoError(t, w.Commit())

	rd, err := r.Reader(ctx)
	require.NoError(t, err)
	defer func() { _ = rd.Close() }()
	got, err := rd.NetworkInterfaces().Get(ctx, nic.ID)
	require.NoError(t, err)
	assert.Equal(t, nic.ID, got.ID)
	assert.Equal(t, subnetID, got.SubnetID)
	assert.Equal(t, "0e:11:22:33:44:55", got.MAC)
}
