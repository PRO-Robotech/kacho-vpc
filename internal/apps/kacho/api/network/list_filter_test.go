package network

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-corelib/authz"
	"github.com/PRO-Robotech/kacho-corelib/grpcsrv"
	"github.com/PRO-Robotech/kacho-corelib/operations"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/services/listauthz"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/kachomock"
)

// fakeAuthzClient — programmable FGA ListObjects response.
type fakeAuthzClient struct {
	err error
	ids map[string][]string // key = action → ids
}

func (f *fakeAuthzClient) ListObjects(_ context.Context, req authz.ListObjectsRequest) (authz.ListObjectsResponse, error) {
	if f.err != nil {
		return authz.ListObjectsResponse{}, f.err
	}
	if f.ids == nil {
		return authz.ListObjectsResponse{}, nil
	}
	return authz.ListObjectsResponse{ResourceIDs: f.ids[req.Action]}, nil
}

// makeListFilterUC — собирает ListNetworksUseCase с реальным
// authz.ListObjectsService + fake FGA client.
func makeListFilterUC(t *testing.T, kr *kachomock.Repository, fake *fakeAuthzClient) (*ListNetworksUseCase, *authz.ListObjectsService) {
	t.Helper()
	svc := authz.NewListObjectsService(fake, authz.ListObjectsConfig{
		TTL:             time.Second,
		MaxEntries:      100,
		MaxResults:      10000,
		FollowupTimeout: time.Second,
		ServiceName:     "kacho-vpc-test",
	})
	uc := NewListNetworksUseCase(kr, listauthz.New(svc))
	return uc, svc
}

// seedNetworks помещает N networks в репозиторий через writer-TX.
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

// P4.GWT-10 / canonical positive: subject has access to 2 networks of 3 → returns those 2.
func TestListFilter_ExactGrant(t *testing.T) {
	kr := kachomock.NewRepository()
	seedNetworks(t, kr, "prj_1", "enp_aaa", "enp_bbb", "enp_ccc")

	fake := &fakeAuthzClient{
		ids: map[string][]string{
			FGAActionNetworkList: {"enp_aaa", "enp_ccc"},
		},
	}
	uc, _ := makeListFilterUC(t, kr, fake)

	nets, _, err := uc.Execute(context.Background(), "user:usr_alice", NetworkFilter{ProjectID: "prj_1"}, Pagination{})
	require.NoError(t, err)
	require.Len(t, nets, 2)
	gotIDs := map[string]bool{nets[0].ID: true, nets[1].ID: true}
	assert.True(t, gotIDs["enp_aaa"])
	assert.True(t, gotIDs["enp_ccc"])
	assert.False(t, gotIDs["enp_bbb"], "enp_bbb should NOT be in result")
}

// P4.GWT-07 / canonical empty: subject has 0 grants → returns empty, NOT PermissionDenied.
func TestListFilter_EmptyGrantReturnsEmpty(t *testing.T) {
	kr := kachomock.NewRepository()
	seedNetworks(t, kr, "prj_1", "enp_aaa", "enp_bbb")

	fake := &fakeAuthzClient{
		ids: map[string][]string{
			FGAActionNetworkList: nil, // no grants
		},
	}
	uc, _ := makeListFilterUC(t, kr, fake)

	nets, next, err := uc.Execute(context.Background(), "user:usr_bob", NetworkFilter{ProjectID: "prj_1"}, Pagination{})
	require.NoError(t, err)
	assert.Empty(t, nets)
	assert.Empty(t, next)
}

// P4.GWT-03 / fail-closed: FGA error → Unavailable.
func TestListFilter_FGAErrorReturnsUnavailable(t *testing.T) {
	kr := kachomock.NewRepository()
	seedNetworks(t, kr, "prj_1", "enp_aaa")

	fake := &fakeAuthzClient{err: errors.New("FGA cluster down")}
	uc, _ := makeListFilterUC(t, kr, fake)

	_, _, err := uc.Execute(context.Background(), "user:usr_alice", NetworkFilter{ProjectID: "prj_1"}, Pagination{})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.Unavailable, st.Code())
}

// FGA-filter only includes ids that EXIST в repo — orphaned tuples (P4.GWT-12).
func TestListFilter_OrphanedTuplesIgnored(t *testing.T) {
	kr := kachomock.NewRepository()
	seedNetworks(t, kr, "prj_1", "enp_real")

	fake := &fakeAuthzClient{
		ids: map[string][]string{
			FGAActionNetworkList: {"enp_real", "enp_ghost", "enp_phantom"},
		},
	}
	uc, _ := makeListFilterUC(t, kr, fake)

	nets, _, err := uc.Execute(context.Background(), "user:usr_alice", NetworkFilter{ProjectID: "prj_1"}, Pagination{})
	require.NoError(t, err)
	require.Len(t, nets, 1)
	assert.Equal(t, "enp_real", nets[0].ID)
}

