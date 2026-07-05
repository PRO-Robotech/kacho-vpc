// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package repo_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

// waitForLockWaiter детерминированно ждёт, пока хотя бы один backend в БД встанет
// в очередь за lock'ом (`pg_locks.granted = false`), т.е. фоновая TX-B реально
// дошла до своего блокирующего `SELECT ... FOR UPDATE` и висит на row-lock'е TX-A.
//
// Заменяет хрупкий фиксированный `time.Sleep`: на нагруженном CI фоновая горутина
// могла не успеть встать в очередь за lock'ом до истечения сна — тогда contended-
// путь не исполнялся, а тест зеленел вхолостую (мнимое покрытие race'а). Здесь мы
// продвигаем foreground-TX ТОЛЬКО увидев реальное lock-contention состояние, так
// что contested-interleaving исполняется на каждом прогоне.
//
// Каждый integration-тест поднимает собственный Postgres-контейнер (setupTestDB),
// поэтому `pg_locks` (cluster-wide вью) видит только backend'ы этого теста —
// ложных срабатываний от параллельных тестов нет. Опрос идёт через отдельное
// соединение из пула (TX-A и TX-B держат по одному; дефолтный pgxpool MaxConns
// ≥4 оставляет место третьему).
func waitForLockWaiter(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var waiting int
		require.NoError(t, pool.QueryRow(ctx,
			`SELECT count(*) FROM pg_locks WHERE NOT granted`).Scan(&waiting))
		if waiting > 0 {
			return
		}
		time.Sleep(5 * time.Millisecond) // poll-backoff, не форсирование interleaving'а
	}
	t.Fatal("timed out waiting for a blocked backend (pg_locks NOT granted) — contended interleaving never established")
}
