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

// networkReader — Get/List поверх произвольной pgx.Tx (read-only или RW).
// Не имеет своего state кроме tx.
type networkReader struct {
	tx pgx.Tx
}

// Get — verbatim YC: well-formed-but-absent → NotFound с "Network <id> not found".
func (r *networkReader) Get(ctx context.Context, id string) (*kacho.NetworkRecord, error) {
	q := fmt.Sprintf(`SELECT %s FROM networks WHERE id = $1`, helpers.NetworkCols)
	row := r.tx.QueryRow(ctx, q, id)
	n, err := helpers.ScanNetwork(row)
	if err != nil {
		return nil, helpers.WrapPgErr(err, "Network", id)
	}
	return n, nil
}

// List — cursor-based pagination + filter.Parse (YC-syntax). Логика идентична
// legacy `*repo.NetworkRepo.List` (KAC-94 Wave 5 — переносим как есть; pilot
// проверяет CQRS-каркас, а не SQL-семантику).
func (r *networkReader) List(ctx context.Context, f kacho.NetworkFilter, p kacho.Pagination) ([]*kacho.NetworkRecord, string, error) {
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
		ts, id, err := helpers.DecodePageToken(p.PageToken)
		if err != nil {
			return nil, "", helpers.InvalidPageTokenErr(err)
		}
		conditions = append(conditions, fmt.Sprintf("(created_at, id) > ($%d, $%d)", argIdx, argIdx+1))
		args = append(args, ts, id)
		argIdx += 2
	}

	var where string
	if len(conditions) > 0 {
		where = "WHERE " + strings.Join(conditions, " AND ")
	}
	q := fmt.Sprintf(`SELECT %s FROM networks %s ORDER BY created_at ASC, id ASC LIMIT $%d`, helpers.NetworkCols, where, argIdx)
	args = append(args, pageSize+1)

	rows, err := r.tx.Query(ctx, q, args...)
	if err != nil {
		return nil, "", helpers.WrapPgErr(err, "Network", "")
	}
	defer rows.Close()

	var result []*kacho.NetworkRecord
	for rows.Next() {
		n, err := helpers.ScanNetwork(rows)
		if err != nil {
			return nil, "", helpers.WrapPgErr(err, "Network", "")
		}
		result = append(result, n)
	}
	if err := rows.Err(); err != nil {
		return nil, "", helpers.WrapPgErr(err, "Network", "")
	}

	var nextToken string
	if int64(len(result)) > pageSize {
		last := result[pageSize-1]
		nextToken = helpers.EncodePageToken(last.CreatedAt, last.ID)
		result = result[:pageSize]
	}
	return result, nextToken, nil
}

// ListByIDs — KAC-127 Phase 4: List с фильтром по `id = ANY($allowedIDs)`.
//
// Comparable list-семантика (filter / cursor) сохраняется; добавляется
// safety-net WHERE-clause с типизированным text[]-параметром (acceptance
// D-10: SQL-injection-safe). Пустой allowedIDs → возвращает (nil, "", nil).
func (r *networkReader) ListByIDs(ctx context.Context, f kacho.NetworkFilter, allowedIDs []string, p kacho.Pagination) ([]*kacho.NetworkRecord, string, error) {
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
		ts, id, err := helpers.DecodePageToken(p.PageToken)
		if err != nil {
			return nil, "", helpers.InvalidPageTokenErr(err)
		}
		conditions = append(conditions, fmt.Sprintf("(created_at, id) > ($%d, $%d)", argIdx, argIdx+1))
		args = append(args, ts, id)
		argIdx += 2
	}

	where := "WHERE " + strings.Join(conditions, " AND ")
	q := fmt.Sprintf(`SELECT %s FROM networks %s ORDER BY created_at ASC, id ASC LIMIT $%d`, helpers.NetworkCols, where, argIdx)
	args = append(args, pageSize+1)

	rows, err := r.tx.Query(ctx, q, args...)
	if err != nil {
		return nil, "", helpers.WrapPgErr(err, "Network", "")
	}
	defer rows.Close()

	var result []*kacho.NetworkRecord
	for rows.Next() {
		n, err := helpers.ScanNetwork(rows)
		if err != nil {
			return nil, "", helpers.WrapPgErr(err, "Network", "")
		}
		result = append(result, n)
	}
	if err := rows.Err(); err != nil {
		return nil, "", helpers.WrapPgErr(err, "Network", "")
	}

	var nextToken string
	if int64(len(result)) > pageSize {
		last := result[pageSize-1]
		nextToken = helpers.EncodePageToken(last.CreatedAt, last.ID)
		result = result[:pageSize]
	}
	return result, nextToken, nil
}

