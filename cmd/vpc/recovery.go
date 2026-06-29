// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package main

// Durable LRO recovery wiring: доменный resolver VPC + corelib-reconciler.
//
// При крахе процесса live-worker'ы умирают, их in-flight операции остаются
// done=false навсегда. Reconciler при старте (RecoverAll — ДО приема трафика) и
// периодическим sweep'ом (Run — backstop) разрешает осиротевшие операции в
// терминал, сверяясь с committed-реальностью ресурса через доменный resolver.

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PRO-Robotech/kacho-corelib/operations"

	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/operationresolver"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

const (
	// reconcileOrphanGrace — orphan-кандидат должен быть старше этого окна, чтобы
	// reconciler не разрешил преждевременно еще-живого worker'а. Должен превышать
	// максимальную ожидаемую длительность операции.
	reconcileOrphanGrace = 5 * time.Minute
	// reconcileInterval — каденция периодического backstop-sweep'а.
	reconcileInterval = 30 * time.Second
	// reconcileBatchSize — размер пачки claim'а за один sweep.
	reconcileBatchSize = 100
)

// startLRORecovery конструирует доменный resolver + corelib-reconciler поверх
// schema kacho_vpc, прогоняет startup-recovery (RecoverAll, ДО Serve) и запускает
// периодический backstop (Run) в фоне до отмены ctx. Ошибка startup-recovery —
// не фатальна (best-effort backstop; периодический Run добьет позже): boot не
// валится из-за transient DB-сбоя reconciler'а.
func startLRORecovery(ctx context.Context, pool *pgxpool.Pool, repo kachorepo.Repository, rec operations.Recorder, logger *slog.Logger) {
	resolver := operationresolver.New(repo, operationresolver.WithLogger(logger))
	reconciler := operations.NewReconciler(pool, resolver, operations.ReconcilerConfig{
		Schema:      "kacho_vpc",
		OrphanGrace: reconcileOrphanGrace,
		BatchSize:   reconcileBatchSize,
		Interval:    reconcileInterval,
	},
		operations.WithReconcilerRecorder(rec),
		operations.WithReconcilerLogger(logger.With(slog.String("component", "lro-reconciler"))),
	)

	if err := reconciler.RecoverAll(ctx); err != nil {
		logger.Error("LRO startup-recovery failed; periodic sweep will retry", "err", err)
	} else {
		logger.Info("LRO startup-recovery complete (orphaned operations resolved)")
	}

	go reconciler.Run(ctx)
	logger.Info("LRO reconciler backstop started", "interval", reconcileInterval, "orphan_grace", reconcileOrphanGrace)
}
