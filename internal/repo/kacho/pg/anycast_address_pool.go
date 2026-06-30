// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package pg

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/PRO-Robotech/kacho-corelib/validate"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/helpers"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// anycastAddressPoolReader — Get/List/счётчики поверх произвольной pgx.Tx
// (read-only или RW). Read-сторона CQRS-разбиения AnycastAddressPool.
type anycastAddressPoolReader struct {
	tx pgx.Tx
}

// Get — well-formed-но-отсутствующий id → NotFound "Anycast address pool <id> not found".
func (r *anycastAddressPoolReader) Get(ctx context.Context, id string) (*kacho.AnycastAddressPoolRecord, error) {
	q := fmt.Sprintf(`SELECT %s FROM anycast_address_pools WHERE id = $1`, helpers.AnycastAddressPoolCols)
	rec, err := helpers.ScanAnycastAddressPool(r.tx.QueryRow(ctx, q, id))
	if err != nil {
		return nil, helpers.WrapPgErr(err, "Anycast address pool", id)
	}
	return rec, nil
}

// List — cursor-based pagination + project_id (+ optional name / network_id) filter.
func (r *anycastAddressPoolReader) List(ctx context.Context, f kacho.AnycastAddressPoolFilter, p kacho.Pagination) ([]*kacho.AnycastAddressPoolRecord, string, error) {
	return r.list(ctx, f, nil, p)
}

// ListByIDs — List, дополнительно суженный explicit allowed-set (per-object
// FGA-фильтр). Пустой набор → пустой результат (no-leak обрабатывает use-case).
func (r *anycastAddressPoolReader) ListByIDs(ctx context.Context, f kacho.AnycastAddressPoolFilter, ids []string, p kacho.Pagination) ([]*kacho.AnycastAddressPoolRecord, string, error) {
	return r.list(ctx, f, ids, p)
}

