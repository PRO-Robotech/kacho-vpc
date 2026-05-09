package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PRO-Robotech/kacho-corelib/validate"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/service"
)

// AddressPoolRepo — реализация service.AddressPoolRepo.
type AddressPoolRepo struct {
	pool *pgxpool.Pool
}

func NewAddressPoolRepo(pool *pgxpool.Pool) *AddressPoolRepo { return &AddressPoolRepo{pool: pool} }

const addressPoolCols = `id, name, description, labels, cidr_blocks, kind, zone_id, is_default, selector_labels, selector_priority, created_at, modified_at`

func (r *AddressPoolRepo) Get(ctx context.Context, id string) (*domain.AddressPool, error) {
	q := fmt.Sprintf(`SELECT %s FROM address_pools WHERE id = $1`, addressPoolCols)
	row := r.pool.QueryRow(ctx, q, id)
	p, err := scanAddressPool(row)
	if err != nil {
		return nil, wrapPgErr(err, "AddressPool", id)
	}
	return p, nil
}

func (r *AddressPoolRepo) List(ctx context.Context, f service.AddressPoolFilter, p service.Pagination) ([]*domain.AddressPool, string, error) {
	pageSize, err := validate.PageSize("page_size", p.PageSize)
	if err != nil {
		return nil, "", err
	}
	args := []any{}
	conds := []string{}
	idx := 1
	if f.Kind != domain.AddressPoolKindUnspecified {
		conds = append(conds, fmt.Sprintf("kind = $%d", idx))
		args = append(args, int16(f.Kind))
		idx++
	}
	if f.ZoneID != "" {
		conds = append(conds, fmt.Sprintf("zone_id = $%d", idx))
		args = append(args, f.ZoneID)
		idx++
	}
	if p.PageToken != "" {
		ts, id, err := decodePageToken(p.PageToken)
		if err != nil {
			return nil, "", invalidPageTokenErr(err)
		}
		conds = append(conds, fmt.Sprintf("(created_at, id) > ($%d, $%d)", idx, idx+1))
		args = append(args, ts, id)
		idx += 2
	}
	where := ""
	if len(conds) > 0 {
		where = "WHERE " + joinAnd(conds)
	}
	q := fmt.Sprintf(`SELECT %s FROM address_pools %s ORDER BY created_at ASC, id ASC LIMIT $%d`,
		addressPoolCols, where, idx)
	args = append(args, pageSize+1)

	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, "", wrapPgErr(err, "AddressPool", "")
	}
	defer rows.Close()
	var out []*domain.AddressPool
	for rows.Next() {
		p, err := scanAddressPool(rows)
		if err != nil {
			return nil, "", wrapPgErr(err, "AddressPool", "")
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, "", wrapPgErr(err, "AddressPool", "")
	}
	var nextToken string
	if int64(len(out)) > pageSize {
		last := out[pageSize-1]
		nextToken = encodePageToken(last.CreatedAt, last.ID)
		out = out[:pageSize]
	}
	return out, nextToken, nil
}

