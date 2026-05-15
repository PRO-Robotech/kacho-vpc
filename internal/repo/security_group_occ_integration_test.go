package repo_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coredb "github.com/PRO-Robotech/kacho-corelib/db"
	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	"github.com/PRO-Robotech/kacho-vpc/internal/service"
)

// TestIntegration_SecurityGroup_UpdateRules_ConcurrentOCC drives two concurrent
// UpdateRules calls against the same SecurityGroup row and asserts the
// optimistic-concurrency guard (xmin::text WHERE-clause) holds: at most one
// UPDATE lands per pair (never a silent lost-update), and any losing call gets
// the conflict error contract (service.ErrFailedPrecondition).
//
// Run for many iterations to make the race fire reliably; assert globally that
// a conflict was observed at least once (otherwise the OCC path is dead code).
func TestIntegration_SecurityGroup_UpdateRules_ConcurrentOCC(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx := context.Background()
	dsn := setupTestDB(t)

	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	nr := repo.NewNetworkRepo(pool)
	sgr := repo.NewSecurityGroupRepo(pool)

	net := &domain.Network{
		ID:       ids.NewID(ids.PrefixNetwork),
		FolderID: "folder-occ",
		Name:     domain.RcNameVPC("net-for-occ-sg"),
	}
	_, err = nr.Insert(ctx, net)
	require.NoError(t, err)

	const iterations = 30
	conflicts := 0
	bothSucceeded := 0

	for i := 0; i < iterations; i++ {
		// Fresh SG per iteration with a single seed rule.
		sg := &domain.SecurityGroup{
			ID:        ids.NewID(ids.PrefixSecurityGroup),
			FolderID:  "folder-occ",
			NetworkID: net.ID,
			CreatedAt: time.Now().UTC(),
			Name:      "", // empty name => not subject to (folder_id, name) partial unique
			Status:    "ACTIVE",
			Rules: []domain.SecurityGroupRule{
				{ID: "seed", Direction: "INGRESS", ProtocolName: "ANY", FromPort: -1, ToPort: -1, V4CidrBlocks: []string{"0.0.0.0/0"}},
			},
		}
		created, insErr := sgr.Insert(ctx, sg)
		require.NoError(t, insErr)

		// Each goroutine reads the SG (capturing the same xmin internally) and
		// calls UpdateRules with a distinct addition rule. Without the OCC guard
		// the second writer would overwrite the first → lost update.
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
					Direction:    "EGRESS",
					ProtocolName: "tcp",
					FromPort:     int64(8000 + idx),
					ToPort:       int64(8000 + idx),
					V4CidrBlocks: []string{"10.0.0.0/8"},
				}}
				_, errs[idx] = sgr.UpdateRules(ctx, created.ID, nil, add)
			}(g)
		}
		close(start)
		wg.Wait()

		switch {
		case errs[0] == nil && errs[1] == nil:
			// No conflict this round — both transactions serialized cleanly.
			bothSucceeded++
		case (errs[0] == nil) != (errs[1] == nil):
			// Exactly one winner — the loser must report the conflict contract.
			conflicts++
			loser := errs[0]
			if errs[1] != nil {
				loser = errs[1]
			}
			require.ErrorIs(t, loser, service.ErrFailedPrecondition,
				"the losing concurrent UpdateRules must return the OCC conflict error")
		default:
			t.Fatalf("iter %d: both UpdateRules failed (errA=%v, errB=%v) — at least one must always win", i, errs[0], errs[1])
		}

		// Invariant check: whichever update(s) succeeded, the row must contain
		// the seed rule plus the additions from every successful writer — and in
		// no case a *lost* update (a successful writer's rule missing).
		final, getErr := sgr.Get(ctx, created.ID)
		require.NoError(t, getErr)
		have := map[string]bool{}
		for _, r := range final.Rules {
			have[r.ID] = true
		}
		assert.True(t, have["seed"], "iter %d: seed rule must survive", i)
		if errs[0] == nil {
			assert.True(t, have[ruleIDs[0]], "iter %d: successful writer A's rule must be present (no lost update)", i)
		}
		if errs[1] == nil {
			assert.True(t, have[ruleIDs[1]], "iter %d: successful writer B's rule must be present (no lost update)", i)
		}
		// When a conflict occurred, exactly one of the two additions should be present.
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

		require.NoError(t, sgr.Delete(ctx, created.ID))
	}

	t.Logf("OCC iterations=%d conflicts=%d both-succeeded=%d", iterations, conflicts, bothSucceeded)
	assert.Positive(t, conflicts,
		"expected at least one xmin-conflict across %d iterations — if zero, the OCC guard may be ineffective or the race never materialised", iterations)
}
