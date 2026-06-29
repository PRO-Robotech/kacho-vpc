// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package pg_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coredb "github.com/PRO-Robotech/kacho-corelib/db"
	"github.com/PRO-Robotech/kacho-corelib/ids"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	kachopg "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/pg"
)

// Группа C среза AddressPool parity: DB CHECK-parity (within-service инвариант
// формы обязан жить на DB-уровне, ban #10). Невалидные INSERT/UPDATE идут
// напрямую через SQL (минуя use-case) — проверяем, что DB сама отбивает (23514),
// а repo не leak'ает pgx-текст наружу.

// vpc8G-C1 — миграция добавляет ровно четыре CHECK; партиал-UNIQUE из 0001 цел.
func TestAddressPoolChecks_vpc8G_C1_ConstraintsPresent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	pool, err := coredb.NewPool(ctx, setupTestDB(t))
	require.NoError(t, err)
	defer pool.Close()

	rows, err := pool.Query(ctx, `
		SELECT conname FROM pg_constraint
		WHERE conrelid = 'kacho_vpc.address_pools'::regclass AND contype = 'c'`)
	require.NoError(t, err)
	defer rows.Close()
	got := map[string]bool{}
	for rows.Next() {
		var n string
		require.NoError(t, rows.Scan(&n))
		got[n] = true
	}
	require.NoError(t, rows.Err())
	for _, want := range []string{
		"address_pools_name_chk",
		"address_pools_description_len_chk",
		"address_pools_kind_chk",
		"address_pools_selector_priority_chk",
	} {
		assert.True(t, got[want], "CHECK %s must exist after migration", want)
	}

	// Партиал-UNIQUE «один default на (zone_id, kind)» из 0001 — нетронут.
	var idxName string
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT indexname FROM pg_indexes
		WHERE schemaname = 'kacho_vpc' AND indexname = 'address_pools_zone_kind_default_uniq'`).
		Scan(&idxName))
	assert.Equal(t, "address_pools_zone_kind_default_uniq", idxName)

	// Существующая валидная строка проходит все новые CHECK.
	_, err = pool.Exec(ctx, `
		INSERT INTO address_pools (id, name, description, kind, selector_priority)
		VALUES ($1, 'valid-pool', 'ok', 1, 0)`, ids.NewID("apl"))
	assert.NoError(t, err, "valid row must pass new CHECKs")
}

// checkViolation — выполнить INSERT/UPDATE и вернуть PgError (ожидается 23514).
func mustPgError(t *testing.T, err error) *pgconn.PgError {
	t.Helper()
	require.Error(t, err)
	var pgErr *pgconn.PgError
	require.True(t, errors.As(err, &pgErr), "expected *pgconn.PgError, got %T", err)
	return pgErr
}

// vpc8G-C2 — прямой INSERT с невалидным name → 23514; repo-маппинг → ErrInvalidArg без leak.
func TestAddressPoolChecks_vpc8G_C2_BadName(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	pool, err := coredb.NewPool(ctx, setupTestDB(t))
	require.NoError(t, err)
	defer pool.Close()

	// (а) прямой SQL → 23514 на address_pools_name_chk.
	_, err = pool.Exec(ctx, `
		INSERT INTO address_pools (id, name, kind) VALUES ($1, '1bad!', 1)`, ids.NewID("apl"))
	pgErr := mustPgError(t, err)
	assert.Equal(t, "23514", pgErr.Code)
	assert.Contains(t, pgErr.ConstraintName, "name_chk")

	// (б) repo writer Insert (минуя use-case Validate) → ErrInvalidArg, без leak'а SQL.
	r := kachopg.New(pool, nil)
	w, err := r.Writer(ctx)
	require.NoError(t, err)
	defer w.Abort()
	_, err = w.AddressPools().Insert(ctx, &domain.AddressPool{
		ID: ids.NewID("apl"), Name: "1bad!", Kind: domain.AddressPoolKindExternalPublic,
		V4CIDRBlocks: []string{"203.0.113.0/24"},
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, repo.ErrInvalidArg), "23514 → repo.ErrInvalidArg, got %v", err)
	assertNoSQLLeak(t, err)
}

// vpc8G-C3 — description > 256 → 23514.
func TestAddressPoolChecks_vpc8G_C3_DescriptionTooLong(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	pool, err := coredb.NewPool(ctx, setupTestDB(t))
	require.NoError(t, err)
	defer pool.Close()

	_, err = pool.Exec(ctx, `
		INSERT INTO address_pools (id, name, description, kind)
		VALUES ($1, 'desc-pool', repeat('x', 257), 1)`, ids.NewID("apl"))
	pgErr := mustPgError(t, err)
	assert.Equal(t, "23514", pgErr.Code)
	assert.Contains(t, pgErr.ConstraintName, "description_len_chk")
}

// vpc8G-C4 — reserved kind → 23514; kind=1 проходит.
func TestAddressPoolChecks_vpc8G_C4_BadKind(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	pool, err := coredb.NewPool(ctx, setupTestDB(t))
	require.NoError(t, err)
	defer pool.Close()

	_, err = pool.Exec(ctx, `
		INSERT INTO address_pools (id, name, kind) VALUES ($1, 'kind-bad', 2)`, ids.NewID("apl"))
	pgErr := mustPgError(t, err)
	assert.Equal(t, "23514", pgErr.Code)
	assert.Contains(t, pgErr.ConstraintName, "kind_chk")

	_, err = pool.Exec(ctx, `
		INSERT INTO address_pools (id, name, kind) VALUES ($1, 'kind-ok', 1)`, ids.NewID("apl"))
	assert.NoError(t, err, "kind=1 (EXTERNAL_PUBLIC) must pass")
}

// vpc8G-C5 — отрицательный selector_priority на INSERT и UPDATE → 23514.
func TestAddressPoolChecks_vpc8G_C5_NegativeSelectorPriority(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	pool, err := coredb.NewPool(ctx, setupTestDB(t))
	require.NoError(t, err)
	defer pool.Close()

	_, err = pool.Exec(ctx, `
		INSERT INTO address_pools (id, name, kind, selector_priority)
		VALUES ($1, 'prio-bad', 1, -1)`, ids.NewID("apl"))
	pgErr := mustPgError(t, err)
	assert.Equal(t, "23514", pgErr.Code)
	assert.Contains(t, pgErr.ConstraintName, "selector_priority_chk")

	// UPDATE валидной строки в отрицательный priority — тоже 23514.
	okID := ids.NewID("apl")
	_, err = pool.Exec(ctx, `
		INSERT INTO address_pools (id, name, kind, selector_priority)
		VALUES ($1, 'prio-ok', 1, 0)`, okID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `UPDATE address_pools SET selector_priority = -5 WHERE id = $1`, okID)
	pgErr = mustPgError(t, err)
	assert.Equal(t, "23514", pgErr.Code)
	assert.Contains(t, pgErr.ConstraintName, "selector_priority_chk")
}

// assertNoSQLLeak — сообщение клиенту не несет SQL/pgx-фрагментов (G3).
func assertNoSQLLeak(t *testing.T, err error) {
	t.Helper()
	msg := err.Error()
	for _, leak := range []string{"SQLSTATE", "23514", "address_pools", "_chk", "INSERT", "UPDATE"} {
		assert.NotContains(t, strings.ToUpper(msg), strings.ToUpper(leak),
			"error message must not leak SQL fragment %q: %s", leak, msg)
	}
}
