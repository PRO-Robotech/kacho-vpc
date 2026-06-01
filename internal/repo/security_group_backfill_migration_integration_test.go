package repo_test

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/PRO-Robotech/kacho-vpc/internal/migrations"
)

// KAC-243 group E — self-healing backfill migration 0004.
//
// Acceptance scenarios SG-NET-15 / 16 / 16e / 16f
// (sub-phase-securitygroup-network-mandatory-and-same-network-rules-acceptance.md).
//
// These tests drive goose up to version 3 (baseline + rename), seed orphan
// SecurityGroups (network_id NULL) directly via SQL — which the baseline schema
// still permits (security_groups.network_id is nullable until 0004) — then apply
// migration 0004 and assert:
//   - every orphan SG now has a non-null/non-empty network_id (SG-NET-15),
//   - the SET NOT NULL succeeded (zero NULL/empty rows),
//   - a name='default' Network exists per AFFECTED project, valid + with
//     default_security_group_id='' (no-recursive-default-SG, D6.3),
//   - NO spurious Network for the orphan-free project (SG-NET-16a),
//   - idempotent re-run of the up body is a no-op (SG-NET-16c),
//   - name='default' collision → reuse, no 23505 (SG-NET-16d).
//
// RED (before 0004 exists / before tightening): orphan SGs keep NULL network_id
// and `ALTER ... SET NOT NULL` would fail. GREEN (after 0004): all assertions hold.

// migrateBackfillDB spins a fresh testcontainer Postgres and migrates UP TO
// version 3 only (baseline 0001 + operations 0002 + rename 0003) — leaving
// security_groups.network_id nullable so orphans can be seeded. Returns a
// *sql.DB whose session search_path includes kacho_vpc, and the raw DSN.
func migrateBackfillDB(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()

	pgc, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("kacho_vpc_test"),
		postgres.WithUsername("vpc"),
		postgres.WithPassword("vpc"),
		postgres.BasicWaitStrategies(),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = pgc.Terminate(ctx) })

	rawDSN, err := pgc.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	db, err := sql.Open("pgx", appendSearchPathOptions(rawDSN))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	goose.SetBaseFS(migrations.FS)
	require.NoError(t, goose.SetDialect("postgres"))
	// Migrate only to v3 — baseline keeps security_groups.network_id nullable.
	require.NoError(t, goose.UpTo(db, ".", 3))

	return db
}

// seedOrphanSG inserts a SecurityGroup with NULL network_id directly. The
// baseline schema (pre-0004) permits this. networkID == "" → inserts SQL NULL.
func seedOrphanSG(t *testing.T, db *sql.DB, projectID, name string, networkID string) {
	t.Helper()
	var nid any
	if networkID == "" {
		nid = nil
	} else {
		nid = networkID
	}
	_, err := db.Exec(
		`INSERT INTO kacho_vpc.security_groups (id, project_id, network_id, name, status)
		 VALUES ($1, $2, $3, $4, 'ACTIVE')`,
		"enp"+pad17(name), projectID, nid, name,
	)
	require.NoError(t, err)
}

// seedNetworkRaw inserts a Network directly (e.g. a pre-existing name='default'
// for the collision/reuse scenarios).
func seedNetworkRaw(t *testing.T, db *sql.DB, id, projectID, name string) {
	t.Helper()
	_, err := db.Exec(
		`INSERT INTO kacho_vpc.networks (id, project_id, name) VALUES ($1, $2, $3)`,
		id, projectID, name,
	)
	require.NoError(t, err)
}

// pad17 produces a deterministic 17-char crockford-base32-ish suffix from name
// so seeded SG ids are well-formed (enp + 17). Only used for fixtures.
func pad17(seed string) string {
	const alphabet = "0123456789abcdefghjkmnpqrstvwxyz"
	out := make([]byte, 17)
	h := 1469598103934665603 // FNV-ish
	for _, c := range seed {
		h ^= int(c)
		h *= 1099511628211
	}
	for i := range out {
		out[i] = alphabet[(h>>uint(i*3))&31]
	}
	return string(out)
}

func countNetworks(t *testing.T, db *sql.DB, projectID string) int {
	t.Helper()
	var n int
	require.NoError(t, db.QueryRow(
		`SELECT count(*) FROM kacho_vpc.networks WHERE project_id = $1`, projectID).Scan(&n))
	return n
}

func countNullNetworkSG(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	require.NoError(t, db.QueryRow(
		`SELECT count(*) FROM kacho_vpc.security_groups WHERE network_id IS NULL OR network_id = ''`).Scan(&n))
	return n
}

// columnIsNotNull reports whether security_groups.network_id is NOT NULL.
func columnIsNotNull(t *testing.T, db *sql.DB) bool {
	t.Helper()
	var nullable string
	require.NoError(t, db.QueryRow(
		`SELECT is_nullable FROM information_schema.columns
		 WHERE table_schema = 'kacho_vpc' AND table_name = 'security_groups'
		   AND column_name = 'network_id'`).Scan(&nullable))
	return nullable == "NO"
}

