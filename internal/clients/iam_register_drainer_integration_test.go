// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

// End-to-end путь register-drainer'а: строка в `kacho_vpc.fga_register_outbox`
// (форму пишет repo-эмиттер FGARegister внутри writer-tx ресурса) →
// LISTEN/NOTIFY-wakeup → corelib-drainer → clients.NewIAMRegisterApplier →
// fake InternalIAMServiceClient.RegisterResource.
//
// Покрывает критичные сценарии, отличающие transactional-outbox от best-effort
// dual-write:
//
//	happy: intent применен, строка помечена sent_at NOT NULL
//	IAM Unavailable N раз → intent durable (sent_at NULL, last_error, растущий
//	    attempt_count) → recover → применен (tuple не теряется навсегда)
//	concurrent: две реплики drainer'а → каждый intent применен ровно один раз
//	permanent-ошибка (InvalidArgument) → poison, очередь не заблокирована
//
// testcontainers Postgres 16 + реальный corelib-drainer + реальный applier поверх
// fake InternalIAMServiceClient (процесс kacho-iam не нужен). Пропускается под -short.
package clients_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	coredb "github.com/PRO-Robotech/kacho-corelib/db"
	"github.com/PRO-Robotech/kacho-corelib/observability"
	"github.com/PRO-Robotech/kacho-corelib/outbox/drainer"
	iamv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/iam/v1"

	"github.com/PRO-Robotech/kacho-vpc/internal/clients"
	"github.com/PRO-Robotech/kacho-vpc/internal/migrations"
)

func setupRegisterOutboxDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration test in -short mode (testcontainers Postgres)")
	}
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

	dsn, err := pgc.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	db, err := sql.Open("pgx", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	goose.SetBaseFS(migrations.FS)
	require.NoError(t, goose.SetDialect("postgres"))
	require.NoError(t, goose.Up(db, "."))

	const optionsParam = "options=-c%20search_path%3Dkacho_vpc%2Cpublic"
	if !strings.Contains(dsn, "options=") && !strings.Contains(dsn, "options%3D") {
		if strings.Contains(dsn, "?") {
			dsn += "&" + optionsParam
		} else {
			dsn += "?" + optionsParam
		}
	}

	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return pool
}

// recordingIAM — concurrency-safe fake InternalIAMServiceClient (подмножество
// register/unreg). errFn(call) опционально возвращает ошибку для 1-based номера вызова.
type recordingIAM struct {
	mu         sync.Mutex
	registered map[string]int // object → счетчик apply (проверка exactly-once)
	errFn      func(call int) error
	calls      int32
}

func newRecordingIAM() *recordingIAM {
	return &recordingIAM{registered: map[string]int{}}
}

func (r *recordingIAM) RegisterResource(_ context.Context, in *iamv1.RegisterResourceRequest, _ ...grpc.CallOption) (*iamv1.RegisterResourceResponse, error) {
	n := atomic.AddInt32(&r.calls, 1)
	if r.errFn != nil {
		if err := r.errFn(int(n)); err != nil {
			return nil, err
		}
	}
	r.mu.Lock()
	r.registered[in.GetObject()]++
	r.mu.Unlock()
	return &iamv1.RegisterResourceResponse{}, nil
}

func (r *recordingIAM) UnregisterResource(_ context.Context, in *iamv1.UnregisterResourceRequest, _ ...grpc.CallOption) (*iamv1.UnregisterResourceResponse, error) {
	atomic.AddInt32(&r.calls, 1)
	r.mu.Lock()
	delete(r.registered, in.GetObject())
	r.mu.Unlock()
	return &iamv1.UnregisterResourceResponse{}, nil
}

func (r *recordingIAM) count(object string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.registered[object]
}

// insertRegisterIntent пишет одну fga.register-строку напрямую (повторяет
// repo-эмиттер FGARegister без полного пути создания ресурса).
func insertRegisterIntent(t *testing.T, ctx context.Context, pool *pgxpool.Pool, eventType, subject, relation, object string) {
	t.Helper()
	payload, _ := json.Marshal(map[string]string{"subject_id": subject, "relation": relation, "object": object})
	_, err := pool.Exec(ctx,
		`INSERT INTO kacho_vpc.fga_register_outbox (event_type, payload, created_at)
		 VALUES ($1, $2::jsonb, now())`, eventType, payload)
	require.NoError(t, err)
}

