// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

// Бэкстоп register-outbox поверх существующего fgaproxy-механизма: reconciler,
// метрики и fail-closed boot-gate. Co-commit-атомарность записи intent'а не
// меняется — бэкстоп лишь чинит застрявшие строки и не дает принимать мутации,
// пока drainer не подключен.
//
// Проверяемые сценарии:
//   - reconciler re-drive'ит «отравленную» строку обратно в claimable → она доставляется;
//   - fail-closed boot-gate: --require-iam без drainer → Create отклонен;
//   - длинная недоступность IAM (transient, > MaxAttempts) не отравляет intent —
//     он доставляется ровно один раз при восстановлении.
//
// Миграция, добавляющая resource_kind/resource_id, аддитивна и backfill-safe —
// ее column-present-проверка лежит тоже здесь (reconciler адресует intent'ы по
// resource_id).
//
// testcontainers Postgres 16; реальные corelib reconciler/drainer + fake IAM.
// Пропускается под -short.
package clients_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-corelib/outbox/bootgate"
	"github.com/PRO-Robotech/kacho-corelib/outbox/metrics"
	"github.com/PRO-Robotech/kacho-corelib/outbox/reconciler"

	"github.com/PRO-Robotech/kacho-vpc/internal/fgaboot"
	pgrepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/pg"
)

const vpcOutboxTable = "kacho_vpc.fga_register_outbox"

// newVPCReconciler собирает reconciler поверх vpc register-outbox и
// per-service-адаптера (FGAReconcileAdapter реализует оба порта).
func newVPCReconciler(t *testing.T, pool *pgxpool.Pool, grace time.Duration) *reconciler.Reconciler {
	t.Helper()
	ad := pgrepo.NewFGAReconcileAdapter(pool)
	rc, err := reconciler.New(pool, reconciler.Config{
		Table:       vpcOutboxTable,
		Channel:     "kacho_vpc_fga_register_outbox",
		MaxAttempts: 10,
		GraceWindow: grace,
	}, reconciler.Adapters{Enumerator: ad, Registry: ad}, nil)
	require.NoError(t, err)
	return rc
}

// Test_1_4_08A_Migration0008_ResourceColumns — миграция добавляет в
// kacho_vpc.fga_register_outbox колонки resource_kind/resource_id аддитивно и
// backfill-safe (NOT NULL DEFAULT пустой строкой): прежний column-minimal INSERT по-прежнему
// работает, а reconciler может адресовать intent'ы по ресурсу.
func Test_1_4_08A_Migration0008_ResourceColumns(t *testing.T) {
	pool := setupRegisterOutboxDB(t)
	ctx := context.Background()

	// Обе колонки есть, нужного типа + NOT NULL DEFAULT '' (backfill-safe).
	for _, col := range []string{"resource_kind", "resource_id"} {
		var dataType, isNullable string
		var def *string
		err := pool.QueryRow(ctx, `
			SELECT data_type, is_nullable, column_default
			  FROM information_schema.columns
			 WHERE table_schema='kacho_vpc' AND table_name='fga_register_outbox' AND column_name=$1`,
			col).Scan(&dataType, &isNullable, &def)
		require.NoError(t, err, "column %s must exist (migration 0008)", col)
		assert.Equal(t, "text", dataType, "%s is text", col)
		assert.Equal(t, "NO", isNullable, "%s is NOT NULL (backfill-safe with default)", col)
		require.NotNil(t, def, "%s has a default", col)
		assert.Contains(t, *def, "''", "%s defaults to empty string (backfill-safe)", col)
	}

	// Backfill-safe: INSERT без новых колонок проходит (прежний путь).
	_, err := pool.Exec(ctx,
		`INSERT INTO kacho_vpc.fga_register_outbox (event_type, payload)
		 VALUES ('fga.register', '{"subject_id":"project:p","relation":"project","object":"vpc_network:net-x"}'::jsonb)`)
	require.NoError(t, err, "legacy column-minimal INSERT still works (backfill-safe)")
	var kind, id string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT resource_kind, resource_id FROM kacho_vpc.fga_register_outbox LIMIT 1`).Scan(&kind, &id))
	assert.Equal(t, "", kind)
	assert.Equal(t, "", id)
}

// Test_1_4_30_ReconcilerRedrivesPoisoned — «отравленный» register-intent
// (attempt_count == MaxAttempts, sent_at NULL) reconciler возвращает в claimable,
// после чего drainer его доставляет (sent_at NOT NULL). Атомарность не затронута
// (resource-writer не меняется) — бэкстоп лишь чинит застрявшую строку.
func Test_1_4_30_ReconcilerRedrivesPoisoned(t *testing.T) {
	pool := setupRegisterOutboxDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// «Отравленный» intent: ранее applier отверг его как permanent; причина
	// устранена, и его нужно доставить заново.
	_, err := pool.Exec(ctx,
		`INSERT INTO kacho_vpc.fga_register_outbox
		   (event_type, resource_kind, resource_id, payload, attempt_count, last_error)
		 VALUES ('fga.register','vpc_network','net-redrive',
		         '{"subject_id":"project:p","relation":"project","object":"vpc_network:net-redrive"}'::jsonb,
		         10,'was permanent')`)
	require.NoError(t, err)

	rc := newVPCReconciler(t, pool, 0)
	n, err := rc.RedrivePoisoned(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, n, "exactly one poisoned row re-driven")

	// Re-driven-строка снова claimable (attempt_count сброшен, last_error очищен).
	var attempt int
	var lastErr *string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT attempt_count, last_error FROM kacho_vpc.fga_register_outbox WHERE resource_id='net-redrive'`).
		Scan(&attempt, &lastErr))
	assert.Less(t, attempt, 10, "attempt_count reset below MaxAttempts (claimable)")
	assert.Nil(t, lastErr, "last_error cleared")

	// Теперь drainer ее доставляет (IAM здоров).
	iam := newRecordingIAM()
	d := newRegisterDrainer(t, pool, iam, 10)
	go func() { _ = d.Run(ctx) }()
	require.Eventually(t, func() bool {
		return iam.count("vpc_network:net-redrive") == 1
	}, 5*time.Second, 50*time.Millisecond, "re-driven intent delivered exactly once")
}