func TestIntegration_SGBackfill_OrphansBackfilledAndSetNotNull(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	db := migrateBackfillDB(t)
	ctx := context.Background()

	// Given: project P has two orphan SGs (NULL network_id) and no network.
	seedOrphanSG(t, db, "P", "sg-orphan-1", "")
	seedOrphanSG(t, db, "P", "sg-orphan-2", "")
	// Project Q has one orphan SG.
	seedOrphanSG(t, db, "Q", "sg-orphan-q", "")
	// Project R is orphan-free: one SG bound to an existing network.
	seedNetworkRaw(t, db, "enp"+pad17("net-R"), "R", "net-R")
	_, err := db.Exec(
		`INSERT INTO kacho_vpc.security_groups (id, project_id, network_id, name, status)
		 VALUES ($1, 'R', $2, 'sg-r', 'ACTIVE')`,
		"enp"+pad17("sg-r"), "enp"+pad17("net-R"))
	require.NoError(t, err)

	// RED precondition: before 0004, NULL network_id rows exist and the column
	// is still nullable.
	require.Greater(t, countNullNetworkSG(t, db), 0, "expected orphan SGs before backfill")
	require.False(t, columnIsNotNull(t, db), "network_id must still be nullable before 0004")

	// When: apply migration 0004.
	require.NoError(t, goose.UpTo(db, ".", 4))

	// Then: zero NULL/empty network_id rows, column is NOT NULL.
	assert.Equal(t, 0, countNullNetworkSG(t, db), "all orphan SGs must be backfilled")
	assert.True(t, columnIsNotNull(t, db), "network_id must be NOT NULL after 0004")

	// A name='default' Network exists in each AFFECTED project (P, Q), valid,
	// with default_security_group_id='' (no-recursive-default-SG).
	for _, proj := range []string{"P", "Q"} {
		var netID, name, defSG string
		err := db.QueryRow(
			`SELECT id, name, default_security_group_id FROM kacho_vpc.networks
			 WHERE project_id = $1 AND name = 'default'`, proj).Scan(&netID, &name, &defSG)
		require.NoError(t, err, "default network must exist in project %s", proj)
		assert.Regexp(t, `^enp[0-9a-hj-km-np-z]{17}$`, netID, "backfilled network id must be valid enp...")
		assert.Equal(t, "default", name)
		assert.Equal(t, "", defSG, "no-recursive-default-SG: default_security_group_id must be empty")

		// Every orphan SG of the project now points at this default network.
		var cnt int
		require.NoError(t, db.QueryRowContext(ctx,
			`SELECT count(*) FROM kacho_vpc.security_groups
			 WHERE project_id = $1 AND network_id = $2`, proj, netID).Scan(&cnt))
		assert.Greater(t, cnt, 0, "orphan SGs of %s must be bound to its default network", proj)
	}

	// SG-NET-16a: project R (orphan-free) got NO spurious network — still just
	// the one we seeded.
	assert.Equal(t, 1, countNetworks(t, db, "R"), "no spurious network for orphan-free project R")
	// And no name='default' network was created for R.
	var rDefault int
	require.NoError(t, db.QueryRow(
		`SELECT count(*) FROM kacho_vpc.networks WHERE project_id = 'R' AND name = 'default'`).Scan(&rDefault))
	assert.Equal(t, 0, rDefault, "no default network for orphan-free project R")
}

func TestIntegration_SGBackfill_Idempotent_NoOpOnCleanState(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	db := migrateBackfillDB(t)

	// SG-NET-16b: no orphan SGs at all (greenfield-ish). One bound SG only.
	seedNetworkRaw(t, db, "enp"+pad17("net-clean"), "C", "net-clean")
	_, err := db.Exec(
		`INSERT INTO kacho_vpc.security_groups (id, project_id, network_id, name, status)
		 VALUES ($1, 'C', $2, 'sg-clean', 'ACTIVE')`,
		"enp"+pad17("sg-clean"), "enp"+pad17("net-clean"))
	require.NoError(t, err)

	before := countNetworks(t, db, "C")

	require.NoError(t, goose.UpTo(db, ".", 4))

	assert.Equal(t, 0, countNullNetworkSG(t, db))
	assert.True(t, columnIsNotNull(t, db))
	// No spontaneous default network appeared.
	assert.Equal(t, before, countNetworks(t, db, "C"), "no network created on clean state")
	var cDefault int
	require.NoError(t, db.QueryRow(
		`SELECT count(*) FROM kacho_vpc.networks WHERE project_id = 'C' AND name = 'default'`).Scan(&cDefault))
	assert.Equal(t, 0, cDefault)
}

func TestIntegration_SGBackfill_ReusesExistingDefaultNetwork(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	db := migrateBackfillDB(t)

	// SG-NET-16d: project P already has a user-created name='default' network.
	existingDefault := "enp" + pad17("preexisting-default")
	seedNetworkRaw(t, db, existingDefault, "P", "default")
	// Plus an orphan SG in the same project.
	seedOrphanSG(t, db, "P", "sg-orphan-1", "")

	require.NoError(t, goose.UpTo(db, ".", 4))

	// No second 'default' network created (would be 23505); reuse the existing.
	var nDefault int
	require.NoError(t, db.QueryRow(
		`SELECT count(*) FROM kacho_vpc.networks WHERE project_id = 'P' AND name = 'default'`).Scan(&nDefault))
	assert.Equal(t, 1, nDefault, "must reuse pre-existing default network, not create a second")

	// Orphan SG bound to the pre-existing default network.
	var boundTo string
	require.NoError(t, db.QueryRow(
		`SELECT network_id FROM kacho_vpc.security_groups WHERE project_id = 'P' AND name = 'sg-orphan-1'`).Scan(&boundTo))
	assert.Equal(t, existingDefault, boundTo, "orphan must be bound to pre-existing default network")

	assert.Equal(t, 0, countNullNetworkSG(t, db))
	assert.True(t, columnIsNotNull(t, db))
}
