// KAC-60: sparse counter-based IPAM для External IPv6 (миграция 0021).
// Реализует методы InitIPv6PoolCursor / AllocateExternalIPv6 / FreeExternalIPv6
// из ports.AddressRepo (см. docstrings там).
package repo

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"net/netip"

	"github.com/jackc/pgx/v5"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/ports"
)

// InitIPv6PoolCursor — INSERT cursor row для pool. Идемпотентно через
// ON CONFLICT DO NOTHING (повторный вызов с тем же poolID — no-op).
func (r *AddressRepo) InitIPv6PoolCursor(ctx context.Context, poolID string) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO ipv6_pool_cursors (pool_id, next_offset)
		 VALUES ($1, 1)
		 ON CONFLICT (pool_id) DO NOTHING`,
		poolID)
	if err != nil {
		return wrapPgErr(err, "AddressPool", poolID)
	}
	return nil
}

// AllocateExternalIPv6 — sparse counter-based allocator. См. ports.AddressRepo
// для семантики. Возвращает IP-литерал в canonical-форме (`netip.Addr.String()`).
// ErrPoolExhausted если cursor превысил host-bits CIDR'а.
//
// Транзакционно: все 4 шага (pop released → allocate offset → INSERT allocated →
// UPDATE address) в одной tx; outbox-эмит Address.UPDATED тоже здесь.
func (r *AddressRepo) AllocateExternalIPv6(ctx context.Context, poolID, addressID, zoneID string) (string, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return "", ports.ErrInternal
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// KAC-71: после split column читаем v6_cidr_blocks напрямую (без runtime-
	// фильтра family). Берём первый prefix. Подтверждаем что pool существует
	// и имеет хотя бы один v6-CIDR.
	var v6Blocks []string
	if err := tx.QueryRow(ctx,
		`SELECT v6_cidr_blocks FROM address_pools WHERE id = $1`, poolID).Scan(&v6Blocks); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ports.ErrNotFound
		}
		return "", fmt.Errorf("pool op: %w", err)
	}
	if len(v6Blocks) == 0 {
		return "", fmt.Errorf("%w: pool %s has no v6_cidr_blocks", ports.ErrFailedPrecondition, poolID)
	}
	prefix, perr := netip.ParsePrefix(v6Blocks[0])
	if perr != nil {
		return "", fmt.Errorf("%w: pool %s has unparseable v6 prefix %q", ports.ErrInternal, poolID, v6Blocks[0])
	}

	// Step 1: пробуем переиспользовать освобождённый offset.
	// pgx не умеет binary-scan NUMERIC → *big.Int; используем text-form через
	// VARCHAR cast и парсим строкой.
	var offset *big.Int
	{
		var offStr string
		err := tx.QueryRow(ctx, `
			DELETE FROM ipv6_released_offsets
			 WHERE (pool_id, "offset") IN (
				SELECT pool_id, "offset" FROM ipv6_released_offsets
				 WHERE pool_id = $1
				 ORDER BY "offset" ASC
				 LIMIT 1 FOR UPDATE SKIP LOCKED)
			RETURNING "offset"::text`, poolID).Scan(&offStr)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			// released пуст — fallthrough к counter.
		case err != nil:
			return "", fmt.Errorf("pool op: %w", err)
		default:
			off, ok := new(big.Int).SetString(offStr, 10)
			if !ok {
				return "", fmt.Errorf("parse offset %q: invalid integer", offStr)
			}
			offset = off
		}
	}

	// Step 2: fresh from cursor.
	if offset == nil {
		var offStr string
		err := tx.QueryRow(ctx, `
			UPDATE ipv6_pool_cursors
			   SET next_offset = next_offset + 1
			 WHERE pool_id = $1
			RETURNING (next_offset - 1)::text`, poolID).Scan(&offStr)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return "", fmt.Errorf("%w: pool %s has no ipv6 cursor (InitIPv6PoolCursor not called?)", ports.ErrFailedPrecondition, poolID)
			}
			return "", fmt.Errorf("pool op: %w", err)
		}
		off, ok := new(big.Int).SetString(offStr, 10)
		if !ok {
			return "", fmt.Errorf("parse cursor offset %q: invalid integer", offStr)
		}
		offset = off
	}

	// Step 3: compute IP = pool_base + offset, проверяем что не вышли за CIDR.
	ip, err := addOffsetToAddr(prefix.Addr(), offset)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ports.ErrInternal, err)
	}
	if !prefix.Contains(ip) {
		return "", ErrPoolExhausted
	}

	// Step 4: INSERT в ipv6_allocated_ips (uniqueness — backstop). offset
	// сериализуем как text → cast в numeric (pgx native binary-write для big.Int
	// не работает с NUMERIC).
	if _, err := tx.Exec(ctx, `
		INSERT INTO ipv6_allocated_ips (pool_id, ip, "offset", address_id)
		VALUES ($1, $2::inet, $3::numeric, $4)`,
		poolID, ip.String(), offset.String(), addressID); err != nil {
		return "", fmt.Errorf("insert ipv6_allocated_ips: %w", err)
	}

	// Step 5: UPDATE addresses.external_ipv6.
	spec := &domain.ExternalIpv6Spec{
		Address:       ip.String(),
		ZoneID:        zoneID,
		AddressPoolID: poolID,
	}
	ext6JSON, err := marshalExternalIPv6(spec)
	if err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE addresses SET external_ipv6 = $2::jsonb WHERE id = $1`,
		addressID, ext6JSON); err != nil {
		return "", wrapPgErr(err, "Address", addressID)
	}

	// Step 6: outbox emit.
	if err := emitVPC(ctx, tx, "Address", addressID, "UPDATED", map[string]any{
		"id": addressID, "external_ipv6_address": ip.String(),
	}); err != nil {
		return "", fmt.Errorf("outbox emit: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return "", wrapPgErr(err, "Address", addressID)
	}
	return ip.String(), nil
}

