// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package repo_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coredb "github.com/PRO-Robotech/kacho-corelib/db"
	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-corelib/operations"
)

// Additive nullable-колонка account_id на kacho_vpc.operations нужна, чтобы INSERT
// из shared-corelib operations.Repo.CreateWithPrincipal (он теперь пишет
// account_id безусловно) проходил. Без колонки КАЖДАЯ async-мутация vpc падает с
// SQLSTATE 42703 undefined_column.
//
// account_id — IAM-only денормализация: метаданные vpc-операции не несут поля
// account_id, поэтому corelib extractAccountID → "" → SQL NULL. Тест фиксирует
// оба факта: (a) INSERT больше не дает 42703, (b) сохраненный account_id остается
// NULL.
func TestIntegration_Operations_AccountIDColumn_NullForVPC(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx := context.Background()
	dsn := setupTestDB(t)

	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	opsRepo := operations.NewRepo(pool, "kacho_vpc")

	opID := ids.NewID(ids.PrefixOperationVPC)
	now := time.Now().UTC().Truncate(time.Second)

	// Тот же INSERT-путь, что у любого vpc Create/Update/Delete worker'а. Без
	// колонки account_id это падает: "column \"account_id\" of relation
	// \"operations\" does not exist (SQLSTATE 42703)".
	require.NoError(t, opsRepo.CreateWithPrincipal(ctx, operations.Operation{
		ID:          opID,
		Description: "create network",
		CreatedAt:   now,
		ModifiedAt:  now,
	}, operations.SystemPrincipal()))

	// account_id для vpc должен быть SQL NULL (IAM-only денормализация).
	var accountID *string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT account_id FROM kacho_vpc.operations WHERE id = $1`, opID,
	).Scan(&accountID))
	assert.Nil(t, accountID, "account_id must be SQL NULL for a vpc operation")
}