func (r *AddressPoolRepo) Insert(ctx context.Context, p *domain.AddressPool) (*domain.AddressPool, error) {
	labels := mustMarshalJSON(p.Labels)
	selector := mustMarshalJSON(p.SelectorLabels)
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, service.ErrInternal
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var zoneArg any
	if p.ZoneID == "" {
		zoneArg = nil
	} else {
		zoneArg = p.ZoneID
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO address_pools (id, name, description, labels, cidr_blocks, kind, zone_id, is_default, selector_labels, selector_priority, created_at, modified_at)
		VALUES ($1,$2,$3,$4::jsonb,$5,$6,$7,$8,$9::jsonb,$10,$11,$12)
	`, p.ID, p.Name, p.Description, labels, p.CIDRBlocks, int16(p.Kind), zoneArg, p.IsDefault, selector, p.SelectorPriority, p.CreatedAt, p.ModifiedAt)
	if err != nil {
		return nil, wrapPgErr(err, "AddressPool", p.ID)
	}
	if err := emitVPC(ctx, tx, "AddressPool", p.ID, "CREATED", domainToMap(p)); err != nil {
		return nil, service.ErrInternal
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, service.ErrInternal
	}
	return p, nil
}

func (r *AddressPoolRepo) Update(ctx context.Context, p *domain.AddressPool) (*domain.AddressPool, error) {
	labels := mustMarshalJSON(p.Labels)
	selector := mustMarshalJSON(p.SelectorLabels)
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, service.ErrInternal
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx, `
		UPDATE address_pools
		SET name=$2, description=$3, labels=$4::jsonb, cidr_blocks=$5, is_default=$6,
		    selector_labels=$7::jsonb, selector_priority=$8, modified_at=$9
		WHERE id = $1
	`, p.ID, p.Name, p.Description, labels, p.CIDRBlocks, p.IsDefault, selector, p.SelectorPriority, p.ModifiedAt)
	if err != nil {
		return nil, wrapPgErr(err, "AddressPool", p.ID)
	}
	if tag.RowsAffected() == 0 {
		return nil, fmt.Errorf("%w: AddressPool %s", service.ErrNotFound, p.ID)
	}
	if err := emitVPC(ctx, tx, "AddressPool", p.ID, "UPDATED", domainToMap(p)); err != nil {
		return nil, service.ErrInternal
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, service.ErrInternal
	}
	return p, nil
}

func (r *AddressPoolRepo) Delete(ctx context.Context, id string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return service.ErrInternal
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `DELETE FROM address_pools WHERE id = $1`, id)
	if err != nil {
		return wrapPgErr(err, "AddressPool", id)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: AddressPool %s", service.ErrNotFound, id)
	}
	if err := emitVPC(ctx, tx, "AddressPool", id, "DELETED", map[string]any{"id": id}); err != nil {
		return service.ErrInternal
	}
	return tx.Commit(ctx)
}

// GetDefaultForZone — вернуть default-pool для zone+kind. Пустой zoneID
// = глобальный default (zone_id IS NULL). NotFound, если default не задан.
func (r *AddressPoolRepo) GetDefaultForZone(ctx context.Context, zoneID string, kind domain.AddressPoolKind) (*domain.AddressPool, error) {
	var (
		q   string
		row pgx.Row
	)
	if zoneID == "" {
		q = fmt.Sprintf(`SELECT %s FROM address_pools WHERE zone_id IS NULL AND kind = $1 AND is_default = true LIMIT 1`, addressPoolCols)
		row = r.pool.QueryRow(ctx, q, int16(kind))
	} else {
		q = fmt.Sprintf(`SELECT %s FROM address_pools WHERE zone_id = $1 AND kind = $2 AND is_default = true LIMIT 1`, addressPoolCols)
		row = r.pool.QueryRow(ctx, q, zoneID, int16(kind))
	}
	p, err := scanAddressPool(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, service.ErrNotFound
		}
		return nil, wrapPgErr(err, "AddressPool", "")
	}
	return p, nil
}

// FindBySelectorMatch — label-cascade резолв (containment: networkSelector ⊆ pool.selector_labels).
// Пустой zoneID = только глобальные пулы (zone_id IS NULL). Иначе — пулы привязанные
// к указанной zone + глобальные (zone_id IS NULL).
func (r *AddressPoolRepo) FindBySelectorMatch(ctx context.Context, networkSelector map[string]string, zoneID string, kind domain.AddressPoolKind, limit int) ([]*domain.AddressPool, error) {
	if len(networkSelector) == 0 {
		return nil, service.ErrNotFound
	}
	if limit <= 0 {
		limit = 1
	}
	selectorJSON := mustMarshalJSON(networkSelector)
	q := `
SELECT id, name, description, labels, cidr_blocks, kind, zone_id, is_default,
       selector_labels, selector_priority, created_at, modified_at
FROM address_pools
WHERE selector_labels @> $1::jsonb
  AND selector_labels <> '{}'::jsonb
  AND (zone_id = $2 OR zone_id IS NULL)
  AND kind = $3
ORDER BY
  ((SELECT count(*) FROM jsonb_object_keys(selector_labels)) -
   (SELECT count(*) FROM jsonb_object_keys($1::jsonb))) ASC,
  selector_priority DESC
LIMIT $4
`
	rows, err := r.pool.Query(ctx, q, selectorJSON, zoneID, int16(kind), limit)
	if err != nil {
		return nil, wrapPgErr(err, "AddressPool", "")
	}
	defer rows.Close()
	var out []*domain.AddressPool
	for rows.Next() {
		p, err := scanAddressPool(rows)
		if err != nil {
			return nil, wrapPgErr(err, "AddressPool", "")
		}
		out = append(out, p)
	}
	if len(out) == 0 {
		return nil, service.ErrNotFound
	}
	return out, rows.Err()
}

// FindAmbiguousSelectorGroups — diagnostic для Check.
func (r *AddressPoolRepo) FindAmbiguousSelectorGroups(ctx context.Context, zoneID string) ([][]*domain.AddressPool, error) {
	args := []any{}
	where := "selector_labels <> '{}'::jsonb"
	if zoneID != "" {
		where += " AND zone_id = $1"
		args = append(args, zoneID)
	}
	// Группируем по (zone_id, kind, selector_labels, selector_priority).
	// Возвращаем pool-id'ы каждой группы с count > 1.
	q := fmt.Sprintf(`
SELECT array_agg(id ORDER BY id) AS ids, count(*) AS cnt
FROM address_pools
WHERE %s
GROUP BY zone_id, kind, selector_labels, selector_priority
HAVING count(*) > 1
`, where)
	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, wrapPgErr(err, "AddressPool", "")
	}
	defer rows.Close()
	var groups [][]*domain.AddressPool
	for rows.Next() {
		var ids []string
		var cnt int
		if err := rows.Scan(&ids, &cnt); err != nil {
			return nil, wrapPgErr(err, "AddressPool", "")
		}
		group := make([]*domain.AddressPool, 0, len(ids))
		for _, id := range ids {
			p, err := r.Get(ctx, id)
			if err != nil {
				continue
			}
			group = append(group, p)
		}
		if len(group) > 1 {
			groups = append(groups, group)
		}
	}
	return groups, rows.Err()
}

// CountAddressesByPool — admin observability: сколько Address используют pool.
func (r *AddressPoolRepo) CountAddressesByPool(ctx context.Context, poolID string) (int64, error) {
	var n int64
	err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM addresses
		 WHERE external_ipv4 ->> 'address_pool_id' = $1
		   AND coalesce(external_ipv4 ->> 'address', '') <> ''`, poolID).Scan(&n)
	if err != nil {
		return 0, wrapPgErr(err, "AddressPool", poolID)
	}
	return n, nil
}

