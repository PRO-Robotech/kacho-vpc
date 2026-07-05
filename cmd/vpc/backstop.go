// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package main

// Обвязка outbox-backstop поверх register-drainer: добавляет observability и
// safety, не меняя co-commit-атомарность register-intent в writer-TX.
//
//   - reconciler: периодический RedrivePoisoned — переотправляет отравленные
//     register-intents через тот же kacho_vpc.fga_register_outbox, что дренит drainer.
//   - metrics: Collector сканирует backlog/oldest/poisoned; WithPoisonObserver
//     drainer'а инкрементит outbox_poisoned_total.
//   - boot-gate: KACHO_VPC_REQUIRE_IAM отказывает мутирующему Create и отдает
//     NotReady, пока register-drainer не подключен к IAM.

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PRO-Robotech/kacho-corelib/outbox/metrics"
	"github.com/PRO-Robotech/kacho-corelib/outbox/reconciler"

	pgrepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/pg"
)

const (
	// fgaRegisterOutboxTable / Channel — register-outbox kacho-vpc, общий для
	// drainer, reconciler и metrics-collector (одна таблица, один путь доставки).
	fgaRegisterOutboxTable   = "kacho_vpc.fga_register_outbox"
	fgaRegisterOutboxChannel = "kacho_vpc_fga_register_outbox"
)

// startBackstop — собирает reconciler + metrics Collector поверх vpc
// register-outbox и крутит их в фоне, пока ctx не отменен. Оба — best-effort
// observability/repair: транзиентная ошибка скана/прохода логируется, но не
// фатальна. Интервалы reconcile/metrics — разумные production-каденции по умолчанию.
func startBackstop(ctx context.Context, pool *pgxpool.Pool, rec metrics.Recorder, logger *slog.Logger) error {
	rc, err := reconciler.New(pool, reconciler.Config{
		Table:       fgaRegisterOutboxTable,
		Channel:     fgaRegisterOutboxChannel,
		GraceWindow: time.Minute, // anti-race: отсрочка, чтобы re-Create успел записать свой intent первым
	}, reconciler.Adapters{
		Enumerator: pgrepo.NewFGAReconcileAdapter(pool),
		Registry:   pgrepo.NewFGAReconcileAdapter(pool),
	}, logger.With(slog.String("component", "fga-register-reconciler")))
	if err != nil {
		return err
	}

	go runReconciler(ctx, rc, logger)

	col := metrics.NewCollector(pool, rec, metrics.CollectorConfig{Table: fgaRegisterOutboxTable})
	go col.Run(ctx, func(err error) {
		logger.Warn("outbox metrics scan failed", "err", err)
	})

	logger.Info("FGA register backstop started (reconciler + metrics)", "table", fgaRegisterOutboxTable)
	return nil
}

// runReconciler гоняет проход RedrivePoisoned на периодическом тикере: отравленные/
// исчерпанные register-intents (sent_at NULL, attempt_count >= MaxAttempts)
// сбрасываются в claimable, чтобы drainer переотправил их с ОРИГИНАЛЬНЫМ,
// корректным для декодера tuple-payload. Re-drive — рабочий backstop для уже
// атомарного сервиса.
//
// BackfillFromState / GCOrphans в этом цикле сознательно НЕ запускаются: они
// переэмитят corelib-fixed payload ({"project_id":…} / {}), который vpc-декодер
// tuple (subject_id/relation/object) не разбирает — их запуск отравил бы здоровое
// состояние. И поскольку каждый vpc Create co-commit'ит свой register-intent в
// writer-TX ресурса, never-enqueued строк для backfill на практике нет.
// Per-service enumerator/registry adapter все равно подключен (его требует
// reconciler.New, и он задает table-scope для RedrivePoisoned) — backstop остается
// готов, если corelib-контракт re-emit обзаведется per-service payload-хуком.
func runReconciler(ctx context.Context, rc *reconciler.Reconciler, logger *slog.Logger) {
	const interval = 5 * time.Minute
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if n, err := rc.RedrivePoisoned(ctx); err != nil {
				logger.Warn("reconciler redrive-poisoned failed", "err", err)
			} else if n > 0 {
				logger.Info("reconciler re-drove poisoned intents", "count", n)
			}
		}
	}
}
