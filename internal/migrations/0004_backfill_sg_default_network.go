package migrations

// 0004_backfill_sg_default_network.go — KAC-243 group E (D6).
//
// Self-healing backfill migration that makes security_groups.network_id
// mandatory (NOT NULL) WITHOUT failing on legacy "orphan" SGs (network_id NULL
// or empty), which existed during the short "optional network_id" window
// (kacho-proto#8). Owner-mandated replacement of the prior fail-fast approach.
//
// This is a Go goose migration (NOT pure SQL) because the default Network it
// creates needs a crockford-base32 `enp…` id from kacho-corelib/ids.NewID,
// which cannot be generated in PL/pgSQL without re-porting that package
// (kacho-vpc/CLAUDE.md §2). goose v3 picks up Go migrations registered via
// AddMigrationContext from `init()`: the version (0004) is parsed from this
// file's name, and the registry is merged with the SQL migrations discovered
// via SetBaseFS (the embed FS holds *.sql only, so goose includes the
// registered Go migration "wholesale"). Both cmd/migrator and
// internal/repo (integration tests) import this package, so 0004 is discovered
// in production AND in tests.
//
// up runs inside a single goose transaction (UseTx=true, *sql.Tx) so the whole
// backfill + SET NOT NULL is atomic — a partial failure rolls back entirely.
//
// Algorithm (FK-ordered; D6.5):
//  1. For each DISTINCT project_id among orphan SGs (network_id IS NULL OR ''):
//     ensure a default Network in that project — reuse an existing
//     name='default' network if present, otherwise INSERT a new one with
//     ids.NewID(ids.PrefixNetwork) (ON CONFLICT (project_id, name) DO NOTHING +
//     re-SELECT, for idempotency / collision-reuse — SG-NET-16c/16d).
//     No recursive default-SG is created: default_security_group_id stays ''
//     (D6.3). The Network insert happens BEFORE the SG update so the FK
//     security_groups.network_id → networks(id) is satisfied.
//  2. UPDATE security_groups SET network_id = <that network id> for that
//     project's orphans.
//  3. After all projects are backfilled, ALTER COLUMN network_id SET NOT NULL
//     (last statement; zero NULL/empty rows remain).
//
// Idempotent: a re-run finds the name='default' network (reuse), and the
// UPDATE touches 0 rows once every orphan is bound (SG-NET-16c). A project with
// no orphan SGs gets no network (the DISTINCT loop is empty — SG-NET-16a/16b).
//
// down: relax the constraint (DROP NOT NULL). Backfilled default Networks are
// data, NOT dropped on down (we cannot tell user-created from backfill-created,
// and orphan SGs still legitimately reference them).

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/pressly/goose/v3"

	"github.com/PRO-Robotech/kacho-corelib/ids"
)

func init() {
	goose.AddMigrationContext(upBackfillSGDefaultNetwork, downBackfillSGDefaultNetwork)
}

