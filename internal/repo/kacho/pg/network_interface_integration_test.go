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

// TestCQRS_NIC_SecurityGroupIDs_DanglingRefSilentlyAccepted — characterization-тест
// известного gap'а **G6** (docs/architecture/within-service-refs-audit.md,
// issue PRO-Robotech/kacho-vpc#27): `network_interfaces.security_group_ids` —
// within-service ссылка на `security_groups(id)` в той же БД `kacho_vpc`, но она
// НЕ выражена ни FK/join-table, ни software-check'ом (NIC use-case не имеет
// SecurityGroups-reader-порта). Поэтому NIC с НЕсуществующим SG id принимается
// молча (dangling ref).
//
// Тест ПИННИТ текущую (документированную, до behavioral-фикса) семантику
// silent-accept: и внезапный reject, и потеря значения в round-trip'е — регрессии,
// которые тест должен поймать. Настоящее закрытие G6 (join-table nic↔sg с FK
// RESTRICT) меняет tenant-видимую семантику `SG.Delete` → отдельный behavioral-PR
// с APPROVED acceptance (#27), вне scope contract-safe-прохода.
func TestCQRS_NIC_SecurityGroupIDs_DanglingRefSilentlyAccepted(t *testing.T) {
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

	// SG id, которого НЕТ в security_groups (well-formed, но не вставлен).
	danglingSG := ids.NewID(ids.PrefixSecurityGroup)

	w, err := r.Writer(ctx)
	require.NoError(t, err)
	nic := &domain.NetworkInterface{
		ID:               ids.NewID(ids.PrefixNetworkInterface),
		ProjectID:        projectID,
		Name:             domain.RcNameVPC("nic-dangling-sg"),
		Description:      domain.RcDescription(""),
		Labels:           domain.LabelsFromMap(nil),
		SubnetID:         subnetID,
		MAC:              "0e:11:22:33:44:66",
		Status:           domain.NIStatusAvailable,
		SecurityGroupIDs: []string{danglingSG},
	}
	created, err := w.NetworkInterfaces().Insert(ctx, nic)
	require.NoError(t, err,
		"NIC Create referencing a nonexistent security_group_id is silently accepted (G6, #27) — no FK/software-check")
	require.Equal(t, []string{danglingSG}, created.SecurityGroupIDs)
	require.NoError(t, w.Outbox().Emit(ctx, "NetworkInterface", created.ID, "CREATED", map[string]any{"id": created.ID}))
	require.NoError(t, w.Commit())

	rd, err := r.Reader(ctx)
	require.NoError(t, err)
	defer func() { _ = rd.Close() }()
	got, err := rd.NetworkInterfaces().Get(ctx, nic.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{danglingSG}, got.SecurityGroupIDs,
		"dangling SG id round-trips unchanged — control plane offers no integrity signal (G6, #27)")
}