// networkWriter — DML над networks через writer-TX. Embeds networkReader
// (G.2 — writer видит свои writes: Get/List доступны после Insert/Update в
// рамках той же TX).
//
// Особенность CQRS-pilot'а: writer **НЕ** emit'ит outbox самостоятельно. После
// успешного DML caller (use-case) вызывает `RepositoryWriter.Outbox().Emit(...)`
// — это явная точка G.5, видно из use-case-кода что происходит outbox-write.
type networkWriter struct {
	networkReader
	emitter kacho.OutboxEmitter // unused внутри writer-методов; нужен для composability
}

// Insert — INSERT networks RETURNING. CreatedAt здесь явно проставляется в
// UTC (как в legacy-репо), хотя БД-колонка имеет DEFAULT now() — это нужно для
// детерминированности тестов.
//
// outbox-write — не здесь, а в use-case'е через `writer.Outbox().Emit(...)`.
func (w *networkWriter) Insert(ctx context.Context, n *domain.Network) (*kacho.NetworkRecord, error) {
	labelsJSON, err := helpers.MarshalJSONB(domain.LabelsToMap(n.Labels), "Network.labels")
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	q := fmt.Sprintf(`
		INSERT INTO networks (id, project_id, created_at, name, description, labels, default_security_group_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING %s`, helpers.NetworkCols)

	row := w.tx.QueryRow(ctx, q,
		n.ID, n.ProjectID, now, string(n.Name), string(n.Description), labelsJSON, n.DefaultSecurityGroupID,
	)
	result, err := helpers.ScanNetwork(row)
	if err != nil {
		return nil, helpers.WrapPgErr(err, "Network", string(n.Name))
	}
	return result, nil
}

// Update — UPDATE networks RETURNING. Мутирует name/description/labels/default_sg_id
// (project_id меняется через SetProjectID — для :move action).
//
// outbox-write — в use-case'е (см. Insert).
func (w *networkWriter) Update(ctx context.Context, n *domain.Network) (*kacho.NetworkRecord, error) {
	labelsJSON, err := helpers.MarshalJSONB(domain.LabelsToMap(n.Labels), "Network.labels")
	if err != nil {
		return nil, err
	}

	q := fmt.Sprintf(`
		UPDATE networks SET name=$2, description=$3, labels=$4, default_security_group_id=$5
		WHERE id=$1
		RETURNING %s`, helpers.NetworkCols)

	row := w.tx.QueryRow(ctx, q,
		n.ID, string(n.Name), string(n.Description), labelsJSON, n.DefaultSecurityGroupID,
	)
	result, err := helpers.ScanNetwork(row)
	if err != nil {
		return nil, helpers.WrapPgErr(err, "Network", n.ID)
	}
	return result, nil
}

// SetDefaultSGID атомарно проставляет networks.default_security_group_id для
// конкретной сети. Wave 5 batch 33/34 (KAC-94, skill evgeniy I.9/I.10):
// узкая UPDATE-операция, чтобы Network.Create мог в одной writer-TX сделать
// Insert(Network) → Insert(SG) → SetDefaultSGID(network, sg) (+ единый outbox-emit
// Network.UPDATED). Без перезаписи name/description/labels — те уже сохранены
// в Insert и менять их в default-SG-link шаге не нужно.
func (w *networkWriter) SetDefaultSGID(ctx context.Context, id, sgID string) (*kacho.NetworkRecord, error) {
	q := fmt.Sprintf(`
		UPDATE networks SET default_security_group_id = $2
		WHERE id = $1
		RETURNING %s`, helpers.NetworkCols)
	row := w.tx.QueryRow(ctx, q, id, sgID)
	result, err := helpers.ScanNetwork(row)
	if err != nil {
		return nil, helpers.WrapPgErr(err, "Network", id)
	}
	return result, nil
}

// SetProjectID меняет project_id у Network (для :move).
func (w *networkWriter) SetProjectID(ctx context.Context, id, folderID string) (*kacho.NetworkRecord, error) {
	q := fmt.Sprintf(`
		UPDATE networks SET project_id = $2
		WHERE id = $1
		RETURNING %s`, helpers.NetworkCols)
	row := w.tx.QueryRow(ctx, q, id, folderID)
	result, err := helpers.ScanNetwork(row)
	if err != nil {
		return nil, helpers.WrapPgErr(err, "Network", id)
	}
	return result, nil
}

// Delete — DELETE networks WHERE id = $1. FK violation (есть дети — subnets/
// route_tables/SGs) → ErrFailedPrecondition с verbatim YC text "network is not empty".
// row not affected → ErrNotFound "Network <id> not found".
//
// outbox-write (DELETED tombstone) — в use-case'е.
func (w *networkWriter) Delete(ctx context.Context, id string) error {
	tag, err := w.tx.Exec(ctx, `DELETE FROM networks WHERE id = $1`, id)
	if err != nil {
		if helpers.IsFKViolation(err) {
			return fmt.Errorf("%w: network is not empty", helpers.ErrFailedPrecondition)
		}
		return helpers.WrapPgErr(err, "Network", id)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: Network %s not found", helpers.ErrNotFound, id)
	}
	return nil
}