// Test_1_4_31_FailClosedBootGate_RefusesCreate — при взведенном --require-iam и
// неподключенном register-drainer guardCreateUnary отклоняет мутирующий Create
// (UNAVAILABLE), ресурс не создается; read-RPC (не-Create) проходят. После
// подключения drainer'а (SetConnected(true)) Create снова разрешен. Postgres не
// нужен — проверяется чистое поведение gate + interceptor.
func Test_1_4_31_FailClosedBootGate_RefusesCreate(t *testing.T) {
	gate := bootgate.New(bootgate.Config{RequireIAM: true, Service: "kacho-vpc"})

	// Еще не подключено → Ready() false, Create отклоняется.
	assert.False(t, gate.Ready(), "require-iam + not connected → NotReady")

	// Мутирующий Create отклонен с UNAVAILABLE; внутренний handler не вызывается.
	createInvoked := false
	createHandler := func(_ context.Context, _ any) (any, error) { createInvoked = true; return "ok", nil }
	guard := fgaboot.GuardCreateUnary(gate)

	_, err := guard(context.Background(), nil,
		&grpc.UnaryServerInfo{FullMethod: "/kacho.cloud.vpc.v1.NetworkService/Create"}, createHandler)
	require.Error(t, err)
	assert.Equal(t, codes.Unavailable, status.Code(err), "Create refused fail-closed (UNAVAILABLE)")
	assert.False(t, createInvoked, "resource not created — handler never reached")

	// Read-RPC (Get) не гейтится — проходит даже при неподключенном drainer'е.
	getInvoked := false
	getHandler := func(_ context.Context, _ any) (any, error) { getInvoked = true; return "net", nil }
	_, err = guard(context.Background(), nil,
		&grpc.UnaryServerInfo{FullMethod: "/kacho.cloud.vpc.v1.NetworkService/Get"}, getHandler)
	require.NoError(t, err)
	assert.True(t, getInvoked, "read RPC works on a not-yet-ready instance")

	// Internal-admin Create (AddressPool) намеренно не гейтится — у него нет owner-tuple.
	adminInvoked := false
	adminHandler := func(_ context.Context, _ any) (any, error) { adminInvoked = true; return "pool", nil }
	_, err = guard(context.Background(), nil,
		&grpc.UnaryServerInfo{FullMethod: "/kacho.cloud.vpc.v1.InternalAddressPoolService/Create"}, adminHandler)
	require.NoError(t, err)
	assert.True(t, adminInvoked, "Internal-admin Create not gated (no owner-tuple)")

	// Drainer подключился → gate открывается, Create разрешен.
	gate.SetConnected(true)
	assert.True(t, gate.Ready(), "connected → Ready")
	createInvoked = false
	_, err = guard(context.Background(), nil,
		&grpc.UnaryServerInfo{FullMethod: "/kacho.cloud.vpc.v1.NetworkService/Create"}, createHandler)
	require.NoError(t, err)
	assert.True(t, createInvoked, "Create allowed once IAM-register path connected")
}

