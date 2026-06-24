// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package pg

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/PRO-Robotech/kacho-corelib/filter"
	"github.com/PRO-Robotech/kacho-corelib/validate"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/helpers"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// gatewayReader — Get/List поверх произвольной pgx.Tx (read-only или RW).
// Не имеет своего state кроме tx. Это read-сторона CQRS-разделения Gateway-репо;
// SQL/scan-семантика вынесена в helpers (helpers.GatewayCols / helpers.ScanGateway /
// helpers.WrapGatewayErr / helpers.MarshalJSONB).
type gatewayReader struct {
	tx pgx.Tx
}

// Get — well-formed-но-нет → NotFound с "Gateway <id> not found".
func (r *gatewayReader) Get(ctx context.Context, id string) (*kacho.GatewayRecord, error) {
	q := fmt.Sprintf(`SELECT %s FROM gateways WHERE id = $1`, helpers.GatewayCols)
	row := r.tx.QueryRow(ctx, q, id)
	g, err := helpers.ScanGateway(row)
	if err != nil {
		return nil, helpers.WrapGatewayErr(err, id)
	}
	return g, nil
}

// List — cursor-based pagination + filter.Parse с whitelist allowedFields=["name"].
func (r *gatewayReader) List(ctx context.Context, f kacho.GatewayFilter, p kacho.Pagination) ([]*kacho.GatewayRecord, string, error) {
	pageSize, err := validate.PageSize("page_size", p.PageSize)
	if err != nil {
		return nil, "", err
	}

	args := []any{}
	conditions := []string{}
	argIdx := 1

	if f.ProjectID != "" {
		conditions = append(conditions, fmt.Sprintf("project_id = $%d", argIdx))
		args = append(args, f.ProjectID)
		argIdx++
	}
	if f.Name != "" {
		conditions = append(conditions, fmt.Sprintf("name = $%d", argIdx))
		args = append(args, f.Name)
		argIdx++
	}
	if f.Filter != "" {
		ast, perr := filter.Parse(f.Filter, []string{"name"})
		if perr != nil {
			return nil, "", helpers.InvalidFilterErr(perr)
		}
		if ast != nil {
			frag, fargs := ast.ToSQL(argIdx)
			conditions = append(conditions, frag)
			args = append(args, fargs...)
			argIdx += len(fargs)
		}
	}
	if p.PageToken != "" {
		ts, id, derr := helpers.DecodePageToken(p.PageToken)
		if derr != nil {
			return nil, "", helpers.InvalidPageTokenErr(derr)
		}
		conditions = append(conditions, fmt.Sprintf("(created_at, id) > ($%d, $%d)", argIdx, argIdx+1))
		args = append(args, ts, id)
		argIdx += 2
	}

	var where string
	if len(conditions) > 0 {
		where = "WHERE " + strings.Join(conditions, " AND ")
	}
	q := fmt.Sprintf(`SELECT %s FROM gateways %s ORDER BY created_at ASC, id ASC LIMIT $%d`, helpers.GatewayCols, where, argIdx)
	args = append(args, pageSize+1)

	rows, err := r.tx.Query(ctx, q, args...)
	if err != nil {
		return nil, "", helpers.WrapGatewayErr(err, "")
	}
	defer rows.Close()

	var result []*kacho.GatewayRecord
	for rows.Next() {
		g, err := helpers.ScanGateway(rows)
		if err != nil {
			return nil, "", helpers.WrapGatewayErr(err, "")
		}
		result = append(result, g)
	}
	if err := rows.Err(); err != nil {
		return nil, "", helpers.WrapGatewayErr(err, "")
	}

	var nextToken string
	if int64(len(result)) > pageSize {
		last := result[pageSize-1]
		nextToken = helpers.EncodePageToken(last.CreatedAt, last.ID)
		result = result[:pageSize]
	}
	return result, nextToken, nil
}

// ListByIDs — List с safety-net `WHERE id = ANY($allowedIDs)`: возвращает только
// явно разрешенные id (authz-фильтрация на стороне use-case). Та же list-семантика
// (project_id/name/filter/cursor); allowedIDs передается типизированным
// text[]-параметром (SQL-injection-safe), pagination применяется к отфильтрованному
// набору. Пустой allowedIDs → (nil, "", nil) short-circuit первым стейтментом.
func (r *gatewayReader) ListByIDs(ctx context.Context, f kacho.GatewayFilter, allowedIDs []string, p kacho.Pagination) ([]*kacho.GatewayRecord, string, error) {
	if len(allowedIDs) == 0 {
		return nil, "", nil
	}
	pageSize, err := validate.PageSize("page_size", p.PageSize)
	if err != nil {
		return nil, "", err
	}

	args := []any{allowedIDs}
	conditions := []string{"id = ANY($1::text[])"}
	argIdx := 2

	if f.ProjectID != "" {
		conditions = append(conditions, fmt.Sprintf("project_id = $%d", argIdx))
		args = append(args, f.ProjectID)
		argIdx++
	}
	if f.Name != "" {
		conditions = append(conditions, fmt.Sprintf("name = $%d", argIdx))
		args = append(args, f.Name)
		argIdx++
	}
	if f.Filter != "" {
		ast, perr := filter.Parse(f.Filter, []string{"name"})
		if perr != nil {
			return nil, "", helpers.InvalidFilterErr(perr)
		}
		if ast != nil {
			frag, fargs := ast.ToSQL(argIdx)
			conditions = append(conditions, frag)
			args = append(args, fargs...)
			argIdx += len(fargs)
		}
	}
	if p.PageToken != "" {
		ts, id, derr := helpers.DecodePageToken(p.PageToken)
		if derr != nil {
			return nil, "", helpers.InvalidPageTokenErr(derr)
		}
		conditions = append(conditions, fmt.Sprintf("(created_at, id) > ($%d, $%d)", argIdx, argIdx+1))
		args = append(args, ts, id)
		argIdx += 2
	}

	where := "WHERE " + strings.Join(conditions, " AND ")
	q := fmt.Sprintf(`SELECT %s FROM gateways %s ORDER BY created_at ASC, id ASC LIMIT $%d`, helpers.GatewayCols, where, argIdx)
	args = append(args, pageSize+1)

	rows, err := r.tx.Query(ctx, q, args...)
	if err != nil {
		return nil, "", helpers.WrapGatewayErr(err, "")
	}
	defer rows.Close()

	var result []*kacho.GatewayRecord
	for rows.Next() {
		g, err := helpers.ScanGateway(rows)
		if err != nil {
			return nil, "", helpers.WrapGatewayErr(err, "")
		}
		result = append(result, g)
	}
	if err := rows.Err(); err != nil {
		return nil, "", helpers.WrapGatewayErr(err, "")
	}

	var nextToken string
	if int64(len(result)) > pageSize {
		last := result[pageSize-1]
		nextToken = helpers.EncodePageToken(last.CreatedAt, last.ID)
		result = result[:pageSize]
	}
	return result, nextToken, nil
}