// project_id filter is applied alongside FGA filter — same FGA grant but different project gets nothing.
func TestListFilter_ProjectScopeRespected(t *testing.T) {
	kr := kachomock.NewRepository()
	seedNetworks(t, kr, "prj_1", "enp_in_prj_1")
	seedNetworks(t, kr, "prj_2", "enp_in_prj_2")

	fake := &fakeAuthzClient{
		ids: map[string][]string{
			FGAActionNetworkList: {"enp_in_prj_1", "enp_in_prj_2"}, // FGA allows both
		},
	}
	uc, _ := makeListFilterUC(t, kr, fake)

	// Query within prj_1 → returns only prj_1 network.
	nets, _, err := uc.Execute(context.Background(), "user:usr_alice", NetworkFilter{ProjectID: "prj_1"}, Pagination{})
	require.NoError(t, err)
	require.Len(t, nets, 1)
	assert.Equal(t, "enp_in_prj_1", nets[0].ID)
}

// nil-authz wired use-case → legacy unfiltered behaviour (compat fallback).
func TestListFilter_NilAuthzFallbackToUnfiltered(t *testing.T) {
	kr := kachomock.NewRepository()
	seedNetworks(t, kr, "prj_1", "enp_a", "enp_b", "enp_c")

	uc := NewListNetworksUseCase(kr, nil)
	nets, _, err := uc.Execute(context.Background(), "user:usr_alice", NetworkFilter{ProjectID: "prj_1"}, Pagination{})
	require.NoError(t, err)
	assert.Len(t, nets, 3, "nil-authz must fallback to legacy List (no filter)")
}

// empty subject (system principal) → fallback to legacy.
func TestListFilter_EmptySubjectFallbackToUnfiltered(t *testing.T) {
	kr := kachomock.NewRepository()
	seedNetworks(t, kr, "prj_1", "enp_a", "enp_b")

	fake := &fakeAuthzClient{
		ids: map[string][]string{FGAActionNetworkList: {"enp_a"}},
	}
	uc, _ := makeListFilterUC(t, kr, fake)

	// subjectID="" (system / no principal) → no FGA call.
	nets, _, err := uc.Execute(context.Background(), "", NetworkFilter{ProjectID: "prj_1"}, Pagination{})
	require.NoError(t, err)
	assert.Len(t, nets, 2, "empty subject → unfiltered (system / migration job)")
}

// P4.GWT-01 / cache: second call within TTL hits cache.
func TestListFilter_CacheHit(t *testing.T) {
	kr := kachomock.NewRepository()
	seedNetworks(t, kr, "prj_1", "enp_a", "enp_b")

	fake := &fakeAuthzClient{
		ids: map[string][]string{FGAActionNetworkList: {"enp_a"}},
	}
	uc, svc := makeListFilterUC(t, kr, fake)

	// First call - cache miss.
	_, _, err := uc.Execute(context.Background(), "user:usr_alice", NetworkFilter{ProjectID: "prj_1"}, Pagination{})
	require.NoError(t, err)
	_, entries1 := svc.Size()

	// Second call - cache hit.
	_, _, err = uc.Execute(context.Background(), "user:usr_alice", NetworkFilter{ProjectID: "prj_1"}, Pagination{})
	require.NoError(t, err)
	_, entries2 := svc.Size()

	assert.Equal(t, entries1, entries2, "cache size unchanged on hit")
	assert.Equal(t, 1, entries1, "exactly one entry cached")
}

// P4.GWT-26 / invalidate: subject revoked → next call misses cache.
func TestListFilter_InvalidateBySubject(t *testing.T) {
	kr := kachomock.NewRepository()
	seedNetworks(t, kr, "prj_1", "enp_a")

	calls := 0
	fake := &fakeAuthzClient{
		ids: map[string][]string{FGAActionNetworkList: {"enp_a"}},
	}
	uc, svc := makeListFilterUC(t, kr, fake)

	// Wrap fake to count calls.
	type counter struct{ inner authz.ListObjectsClient }
	_ = counter{} // placeholder; we use svc.Size() instead

	_, _, _ = uc.Execute(context.Background(), "user:usr_alice", NetworkFilter{ProjectID: "prj_1"}, Pagination{})
	_, e1 := svc.Size()
	require.Equal(t, 1, e1)

	svc.InvalidateBySubject("user:usr_alice")
	_, e2 := svc.Size()
	assert.Equal(t, 0, e2, "after invalidate subject cache is empty")

	_, _, _ = uc.Execute(context.Background(), "user:usr_alice", NetworkFilter{ProjectID: "prj_1"}, Pagination{})
	_, e3 := svc.Size()
	assert.Equal(t, 1, e3, "next call re-populates cache")
	_ = calls
}

// Project_id required is still enforced when subject is set.
func TestListFilter_ProjectIDRequired(t *testing.T) {
	kr := kachomock.NewRepository()
	fake := &fakeAuthzClient{}
	uc, _ := makeListFilterUC(t, kr, fake)

	_, _, err := uc.Execute(context.Background(), "user:usr_alice", NetworkFilter{ProjectID: ""}, Pagination{})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

// subjectFromCtx — extracts subject from principal ctx.
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
	// Simulate interceptor extraction.
	// We don't invoke real interceptor here; just verify operations.WithPrincipal
	// path equivalence.
	p := operations.Principal{Type: "user", ID: "usr_alice", DisplayName: "alice@example.com"}
	ctx = operations.WithPrincipal(ctx, p)
	assert.Equal(t, "user:usr_alice", subjectFromCtx(ctx))
}
