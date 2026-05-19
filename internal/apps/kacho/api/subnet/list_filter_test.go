package subnet

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-corelib/authz"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/services/listauthz"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/kachomock"
)

type fakeFGAClient struct {
	ids map[string][]string
	err error
}

func (f *fakeFGAClient) ListObjects(_ context.Context, req authz.ListObjectsRequest) (authz.ListObjectsResponse, error) {
	if f.err != nil {
		return authz.ListObjectsResponse{}, f.err
	}
	if f.ids == nil {
		return authz.ListObjectsResponse{}, nil
	}
	return authz.ListObjectsResponse{ResourceIDs: f.ids[req.Action]}, nil
}

func seedSubnets(t *testing.T, kr *kachomock.Repository, projectID, networkID string, ids ...string) {
	t.Helper()
	w, err := kr.Writer(context.Background())
	require.NoError(t, err)
	defer w.Abort()
	for _, id := range ids {
		s := &domain.Subnet{
			ID: id, ProjectID: projectID, NetworkID: networkID,
			Name: domain.RcNameVPC("sub-" + id),
		}
		_, ierr := w.Subnets().Insert(context.Background(), s)
		require.NoError(t, ierr)
	}
	require.NoError(t, w.Commit())
}

func makeFilterUC(t *testing.T, kr *kachomock.Repository, fake *fakeFGAClient) *ListSubnetsUseCase {
	t.Helper()
	svc := authz.NewListObjectsService(fake, authz.ListObjectsConfig{TTL: time.Second})
	return NewListSubnetsUseCase(kr, listauthz.New(svc))
}

func TestListSubnetsFilter_ExactGrant(t *testing.T) {
	kr := kachomock.NewRepository()
	seedSubnets(t, kr, "prj_1", "enp_n1", "e9b_a", "e9b_b", "e9b_c")

	fake := &fakeFGAClient{ids: map[string][]string{FGAActionSubnetList: {"e9b_a", "e9b_c"}}}
	uc := makeFilterUC(t, kr, fake)

	subs, _, err := uc.Execute(context.Background(), "user:usr_alice", SubnetFilter{ProjectID: "prj_1"}, Pagination{})
	require.NoError(t, err)
	require.Len(t, subs, 2)
	got := map[string]bool{}
	for _, s := range subs {
		got[s.ID] = true
	}
	assert.True(t, got["e9b_a"])
	assert.True(t, got["e9b_c"])
	assert.False(t, got["e9b_b"])
}

func TestListSubnetsFilter_EmptyGrant(t *testing.T) {
	kr := kachomock.NewRepository()
	seedSubnets(t, kr, "prj_1", "enp_n1", "e9b_a", "e9b_b")

	fake := &fakeFGAClient{ids: map[string][]string{FGAActionSubnetList: nil}}
	uc := makeFilterUC(t, kr, fake)

	subs, _, err := uc.Execute(context.Background(), "user:usr_bob", SubnetFilter{ProjectID: "prj_1"}, Pagination{})
	require.NoError(t, err)
	assert.Empty(t, subs)
}

func TestListSubnetsFilter_FGAError(t *testing.T) {
	kr := kachomock.NewRepository()
	fake := &fakeFGAClient{err: errors.New("FGA down")}
	uc := makeFilterUC(t, kr, fake)

	_, _, err := uc.Execute(context.Background(), "user:usr_alice", SubnetFilter{ProjectID: "prj_1"}, Pagination{})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.Unavailable, st.Code())
}

func TestListSubnetsFilter_NilAuthzFallbackLegacy(t *testing.T) {
	kr := kachomock.NewRepository()
	seedSubnets(t, kr, "prj_1", "enp_n1", "e9b_a", "e9b_b")

	uc := NewListSubnetsUseCase(kr, nil)
	subs, _, err := uc.Execute(context.Background(), "user:usr_alice", SubnetFilter{ProjectID: "prj_1"}, Pagination{})
	require.NoError(t, err)
	assert.Len(t, subs, 2)
}

func TestListSubnetsFilter_EmptySubjectFallbackLegacy(t *testing.T) {
	kr := kachomock.NewRepository()
	seedSubnets(t, kr, "prj_1", "enp_n1", "e9b_a", "e9b_b")

	fake := &fakeFGAClient{ids: map[string][]string{FGAActionSubnetList: {"e9b_a"}}}
	uc := makeFilterUC(t, kr, fake)

	subs, _, err := uc.Execute(context.Background(), "", SubnetFilter{ProjectID: "prj_1"}, Pagination{})
	require.NoError(t, err)
	// Empty subject → fallback to unfiltered List → returns both.
	assert.Len(t, subs, 2)
}

func TestListSubnetsFilter_OrphanedTuples(t *testing.T) {
	kr := kachomock.NewRepository()
	seedSubnets(t, kr, "prj_1", "enp_n1", "e9b_real")

	fake := &fakeFGAClient{ids: map[string][]string{FGAActionSubnetList: {"e9b_real", "e9b_ghost"}}}
	uc := makeFilterUC(t, kr, fake)

	subs, _, err := uc.Execute(context.Background(), "user:usr_alice", SubnetFilter{ProjectID: "prj_1"}, Pagination{})
	require.NoError(t, err)
	require.Len(t, subs, 1)
	assert.Equal(t, "e9b_real", subs[0].ID)
}

// Ensure repo type assertions remain stable.
var _ = kacho.SubnetRecord{}
