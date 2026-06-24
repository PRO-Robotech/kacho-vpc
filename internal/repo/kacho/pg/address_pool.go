// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

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

// addressPoolReader — Get/List/GetDefaultForZone/CountAddressesByPool/... поверх
// произвольной pgx.Tx (read-only или RW). Не имеет своего state кроме tx.
//
// Read-сторона CQRS-разбиения AddressPool: чтения и запись делят одну writer-TX,
// чтобы Create/Update/Delete + PopulateFreelistForPool + outbox emit были атомарны.
type addressPoolReader struct {
	tx pgx.Tx
}

// Get — well-formed-но-отсутствующий id → NotFound с "AddressPool <id> not found".
func (r *addressPoolReader) Get(ctx context.Context, id string) (*kacho.AddressPoolRecord, error) {
	q := fmt.Sprintf(`SELECT %s FROM address_pools WHERE id = $1`, helpers.AddressPoolCols)
	row := r.tx.QueryRow(ctx, q, id)
	rec, err := helpers.ScanAddressPool(row)
	if err != nil {
		return nil, helpers.WrapPgErr(err, "AddressPool", id)
	}
	return rec, nil
}

// List — cursor-based pagination + (kind, zone_id) filter.
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

// CountAddressesByPool — admin observability: сколько Address используют pool.
func (r *addressPoolReader) CountAddressesByPool(ctx context.Context, poolID string) (int64, error) {
	var n int64
	// Считаем выделенные адреса ОБЕИХ семей: пул мог раздать только external IPv6,
	// и учет лишь v4 ошибочно счел бы пул пустым → удаление оставило бы
	// dangling-ссылку в external_ipv6.address_pool_id.
	err := r.tx.QueryRow(ctx,
		`SELECT count(*) FROM addresses
		 WHERE (external_ipv4 ->> 'address_pool_id' = $1
		         AND coalesce(external_ipv4 ->> 'address', '') <> '')
		    OR (external_ipv6 ->> 'address_pool_id' = $1
		         AND coalesce(external_ipv6 ->> 'address', '') <> '')`, poolID).Scan(&n)
	if err != nil {
		return 0, helpers.WrapPgErr(err, "AddressPool", poolID)
	}
	return n, nil
}

// CountAddressesByPoolPerCIDR — для каждого V4CIDR — allocated count. Для
// V6-блоков возвращает count=0 placeholder (sparse v6-allocator ведет свою
// бухгалтерию через ipv6_pool_cursors / ipv6_allocated_ips).
//
// Single-roundtrip: один SELECT с unnest WITH ORDINALITY + LEFT JOIN addresses
// через inet-оператор `<<`. Ключи возвращаются caller'у в том же raw-string виде,
// что в pool.V4CIDRBlocks — без канонизации.
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

