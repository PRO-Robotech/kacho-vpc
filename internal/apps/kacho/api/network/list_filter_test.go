// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package network

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"

	"github.com/PRO-Robotech/kacho-corelib/grpcsrv"
	"github.com/PRO-Robotech/kacho-corelib/operations"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/shared/pbconv"
	"github.com/PRO-Robotech/kacho-vpc/internal/authzfilter"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/kachomock"
)

// seedNetworks помещает N networks в репозиторий через writer-TX. Общий helper
// для list-фильтр-тестов (project-level authz).
func seedNetworks(t *testing.T, kr *kachomock.Repository, projectID string, ids ...string) []*kacho.NetworkRecord {
	t.Helper()
	w, err := kr.Writer(context.Background())
	require.NoError(t, err)
	defer w.Abort()
	var out []*kacho.NetworkRecord
	for _, id := range ids {
		n := &domain.Network{ID: id, ProjectID: projectID, Name: domain.RcNameVPC("net-" + id)}
		rec, ierr := w.Networks().Insert(context.Background(), n)
		require.NoError(t, ierr)
		out = append(out, rec)
	}
	require.NoError(t, w.Commit())
	return out
}

// Тесты маппинга principal-ctx → FGA-subject, который используют все List-handler'ы.
func TestSubjectFromCtx_UserPrincipal(t *testing.T) {
	ctx := operations.WithPrincipal(context.Background(), operations.Principal{
		Type:        "user",
		ID:          "usr_alice",
		DisplayName: "alice@example.com",
	})
	got := pbconv.SubjectFromContext(ctx)
	assert.Equal(t, "user:usr_alice", got)
}

func TestSubjectFromCtx_ServiceAccountPrincipal(t *testing.T) {
	ctx := operations.WithPrincipal(context.Background(), operations.Principal{
		Type: "service_account",
		ID:   "sva_bot",
	})
	got := pbconv.SubjectFromContext(ctx)
	assert.Equal(t, "service_account:sva_bot", got)
}

// Явно установленный system-principal → SystemSubject-sentinel (доверенный
// passthrough), НЕ обычный FGA-subject и НЕ пустой. Отличается от анонимного ctx
// (principal не устанавливался) → "" → fail-closed.
func TestSubjectFromCtx_SystemPrincipalReturnsSentinel(t *testing.T) {
	ctx := operations.WithPrincipal(context.Background(), operations.SystemPrincipal())
	got := pbconv.SubjectFromContext(ctx)
	assert.Equal(t, authzfilter.SystemSubject, got, "explicit system principal → SystemSubject sentinel")
}

func TestSubjectFromCtx_NoPrincipalReturnsEmpty(t *testing.T) {
	got := pbconv.SubjectFromContext(context.Background())
	assert.Empty(t, got)
}

// Извлечение principal через grpcsrv-interceptor (production-flow) → subject проходит насквозь.
func TestSubjectFromCtx_ViaGrpcMetadata(t *testing.T) {
	md := metadata.New(map[string]string{
		grpcsrv.MDKeyPrincipalType:    "user",
		grpcsrv.MDKeyPrincipalID:      "usr_alice",
		grpcsrv.MDKeyPrincipalDisplay: "alice@example.com",
	})
	ctx := metadata.NewIncomingContext(context.Background(), md)
	p := operations.Principal{Type: "user", ID: "usr_alice", DisplayName: "alice@example.com"}
	ctx = operations.WithPrincipal(ctx, p)
	assert.Equal(t, "user:usr_alice", pbconv.SubjectFromContext(ctx))
}
