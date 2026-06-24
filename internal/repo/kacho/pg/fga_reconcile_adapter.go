// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

// Per-service адаптеры для outbox/reconciler из corelib (backstop поверх
// register-drainer).
//
// Reconciler оркеструет re-drive / derive-from-state backfill / inverse-orphan GC,
// но доменное знание (в каких таблицах лежат tenant-ресурсы, как читать их
// project_id, существует ли ресурс) — per-service и инжектится через
// reconciler.ResourceEnumerator + reconciler.TupleRegistry. Этот файл реализует оба
// порта поверх resource-таблиц kacho_vpc.
//
// Scope — только project-hierarchy (каждый VPC-ресурс несет project_id): reconciler
// backfill-ит лишь выводимый project-hierarchy-tuple и никогда owner-self-grant (у
// vpc его нет). FGA kind для каждой таблицы — тот же vpc_*-тип, что эмитит Create
// ресурса (network/create.go и т.п.).
package pg

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PRO-Robotech/kacho-corelib/outbox/reconciler"
)

// vpcResourceTables сопоставляет каждый FGA object kind его resource-таблице в
// kacho_vpc. Каждая строка несет (id, project_id) — единственные колонки, нужные
// reconciler'у для синтеза project-hierarchy-intent. Kind'ы совпадают с тем, что
// эмитят Create-use-case'ы через fgaregister.ProjectHierarchy(..., "<kind>", id).
var vpcResourceTables = []struct {
	kind  string
	table string
}{
	{"vpc_network", "kacho_vpc.networks"},
	{"vpc_subnet", "kacho_vpc.subnets"},
	{"vpc_address", "kacho_vpc.addresses"},
	{"vpc_route_table", "kacho_vpc.route_tables"},
	{"vpc_security_group", "kacho_vpc.security_groups"},
	{"vpc_gateway", "kacho_vpc.gateways"},
	{"vpc_network_interface", "kacho_vpc.network_interfaces"},
}

// FGAReconcileAdapter реализует reconciler.ResourceEnumerator (живые resource-строки
// + проверка существования) поверх resource-таблиц kacho_vpc.
type FGAReconcileAdapter struct {
	pool *pgxpool.Pool
}

// NewFGAReconcileAdapter конструирует per-service reconciler-enumerator.
func NewFGAReconcileAdapter(pool *pgxpool.Pool) *FGAReconcileAdapter {
	return &FGAReconcileAdapter{pool: pool}
}

// kindToTable резолвит kind-метку в ее таблицу (или "" для неизвестного kind).
func kindToTable(kind string) string {
	for _, rt := range vpcResourceTables {
		if rt.kind == kind {
			return rt.table
		}
	}
	return ""
}

// ListResources перечисляет каждый живой VPC-ресурс как (kind, id, project_id) —
// источник истины для derive-from-state backfill. AddressPool и прочие
// Internal-admin-каталоги by-design без owner-tuple, поэтому не перечисляются.
func (a *FGAReconcileAdapter) ListResources(ctx context.Context) ([]reconciler.ResourceRow, error) {
	var out []reconciler.ResourceRow
	for _, rt := range vpcResourceTables {
		rows, err := a.pool.Query(ctx,
			fmt.Sprintf(`SELECT id, project_id FROM %s`, rt.table)) //nolint:gosec // trusted literal table name
		if err != nil {
			return nil, fmt.Errorf("vpc reconcile enumerate %s: %w", rt.table, err)
		}
		for rows.Next() {
			var id, projectID string
			if err := rows.Scan(&id, &projectID); err != nil {
				rows.Close()
				return nil, fmt.Errorf("vpc reconcile scan %s: %w", rt.table, err)
			}
			out = append(out, reconciler.ResourceRow{Kind: rt.kind, ID: id, ProjectID: projectID})
		}
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("vpc reconcile rows %s: %w", rt.table, err)
		}
	}
	return out, nil
}

// ResourceExists сообщает, существует ли еще (kind,id) — нужно inverse-orphan GC,
// чтобы убедиться, что ресурс зарегистрированного tuple действительно исчез, прежде
// чем снимать регистрацию.
func (a *FGAReconcileAdapter) ResourceExists(ctx context.Context, kind, id string) (bool, error) {
	table := kindToTable(kind)
	if table == "" {
		// Неизвестный kind → считаем несуществующим, чтобы залетный tuple можно было
		// собрать GC, но не паникуем на метке вне домена.
		return false, nil
	}
	var exists bool
	if err := a.pool.QueryRow(ctx,
		fmt.Sprintf(`SELECT EXISTS (SELECT 1 FROM %s WHERE id = $1)`, table), //nolint:gosec // trusted literal
		id,
	).Scan(&exists); err != nil {
		return false, fmt.Errorf("vpc reconcile exists %s/%s: %w", kind, id, err)
	}
	return exists, nil
}

// ListRegistered реализует reconciler.TupleRegistry: набор кандидатов на orphan-GC
// выводится из самого register-outbox — каждый (resource_kind, resource_id), чей
// ПОСЛЕДНИЙ intent — доставленный fga.register (sent_at NOT NULL). Reconciler затем
// подтверждает отсутствие ресурса + anti-race, прежде чем эмитить unregister, так
// что ложный кандидат (ресурс еще жив или есть более новый register-intent) никогда
// не собирается GC. Это избегает прямого чтения из FGA (clients в FGA не ходят).
func (a *FGAReconcileAdapter) ListRegistered(ctx context.Context) ([]reconciler.RegisteredTuple, error) {
	rows, err := a.pool.Query(ctx, `
		SELECT DISTINCT ON (resource_id) resource_kind, resource_id, event_type
		  FROM kacho_vpc.fga_register_outbox
		 WHERE resource_id <> '' AND sent_at IS NOT NULL
		 ORDER BY resource_id, id DESC`)
	if err != nil {
		return nil, fmt.Errorf("vpc reconcile list-registered: %w", err)
	}
	defer rows.Close()
	var out []reconciler.RegisteredTuple
	for rows.Next() {
		var kind, id, eventType string
		if err := rows.Scan(&kind, &id, &eventType); err != nil {
			return nil, fmt.Errorf("vpc reconcile list-registered scan: %w", err)
		}
		if eventType != "fga.register" {
			continue // последний intent — unregister → tuple не живой
		}
		out = append(out, reconciler.RegisteredTuple{Kind: kind, ID: id})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("vpc reconcile list-registered rows: %w", err)
	}
	return out, nil
}

// Проверка соответствия портам на этапе компиляции.
var (
	_ reconciler.ResourceEnumerator = (*FGAReconcileAdapter)(nil)
	_ reconciler.TupleRegistry      = (*FGAReconcileAdapter)(nil)
)
