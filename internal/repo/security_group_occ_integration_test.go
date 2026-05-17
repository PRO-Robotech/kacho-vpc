package repo_test

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coredb "github.com/PRO-Robotech/kacho-corelib/db"
	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/helpers"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
	kachopg "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/pg"
)

// TestIntegration_SecurityGroup_UpdateRules_ConcurrentOCC drives two concurrent
// UpdateRules calls against the same SecurityGroup row and asserts the
// optimistic-concurrency guard (xmin::text WHERE-clause) holds: at most one
// UPDATE lands per pair (never a silent lost-update), and any losing call gets
// the conflict error contract (helpers.ErrFailedPrecondition).
//
// Run for many iterations to make the race fire reliably; assert globally that
// a conflict was observed at least once (otherwise the OCC path is dead code).
//
// KAC-94 A.7 sub-PR 5/6: переписан на CQRS Writer (раньше — repo.SecurityGroupRepo
// с автоматической tx-обёрткой; теперь каждый UpdateRules идёт в своей writer-TX).
func TestIntegration_SecurityGroup_UpdateRules_ConcurrentOCC(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx := context.Background()
	dsn := setupTestDB(t)

	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	r := kachopg.New(pool, nil)
	defer r.Close()

	withTx := func(t *testing.T, fn func(kacho.RepositoryWriter) error) error {
		t.Helper()
		w, err := r.Writer(ctx)
		require.NoError(t, err)
		if err := fn(w); err != nil {
			w.Abort()
			return err
		}
		return w.Commit()
	}

	net := &domain.Network{
		ID:       ids.NewID(ids.PrefixNetwork),
		ProjectID: "folder-occ",
		Name:     domain.RcNameVPC("net-for-occ-sg"),
	}
	require.NoError(t, withTx(t, func(w kacho.RepositoryWriter) error {
		_, e := w.Networks().Insert(ctx, net)
		return e
	}))

	const iterations = 30
	conflicts := 0
	bothSucceeded := 0

	for i := 0; i < iterations; i++ {
		// Fresh SG per iteration with a single seed rule.
		sg := &domain.SecurityGroup{
			ID:        ids.NewID(ids.PrefixSecurityGroup),
			ProjectID:  "folder-occ",
			NetworkID: net.ID,
			Name:      "",
			Status:    domain.SecurityGroupStatusActive,
			Rules: []domain.SecurityGroupRule{
				{ID: "seed", Direction: domain.SecurityGroupRuleDirectionIngress, ProtocolName: "ANY", FromPort: -1, ToPort: -1, V4CidrBlocks: []string{"0.0.0.0/0"}},
			},
		}
		require.NoError(t, withTx(t, func(w kacho.RepositoryWriter) error {
			_, e := w.SecurityGroups().Insert(ctx, sg)
			return e
		}))

		// Each goroutine opens own writer-TX and calls UpdateRules.
		var wg sync.WaitGroup
		errs := make([]error, 2)
		ruleIDs := [2]string{"add-a", "add-b"}
		start := make(chan struct{})
		for g := 0; g < 2; g++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				<-start
				add := []domain.SecurityGroupRule{{
					ID:           ruleIDs[idx],
					Direction:    domain.SecurityGroupRuleDirectionEgress,
					ProtocolName: "tcp",
					FromPort:     int64(8000 + idx),
					ToPort:       int64(8000 + idx),
					V4CidrBlocks: []string{"10.0.0.0/8"},
				}}
				errs[idx] = withTx(t, func(w kacho.RepositoryWriter) error {
					_, e := w.SecurityGroups().UpdateRules(ctx, sg.ID, nil, add)
					return e
				})
			}(g)
		}
		close(start)
		wg.Wait()

		switch {
		case errs[0] == nil && errs[1] == nil:
			bothSucceeded++
		case (errs[0] == nil) != (errs[1] == nil):
			conflicts++
			loser := errs[0]
			if errs[1] != nil {
				loser = errs[1]
			}
			require.ErrorIs(t, loser, helpers.ErrFailedPrecondition,
				"the losing concurrent UpdateRules must return the OCC conflict error")
		default:
			t.Fatalf("iter %d: both UpdateRules failed (errA=%v, errB=%v) — at least one must always win", i, errs[0], errs[1])
		}

		// Invariant check: whichever update(s) succeeded, the row must contain
		// the seed rule plus the additions from every successful writer.
		rd, err := r.Reader(ctx)
		require.NoError(t, err)
		final, getErr := rd.SecurityGroups().Get(ctx, sg.ID)
		require.NoError(t, rd.Close())
		require.NoError(t, getErr)
		have := map[string]bool{}
		for _, rr := range final.Rules {
			have[rr.ID] = true
		}
		assert.True(t, have["seed"], "iter %d: seed rule must survive", i)
		if errs[0] == nil {
			assert.True(t, have[ruleIDs[0]], "iter %d: successful writer A's rule must be present (no lost update)", i)
		}
		if errs[1] == nil {
			assert.True(t, have[ruleIDs[1]], "iter %d: successful writer B's rule must be present (no lost update)", i)
		}
		if conflicts > 0 && errs[0] != errs[1] {
			cnt := 0
			if have[ruleIDs[0]] {
				cnt++
			}
			if have[ruleIDs[1]] {
				cnt++
			}
			assert.Equal(t, 1, cnt, "iter %d: on conflict exactly one addition must land", i)
		}

		require.NoError(t, withTx(t, func(w kacho.RepositoryWriter) error {
			return w.SecurityGroups().Delete(ctx, sg.ID)
		}))
	}

	t.Logf("OCC iterations=%d conflicts=%d both-succeeded=%d", iterations, conflicts, bothSucceeded)
	assert.Positive(t, conflicts,
		"expected at least one xmin-conflict across %d iterations — if zero, the OCC guard may be ineffective or the race never materialised", iterations)
}
