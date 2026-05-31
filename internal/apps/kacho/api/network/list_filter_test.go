package network

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"

	"github.com/PRO-Robotech/kacho-corelib/grpcsrv"
	"github.com/PRO-Robotech/kacho-corelib/operations"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/kachomock"
)

// seedNetworks помещает N networks в репозиторий через writer-TX. Общий helper
// для list-фильтр-тестов (project-level authz, KAC-240).
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

// subjectFromCtx — extracts subject from principal ctx. Unchanged by KAC-240;
// these tests guard the principal→FGA-subject mapping used by every List handler.
func TestSubjectFromCtx_UserPrincipal(t *testing.T) {
	ctx := operations.WithPrincipal(context.Background(), operations.Principal{
		Type:        "user",
		ID:          "usr_alice",
		DisplayName: "alice@example.com",
	})
	got := subjectFromCtx(ctx)
	assert.Equal(t, "user:usr_alice", got)
}

func TestSubjectFromCtx_ServiceAccountPrincipal(t *testing.T) {
	ctx := operations.WithPrincipal(context.Background(), operations.Principal{
		Type: "service_account",
		ID:   "sva_bot",
	})
	got := subjectFromCtx(ctx)
	assert.Equal(t, "service_account:sva_bot", got)
}

func TestSubjectFromCtx_SystemPrincipalReturnsEmpty(t *testing.T) {
	ctx := operations.WithPrincipal(context.Background(), operations.SystemPrincipal())
	got := subjectFromCtx(ctx)
	assert.Empty(t, got, "system principal should not produce FGA subject")
}

func TestSubjectFromCtx_NoPrincipalReturnsEmpty(t *testing.T) {
	got := subjectFromCtx(context.Background())
	assert.Empty(t, got)
}

// Principal extraction via grpcsrv interceptor (production flow) → subject pipes through.
func TestSubjectFromCtx_ViaGrpcMetadata(t *testing.T) {
	md := metadata.New(map[string]string{
		grpcsrv.MDKeyPrincipalType:    "user",
		grpcsrv.MDKeyPrincipalID:      "usr_alice",
		grpcsrv.MDKeyPrincipalDisplay: "alice@example.com",
	})
	ctx := metadata.NewIncomingContext(context.Background(), md)
	p := operations.Principal{Type: "user", ID: "usr_alice", DisplayName: "alice@example.com"}
	ctx = operations.WithPrincipal(ctx, p)
	assert.Equal(t, "user:usr_alice", subjectFromCtx(ctx))
}