// CountAddressesByPoolPerCIDR — для каждого CIDR pool'а считаем allocated IPs.
// Single-roundtrip: один SELECT с unnest($cidrs) + LEFT JOIN addresses через
// inet-оператор `<<`. Раньше было N+1 (по запросу на каждый CIDR) — заменено
// одним запросом с GROUP BY (concurrency P0 #4 closure).
func (r *AddressPoolRepo) CountAddressesByPoolPerCIDR(ctx context.Context, poolID string) (map[string]int64, error) {
	pool, err := r.Get(ctx, poolID)
	if err != nil {
		return nil, err
	}
	out := make(map[string]int64, len(pool.CIDRBlocks))
	if len(pool.CIDRBlocks) == 0 {
		return out, nil
	}
	rows, err := r.pool.Query(ctx, `
SELECT c.cidr::text, COALESCE(count(a.id) FILTER (
    WHERE coalesce(a.external_ipv4 ->> 'address','') <> ''
      AND a.external_ipv4 ->> 'address_pool_id' = $1
      AND (a.external_ipv4 ->> 'address')::inet << c.cidr
), 0) AS n
FROM unnest($2::cidr[]) AS c(cidr)
LEFT JOIN addresses a ON a.external_ipv4 ->> 'address_pool_id' = $1
GROUP BY c.cidr
`, poolID, pool.CIDRBlocks)
	if err != nil {
		return nil, wrapPgErr(err, "AddressPool", poolID)
	}
	defer rows.Close()
	for rows.Next() {
		var cidr string
		var n int64
		if err := rows.Scan(&cidr, &n); err != nil {
			return nil, wrapPgErr(err, "AddressPool", poolID)
		}
		out[cidr] = n
	}
	if err := rows.Err(); err != nil {
		return nil, wrapPgErr(err, "AddressPool", poolID)
	}
	// Postgres canonicalizes "198.51.100.0/24" identically; но если pool.CIDRBlocks
	// хранит спецификации без canonical form, добавим missing keys как 0.
	for _, c := range pool.CIDRBlocks {
		if _, ok := out[c]; !ok {
			out[c] = 0
		}
	}
	return out, nil
}