// Test_1_4_31_RequireIAMOff_NoOp — контраст: --require-iam=false (dev) → gate
// no-op, Create всегда разрешен, Ready() всегда true.
func Test_1_4_31_RequireIAMOff_NoOp(t *testing.T) {
	gate := bootgate.New(bootgate.Config{RequireIAM: false, Service: "kacho-vpc"})
	assert.True(t, gate.Ready(), "require-iam off → always Ready (dev)")
	guard := fgaboot.GuardCreateUnary(gate)
	invoked := false
	_, err := guard(context.Background(), nil,
		&grpc.UnaryServerInfo{FullMethod: "/kacho.cloud.vpc.v1.NetworkService/Create"},
		func(_ context.Context, _ any) (any, error) { invoked = true; return "ok", nil })
	require.NoError(t, err)
	assert.True(t, invoked, "Create allowed in dev back-compat mode")
}

// Test_1_4_32_LongOutageNoPoison_ThenMetricsSurface — IAM Unavailable дольше, чем
// MaxAttempts подряд (transient-класс) → intent НЕ отравляется → доставляется
// ровно один раз при восстановлении, а Collector метрик показывает backlog, пока
// строка pending. Контракт классификации corelib (Unavailable → transient, никогда
// не poison) прогоняется через реальный vpc-applier.
func Test_1_4_32_LongOutageNoPoison_ThenMetricsSurface(t *testing.T) {
	pool := setupRegisterOutboxDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const maxAttempts = 5
	// down остается true, пока тест его не переключит — это гарантирует, что drainer
	// сделает заметно БОЛЬШЕ maxAttempts подряд transient-попыток (Unavailable) до
	// любой возможности доставки. По правилу классификации corelib (Unavailable →
	// transient, никогда не poison) intent обязан пережить всю недоступность.
	var down atomic.Bool
	down.Store(true)
	var attempts atomic.Int32
	iam := newRecordingIAM()
	iam.errFn = func(_ int) error {
		if down.Load() {
			attempts.Add(1)
			return status.Error(codes.Unavailable, "iam down")
		}
		return nil
	}
	d := newRegisterDrainer(t, pool, iam, maxAttempts)
	go func() { _ = d.Run(ctx) }()

	insertRegisterIntent(t, ctx, pool, "fga.register", "project:p", "project", "vpc_network:net-long")

	// Пока IAM недоступен: drainer делает > maxAttempts попыток, но intent НЕ
	// отравлен (все еще pending, sent_at NULL) — и Collector метрик показывает
	// backlog + oldest-age. Это и есть гарантия no-poison для transient-ошибок.
	rec := metrics.NewMemRecorder()
	col := metrics.NewCollector(pool, rec, metrics.CollectorConfig{Table: vpcOutboxTable, MaxAttempts: maxAttempts})
	require.Eventually(t, func() bool {
		_ = col.Scan(ctx)
		return attempts.Load() > maxAttempts &&
			rec.BacklogDepth(vpcOutboxTable) >= 1 && rec.OldestPendingAgeSeconds(vpcOutboxTable) > 0
	}, 10*time.Second, 100*time.Millisecond, "> maxAttempts transient attempts, still pending (not poisoned), backlog surfaced")

	// Строка по-прежнему pending (sent_at NULL), несмотря на > maxAttempts transient-
	// сбоев — НЕ отравлена (transient-класс никогда не открывает poison-gate).
	var sentNullDuringOutage bool
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT sent_at IS NULL FROM kacho_vpc.fga_register_outbox WHERE payload->>'object'='vpc_network:net-long'`).
		Scan(&sentNullDuringOutage))
	assert.True(t, sentNullDuringOutage, "intent durable (pending) through a transient outage longer than MaxAttempts")

	// IAM восстановился → тот же durable-intent доставляется ровно один раз (не потерян).
	down.Store(false)
	require.Eventually(t, func() bool {
		return iam.count("vpc_network:net-long") == 1
	}, 10*time.Second, 100*time.Millisecond, "tuple delivered exactly once after long transient outage (no poison)")

	var sentNotNull bool
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT sent_at IS NOT NULL FROM kacho_vpc.fga_register_outbox WHERE payload->>'object'='vpc_network:net-long'`).
		Scan(&sentNotNull))
	assert.True(t, sentNotNull, "intent ultimately delivered (not lost to transient outage)")

	// Счетчик poisoned остается 0 (permanent-ошибки не было).
	col2 := metrics.NewCollector(pool, rec, metrics.CollectorConfig{Table: vpcOutboxTable, MaxAttempts: maxAttempts})
	require.NoError(t, col2.Scan(ctx))
	assert.Equal(t, float64(0), rec.PoisonedCount(vpcOutboxTable),
		"a transient (Unavailable) outage must NOT poison — outbox_poisoned stays 0")
}
