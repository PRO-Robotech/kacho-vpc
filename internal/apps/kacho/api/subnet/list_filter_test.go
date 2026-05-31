package subnet

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-corelib/authz"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/services/listauthz"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/kachomock"
)

// KAC-240: Subnet List authorizes at the PROJECT level (viewer on project:<id>),
// not via per-object FGA tuples. repo.List is already project-scoped (the
// isolation boundary); CanViewProject only decides whether the subject may view
// the project. View → all rows; no view → empty (fail-closed). A freshly-created
// subnet appears in List as soon as the subject may view the project, regardless
// of whether its per-object tuple has been written yet.

func makeSubnetProjectAuthzUC(kr *kachomock.Repository, checker authz.CheckClient) *ListSubnetsUseCase {
	return NewListSubnetsUseCase(kr, listauthz.NewProjectChecker(checker))
}

// subnetProjectViewChecker allows `viewer` on the listed projects for subject.
func subnetProjectViewChecker(subject string, allowedProjects ...string) authz.CheckClient {
	allow := make(map[string]struct{}, len(allowedProjects))
	for _, p := range allowedProjects {
		allow["project:"+p] = struct{}{}
	}
	return authz.CheckClientFunc(func(_ context.Context, s, relation, object string) (bool, error) {
		if s != subject || relation != "viewer" {
			return false, nil
		}
		_, ok := allow[object]
		return ok, nil
	})
}

// seedSubnets inserts N subnets через writer-TX.
func seedSubnets(t *testing.T, kr *kachomock.Repository, projectID, networkID string, ids ...string) {
	t.Helper()
	w, err := kr.Writer(context.Background())
	require.NoError(t, err)
	defer w.Abort()
	for _, id := range ids {
		s := &domain.Subnet{ID: id, ProjectID: projectID, NetworkID: networkID, Name: domain.RcNameVPC("sub-" + id)}
		if _, ierr := w.Subnets().Insert(context.Background(), s); ierr != nil {
			require.NoError(t, ierr)
		}
	}
	require.NoError(t, w.Commit())
}

// SUB-LF-VIEWABLE: subject can view project → all project subnets returned (no
// per-object tuples needed — fixes the create→list race, KAC-240).
func TestSubnetListFilter_ViewableProjectReturnsAll(t *testing.T) {
	kr := kachomock.NewRepository()
	seedSubnets(t, kr, "prj_1", "enp_net1", "e9b_aaa", "e9b_bbb", "e9b_ccc")

	uc := makeSubnetProjectAuthzUC(kr, subnetProjectViewChecker("user:usr_alice", "prj_1"))

	subs, _, err := uc.Execute(context.Background(), "user:usr_alice", SubnetFilter{ProjectID: "prj_1"}, Pagination{})
	require.NoError(t, err)
	require.Len(t, subs, 3)
	got := map[string]bool{}
	for _, s := range subs {
		got[s.ID] = true
	}
	assert.True(t, got["e9b_aaa"])
	assert.True(t, got["e9b_bbb"])
	assert.True(t, got["e9b_ccc"])
}

// SUB-LF-DENY: subject cannot view project → empty (fail-closed, no leak).
func TestSubnetListFilter_NonViewableProjectReturnsEmpty(t *testing.T) {
	kr := kachomock.NewRepository()
	seedSubnets(t, kr, "prj_2", "enp_net2", "e9b_secret")

	uc := makeSubnetProjectAuthzUC(kr, subnetProjectViewChecker("user:usr_alice", "prj_1"))

	subs, next, err := uc.Execute(context.Background(), "user:usr_alice", SubnetFilter{ProjectID: "prj_2"}, Pagination{})
	require.NoError(t, err)
	assert.Empty(t, subs)
	assert.Empty(t, next)
}

// SUB-LF-FAIL: Check infra error → Unavailable (fail-closed).
func TestSubnetListFilter_CheckError(t *testing.T) {
	kr := kachomock.NewRepository()
	seedSubnets(t, kr, "prj_1", "enp_net1", "e9b_aaa")

	checker := authz.CheckClientFunc(func(_ context.Context, _, _, _ string) (bool, error) {
		return false, errors.New("FGA down")
	})
	uc := makeSubnetProjectAuthzUC(kr, checker)

	_, _, err := uc.Execute(context.Background(), "user:usr_alice", SubnetFilter{ProjectID: "prj_1"}, Pagination{})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.Unavailable, st.Code())
}

// SUB-LF-NIL: nil-authz → unfiltered passthrough (dev / no-authz mode).
func TestSubnetListFilter_NilAuthz(t *testing.T) {
	kr := kachomock.NewRepository()
	seedSubnets(t, kr, "prj_1", "enp_net1", "e9b_a", "e9b_b")

	uc := NewListSubnetsUseCase(kr, nil)
	subs, _, err := uc.Execute(context.Background(), "user:usr_alice", SubnetFilter{ProjectID: "prj_1"}, Pagination{})
	require.NoError(t, err)
	assert.Len(t, subs, 2)
}

// SUB-LF-EMPTYSUBJ: empty subject (system principal) → unfiltered passthrough.
func TestSubnetListFilter_EmptySubjectPassthrough(t *testing.T) {
	kr := kachomock.NewRepository()
	seedSubnets(t, kr, "prj_1", "enp_net1", "e9b_a", "e9b_b")

	uc := makeSubnetProjectAuthzUC(kr, subnetProjectViewChecker("user:usr_alice", "prj_1"))
	subs, _, err := uc.Execute(context.Background(), "", SubnetFilter{ProjectID: "prj_1"}, Pagination{})
	require.NoError(t, err)
	assert.Len(t, subs, 2)
}