// FreeExternalIPv6 — освобождает IPv6 для address. Если нет аллокации (already
// freed / address без external_ipv6) — no-op (идемпотент).
func (r *AddressRepo) FreeExternalIPv6(ctx context.Context, addressID string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return ports.ErrInternal
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var (
		poolID string
		offStr string
	)
	err = tx.QueryRow(ctx, `
		DELETE FROM ipv6_allocated_ips
		 WHERE address_id = $1
		RETURNING pool_id, "offset"::text`, addressID).Scan(&poolID, &offStr)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// Уже освобождён (или не аллоцировался). Идемпотент.
		return tx.Commit(ctx)
	case err != nil:
		return fmt.Errorf("free ipv6: %w", err)
	}

	// Возвращаем offset в released (для переиспользования).
	if _, err := tx.Exec(ctx,
		`INSERT INTO ipv6_released_offsets (pool_id, "offset") VALUES ($1, $2::numeric)
		 ON CONFLICT (pool_id, "offset") DO NOTHING`,
		poolID, offStr); err != nil {
		return fmt.Errorf("insert ipv6_released_offsets: %w", err)
	}

	// Очищаем addresses.external_ipv6 (в той же tx).
	if _, err := tx.Exec(ctx,
		`UPDATE addresses SET external_ipv6 = NULL WHERE id = $1`, addressID); err != nil {
		return wrapPgErr(err, "Address", addressID)
	}

	if err := emitVPC(ctx, tx, "Address", addressID, "UPDATED", map[string]any{
		"id": addressID, "external_ipv6_released": true,
	}); err != nil {
		return ports.ErrInternal
	}
	return tx.Commit(ctx)
}

// addOffsetToAddr — IP + offset (big.Int) = новый IP. Для IPv6 — 128-bit math.
func addOffsetToAddr(base netip.Addr, offset *big.Int) (netip.Addr, error) {
	if !base.Is6() {
		return netip.Addr{}, fmt.Errorf("addOffsetToAddr: only IPv6 supported, got %v", base)
	}
	b := base.As16()
	baseInt := new(big.Int).SetBytes(b[:])
	resultInt := new(big.Int).Add(baseInt, offset)
	resultBytes := resultInt.Bytes()
	if len(resultBytes) > 16 {
		return netip.Addr{}, fmt.Errorf("addOffsetToAddr: overflow (offset %s + base %s)", offset.String(), base.String())
	}
	var out [16]byte
	copy(out[16-len(resultBytes):], resultBytes)
	return netip.AddrFrom16(out), nil
}
