// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package repo_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	coredb "github.com/PRO-Robotech/kacho-corelib/db"
	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/services/networkinternal"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/cqrsadapter"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
	kachopg "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/pg"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// InternalNetworkService.SetDefaultSecurityGroupId обязан быть атомарным CAS:
// он НЕ должен затирать конкурентный Network.Update (name/description/labels)
// снимком read-modify-write (lost-update) и не должен перезаписывать уже
// выставленный другой default SG. Защита от TOCTOU + lost-update.

func newDefaultSGFixture(t *testing.T) (context.Context, kacho.Repository, *networkinternal.Service) {
	t.Helper()
	ctx := context.Background()
	dsn := setupTestDB(t)
	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	r := kachopg.New(pool, nil)
	t.Cleanup(func() { r.Close() })
	svc := networkinternal.NewService(cqrsadapter.NewNetwork(r), cqrsadapter.NewSecurityGroup(r))
	return ctx, r, svc
}

func seedNet(t *testing.T, ctx context.Context, r kacho.Repository, name string) string {
	t.Helper()
	id := ids.NewID(ids.PrefixNetwork)
	require.NoError(t, legacyWithTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		_, e := w.Networks().Insert(ctx, &domain.Network{ID: id, ProjectID: "project-dsg", Name: domain.RcNameVPC(name)})
		return e
	}))
	return id
}

func seedSGForNet(t *testing.T, ctx context.Context, r kacho.Repository, netID string) string {
	t.Helper()
	id := ids.NewID(ids.PrefixSecurityGroup)
	require.NoError(t, legacyWithTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		_, e := w.SecurityGroups().Insert(ctx, &domain.SecurityGroup{ID: id, ProjectID: "project-dsg", NetworkID: netID})
		return e
	}))
	return id
}

func getNet(t *testing.T, ctx context.Context, r kacho.Repository, id string) *kacho.NetworkRecord {
	t.Helper()
	rd, err := r.Reader(ctx)
	require.NoError(t, err)
	defer func() { _ = rd.Close() }()
	n, err := rd.Networks().Get(ctx, id)
	require.NoError(t, err)
	return n
}

// Happy path: пустое поле → проставляется; повторный тот же sg — идемпотентно.
func TestSetDefaultSG_SetsAndIdempotent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx, r, svc := newDefaultSGFixture(t)
	netID := seedNet(t, ctx, r, "net-dsg-1")
	sgID := seedSGForNet(t, ctx, r, netID)

	require.NoError(t, svc.SetDefaultSecurityGroupId(ctx, netID, sgID))
	require.Equal(t, sgID, getNet(t, ctx, r, netID).DefaultSecurityGroupID)
	// идемпотентность
	require.NoError(t, svc.SetDefaultSecurityGroupId(ctx, netID, sgID))
	require.Equal(t, sgID, getNet(t, ctx, r, netID).DefaultSecurityGroupID)
}

// Lost-update: между чтением и записью SetDefaultSecurityGroupId происходит
// конкурентный Network.Update, меняющий name. CAS-узкий column-update НЕ должен
// затереть это имя (раньше full-Update писал stale name из снимка).
func TestSetDefaultSG_DoesNotClobberConcurrentRename(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx, r, svc := newDefaultSGFixture(t)
	netID := seedNet(t, ctx, r, "net-original-name")
	sgID := seedSGForNet(t, ctx, r, netID)

	// Конкурентный rename коммитится ДО CAS (узкий column-update не читает name).
	require.NoError(t, legacyWithTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		_, e := w.Networks().Update(ctx, &domain.Network{ID: netID, ProjectID: "project-dsg", Name: domain.RcNameVPC("net-renamed")})
		return e
	}))

	require.NoError(t, svc.SetDefaultSecurityGroupId(ctx, netID, sgID))

	got := getNet(t, ctx, r, netID)
	require.Equal(t, sgID, got.DefaultSecurityGroupID)
	require.Equal(t, "net-renamed", string(got.Name), "SetDefaultSecurityGroupId must not clobber a concurrent rename")
}

// Уже выставлен ДРУГОЙ default SG → FailedPrecondition, поле не меняется.
func TestSetDefaultSG_RefusesOverwrite(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx, r, svc := newDefaultSGFixture(t)
	netID := seedNet(t, ctx, r, "net-dsg-2")
	sg1 := seedSGForNet(t, ctx, r, netID)
	sg2 := seedSGForNet(t, ctx, r, netID)

	require.NoError(t, svc.SetDefaultSecurityGroupId(ctx, netID, sg1))
	err := svc.SetDefaultSecurityGroupId(ctx, netID, sg2)
	require.Error(t, err)
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
	require.Equal(t, sg1, getNet(t, ctx, r, netID).DefaultSecurityGroupID)
}