func newRegisterDrainer(t *testing.T, pool *pgxpool.Pool, iam clients.IAMRegisterRPC, maxAttempts int) *drainer.Drainer[clients.FGARegisterPayload] {
	t.Helper()
	logger := observability.NewSlogger(testLoggerWriter{t})
	d, err := drainer.New[clients.FGARegisterPayload](
		pool,
		drainer.Config{
			Table:        "kacho_vpc.fga_register_outbox",
			Channel:      "kacho_vpc_fga_register_outbox",
			BatchSize:    32,
			PollFallback: 1 * time.Second,
			MaxAttempts:  maxAttempts,
			BackoffMin:   50 * time.Millisecond,
			BackoffMax:   200 * time.Millisecond,
			ApplyTimeout: 2 * time.Second,
		},
		clients.DecodeFGARegisterPayload,
		clients.NewIAMRegisterApplier(iam),
		logger,
	)
	require.NoError(t, err)
	return d
}

// TestVPC_SEC_D_09_RegisterDrainerHappyApply — drainer применяет register-intent
// через RegisterResource и помечает строку sent_at NOT NULL.
func TestVPC_SEC_D_09_RegisterDrainerHappyApply(t *testing.T) {
	pool := setupRegisterOutboxDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	iam := newRecordingIAM()
	d := newRegisterDrainer(t, pool, iam, 5)
	go func() { _ = d.Run(ctx) }()

	insertRegisterIntent(t, ctx, pool, "fga.register", "project:proj-x", "project", "vpc_network:net-1")

	require.Eventually(t, func() bool {
		return iam.count("vpc_network:net-1") == 1
	}, 3*time.Second, 50*time.Millisecond, "RegisterResource applied exactly once")

	var sentNotNull bool
	require.Eventually(t, func() bool {
		_ = pool.QueryRow(ctx, `SELECT sent_at IS NOT NULL FROM kacho_vpc.fga_register_outbox LIMIT 1`).Scan(&sentNotNull)
		return sentNotNull
	}, 2*time.Second, 50*time.Millisecond)

	var lastErrNull bool
	require.NoError(t, pool.QueryRow(ctx, `SELECT last_error IS NULL FROM kacho_vpc.fga_register_outbox LIMIT 1`).Scan(&lastErrNull))
	assert.True(t, lastErrNull)
}

// TestVPC_SEC_D_11_RegisterDrainerIAMDownThenRecover (КРИТИЧНО) — IAM Unavailable
// на первых N apply → intent durable (sent_at NULL, last_error LIKE Unavailable,
// растущий attempt_count) → IAM восстанавливается → drainer применяет; tuple не
// теряется навсегда.
func TestVPC_SEC_D_11_RegisterDrainerIAMDownThenRecover(t *testing.T) {
	pool := setupRegisterOutboxDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var downUntil int32 = 3 // первые 3 вызова Unavailable, дальше OK (recovery)
	iam := newRecordingIAM()
	iam.errFn = func(call int) error {
		if int32(call) <= atomic.LoadInt32(&downUntil) {
			return status.Error(codes.Unavailable, "iam down")
		}
		return nil
	}
	d := newRegisterDrainer(t, pool, iam, 20)
	go func() { _ = d.Run(ctx) }()

	insertRegisterIntent(t, ctx, pool, "fga.register", "project:proj-x", "project", "vpc_network:net-1")

	// Пока IAM лежит: intent остается durable (sent_at NULL) с растущим attempts и
	// last_error Unavailable.
	require.Eventually(t, func() bool {
		var sentNull bool
		var attempts int
		var lastErr *string
		_ = pool.QueryRow(ctx,
			`SELECT sent_at IS NULL, attempt_count, last_error FROM kacho_vpc.fga_register_outbox LIMIT 1`).
			Scan(&sentNull, &attempts, &lastErr)
		return sentNull && attempts >= 1 && lastErr != nil && strings.Contains(*lastErr, "Unavailable")
	}, 3*time.Second, 50*time.Millisecond, "intent durable while IAM down (not lost)")

	// После recovery: drainer применяет, строка помечена sent.
	require.Eventually(t, func() bool {
		return iam.count("vpc_network:net-1") == 1
	}, 5*time.Second, 50*time.Millisecond, "tuple applied after IAM recovery")

	var sentNotNull bool
	require.Eventually(t, func() bool {
		_ = pool.QueryRow(ctx, `SELECT sent_at IS NOT NULL FROM kacho_vpc.fga_register_outbox LIMIT 1`).Scan(&sentNotNull)
		return sentNotNull
	}, 2*time.Second, 50*time.Millisecond)
}