// ListAddressesByPool — кросс-folder список Address, держащих IP из pool'а.
// Используется в admin UI для AddressPool detail page.
func (r *AddressPoolRepo) ListAddressesByPool(ctx context.Context, poolID, folderFilter string, p service.Pagination) ([]*domain.Address, string, error) {
	pageSize, err := validate.PageSize("page_size", p.PageSize)
	if err != nil {
		return nil, "", err
	}
	args := []any{poolID}
	conds := []string{"external_ipv4 ->> 'address_pool_id' = $1"}
	idx := 2
	if folderFilter != "" {
		conds = append(conds, fmt.Sprintf("folder_id = $%d", idx))
		args = append(args, folderFilter)
		idx++
	}
	if p.PageToken != "" {
		ts, id, err := decodePageToken(p.PageToken)
		if err != nil {
			return nil, "", invalidPageTokenErr(err)
		}
		conds = append(conds, fmt.Sprintf("(created_at, id) > ($%d, $%d)", idx, idx+1))
		args = append(args, ts, id)
		idx += 2
	}
	q := fmt.Sprintf(`
SELECT id, folder_id, created_at, name, description, labels,
       addr_type, ip_version, reserved, used, deletion_protection,
       external_ipv4, internal_ipv4
FROM addresses
WHERE %s
ORDER BY created_at ASC, id ASC
LIMIT $%d`, joinAnd(conds), idx)
	args = append(args, pageSize+1)

	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, "", wrapPgErr(err, "Address", "")
	}
	defer rows.Close()

	var out []*domain.Address
	for rows.Next() {
		a, scanErr := scanAddress(rows)
		if scanErr != nil {
			return nil, "", wrapPgErr(scanErr, "Address", "")
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, "", wrapPgErr(err, "Address", "")
	}
	var nextToken string
	if int64(len(out)) > pageSize {
		last := out[pageSize-1]
		nextToken = encodePageToken(last.CreatedAt, last.ID)
		out = out[:pageSize]
	}
	return out, nextToken, nil
}

// scanAddressPool — общий row-scanner.
func scanAddressPool(row pgx.Row) (*domain.AddressPool, error) {
	var (
		p            domain.AddressPool
		labelsJSON   []byte
		selectorJSON []byte
		kindByte     int16
		zoneIDPtr    *string
	)
	err := row.Scan(
		&p.ID, &p.Name, &p.Description, &labelsJSON,
		&p.CIDRBlocks, &kindByte, &zoneIDPtr, &p.IsDefault,
		&selectorJSON, &p.SelectorPriority, &p.CreatedAt, &p.ModifiedAt,
	)
	if err != nil {
		return nil, err
	}
	if zoneIDPtr != nil {
		p.ZoneID = *zoneIDPtr
	}
	p.Kind = domain.AddressPoolKind(kindByte)
	if len(labelsJSON) > 0 {
		_ = json.Unmarshal(labelsJSON, &p.Labels)
	}
	if len(selectorJSON) > 0 {
		_ = json.Unmarshal(selectorJSON, &p.SelectorLabels)
	}
	return &p, nil
}

// joinAnd — local helper to avoid importing strings (mirror style).
func joinAnd(conds []string) string {
	out := ""
	for i, c := range conds {
		if i > 0 {
			out += " AND "
		}
		out += c
	}
	return out
}
