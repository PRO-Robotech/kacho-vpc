// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package main

// Observability-проводка composition root: Prometheus diagnostic-listener
// (/metrics + /healthz + /readyz), dependency-aware readiness и build-info.
// prometheus импортируется только в adapter-пакете internal/observability/metrics
// (Clean Architecture) — здесь лишь wiring.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/connectivity"

	"github.com/PRO-Robotech/kacho-corelib/operations"
	"github.com/PRO-Robotech/kacho-corelib/outbox/bootgate"

	"github.com/PRO-Robotech/kacho-vpc/internal/clients"
	"github.com/PRO-Robotech/kacho-vpc/internal/observability/health"
	vpcmetrics "github.com/PRO-Robotech/kacho-vpc/internal/observability/metrics"
)

// Сентинелы readiness-чекеров: причина «down» в логах/ответе /readyz без leak'а
// внутренних деталей наружу (имена зависимостей — operational, cluster-internal).
var (
	errDrainerNotConnected = errors.New("register-drainer not connected to kacho-iam")
	errLROWorkerDown       = errors.New("LRO dispatcher loop not running")
	errIAMConnShutdown     = errors.New("kacho-iam authz connection is shut down")
)

// build-info — инжектится через -ldflags "-X main.buildVersion=… -X main.buildCommit=…";
// дефолты для локальной сборки.
var (
	buildVersion = "dev"
	buildCommit  = "unknown"
)

// startLROWorker подключает Prometheus-Recorder и логгер к package-level
// default-registry LRO-worker'а (ConfigureDefault) и поднимает его dispatcher-loop
// (Start) ДО приема трафика. Решает два дефекта boot'а:
//   - readiness-deadlock: без явного Start dispatcher стартует лениво на первом Run,
//     но под в NotReady трафика не получает → Run не происходит → вечный NotReady.
//     Явный Start делает Ready()=true до трафика;
//   - dead live-worker метрики: default-registry создается с NopRecorder, поэтому
//     terminal-write retries/failures и inflight gauge от ЖИВОГО worker-пути не
//     эмитились. WithRecorder подключает их к /metrics.
//
// ConfigureDefault обязан предшествовать Start; вызывается один раз из composition
// root (повторный вызов после старта вернул бы ErrWorkerStarted).
func startLROWorker(rec operations.Recorder, logger *slog.Logger) error {
	if err := operations.ConfigureDefault(operations.WithRecorder(rec), operations.WithLogger(logger)); err != nil {
		return fmt.Errorf("configure LRO default-registry: %w", err)
	}
	operations.Start()
	return nil
}

// buildReadinessCheckers собирает чекеры критичных зависимостей для readiness.
// liveness намеренно НЕ включает их (защита от restart-storm). iam-authz
// регистрируется только когда authzConn реально сконфигурирован (production).
func buildReadinessCheckers(pool *pgxpool.Pool, gate *bootgate.Gate, authzConn clients.Conn) []health.Checker {
	checkers := []health.Checker{
		{Name: "database", Check: func(ctx context.Context) error { return pool.Ping(ctx) }},
		{Name: "register-drainer", Check: func(context.Context) error {
			if gate.Ready() {
				return nil
			}
			return errDrainerNotConnected
		}},
		{Name: "lro-worker", Check: func(context.Context) error {
			if operations.Ready() {
				return nil
			}
			return errLROWorkerDown
		}},
	}
	if authzConn != nil {
		checkers = append(checkers, health.Checker{Name: "iam-authz", Check: func(context.Context) error {
			return authzConnHealth(authzConn)
		}})
	}
	return checkers
}

// authzConnHealth — best-effort проверка состояния gRPC-conn в kacho-iam. Shutdown
// → down; прочие состояния (Idle/Connecting/Ready/TransientFailure) считаем «up»:
// gRPC лениво переподключается, а readiness-down при кратком TransientFailure дал
// бы ложный flap. Conn без GetState (нестандартная обертка) → считаем up.
func authzConnHealth(conn clients.Conn) error {
	stater, ok := conn.(interface{ GetState() connectivity.State })
	if !ok {
		return nil
	}
	if stater.GetState() == connectivity.Shutdown {
		return errIAMConnShutdown
	}
	return nil
}

// startDiagnosticListener поднимает cluster-internal HTTP-listener для метрик и
// health-проб. Возвращает task для parallel.ExecAbstract и shutdown-функцию.
// Отключен (enable=false / пустой endpoint) → (nil, no-op): byte-identical
// back-compat, листенер не поднимается.
func startDiagnosticListener(addr string, m *vpcmetrics.Metrics, agg *health.Aggregator, logger *slog.Logger) (task func() error, shutdown func(context.Context), err error) {
	if addr == "" {
		return nil, func(context.Context) {}, nil
	}
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", m.Handler())
	mux.Handle("GET /healthz", agg.LiveHandler())
	mux.Handle("GET /readyz", agg.ReadyHandler())

	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	lis, lerr := net.Listen("tcp", addr)
	if lerr != nil {
		return nil, nil, lerr
	}
	logger.Info("kacho-vpc diagnostic listener", "endpoint", addr, "paths", "/metrics,/healthz,/readyz")

	task = func() error {
		if serr := srv.Serve(lis); serr != nil && serr != http.ErrServerClosed {
			return serr
		}
		return nil
	}
	shutdown = func(ctx context.Context) { _ = srv.Shutdown(ctx) }
	return task, shutdown, nil
}