func upBackfillSGDefaultNetwork(ctx context.Context, tx *sql.Tx) error {
	// 1. Collect the distinct projects that own at least one orphan SG. We only
	//    ever create networks for these — orphan-free projects are untouched
	//    (SG-NET-16a/16b).
	rows, err := tx.QueryContext(ctx,
		`SELECT DISTINCT project_id
		   FROM kacho_vpc.security_groups
		  WHERE network_id IS NULL OR network_id = ''`)
	if err != nil {
		return fmt.Errorf("0004: select orphan projects: %w", err)
	}
	var projects []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			rows.Close()
			return fmt.Errorf("0004: scan orphan project: %w", err)
		}
		projects = append(projects, p)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("0004: iterate orphan projects: %w", err)
	}
	rows.Close()

	for _, project := range projects {
		netID, err := ensureDefaultNetwork(ctx, tx, project)
		if err != nil {
			return err
		}
		// 2. Bind this project's orphan SGs to the default network. FK is
		//    satisfied because the network was inserted/located above.
		if _, err := tx.ExecContext(ctx,
			`UPDATE kacho_vpc.security_groups
			    SET network_id = $1
			  WHERE project_id = $2
			    AND (network_id IS NULL OR network_id = '')`,
			netID, project,
		); err != nil {
			return fmt.Errorf("0004: bind orphan SGs in project %q: %w", project, err)
		}
	}

	// 3. Now that no orphan rows remain, tighten the column. Idempotent: a
	//    re-applied tightening on an already-NOT-NULL column is a no-op in
	//    Postgres.
	if _, err := tx.ExecContext(ctx,
		`ALTER TABLE kacho_vpc.security_groups
		   ALTER COLUMN network_id SET NOT NULL`,
	); err != nil {
		return fmt.Errorf("0004: set network_id NOT NULL: %w", err)
	}

	// Refresh the source-of-truth comment on the column (the baseline said
	// "unbound / folder-level SG, kacho-proto#8").
	if _, err := tx.ExecContext(ctx,
		`COMMENT ON COLUMN kacho_vpc.security_groups.network_id IS
		 'network_id — mandatory + immutable (KAC-243); legacy orphan-SG backfilled to per-project default-Network'`,
	); err != nil {
		return fmt.Errorf("0004: comment network_id: %w", err)
	}

	return nil
}

// ensureDefaultNetwork returns the id of the project's name='default' Network,
// reusing an existing one (user-created or backfill-created) or inserting a new
// one. Idempotent and collision-safe via ON CONFLICT (project_id, name) +
// re-SELECT.
func ensureDefaultNetwork(ctx context.Context, tx *sql.Tx, project string) (string, error) {
	// Fast path / idempotent reuse: an existing name='default' network of the
	// project (SG-NET-16c/16d).
	var existing string
	err := tx.QueryRowContext(ctx,
		`SELECT id FROM kacho_vpc.networks
		  WHERE project_id = $1 AND name = 'default'`, project,
	).Scan(&existing)
	switch {
	case err == nil:
		return existing, nil
	case err != sql.ErrNoRows:
		return "", fmt.Errorf("0004: lookup default network in project %q: %w", project, err)
	}

	// None yet — insert a fresh default Network. ON CONFLICT guards against a
	// race / re-run; DB defaults fill description/labels/default_security_group_id/
	// route_distinguisher/created_at (D6.2/D6.3).
	newID := ids.NewID(ids.PrefixNetwork)
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO kacho_vpc.networks (id, project_id, name)
		 VALUES ($1, $2, 'default')
		 ON CONFLICT (project_id, name) DO NOTHING`,
		newID, project,
	); err != nil {
		return "", fmt.Errorf("0004: insert default network in project %q: %w", project, err)
	}

	// Re-SELECT: returns either our freshly inserted row, or a row inserted by a
	// concurrent path that won the ON CONFLICT (collision-reuse).
	var netID string
	if err := tx.QueryRowContext(ctx,
		`SELECT id FROM kacho_vpc.networks
		  WHERE project_id = $1 AND name = 'default'`, project,
	).Scan(&netID); err != nil {
		return "", fmt.Errorf("0004: re-select default network in project %q: %w", project, err)
	}
	return netID, nil
}

func downBackfillSGDefaultNetwork(ctx context.Context, tx *sql.Tx) error {
	// Relax the mandatory constraint. Backfilled networks and the SG→network
	// bindings are intentionally NOT undone (they are data; orphan SGs still
	// reference them and we cannot distinguish backfill-created from
	// user-created default networks).
	if _, err := tx.ExecContext(ctx,
		`ALTER TABLE kacho_vpc.security_groups
		   ALTER COLUMN network_id DROP NOT NULL`,
	); err != nil {
		return fmt.Errorf("0004 down: drop NOT NULL on network_id: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`COMMENT ON COLUMN kacho_vpc.security_groups.network_id IS NULL`,
	); err != nil {
		return fmt.Errorf("0004 down: reset network_id comment: %w", err)
	}
	return nil
}