// TestVPC_SEC_D_13_RegisterDrainerConcurrentTwoReplicas — две реплики drainer'а на
// одной БД: 20 register-intent'ов → все применены ровно один раз (CAS-claim, без
// double-apply, без пропусков между репликами).
func TestVPC_SEC_D_13_RegisterDrainerConcurrentTwoReplicas(t *testing.T) {
	pool := setupRegisterOutboxDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	iam := newRecordingIAM()
	d1 := newRegisterDrainer(t, pool, iam, 5)
	d2 := newRegisterDrainer(t, pool, iam, 5)
	go func() { _ = d1.Run(ctx) }()
	go func() { _ = d2.Run(ctx) }()

	const n = 20
	for i := 0; i < n; i++ {
		obj := "vpc_network:net-" + itoa(i)
		insertRegisterIntent(t, ctx, pool, "fga.register", "project:proj-x", "project", obj)
	}

	require.Eventually(t, func() bool {
		var sent int
		_ = pool.QueryRow(ctx, `SELECT count(*) FROM kacho_vpc.fga_register_outbox WHERE sent_at IS NOT NULL`).Scan(&sent)
		return sent == n
	}, 5*time.Second, 100*time.Millisecond, "all 20 intents marked sent")

	// Exactly-once: всего успешных вызовов RegisterResource == n (без double-apply).
	assert.Equal(t, int32(n), atomic.LoadInt32(&iam.calls), "exactly-once across two replicas")
	for i := 0; i < n; i++ {
		assert.Equal(t, 1, iam.count("vpc_network:net-"+itoa(i)))
	}
}

// TestVPC_SEC_D_14_RegisterDrainerPermanentPoison — IAM InvalidArgument → poison
// (attempt_count >= MaxAttempts, sent_at NULL), а drainer продолжает обрабатывать
// остальные (здоровые) строки — не застревает на poison-строке.
func TestVPC_SEC_D_14_RegisterDrainerPermanentPoison(t *testing.T) {
	pool := setupRegisterOutboxDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const maxAttempts = 3
	iam := newRecordingIAM()
	iam.errFn = func(call int) error {
		// Первый вызов (poison-строка) отвергается перманентно; остальные OK.
		if call == 1 {
			return status.Error(codes.InvalidArgument, "malformed tuple")
		}
		return nil
	}
	d := newRegisterDrainer(t, pool, iam, maxAttempts)
	go func() { _ = d.Run(ctx) }()

	// Сначала poison-строка, затем здоровая. Порядок «poison первой» фиксируем
	// детерминированно: ждём, пока drainer РЕАЛЬНО заберёт poison-строку
	// (attempt_count вырос) — а не фиксированным сном, который на медленном CI мог
	// бы истечь до того, как drainer коснулся poison, и порядок не гарантировался.
	insertRegisterIntent(t, ctx, pool, "fga.register", "project:proj-x", "project", "vpc_network:poison")
	require.Eventually(t, func() bool {
		var attempts int
		if err := pool.QueryRow(ctx,
			`SELECT attempt_count FROM kacho_vpc.fga_register_outbox
			   WHERE payload->>'object' = 'vpc_network:poison'`).Scan(&attempts); err != nil {
			return false
		}
		return attempts >= 1
	}, 5*time.Second, 20*time.Millisecond, "drainer must claim the poison row before healthy is enqueued")
	insertRegisterIntent(t, ctx, pool, "fga.register", "project:proj-x", "project", "vpc_network:healthy")

	// Здоровая строка в итоге применена — drainer не застрял на poison.
	require.Eventually(t, func() bool {
		return iam.count("vpc_network:healthy") == 1
	}, 5*time.Second, 100*time.Millisecond, "healthy row applied despite poison ahead")

	// Poison-строка: attempt_count >= MaxAttempts, sent_at NULL, last_error содержит
	// InvalidArgument.
	require.Eventually(t, func() bool {
		var attempts int
		var sentNull bool
		var lastErr *string
		_ = pool.QueryRow(ctx,
			`SELECT attempt_count, sent_at IS NULL, last_error FROM kacho_vpc.fga_register_outbox
			   WHERE payload->>'object' = 'vpc_network:poison'`).Scan(&attempts, &sentNull, &lastErr)
		return attempts >= maxAttempts && sentNull && lastErr != nil && strings.Contains(*lastErr, "InvalidArgument")
	}, 3*time.Second, 100*time.Millisecond, "poison row marked, not retried forever")
}

// itoa — локальный int→string (чтобы не тянуть strconv в тест).
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(b[pos:])
}