// list — общий list с опциональным id-allowlist'ом.
func (r *anycastAddressPoolReader) list(ctx context.Context, f kacho.AnycastAddressPoolFilter, allowedIDs []string, p kacho.Pagination) ([]*kacho.AnycastAddressPoolRecord, string, error) {
	pageSize, err := validate.PageSize("page_size", p.PageSize)
	if err != nil {
		return nil, "", err
	}
	args := []any{}
	conds := []string{}
	idx := 1
	if f.ProjectID != "" {
		conds = append(conds, fmt.Sprintf("p.project_id = $%d", idx))
		args = append(args, f.ProjectID)
		idx++
	}
	if f.Name != "" {
		conds = append(conds, fmt.Sprintf("p.name = $%d", idx))
		args = append(args, f.Name)
		idx++
	}
	if allowedIDs != nil {
		conds = append(conds, fmt.Sprintf("p.id = ANY($%d)", idx))
		args = append(args, allowedIDs)
		idx++
	}
	from := "anycast_address_pools p"
	if f.NetworkID != "" {
		from += " JOIN anycast_pool_network_attachments a ON a.pool_id = p.id"
		conds = append(conds, fmt.Sprintf("a.network_id = $%d", idx))
		args = append(args, f.NetworkID)
		idx++
	}
	if p.PageToken != "" {
		ts, id, derr := helpers.DecodePageToken(p.PageToken)
		if derr != nil {
			return nil, "", helpers.InvalidPageTokenErr(derr)
		}
		conds = append(conds, fmt.Sprintf("(p.created_at, p.id) > ($%d, $%d)", idx, idx+1))
		args = append(args, ts, id)
		idx += 2
	}
	where := ""
	if len(conds) > 0 {
		where = "WHERE " + helpers.JoinAnd(conds)
	}
	cols := "p." + helpers.AnycastAddressPoolCols
	q := fmt.Sprintf(`SELECT %s FROM %s %s ORDER BY p.created_at ASC, p.id ASC LIMIT $%d`,
		colsWithAlias(cols), from, where, idx)
	args = append(args, pageSize+1)

	rows, err := r.tx.Query(ctx, q, args...)
	if err != nil {
		return nil, "", helpers.WrapPgErr(err, "Anycast address pool", "")
	}
	defer rows.Close()

	var out []*kacho.AnycastAddressPoolRecord
	for rows.Next() {
		rec, scanErr := helpers.ScanAnycastAddressPool(rows)
		if scanErr != nil {
			return nil, "", helpers.WrapPgErr(scanErr, "Anycast address pool", "")
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, "", helpers.WrapPgErr(err, "Anycast address pool", "")
	}
	var nextToken string
	if int64(len(out)) > pageSize {
		last := out[pageSize-1]
		nextToken = helpers.EncodePageToken(last.CreatedAt, last.ID)
		out = out[:pageSize]
	}
	return out, nextToken, nil
}

// colsWithAlias квалифицирует каждый столбец AnycastAddressPoolCols алиасом `p.`
// — список задан без алиаса, а JOIN по network_id требует квалификации (иначе
// id/created_at неоднозначны). На вход уже приходит "p.id, project_id, ..." —
// заменяем разделитель ", " на ", p." (первый столбец уже p.id).
func colsWithAlias(prefixed string) string {
	return strings.ReplaceAll(prefixed, ", ", ", p.")
}

// AllocatedBlocks — все блоки из anycast_address_pool_cidrs (глобально) для
// детерминированного free-scan при platform-assign.
func (r *anycastAddressPoolReader) AllocatedBlocks(ctx context.Context) ([]string, error) {
	rows, err := r.tx.Query(ctx, `SELECT block::text FROM anycast_address_pool_cidrs`)
	if err != nil {
		return nil, helpers.WrapPgErr(err, "Anycast address pool", "")
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var b string
		if scanErr := rows.Scan(&b); scanErr != nil {
			return nil, helpers.WrapPgErr(scanErr, "Anycast address pool", "")
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, helpers.WrapPgErr(err, "Anycast address pool", "")
	}
	return out, nil
}

// CountAttachments — число pivot-строк (приаттаченных сетей) пула.
func (r *anycastAddressPoolReader) CountAttachments(ctx context.Context, poolID string) (int64, error) {
	var n int64
	err := r.tx.QueryRow(ctx,
		`SELECT count(*) FROM anycast_pool_network_attachments WHERE pool_id = $1`, poolID).Scan(&n)
	if err != nil {
		return 0, helpers.WrapPgErr(err, "Anycast address pool", poolID)
	}
	return n, nil
}

// IsAttached — приаттачен ли пул к сети (для idempotent-detach no-op).
func (r *anycastAddressPoolReader) IsAttached(ctx context.Context, poolID, networkID string) (bool, error) {
	var exists bool
	err := r.tx.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM anycast_pool_network_attachments WHERE pool_id = $1 AND network_id = $2)`,
		poolID, networkID).Scan(&exists)
	if err != nil {
		return false, helpers.WrapPgErr(err, "Anycast address pool", poolID)
	}
	return exists, nil
}

// CountAllocationsInNetwork — число живых anycast-Address'ов пула в сети:
// anycast.pool_id = poolID И anycast_network_id = networkID. Detach с >0
// аллокаций отвергается (FailedPrecondition) — иначе claim снимется, а
// выданные адреса повиснут вне пула.
func (r *anycastAddressPoolReader) CountAllocationsInNetwork(ctx context.Context, poolID, networkID string) (int64, error) {
	var n int64
	err := r.tx.QueryRow(ctx,
		`SELECT count(*) FROM addresses
		  WHERE anycast->>'pool_id' = $1 AND anycast_network_id = $2`,
		poolID, networkID).Scan(&n)
	if err != nil {
		return 0, helpers.WrapPgErr(err, "Anycast address pool", poolID)
	}
	return n, nil
}

// DefaultForFamily — платформенный is_default INTERNAL-пул семейства (IPV4/IPV6).
// partial UNIQUE на (scope, ip_version) WHERE is_default гарантирует ≤1 строки.
// ErrNotFound, если default-пула для семейства нет.
func (r *anycastAddressPoolReader) DefaultForFamily(ctx context.Context, ipVersion domain.IpVersion) (*kacho.AnycastAddressPoolRecord, error) {
	q := fmt.Sprintf(`SELECT %s FROM anycast_address_pools
		WHERE is_default AND scope = 'INTERNAL' AND ip_version = $1 LIMIT 1`,
		helpers.AnycastAddressPoolCols)
	rec, err := helpers.ScanAnycastAddressPool(r.tx.QueryRow(ctx, q, helpers.IPVersionToText(ipVersion)))
	if err != nil {
		return nil, helpers.WrapPgErr(err, "Anycast address pool", "")
	}
	return rec, nil
}

// anycastAddressPoolWriter — DML над anycast_address_pools + child-таблицами
// через writer-TX. Embeds reader (writer видит свои writes). outbox-write — НЕ
// здесь, а в use-case'е.
type anycastAddressPoolWriter struct {
	anycastAddressPoolReader
}

// Insert — INSERT пула + материализация блоков в child anycast_address_pool_cidrs
// в ТОЙ ЖЕ writer-TX. cidr_blocks на пуловой строке — денорм-вид; источник истины
// и within-pool non-overlap (EXCLUDE) — child-таблица. 23P01 на child →
// ErrFailedPrecondition (use-case делает primary within-pool pre-check как
// InvalidArgument; это DB-backstop).
func (w *anycastAddressPoolWriter) Insert(ctx context.Context, p *domain.AnycastAddressPool) (*kacho.AnycastAddressPoolRecord, error) {
	labels, err := helpers.MarshalJSONB(domain.LabelsToMap(p.Labels), "AnycastAddressPool.labels")
	if err != nil {
		return nil, err
	}
	blocks := p.CIDRBlocks
	if blocks == nil {
		blocks = []string{}
	}
	now := time.Now().UTC()
	q := fmt.Sprintf(`
		INSERT INTO anycast_address_pools
			(id, project_id, created_at, name, description, labels, scope, ip_version, cidr_blocks, is_default, status)
		VALUES ($1,$2,$3,$4,$5::jsonb,$6,$7,$8,$9,$10)
		RETURNING %s`, helpers.AnycastAddressPoolCols)
	rec, err := helpers.ScanAnycastAddressPool(w.tx.QueryRow(ctx, q,
		p.ID, p.ProjectID, now, string(p.Name), string(p.Description), labels,
		helpers.AnycastScopeToText(p.Scope), helpers.IPVersionToText(p.IPVersion),
		blocks, p.IsDefault, helpers.AnycastStatusToText(p.Status),
	))
	if err != nil {
		return nil, helpers.WrapPgErr(err, "Anycast address pool", p.ID)
	}
	// Материализуем блоки в нормализованную child-таблицу (источник истины +
	// EXCLUDE within-pool). Пустые блоки игнорируем.
	for _, block := range blocks {
		if block == "" {
			continue
		}
		if _, ierr := w.tx.Exec(ctx,
			`INSERT INTO anycast_address_pool_cidrs (pool_id, block) VALUES ($1, $2::cidr)`,
			rec.ID, block); ierr != nil {
			if helpers.IsExclusionViolation(ierr) || helpers.IsUniqueViolation(ierr) {
				return nil, fmt.Errorf("%w: anycast address pool CIDRs can not overlap", helpers.ErrFailedPrecondition)
			}
			return nil, helpers.WrapPgErr(ierr, "Anycast address pool", rec.ID)
		}
	}
	return rec, nil
}

// Update — UPDATE name/description/labels RETURNING. scope/ip_version/cidr_blocks/
// is_default/status здесь НЕ трогаются (immutable / lifecycle). 0 строк → NotFound.
func (w *anycastAddressPoolWriter) Update(ctx context.Context, p *domain.AnycastAddressPool) (*kacho.AnycastAddressPoolRecord, error) {
	labels, err := helpers.MarshalJSONB(domain.LabelsToMap(p.Labels), "AnycastAddressPool.labels")
	if err != nil {
		return nil, err
	}
	q := fmt.Sprintf(`
		UPDATE anycast_address_pools
		SET name=$2, description=$3, labels=$4::jsonb
		WHERE id=$1
		RETURNING %s`, helpers.AnycastAddressPoolCols)
	rec, err := helpers.ScanAnycastAddressPool(w.tx.QueryRow(ctx, q,
		p.ID, string(p.Name), string(p.Description), labels))
	if err != nil {
		return nil, helpers.WrapPgErr(err, "Anycast address pool", p.ID)
	}
	return rec, nil
}

// Delete — снять anycast-claim'ы пула (у network_cidr_claims нет FK на пул —
// cleanup явный) + DELETE пула (FK CASCADE снимает child-блоки и pivot). 0 строк
// (id нет) → NotFound.
func (w *anycastAddressPoolWriter) Delete(ctx context.Context, id string) error {
	if _, err := w.tx.Exec(ctx,
		`DELETE FROM network_cidr_claims WHERE kind = 'anycast_pool' AND ref_id = $1`, id); err != nil {
		return helpers.WrapPgErr(err, "Anycast address pool", id)
	}
	tag, err := w.tx.Exec(ctx, `DELETE FROM anycast_address_pools WHERE id = $1`, id)
	if err != nil {
		return helpers.WrapPgErr(err, "Anycast address pool", id)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: Anycast address pool %s not found", helpers.ErrNotFound, id)
	}
	return nil
}

// AttachNetwork — INSERT pivot ON CONFLICT DO NOTHING + INSERT claim на КАЖДЫЙ
// блок пула ON CONFLICT DO NOTHING (идемпотентность повторного attach). Claim
// пишется в той же writer-TX. ON CONFLICT подавляет PK-дубль того же пула;
// EXCLUDE-overlap с subnet/другим пулом (23P01) НЕ подавляется → вся TX откат,
// ErrFailedPrecondition "network CIDR claims can not overlap".
func (w *anycastAddressPoolWriter) AttachNetwork(ctx context.Context, poolID, networkID string, blocks []string) error {
	if _, err := w.tx.Exec(ctx,
		`INSERT INTO anycast_pool_network_attachments (pool_id, network_id)
		 VALUES ($1, $2) ON CONFLICT (pool_id, network_id) DO NOTHING`,
		poolID, networkID); err != nil {
		return helpers.WrapPgErr(err, "Anycast address pool", poolID)
	}
	for _, block := range blocks {
		if block == "" {
			continue
		}
		if _, err := w.tx.Exec(ctx,
			`INSERT INTO network_cidr_claims (network_id, kind, ref_id, cidr_range)
			 VALUES ($1, 'anycast_pool', $2, $3::cidr)
			 ON CONFLICT (network_id, kind, ref_id, cidr_range) DO NOTHING`,
			networkID, poolID, block); err != nil {
			if helpers.IsExclusionViolation(err) {
				return fmt.Errorf("%w: network CIDR claims can not overlap", helpers.ErrFailedPrecondition)
			}
			return helpers.WrapPgErr(err, "Anycast address pool", poolID)
		}
	}
	return nil
}

// DetachNetwork — DELETE anycast-claim'ы пула в сети + DELETE pivot в одной TX.
// Detach непривязанного — no-op (DELETE затрагивает 0 строк, без ошибки).
func (w *anycastAddressPoolWriter) DetachNetwork(ctx context.Context, poolID, networkID string) error {
	if _, err := w.tx.Exec(ctx,
		`DELETE FROM network_cidr_claims
		 WHERE kind = 'anycast_pool' AND ref_id = $1 AND network_id = $2`,
		poolID, networkID); err != nil {
		return helpers.WrapPgErr(err, "Anycast address pool", poolID)
	}
	if _, err := w.tx.Exec(ctx,
		`DELETE FROM anycast_pool_network_attachments WHERE pool_id = $1 AND network_id = $2`,
		poolID, networkID); err != nil {
		return helpers.WrapPgErr(err, "Anycast address pool", poolID)
	}
	return nil
}
