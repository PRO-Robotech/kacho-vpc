package pg

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/PRO-Robotech/kacho-corelib/validate"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/helpers"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// addressPoolReader — Get/List/GetDefaultForZone/FindBySelectorMatch/... поверх
// произвольной pgx.Tx (read-only или RW). Не имеет своего state кроме tx.
//
// Wave 5 replicate (KAC-94 A.7 sub-PR 1/6, skill evgeniy §6 G.1-G.7): AddressPool
// переезжает на CQRS, чтобы Create/Update/Delete + PopulateFreelistForPool +
// outbox emit шли в одной writer-TX. SQL/scan-семантика — parity с legacy
// `*repo.AddressPoolRepo`; shim-helpers экспортированы через `internal/repo/shim_kacho.go`.
type addressPoolReader struct {
	tx pgx.Tx
}

// Get — verbatim YC: well-formed-but-absent → NotFound с "AddressPool <id> not found".
func (r *addressPoolReader) Get(ctx context.Context, id string) (*kacho.AddressPoolRecord, error) {
	q := fmt.Sprintf(`SELECT %s FROM address_pools WHERE id = $1`, helpers.AddressPoolCols)
	row := r.tx.QueryRow(ctx, q, id)
	rec, err := helpers.ScanAddressPool(row)
	if err != nil {
		return nil, helpers.WrapPgErr(err, "AddressPool", id)
	}
	return rec, nil
}

// List — cursor-based pagination + (kind, zone_id) filter. Parity с legacy
// `*AddressPoolRepo.List`.
func (r *addressPoolReader) List(ctx context.Context, f kacho.AddressPoolFilter, p kacho.Pagination) ([]*kacho.AddressPoolRecord, string, error) {
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
		ts, id, derr := helpers.DecodePageToken(p.PageToken)
		if derr != nil {
			return nil, "", helpers.InvalidPageTokenErr(derr)
		}
		conds = append(conds, fmt.Sprintf("(created_at, id) > ($%d, $%d)", idx, idx+1))
		args = append(args, ts, id)
		idx += 2
	}
	where := ""
	if len(conds) > 0 {
		where = "WHERE " + helpers.JoinAnd(conds)
	}
	q := fmt.Sprintf(`SELECT %s FROM address_pools %s ORDER BY created_at ASC, id ASC LIMIT $%d`,
		helpers.AddressPoolCols, where, idx)
	args = append(args, pageSize+1)

	rows, err := r.tx.Query(ctx, q, args...)
	if err != nil {
		return nil, "", helpers.WrapPgErr(err, "AddressPool", "")
	}
	defer rows.Close()

	var out []*kacho.AddressPoolRecord
	for rows.Next() {
		rec, scanErr := helpers.ScanAddressPool(rows)
		if scanErr != nil {
			return nil, "", helpers.WrapPgErr(scanErr, "AddressPool", "")
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, "", helpers.WrapPgErr(err, "AddressPool", "")
	}
	var nextToken string
	if int64(len(out)) > pageSize {
		last := out[pageSize-1]
		nextToken = helpers.EncodePageToken(last.CreatedAt, last.ID)
		out = out[:pageSize]
	}
	return out, nextToken, nil
}

// GetDefaultForZone — default pool для (zone, kind). zoneID == "" → глобальный
// (`zone_id IS NULL`). NotFound если default не задан.
func (r *addressPoolReader) GetDefaultForZone(ctx context.Context, zoneID string, kind domain.AddressPoolKind) (*kacho.AddressPoolRecord, error) {
	var (
		q   string
		row pgx.Row
	)
	if zoneID == "" {
		q = fmt.Sprintf(`SELECT %s FROM address_pools WHERE zone_id IS NULL AND kind = $1 AND is_default = true LIMIT 1`, helpers.AddressPoolCols)
		row = r.tx.QueryRow(ctx, q, int16(kind))
	} else {
		q = fmt.Sprintf(`SELECT %s FROM address_pools WHERE zone_id = $1 AND kind = $2 AND is_default = true LIMIT 1`, helpers.AddressPoolCols)
		row = r.tx.QueryRow(ctx, q, zoneID, int16(kind))
	}
	rec, err := helpers.ScanAddressPool(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, helpers.ErrNotFound
		}
		return nil, helpers.WrapPgErr(err, "AddressPool", "")
	}
	return rec, nil
}