// ListAddressesByPool — кросс-project список Address с IP из pool.
// projectFilter == "" → без фильтра. Возвращает *kacho.AddressRecord.
func (r *addressPoolReader) ListAddressesByPool(ctx context.Context, poolID, projectFilter string, p kacho.Pagination) ([]*kacho.AddressRecord, string, error) {
	pageSize, err := validate.PageSize("page_size", p.PageSize)
	if err != nil {
		return nil, "", err
	}
	args := []any{poolID}
	conds := []string{"external_ipv4 ->> 'address_pool_id' = $1"}
	idx := 2
	if projectFilter != "" {
		conds = append(conds, fmt.Sprintf("project_id = $%d", idx))
		args = append(args, projectFilter)
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
// addressPoolReader (writer видит свои writes).
//
// outbox-write — НЕ здесь, а в use-case'е через `RepositoryWriter.Outbox().Emit(...)`.
// Атомарность DML + outbox гарантируется одной pgx.Tx writer'а.
type addressPoolWriter struct {
	addressPoolReader
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
	// nil-slice → SQL NULL нарушил бы NOT NULL. Используем пустой []string{}
	// (text[] empty array — колонка его допускает).
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

// LockForUpdate — row-lock pool (`FOR UPDATE`). Конфликтует с `FOR SHARE`,
// который берут external-allocate пути → сериализует Delete vs in-flight
// allocate. ErrNotFound если pool отсутствует.
func (w *addressPoolWriter) LockForUpdate(ctx context.Context, id string) error {
	var got string
	err := w.tx.QueryRow(ctx, `SELECT id FROM address_pools WHERE id = $1 FOR UPDATE`, id).Scan(&got)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: AddressPool %s", helpers.ErrNotFound, id)
	}
	if err != nil {
		return helpers.WrapPgErr(err, "AddressPool", id)
	}
	return nil
}

// PopulateFreelistForPool — materialise per-IP freelist из V4CIDRBlocks.
// Идемпотентно (ON CONFLICT DO NOTHING). V6-блоки идут через sparse counter
// (см. addressWriter.InitIPv6PoolCursor). Family-фильтрация — на уровне колонки
// v4_cidr_blocks (читаем ее напрямую).
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
	return w.populateFreelistForCidrs(ctx, poolID, cidrs)
}

// AddCidrToFreelist — материализует per-IP freelist только для переданных
// (новых) v4-CIDR'ов (:addCidrBlocks). В отличие от PopulateFreelistForPool
// обрабатывает не весь pool, а дельту, переданную caller'ом — чтобы не
// реитерировать уже существующие free_ips (ON CONFLICT DO NOTHING все равно
// защищает от дублей, но дельта дешевле). V6-блоки игнорируются (sparse counter,
// см. addressWriter.InitIPv6PoolCursor — caller вызывает его отдельно).
func (w *addressPoolWriter) AddCidrToFreelist(ctx context.Context, poolID string, newV4Cidrs []string) error {
	return w.populateFreelistForCidrs(ctx, poolID, newV4Cidrs)
}

// populateFreelistForCidrs — общая materialise-логика для списка v4-CIDR.
// Идемпотентна (ON CONFLICT DO NOTHING). Не-v4 (`family <> 4`) фильтруется в SQL.
func (w *addressPoolWriter) populateFreelistForCidrs(ctx context.Context, poolID string, cidrs []string) error {
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

// DeleteFreelistForCidrs — удаляет free_ips, попадающие в любой из переданных
// CIDR'ов (:removeCidrBlocks). Берет row-lock'и на удаляемые free_ips — в одной
// TX с use-check'ом это сериализует remove относительно конкурентного
// AllocateIPFromFreelistSQL (тот делает `FOR UPDATE SKIP LOCKED` и DELETE тех же
// строк): либо allocate успел (его IP уже в addresses → use-check видит и
// remove abort'ится), либо remove успел (free_ip удален → allocate его не
// возьмет). v6-CIDR в списке безвредны: ни одна free_ip-строка (всегда v4) не
// попадет под `<<= cidr` для v6-префикса.
func (w *addressPoolWriter) DeleteFreelistForCidrs(ctx context.Context, poolID string, cidrs []string) error {
	if len(cidrs) == 0 {
		return nil
	}
	_, err := w.tx.Exec(ctx, `
		DELETE FROM address_pool_free_ips
		WHERE pool_id = $1 AND ip <<= ANY($2::cidr[])
	`, poolID, cidrs)
	if err != nil {
		return helpers.WrapPgErr(err, "AddressPool", poolID)
	}
	return nil
}

// CountAllocatedInCidrs — сколько Address имеют выделенный external_ipv4.address,
// принадлежащий пулу poolID И попадающий в любой из переданных CIDR'ов
// (use-check для :removeCidrBlocks). Используется для запрета удаления CIDR,
// в котором есть аллоцированные IP (FailedPrecondition). Считает только v4
// (external_ipv4); v6-CIDR в списке не дают совпадений по `::inet << cidr` для
// v4-адресов и наоборот — безопасно.
func (w *addressPoolWriter) CountAllocatedInCidrs(ctx context.Context, poolID string, cidrs []string) (int64, error) {
	if len(cidrs) == 0 {
		return 0, nil
	}
	var n int64
	err := w.tx.QueryRow(ctx, `
		SELECT count(*) FROM addresses a
		WHERE a.external_ipv4 ->> 'address_pool_id' = $1
		  AND coalesce(a.external_ipv4 ->> 'address', '') <> ''
		  AND (a.external_ipv4 ->> 'address')::inet <<= ANY($2::cidr[])
	`, poolID, cidrs).Scan(&n)
	if err != nil {
		return 0, helpers.WrapPgErr(err, "AddressPool", poolID)
	}
	return n, nil
}

// InsertCidrBlocks — материализует каждый v4/v6 CIDR-блок пула в нормализованную
// child-таблицу address_pool_cidrs. EXCLUDE gist `(kind WITH =, block && )`
// атомарно (race-free by construction) запрещает пересечение CIDR внутри одного
// kind — внутри пула И между пулами. На пустые блоки ("") не реагируем.
// Пустой список → no-op.
//
// SQLSTATE 23P01 (exclusion_violation) → ErrFailedPrecondition с текстом
// "address pool CIDRs can not overlap" (паритет с Subnet "Subnet CIDRs can not
// overlap"). 23505 (PK dup, не должно случаться после dedup в use-case) — тоже в
// FailedPrecondition (тот же текст: дубль блока = пересечение сам с собой).
// Прочие классы — через общий wrapPgErr.
func (w *addressPoolWriter) InsertCidrBlocks(ctx context.Context, poolID string, kind domain.AddressPoolKind, v4, v6 []string) error {
	blocks := make([]string, 0, len(v4)+len(v6))
	for _, b := range v4 {
		if b != "" {
			blocks = append(blocks, b)
		}
	}
	for _, b := range v6 {
		if b != "" {
			blocks = append(blocks, b)
		}
	}
	for _, block := range blocks {
		if _, err := w.tx.Exec(ctx, `
			INSERT INTO address_pool_cidrs (pool_id, kind, block)
			VALUES ($1, $2, $3::cidr)
		`, poolID, int16(kind), block); err != nil {
			return mapCidrOverlapErr(err, poolID)
		}
	}
	return nil
}

// DeleteCidrBlocks — удаляет конкретные v4/v6 block'и пула из address_pool_cidrs
// (для :removeCidrBlocks — освобождает CIDR-диапазон для будущих пулов).
// Pool.Delete каскадит через FK ON DELETE CASCADE, поэтому при удалении пула
// явный вызов не нужен. Пустые блоки игнорируются; пустой список → no-op.
func (w *addressPoolWriter) DeleteCidrBlocks(ctx context.Context, poolID string, v4, v6 []string) error {
	blocks := make([]string, 0, len(v4)+len(v6))
	for _, b := range v4 {
		if b != "" {
			blocks = append(blocks, b)
		}
	}
	for _, b := range v6 {
		if b != "" {
			blocks = append(blocks, b)
		}
	}
	if len(blocks) == 0 {
		return nil
	}
	_, err := w.tx.Exec(ctx, `
		DELETE FROM address_pool_cidrs
		WHERE pool_id = $1 AND block = ANY($2::cidr[])
	`, poolID, blocks)
	if err != nil {
		return helpers.WrapPgErr(err, "AddressPool", poolID)
	}
	return nil
}

// mapCidrOverlapErr — EXCLUDE (23P01) и PK-dup (23505) на address_pool_cidrs
// маппятся в ErrFailedPrecondition с текстом "address pool CIDRs can not overlap"
// (паритет с Subnet CIDR-overlap). Прочие SQLSTATE → общий wrapPgErr (не leak'аем
// raw PG).
func mapCidrOverlapErr(err error, poolID string) error {
	if err == nil {
		return nil
	}
	if helpers.IsExclusionViolation(err) || helpers.IsUniqueViolation(err) {
		return fmt.Errorf("%w: address pool CIDRs can not overlap", helpers.ErrFailedPrecondition)
	}
	return helpers.WrapPgErr(err, "AddressPool", poolID)
}
