package network

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
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/kachomock"
)

// KAC-240: List authorizes at the PROJECT level (viewer on project:<id>),
// not via per-object FGA tuples. A freshly-created resource whose per-object
// tuple is not yet written (or whose write silently failed) MUST still appear
// in List as long as the subject may view the project. The repo query is the
// tenant-isolation boundary (it is already project-scoped); project-level
// authz only decides "may this subject see this project at all".

// makeProjectAuthzUC wires ListNetworksUseCase with a project-level checker
// backed by a fake authz.CheckClient (relation=viewer, object=project:<id>).
func makeProjectAuthzUC(t *testing.T, kr *kachomock.Repository, checker authz.CheckClient) *ListNetworksUseCase {
	t.Helper()
	return NewListNetworksUseCase(kr, listauthz.NewProjectChecker(checker))
}

// projectViewChecker returns a CheckClient that allows `viewer` on the given
// set of project objects for the given subject, denies everything else.
func projectViewChecker(subject string, allowedProjects ...string) authz.CheckClient {
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

// KAC-240 RED→GREEN #1: subject can view the project; the three networks have
// NO per-object tuples — they MUST all be returned (this is the bug being fixed:
// previously per-object filtering dropped them all).
func TestListProjectAuthz_ViewableProjectReturnsAllRows(t *testing.T) {
	kr := kachomock.NewRepository()
	seedNetworks(t, kr, "prj_1", "enp_aaa", "enp_bbb", "enp_ccc")

	uc := makeProjectAuthzUC(t, kr, projectViewChecker("user:usr_alice", "prj_1"))

	nets, _, err := uc.Execute(context.Background(), "user:usr_alice", NetworkFilter{ProjectID: "prj_1"}, Pagination{})
	require.NoError(t, err)
	require.Len(t, nets, 3, "all project-scoped rows must be returned when subject can view the project")
	got := map[string]bool{}
	for _, n := range nets {
		got[n.ID] = true
	}
	assert.True(t, got["enp_aaa"])
	assert.True(t, got["enp_bbb"])
	assert.True(t, got["enp_ccc"])
}

// KAC-240 RED→GREEN #2: subject CANNOT view the project → empty result
// (fail-closed). No cross-project leak; the repo query stays project-scoped.
func TestListProjectAuthz_NonViewableProjectReturnsEmpty(t *testing.T) {
	kr := kachomock.NewRepository()
	seedNetworks(t, kr, "prj_2", "enp_secret")

	// alice may view prj_1 only; she queries prj_2 → deny → empty (NOT all rows).
	uc := makeProjectAuthzUC(t, kr, projectViewChecker("user:usr_alice", "prj_1"))

	nets, next, err := uc.Execute(context.Background(), "user:usr_alice", NetworkFilter{ProjectID: "prj_2"}, Pagination{})
	require.NoError(t, err)
	assert.Empty(t, nets, "subject without project view must get empty list (fail-closed)")
	assert.Empty(t, next)
}

// KAC-240: CheckClient infra error → Unavailable (fail-closed, not a leak).
func TestListProjectAuthz_CheckErrorReturnsUnavailable(t *testing.T) {
	kr := kachomock.NewRepository()
	seedNetworks(t, kr, "prj_1", "enp_aaa")

	checker := authz.CheckClientFunc(func(_ context.Context, _, _, _ string) (bool, error) {
		return false, errors.New("iam down")
	})
	uc := makeProjectAuthzUC(t, kr, checker)

	_, _, err := uc.Execute(context.Background(), "user:usr_alice", NetworkFilter{ProjectID: "prj_1"}, Pagination{})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.Unavailable, st.Code())
}

// KAC-240: nil-authz (dev / no-authz mode) → unfiltered passthrough preserved.
func TestListProjectAuthz_NilAuthzPassthrough(t *testing.T) {
	kr := kachomock.NewRepository()
	seedNetworks(t, kr, "prj_1", "enp_a", "enp_b")

	uc := NewListNetworksUseCase(kr, nil)
	nets, _, err := uc.Execute(context.Background(), "user:usr_alice", NetworkFilter{ProjectID: "prj_1"}, Pagination{})
	require.NoError(t, err)
	assert.Len(t, nets, 2, "nil-authz must passthrough (dev/no-authz mode)")
}

// KAC-240: empty subject (system principal) → unfiltered passthrough preserved.
func TestListProjectAuthz_EmptySubjectPassthrough(t *testing.T) {
	kr := kachomock.NewRepository()
	seedNetworks(t, kr, "prj_1", "enp_a", "enp_b")

	uc := makeProjectAuthzUC(t, kr, projectViewChecker("user:usr_alice", "prj_1"))
	nets, _, err := uc.Execute(context.Background(), "", NetworkFilter{ProjectID: "prj_1"}, Pagination{})
	require.NoError(t, err)
	assert.Len(t, nets, 2, "empty subject → passthrough (system / migration job)")
}