// FindBySelectorMatch — label-cascade резолв (containment: networkSelector ⊆
// pool.selector_labels). zoneID == "" → только глобальные (zone_id IS NULL);
// иначе — pool'ы привязанные к zone + глобальные.
func (r *addressPoolReader) FindBySelectorMatch(ctx context.Context, networkSelector map[string]string, zoneID string, kind domain.AddressPoolKind, limit int) ([]*kacho.AddressPoolRecord, error) {
	if len(networkSelector) == 0 {
		return nil, helpers.ErrNotFound
	}
	if limit <= 0 {
		limit = 1
	}
	selectorJSON, err := helpers.MarshalJSONB(networkSelector, "AddressPool.selector_labels")
	if err != nil {
		return nil, err
	}
	q := `
SELECT id, name, description, labels, v4_cidr_blocks, v6_cidr_blocks, kind, zone_id, is_default,
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
	rows, err := r.tx.Query(ctx, q, selectorJSON, zoneID, int16(kind), limit)
	if err != nil {
		return nil, helpers.WrapPgErr(err, "AddressPool", "")
	}
	defer rows.Close()
	var out []*kacho.AddressPoolRecord
	for rows.Next() {
		rec, scanErr := helpers.ScanAddressPool(rows)
		if scanErr != nil {
			return nil, helpers.WrapPgErr(scanErr, "AddressPool", "")
		}
		out = append(out, rec)
	}
	if len(out) == 0 {
		return nil, helpers.ErrNotFound
	}
	return out, rows.Err()
}

// FindAmbiguousSelectorGroups — diagnostic для Check. Группы pool'ов с
// identical (zone_id, kind, selector_labels, selector_priority).
//
// Wave 5 A.7 sub-PR 1/6: над **одной** pgx.Tx нельзя держать открытый rows-
// курсор и параллельно гонять Get-запросы (pgx запретит — only one query per
// connection at a time). Поэтому сначала вычитываем все group-id'ы во временный
// слайс, закрываем rows, потом делаем Get-loop. Legacy *AddressPoolRepo
// работал на pgxpool — каждый Get шёл по СВОЕМУ соединению из пула, что
// маскировало этот anti-pattern.
func (r *addressPoolReader) FindAmbiguousSelectorGroups(ctx context.Context, zoneID string) ([][]*kacho.AddressPoolRecord, error) {
	args := []any{}
	where := "selector_labels <> '{}'::jsonb"
	if zoneID != "" {
		where += " AND zone_id = $1"
		args = append(args, zoneID)
	}
	q := fmt.Sprintf(`
SELECT array_agg(id ORDER BY id) AS ids, count(*) AS cnt
FROM address_pools
WHERE %s
GROUP BY zone_id, kind, selector_labels, selector_priority
HAVING count(*) > 1
`, where)
	rows, err := r.tx.Query(ctx, q, args...)
	if err != nil {
		return nil, helpers.WrapPgErr(err, "AddressPool", "")
	}

	// Сначала вычитываем все group-id'ы во временный слайс — нельзя гонять
	// `r.Get` внутри rows-iterate'а (single-conn TX блокирует второй query).
	type rawGroup struct{ ids []string }
	var raw []rawGroup
	for rows.Next() {
		var ids []string
		var cnt int
		if scanErr := rows.Scan(&ids, &cnt); scanErr != nil {
			rows.Close()
			return nil, helpers.WrapPgErr(scanErr, "AddressPool", "")
		}
		raw = append(raw, rawGroup{ids: ids})
	}
	if rerr := rows.Err(); rerr != nil {
		rows.Close()
		return nil, helpers.WrapPgErr(rerr, "AddressPool", "")
	}
	rows.Close()

	// Теперь rows закрыт — можно делать Get по той же TX.
	var groups [][]*kacho.AddressPoolRecord
	for _, rg := range raw {
		group := make([]*kacho.AddressPoolRecord, 0, len(rg.ids))
		for _, id := range rg.ids {
			rec, gerr := r.Get(ctx, id)
			if gerr != nil {
				continue
			}
			group = append(group, rec)
		}
		if len(group) > 1 {
			groups = append(groups, group)
		}
	}
	return groups, nil
}

// CountAddressesByPool — admin observability: сколько Address используют pool.
func (r *addressPoolReader) CountAddressesByPool(ctx context.Context, poolID string) (int64, error) {
	var n int64
	err := r.tx.QueryRow(ctx,
		`SELECT count(*) FROM addresses
		 WHERE external_ipv4 ->> 'address_pool_id' = $1
		   AND coalesce(external_ipv4 ->> 'address', '') <> ''`, poolID).Scan(&n)
	if err != nil {
		return 0, helpers.WrapPgErr(err, "AddressPool", poolID)
	}
	return n, nil
}

// CountAddressesByPoolPerCIDR — для каждого V4CIDR — allocated count. Для
// V6-блоков возвращает count=0 placeholder (sparse v6-allocator ведёт свою
// бухгалтерию через ipv6_pool_cursors / ipv6_allocated_ips).
//
// Single-roundtrip: один SELECT с unnest WITH ORDINALITY + LEFT JOIN addresses
// через inet-оператор `<<`. Возвращает caller'у ключи в том же raw-string виде,
// что в pool.V4CIDRBlocks — без канонизации (R7 regression closure).
func (r *addressPoolReader) CountAddressesByPoolPerCIDR(ctx context.Context, poolID string) (map[string]int64, error) {
	pool, err := r.Get(ctx, poolID)
	if err != nil {
		return nil, err
	}
	v4Cidrs := pool.V4CIDRBlocks
	out := make(map[string]int64, len(v4Cidrs)+len(pool.V6CIDRBlocks))
	for _, c := range pool.V6CIDRBlocks {
		out[c] = 0
	}
	if len(v4Cidrs) == 0 {
		return out, nil
	}
	rows, err := r.tx.Query(ctx, `
SELECT c.idx, COALESCE(count(a.id) FILTER (
    WHERE coalesce(a.external_ipv4 ->> 'address','') <> ''
      AND (a.external_ipv4 ->> 'address')::inet << c.cidr
), 0) AS n
FROM unnest($2::cidr[]) WITH ORDINALITY AS c(cidr, idx)
LEFT JOIN addresses a ON a.external_ipv4 ->> 'address_pool_id' = $1
GROUP BY c.idx
`, poolID, v4Cidrs)
	if err != nil {
		return nil, helpers.WrapPgErr(err, "AddressPool", poolID)
	}
	defer rows.Close()
	counts := make(map[int]int64, len(v4Cidrs))
	for rows.Next() {
		var idx int
		var n int64
		if scanErr := rows.Scan(&idx, &n); scanErr != nil {
			return nil, helpers.WrapPgErr(scanErr, "AddressPool", poolID)
		}
		counts[idx] = n
	}
	if rerr := rows.Err(); rerr != nil {
		return nil, helpers.WrapPgErr(rerr, "AddressPool", poolID)
	}
	for i, c := range v4Cidrs {
		out[c] = counts[i+1]
	}
	return out, nil
}

// ListAddressesByPool — кросс-folder список Address с IP из pool.
// folderFilter == "" → без фильтра. Возвращает *kacho.AddressRecord.
func (r *addressPoolReader) ListAddressesByPool(ctx context.Context, poolID, folderFilter string, p kacho.Pagination) ([]*kacho.AddressRecord, string, error) {
	pageSize, err := validate.PageSize("page_size", p.PageSize)
	if err != nil {
		return nil, "", err
	}
	args := []any{poolID}
	conds := []string{"external_ipv4 ->> 'address_pool_id' = $1"}
	idx := 2
	if folderFilter != "" {
		conds = append(conds, fmt.Sprintf("project_id = $%d", idx))
		args = append(args, folderFilter)
		idx++
	}
	if p.PageToken != "" {
		ts, id, derr := helpers.DecodePageToken(p.PageToken)
		if derr != nil {
			return nil, "", helpers.InvalidPageTokenErr(derr)
		}
		conds = append(conds, fmt.Sprintf("(created_at, id) > ($%d, $%d)", idx, idx+1))
		args = append(args, ts, id)
		idx += 2
	}
	q := fmt.Sprintf(`
SELECT `+helpers.AddressCols+`
FROM addresses
WHERE %s
ORDER BY created_at ASC, id ASC
LIMIT $%d`, helpers.JoinAnd(conds), idx)
	args = append(args, pageSize+1)

	rows, err := r.tx.Query(ctx, q, args...)
	if err != nil {
		return nil, "", helpers.WrapPgErr(err, "Address", "")
	}
	defer rows.Close()

	var out []*kacho.AddressRecord
	for rows.Next() {
		a, scanErr := helpers.ScanAddress(rows)
		if scanErr != nil {
			return nil, "", helpers.WrapPgErr(scanErr, "Address", "")
		}
		out = append(out, a)
	}
	if rerr := rows.Err(); rerr != nil {
		return nil, "", helpers.WrapPgErr(rerr, "Address", "")
	}
	var nextToken string
	if int64(len(out)) > pageSize {
		last := out[pageSize-1]
		nextToken = helpers.EncodePageToken(last.CreatedAt, last.ID)
		out = out[:pageSize]
	}
	return out, nextToken, nil
}

// addressPoolWriter — DML над address_pools через writer-TX. Embeds
// addressPoolReader (G.2 — writer видит свои writes).
//
// outbox-write — НЕ здесь, а в use-case'е через `RepositoryWriter.Outbox().Emit(...)`.
// Atomicity DML + outbox гарантируется одной pgx.Tx writer'а.
type addressPoolWriter struct {
	addressPoolReader
	emitter kacho.OutboxEmitter // unused внутри writer-методов; нужен для composability
}

// Insert — INSERT address_pools RETURNING.
func (w *addressPoolWriter) Insert(ctx context.Context, p *domain.AddressPool) (*kacho.AddressPoolRecord, error) {
	labels, err := helpers.MarshalJSONB(p.Labels, "AddressPool.labels")
	if err != nil {
		return nil, err
	}
	selector, err := helpers.MarshalJSONB(p.SelectorLabels, "AddressPool.selector_labels")
	if err != nil {
		return nil, err
	}
	var zoneArg any
	if p.ZoneID == "" {
		zoneArg = nil
	} else {
		zoneArg = p.ZoneID
	}
	// KAC-71: nil-slice → SQL NULL нарушит NOT NULL. Используем пустой []string{}
	// (text[] empty array — allowed by column).
	v4 := p.V4CIDRBlocks
	if v4 == nil {
		v4 = []string{}
	}
	v6 := p.V6CIDRBlocks
	if v6 == nil {
		v6 = []string{}
	}
	_, err = w.tx.Exec(ctx, `
		INSERT INTO address_pools (id, name, description, labels, v4_cidr_blocks, v6_cidr_blocks, kind, zone_id, is_default, selector_labels, selector_priority, created_at, modified_at)
		VALUES ($1,$2,$3,$4::jsonb,$5,$6,$7,$8,$9,$10::jsonb,$11,$12,$13)
	`, p.ID, p.Name, p.Description, labels, v4, v6, int16(p.Kind), zoneArg, p.IsDefault, selector, p.SelectorPriority, p.CreatedAt, p.ModifiedAt)
	if err != nil {
		return nil, helpers.WrapPgErr(err, "AddressPool", p.ID)
	}
	return &kacho.AddressPoolRecord{AddressPool: *p}, nil
}

// Update — UPDATE address_pools.
func (w *addressPoolWriter) Update(ctx context.Context, p *domain.AddressPool) (*kacho.AddressPoolRecord, error) {
	labels, err := helpers.MarshalJSONB(p.Labels, "AddressPool.labels")
	if err != nil {
		return nil, err
	}
	selector, err := helpers.MarshalJSONB(p.SelectorLabels, "AddressPool.selector_labels")
	if err != nil {
		return nil, err
	}
	v4 := p.V4CIDRBlocks
	if v4 == nil {
		v4 = []string{}
	}
	v6 := p.V6CIDRBlocks
	if v6 == nil {
		v6 = []string{}
	}
	tag, err := w.tx.Exec(ctx, `
		UPDATE address_pools
		SET name=$2, description=$3, labels=$4::jsonb,
		    v4_cidr_blocks=$5, v6_cidr_blocks=$6,
		    is_default=$7, selector_labels=$8::jsonb, selector_priority=$9, modified_at=$10
		WHERE id = $1
	`, p.ID, p.Name, p.Description, labels, v4, v6, p.IsDefault, selector, p.SelectorPriority, p.ModifiedAt)
	if err != nil {
		return nil, helpers.WrapPgErr(err, "AddressPool", p.ID)
	}
	if tag.RowsAffected() == 0 {
		return nil, fmt.Errorf("%w: AddressPool %s", helpers.ErrNotFound, p.ID)
	}
	return &kacho.AddressPoolRecord{AddressPool: *p}, nil
}

// Delete — DELETE address_pools WHERE id = $1. FK violation (есть Address с
// external_ipv4.address_pool_id) маппится через wrapPgErr → ErrFailedPrecondition
// (если FK задан на DB-уровне) ИЛИ caller отвергает sync через
// CountAddressesByPool guard (DeleteAddressPoolUseCase).
func (w *addressPoolWriter) Delete(ctx context.Context, id string) error {
	tag, err := w.tx.Exec(ctx, `DELETE FROM address_pools WHERE id = $1`, id)
	if err != nil {
		return helpers.WrapPgErr(err, "AddressPool", id)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: AddressPool %s", helpers.ErrNotFound, id)
	}
	return nil
}

// PopulateFreelistForPool — materialise per-IP freelist из V4CIDRBlocks
// (миграция 0014). Идемпотентно (ON CONFLICT DO NOTHING). V6-блоки идут через
// sparse counter (см. addressWriter.InitIPv6PoolCursor).
//
// KAC-71: после split CIDR-блоков читаем v4_cidr_blocks напрямую (раньше
// фильтровали unified cidr_blocks по family во время recursive CTE — теперь
// family-фильтрация на уровне колонки).
func (w *addressPoolWriter) PopulateFreelistForPool(ctx context.Context, poolID string) error {
	var cidrs []string
	err := w.tx.QueryRow(ctx,
		`SELECT v4_cidr_blocks FROM address_pools WHERE id = $1`, poolID,
	).Scan(&cidrs)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("populate freelist: pool %s: %w", poolID, helpers.ErrNotFound)
		}
		return fmt.Errorf("read v4_cidr_blocks for pool %s: %w", poolID, err)
	}
	for _, cidr := range cidrs {
		if _, err := w.tx.Exec(ctx, `
			WITH RECURSIVE ips(ip, stop) AS (
				SELECT (network($2::cidr) + 1)::inet, broadcast($2::cidr)::inet
				WHERE family($2::cidr) = 4
				UNION ALL
				SELECT (ip + 1)::inet, stop FROM ips WHERE ip + 1 < stop
			)
			INSERT INTO address_pool_free_ips (pool_id, ip)
			SELECT $1, ip FROM ips
			ON CONFLICT (pool_id, ip) DO NOTHING
		`, poolID, cidr); err != nil {
			return fmt.Errorf("populate freelist for cidr %s: %w", cidr, err)
		}
	}
	return nil
}