// gatewayWriter — DML над gateways через writer-TX. Встраивает gatewayReader, чтобы
// writer видел собственные writes.
//
// Особенность CQRS: writer НЕ emit-ит outbox самостоятельно — caller (use-case)
// делает RepositoryWriter.Outbox().Emit(...) явно после успешного DML. Это
// гарантирует, что outbox-write идет в той же pgx.Tx и что последовательность
// outbox-событий — явное решение use-case-а, а не «из глубины» repo.
type gatewayWriter struct {
	gatewayReader
}

// Insert — INSERT gateways RETURNING. CreatedAt явно проставляется в UTC, хотя
// БД-колонка имеет DEFAULT now() — это нужно для детерминированности тестов и для
// возврата RETURNING без второго round-trip.
//
// outbox-write — не здесь, а в use-case-е через writer.Outbox().Emit(...).
func (w *gatewayWriter) Insert(ctx context.Context, g *domain.Gateway) (*kacho.GatewayRecord, error) {
	labelsJSON, err := helpers.MarshalJSONB(domain.LabelsToMap(g.Labels), "Gateway.labels")
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	q := fmt.Sprintf(`
		INSERT INTO gateways (id, project_id, created_at, name, description, labels, gateway_type)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING %s`, helpers.GatewayCols)

	row := w.tx.QueryRow(ctx, q,
		g.ID, g.ProjectID, now, string(g.Name), string(g.Description), labelsJSON, string(g.GatewayType),
	)
	result, err := helpers.ScanGateway(row)
	if err != nil {
		return nil, helpers.WrapGatewayErr(err, string(g.Name))
	}
	return result, nil
}

// Update — UPDATE gateways RETURNING name/description/labels/gateway_type.
//
// outbox-write — в use-case-е (см. Insert).
func (w *gatewayWriter) Update(ctx context.Context, g *domain.Gateway) (*kacho.GatewayRecord, error) {
	labelsJSON, err := helpers.MarshalJSONB(domain.LabelsToMap(g.Labels), "Gateway.labels")
	if err != nil {
		return nil, err
	}

	q := fmt.Sprintf(`
		UPDATE gateways SET name=$2, description=$3, labels=$4, gateway_type=$5
		WHERE id=$1
		RETURNING %s`, helpers.GatewayCols)

	row := w.tx.QueryRow(ctx, q,
		g.ID, string(g.Name), string(g.Description), labelsJSON, string(g.GatewayType),
	)
	result, err := helpers.ScanGateway(row)
	if err != nil {
		return nil, helpers.WrapGatewayErr(err, g.ID)
	}
	return result, nil
}

// GetForUpdate — Get с row-lock (`FOR UPDATE`) в writer-TX. Сериализует
// конкурентный read-modify-write в Update: второй concurrent Update блокируется на
// GetForUpdate до commit первого, затем читает уже обновленный row и применяет свою
// маску поверх — lost-update исключен.
func (w *gatewayWriter) GetForUpdate(ctx context.Context, id string) (*kacho.GatewayRecord, error) {
	q := fmt.Sprintf(`SELECT %s FROM gateways WHERE id = $1 FOR UPDATE`, helpers.GatewayCols)
	g, err := helpers.ScanGateway(w.tx.QueryRow(ctx, q, id))
	if err != nil {
		return nil, helpers.WrapPgErr(err, "Gateway", id)
	}
	return g, nil
}

// Delete — DELETE gateways WHERE id = $1. FK violation (gateway в использовании)
// → ErrFailedPrecondition с текстом "gateway is in use". row not affected →
// ErrNotFound "Gateway <id> not found".
//
// outbox-write (DELETED tombstone) — в use-case-е.
func (w *gatewayWriter) Delete(ctx context.Context, id string) error {
	tag, err := w.tx.Exec(ctx, `DELETE FROM gateways WHERE id = $1`, id)
	if err != nil {
		if helpers.IsFKViolation(err) {
			return fmt.Errorf("%w: gateway is in use", helpers.ErrFailedPrecondition)
		}
		return helpers.WrapGatewayErr(err, id)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: Gateway %s not found", helpers.ErrNotFound, id)
	}
	return nil
}
