package pg

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"net/netip"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/PRO-Robotech/kacho-corelib/filter"
	"github.com/PRO-Robotech/kacho-corelib/validate"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/helpers"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// addressReader — Get/List/GetByValue/Reference-lookups поверх произвольной
// pgx.Tx (read-only или RW). Не имеет своего state кроме tx.
//
// Wave 5 replicate (KAC-94, skill evgeniy §6 G.1-G.7): Address переезжает на
// CQRS, чтобы IPAM allocate-flow (Insert + AllocateIPFromFreelist + outbox
// emit) шёл в одной writer-TX. SQL/scan-семантика — parity с legacy
// `*repo.AddressRepo` (см. `internal/repo/address_repo.go`); shim-helpers
// экспортированы через `internal/repo/shim_kacho.go`.
type addressReader struct {
	tx pgx.Tx
}

// Get — verbatim YC: well-formed-but-absent → NotFound с "Address <id> not found".
func (r *addressReader) Get(ctx context.Context, id string) (*kacho.AddressRecord, error) {
	q := fmt.Sprintf(`SELECT %s FROM addresses WHERE id = $1`, helpers.AddressCols)
	row := r.tx.QueryRow(ctx, q, id)
	a, err := helpers.ScanAddress(row)
	if err != nil {
		return nil, helpers.WrapPgErr(err, "Address", id)
	}
	return a, nil
}

// List — cursor-based pagination + filter.Parse (YC-syntax). Парность с
// legacy AddressRepo: SubnetID-filter матчит internal_ipv4.subnet_id ИЛИ
// internal_ipv6.subnet_id (миграция 0013 для v6 parity).
func (r *addressReader) List(ctx context.Context, f kacho.AddressFilter, p kacho.Pagination) ([]*kacho.AddressRecord, string, error) {
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
	if f.SubnetID != "" {
		conditions = append(conditions, fmt.Sprintf("(internal_ipv4->>'subnet_id' = $%d OR internal_ipv6->>'subnet_id' = $%d)", argIdx, argIdx))
		args = append(args, f.SubnetID)
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
	q := fmt.Sprintf(`SELECT %s FROM addresses %s ORDER BY created_at ASC, id ASC LIMIT $%d`, helpers.AddressCols, where, argIdx)
	args = append(args, pageSize+1)

	rows, err := r.tx.Query(ctx, q, args...)
	if err != nil {
		return nil, "", helpers.WrapPgErr(err, "Address", "")
	}
	defer rows.Close()

	var result []*kacho.AddressRecord
	for rows.Next() {
		a, err := helpers.ScanAddress(rows)
		if err != nil {
			return nil, "", helpers.WrapPgErr(err, "Address", "")
		}
		result = append(result, a)
	}
	if err := rows.Err(); err != nil {
		return nil, "", helpers.WrapPgErr(err, "Address", "")
	}

	var nextToken string
	if int64(len(result)) > pageSize {
		last := result[pageSize-1]
		nextToken = helpers.EncodePageToken(last.CreatedAt, last.ID)
		result = result[:pageSize]
	}
	return result, nextToken, nil
}

// GetByValue — lookup-by-IP. subnetID — optional scope.
func (r *addressReader) GetByValue(ctx context.Context, externalIP, internalIP, subnetID string) (*kacho.AddressRecord, error) {
	args := []any{}
	conds := []string{}
	argIdx := 1
	if externalIP != "" {
		conds = append(conds, fmt.Sprintf("external_ipv4->>'address' = $%d", argIdx))
		args = append(args, externalIP)
		argIdx++
	}
	if internalIP != "" {
		conds = append(conds, fmt.Sprintf("internal_ipv4->>'address' = $%d", argIdx))
		args = append(args, internalIP)
		argIdx++
	}
	if len(conds) == 0 {
		return nil, helpers.ErrInvalidArg
	}
	where := "(" + strings.Join(conds, " OR ") + ")"
	if subnetID != "" {
		where = fmt.Sprintf(`%s AND internal_ipv4->>'subnet_id' = $%d`, where, argIdx)
		args = append(args, subnetID)
	}
	q := fmt.Sprintf(`SELECT %s FROM addresses WHERE %s LIMIT 1`, helpers.AddressCols, where)
	row := r.tx.QueryRow(ctx, q, args...)
	a, err := helpers.ScanAddress(row)
	if err != nil {
		return nil, helpers.WrapPgErr(err, "Address", "")
	}
	return a, nil
}

// ExistsIP — uniqueness-check для IPv4 (external OR internal).
func (r *addressReader) ExistsIP(ctx context.Context, ip string) (bool, error) {
	var count int
	err := r.tx.QueryRow(ctx, `
		SELECT COUNT(*) FROM addresses
		WHERE (
			(external_ipv4->>'address' = $1) OR
			(internal_ipv4->>'address' = $1)
		)
	`, ip).Scan(&count)
	if err != nil {
		return false, helpers.WrapPgErr(err, "Address", "")
	}
	return count > 0, nil
}

// GetReference — referrer-row. ErrNotFound если адреса нет ИЛИ нет referrer'а.
func (r *addressReader) GetReference(ctx context.Context, addressID string) (*domain.AddressReference, error) {
	var out domain.AddressReference
	err := r.tx.QueryRow(ctx, `
		SELECT address_id, referrer_type, referrer_id, referrer_name, attached_at
		FROM address_references WHERE address_id = $1`, addressID).
		Scan(&out.AddressID, &out.ReferrerType, &out.ReferrerID, &out.ReferrerName, &out.AttachedAt)
	if err != nil {
		return nil, helpers.WrapPgErr(err, "Address", addressID)
	}
	return &out, nil
}

// ReferencesForAddresses — batch lookup referrer'ов.
func (r *addressReader) ReferencesForAddresses(ctx context.Context, addressIDs []string) (map[string]*domain.AddressReference, error) {
	out := make(map[string]*domain.AddressReference, len(addressIDs))
	if len(addressIDs) == 0 {
		return out, nil
	}
	rows, err := r.tx.Query(ctx, `
		SELECT address_id, referrer_type, referrer_id, referrer_name, attached_at
		FROM address_references WHERE address_id = ANY($1)`, addressIDs)
	if err != nil {
		return nil, helpers.WrapPgErr(err, "Address", "")
	}
	defer rows.Close()
	for rows.Next() {
		var ref domain.AddressReference
		if err := rows.Scan(&ref.AddressID, &ref.ReferrerType, &ref.ReferrerID, &ref.ReferrerName, &ref.AttachedAt); err != nil {
			return nil, helpers.WrapPgErr(err, "Address", "")
		}
		out[ref.AddressID] = &ref
	}
	if err := rows.Err(); err != nil {
		return nil, helpers.WrapPgErr(err, "Address", "")
	}
	return out, nil
}

// addressWriter — DML над addresses через writer-TX. Embeds addressReader
// (G.2 — writer видит свои writes). Особенность CQRS-replicate: writer НЕ
// emit'ит outbox самостоятельно — после успешного DML caller (use-case) вызывает
// `RepositoryWriter.Outbox().Emit(...)` (явная точка G.5; atomicity DML +
// outbox гарантируется единой pgx.Tx).
//
// IPAM allocate-flow (skill evgeniy I.9/I.10): use-case-уровень открывает
// writer ONE раз, делает Insert(addr) → AllocateIPFromFreelist(pool, addr) →
// Outbox().Emit(Address.CREATED) → Commit. Никаких отдельных tx внутри
// allocate-step'ов (legacy `*repo.AddressRepo` открывал свою tx на каждый
// SetIPSpec — после Wave 5 replicate этого нет).
type addressWriter struct {
	addressReader
	emitter kacho.OutboxEmitter // unused в writer-методах; held for parity with networkWriter
}

// Insert — INSERT addresses RETURNING. CreatedAt — UTC `time.Now()`.
func (w *addressWriter) Insert(ctx context.Context, a *domain.Address) (*kacho.AddressRecord, error) {
	labelsJSON, err := helpers.MarshalJSONB(domain.LabelsToMap(a.Labels), "Address.labels")
	if err != nil {
		return nil, err
	}
	extJSON, err := marshalIPSpec(a.ExternalIpv4, "Address.external_ipv4")
	if err != nil {
		return nil, err
	}
	intJSON, err := marshalIPSpec(a.InternalIpv4, "Address.internal_ipv4")
	if err != nil {
		return nil, err
	}
	int6JSON, err := marshalIPSpec(a.InternalIpv6, "Address.internal_ipv6")
	if err != nil {
		return nil, err
	}
	ext6JSON, err := marshalIPSpec(a.ExternalIpv6, "Address.external_ipv6")
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	q := fmt.Sprintf(`
		INSERT INTO addresses (id, project_id, created_at, name, description, labels, addr_type, ip_version, reserved, used, deletion_protection, external_ipv4, internal_ipv4, internal_ipv6, external_ipv6)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
		RETURNING %s`, helpers.AddressCols)
	row := w.tx.QueryRow(ctx, q,
		a.ID, a.ProjectID, now, string(a.Name), string(a.Description), labelsJSON,
		int32(a.Type), int32(a.IpVersion), a.Reserved, a.Used, a.DeletionProtection,
		extJSON, intJSON, int6JSON, ext6JSON,
	)
	result, err := helpers.ScanAddress(row)
	if err != nil {
		return nil, helpers.WrapPgErr(err, "Address", string(a.Name))
	}
	return result, nil
}

// Update — UPDATE name/description/labels/reserved/used/deletion_protection.
// IP-spec колонки НЕ трогаем (immutable verbatim YC; для них есть SetIPSpec).
func (w *addressWriter) Update(ctx context.Context, a *domain.Address) (*kacho.AddressRecord, error) {
	labelsJSON, err := helpers.MarshalJSONB(domain.LabelsToMap(a.Labels), "Address.labels")
	if err != nil {
		return nil, err
	}
	q := fmt.Sprintf(`
		UPDATE addresses SET name=$2, description=$3, labels=$4, reserved=$5, used=$6, deletion_protection=$7
		WHERE id=$1
		RETURNING %s`, helpers.AddressCols)
	row := w.tx.QueryRow(ctx, q,
		a.ID, string(a.Name), string(a.Description), labelsJSON, a.Reserved, a.Used, a.DeletionProtection,
	)
	result, err := helpers.ScanAddress(row)
	if err != nil {
		return nil, helpers.WrapPgErr(err, "Address", a.ID)
	}
	return result, nil
}

// SetProjectID меняет project_id у Address (для :move).
func (w *addressWriter) SetProjectID(ctx context.Context, id, folderID string) (*kacho.AddressRecord, error) {
	q := fmt.Sprintf(`UPDATE addresses SET project_id = $2 WHERE id = $1 RETURNING %s`, helpers.AddressCols)
	row := w.tx.QueryRow(ctx, q, id, folderID)
	a, err := helpers.ScanAddress(row)
	if err != nil {
		return nil, helpers.WrapPgErr(err, "Address", id)
	}
	return a, nil
}

// SetIPSpec — атомарный UPDATE external_ipv4 / internal_ipv4. Передавайте nil
// для поля, которое не нужно менять; оба nil — no-op (вернуть Get).
func (w *addressWriter) SetIPSpec(ctx context.Context, id string, ext *domain.ExternalIpv4Spec, intn *domain.InternalIpv4Spec) (*kacho.AddressRecord, error) {
	if ext == nil && intn == nil {
		return w.Get(ctx, id)
	}
	q := `UPDATE addresses SET `
	args := []any{id}
	switch {
	case ext != nil && intn == nil:
		extJSON, err := helpers.MarshalJSONB(ext, "Address.external_ipv4")
		if err != nil {
			return nil, err
		}
		q += `external_ipv4 = $2::jsonb`
		args = append(args, extJSON)
	case ext == nil && intn != nil:
		intJSON, err := helpers.MarshalJSONB(intn, "Address.internal_ipv4")
		if err != nil {
			return nil, err
		}
		q += `internal_ipv4 = $2::jsonb`
		args = append(args, intJSON)
	default:
		extJSON, err := helpers.MarshalJSONB(ext, "Address.external_ipv4")
		if err != nil {
			return nil, err
		}
		intJSON, err := helpers.MarshalJSONB(intn, "Address.internal_ipv4")
		if err != nil {
			return nil, err
		}
		q += `external_ipv4 = $2::jsonb, internal_ipv4 = $3::jsonb`
		args = append(args, extJSON, intJSON)
	}
	q += ` WHERE id = $1 RETURNING ` + helpers.AddressCols
	row := w.tx.QueryRow(ctx, q, args...)
	a, err := helpers.ScanAddress(row)
	if err != nil {
		return nil, helpers.WrapPgErr(err, "Address", id)
	}
	return a, nil
}

// SetInternalIPv6 — атомарный UPDATE internal_ipv6 (random-pick allocator).
func (w *addressWriter) SetInternalIPv6(ctx context.Context, id string, spec *domain.InternalIpv6Spec) (*kacho.AddressRecord, error) {
	if spec == nil {
		return w.Get(ctx, id)
	}
	int6JSON, err := helpers.MarshalJSONB(spec, "Address.internal_ipv6")
	if err != nil {
		return nil, err
	}
	row := w.tx.QueryRow(ctx, `UPDATE addresses SET internal_ipv6 = $2::jsonb WHERE id = $1 RETURNING `+helpers.AddressCols, id, int6JSON)
	a, err := helpers.ScanAddress(row)
	if err != nil {
		return nil, helpers.WrapPgErr(err, "Address", id)
	}
	return a, nil
}

// Delete — DELETE addresses WHERE id = $1. FK violation → ErrFailedPrecondition
// "address is in use". row not affected → ErrNotFound.
func (w *addressWriter) Delete(ctx context.Context, id string) error {
	tag, err := w.tx.Exec(ctx, `DELETE FROM addresses WHERE id = $1`, id)
	if err != nil {
		if helpers.IsFKViolation(err) {
			return fmt.Errorf("%w: address is in use", helpers.ErrFailedPrecondition)
		}
		return helpers.WrapPgErr(err, "Address", id)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: Address %s not found", helpers.ErrNotFound, id)
	}
	return nil
}

// ---- IPAM v4 freelist ----

// AllocateIPFromFreelist — single-statement atomic pop из address_pool_free_ips
// (FOR UPDATE SKIP LOCKED) + UPDATE addresses.external_ipv4. Parity с legacy
// `*repo.AddressRepo.AllocateIPFromFreelist`.
func (w *addressWriter) AllocateIPFromFreelist(ctx context.Context, poolID, addressID string) (string, error) {
	var ip string
	err := w.tx.QueryRow(ctx, helpers.AllocateFromFreelistSQL, poolID, addressID).Scan(&ip)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", helpers.ErrPoolExhausted
	}
	if err != nil {
		return "", fmt.Errorf("allocate from freelist: %w", err)
	}
	return ip, nil
}

// ReturnIPToFreelist — INSERT … ON CONFLICT DO NOTHING.
func (w *addressWriter) ReturnIPToFreelist(ctx context.Context, poolID, ip string) error {
	_, err := w.tx.Exec(ctx, `
		INSERT INTO address_pool_free_ips (pool_id, ip)
		VALUES ($1, $2::inet)
		ON CONFLICT (pool_id, ip) DO NOTHING
	`, poolID, ip)
	if err != nil {
		return fmt.Errorf("return ip to freelist: %w", err)
	}
	return nil
}

// ---- IPAM v6 sparse counter (KAC-60) ----

// InitIPv6PoolCursor — INSERT cursor row для pool (idempotent).
func (w *addressWriter) InitIPv6PoolCursor(ctx context.Context, poolID string) error {
	_, err := w.tx.Exec(ctx,
		`INSERT INTO ipv6_pool_cursors (pool_id, next_offset)
		 VALUES ($1, 1)
		 ON CONFLICT (pool_id) DO NOTHING`,
		poolID)
	if err != nil {
		return helpers.WrapPgErr(err, "AddressPool", poolID)
	}
	return nil
}

// AllocateExternalIPv6 — sparse counter allocator. Parity с legacy
// `*repo.AddressRepo.AllocateExternalIPv6` (см. её doc); вся 5-шаговая логика
// (pop released → fresh cursor → INSERT allocated → UPDATE addresses → return)
// идёт через текущую writer-tx — caller (use-case) делает один Commit для всего
// allocate-flow.
func (w *addressWriter) AllocateExternalIPv6(ctx context.Context, poolID, addressID, zoneID string) (string, error) {
	var v6Blocks []string
	if err := w.tx.QueryRow(ctx,
		`SELECT v6_cidr_blocks FROM address_pools WHERE id = $1`, poolID).Scan(&v6Blocks); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", helpers.ErrNotFound
		}
		return "", fmt.Errorf("pool op: %w", err)
	}
	if len(v6Blocks) == 0 {
		return "", fmt.Errorf("%w: pool %s has no v6_cidr_blocks", helpers.ErrFailedPrecondition, poolID)
	}
	prefix, perr := netip.ParsePrefix(v6Blocks[0])
	if perr != nil {
		return "", fmt.Errorf("%w: pool %s has unparseable v6 prefix %q", helpers.ErrInternal, poolID, v6Blocks[0])
	}

	var offset *big.Int
	{
		var offStr string
		err := w.tx.QueryRow(ctx, `
			DELETE FROM ipv6_released_offsets
			 WHERE (pool_id, "offset") IN (
				SELECT pool_id, "offset" FROM ipv6_released_offsets
				 WHERE pool_id = $1
				 ORDER BY "offset" ASC
				 LIMIT 1 FOR UPDATE SKIP LOCKED)
			RETURNING "offset"::text`, poolID).Scan(&offStr)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			// fallthrough
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

	if offset == nil {
		var offStr string
		err := w.tx.QueryRow(ctx, `
			UPDATE ipv6_pool_cursors
			   SET next_offset = next_offset + 1
			 WHERE pool_id = $1
			RETURNING (next_offset - 1)::text`, poolID).Scan(&offStr)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return "", fmt.Errorf("%w: pool %s has no ipv6 cursor (InitIPv6PoolCursor not called?)", helpers.ErrFailedPrecondition, poolID)
			}
			return "", fmt.Errorf("pool op: %w", err)
		}
		off, ok := new(big.Int).SetString(offStr, 10)
		if !ok {
			return "", fmt.Errorf("parse cursor offset %q: invalid integer", offStr)
		}
		offset = off
	}

	ip, err := addOffsetToAddr(prefix.Addr(), offset)
	if err != nil {
		return "", fmt.Errorf("%w: %v", helpers.ErrInternal, err)
	}
	if !prefix.Contains(ip) {
		return "", helpers.ErrPoolExhausted
	}

	if _, err := w.tx.Exec(ctx, `
		INSERT INTO ipv6_allocated_ips (pool_id, ip, "offset", address_id)
		VALUES ($1, $2::inet, $3::numeric, $4)`,
		poolID, ip.String(), offset.String(), addressID); err != nil {
		return "", fmt.Errorf("insert ipv6_allocated_ips: %w", err)
	}

	spec := &domain.ExternalIpv6Spec{
		Address:       ip.String(),
		ZoneID:        zoneID,
		AddressPoolID: poolID,
	}
	ext6JSON, err := helpers.MarshalJSONB(spec, "Address.external_ipv6")
	if err != nil {
		return "", err
	}
	if _, err := w.tx.Exec(ctx,
		`UPDATE addresses SET external_ipv6 = $2::jsonb WHERE id = $1`,
		addressID, ext6JSON); err != nil {
		return "", helpers.WrapPgErr(err, "Address", addressID)
	}
	return ip.String(), nil
}

// FreeExternalIPv6 — освобождает v6 (released_offsets ← offset; addresses.external_ipv6 ← NULL).
// Идемпотент: no-op если адрес не аллоцирован.
func (w *addressWriter) FreeExternalIPv6(ctx context.Context, addressID string) error {
	var (
		poolID string
		offStr string
	)
	err := w.tx.QueryRow(ctx, `
		DELETE FROM ipv6_allocated_ips
		 WHERE address_id = $1
		RETURNING pool_id, "offset"::text`, addressID).Scan(&poolID, &offStr)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil
	case err != nil:
		return fmt.Errorf("free ipv6: %w", err)
	}
	if _, err := w.tx.Exec(ctx,
		`INSERT INTO ipv6_released_offsets (pool_id, "offset") VALUES ($1, $2::numeric)
		 ON CONFLICT (pool_id, "offset") DO NOTHING`,
		poolID, offStr); err != nil {
		return fmt.Errorf("insert ipv6_released_offsets: %w", err)
	}
	if _, err := w.tx.Exec(ctx,
		`UPDATE addresses SET external_ipv6 = NULL WHERE id = $1`, addressID); err != nil {
		return helpers.WrapPgErr(err, "Address", addressID)
	}
	return nil
}

// ---- Referrer-tracking (KAC-88 atomic CAS upsert) ----

// SetReference — single-statement CAS upsert referrer-row + addresses.used=true.
// Конфликт по адресу с ЧУЖИМ referrer'ом → ErrFailedPrecondition. Idempotent
// re-attach к тому же referrer проходит.
func (w *addressWriter) SetReference(ctx context.Context, ref *domain.AddressReference) (*domain.AddressReference, error) {
	tag, err := w.tx.Exec(ctx, `UPDATE addresses SET used = true WHERE id = $1`, ref.AddressID)
	if err != nil {
		return nil, helpers.WrapPgErr(err, "Address", ref.AddressID)
	}
	if tag.RowsAffected() == 0 {
		return nil, fmt.Errorf("%w: Address %s not found", helpers.ErrNotFound, ref.AddressID)
	}
	const q = `
		INSERT INTO address_references (address_id, referrer_type, referrer_id, referrer_name, attached_at)
		VALUES ($1, $2, $3, $4, now())
		ON CONFLICT (address_id) DO UPDATE
		  SET referrer_type = EXCLUDED.referrer_type,
		      referrer_id   = EXCLUDED.referrer_id,
		      referrer_name = EXCLUDED.referrer_name,
		      attached_at   = now()
		  WHERE address_references.referrer_id = EXCLUDED.referrer_id
		RETURNING address_id, referrer_type, referrer_id, referrer_name, attached_at`
	var out domain.AddressReference
	if err := w.tx.QueryRow(ctx, q, ref.AddressID, ref.ReferrerType, ref.ReferrerID, ref.ReferrerName).
		Scan(&out.AddressID, &out.ReferrerType, &out.ReferrerID, &out.ReferrerName, &out.AttachedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: address already referenced by another resource", helpers.ErrFailedPrecondition)
		}
		return nil, helpers.WrapPgErr(err, "Address", ref.AddressID)
	}
	return &out, nil
}

// MarkEphemeralInUse — атомарно reserved=false + used=true + CAS upsert referrer.
func (w *addressWriter) MarkEphemeralInUse(ctx context.Context, ref *domain.AddressReference) (*domain.AddressReference, error) {
	tag, err := w.tx.Exec(ctx, `UPDATE addresses SET reserved = false, used = true WHERE id = $1`, ref.AddressID)
	if err != nil {
		return nil, helpers.WrapPgErr(err, "Address", ref.AddressID)
	}
	if tag.RowsAffected() == 0 {
		return nil, fmt.Errorf("%w: Address %s not found", helpers.ErrNotFound, ref.AddressID)
	}
	const q = `
		INSERT INTO address_references (address_id, referrer_type, referrer_id, referrer_name, attached_at)
		VALUES ($1, $2, $3, $4, now())
		ON CONFLICT (address_id) DO UPDATE
		  SET referrer_type = EXCLUDED.referrer_type,
		      referrer_id   = EXCLUDED.referrer_id,
		      referrer_name = EXCLUDED.referrer_name,
		      attached_at   = now()
		  WHERE address_references.referrer_id = EXCLUDED.referrer_id
		RETURNING address_id, referrer_type, referrer_id, referrer_name, attached_at`
	var out domain.AddressReference
	if err := w.tx.QueryRow(ctx, q, ref.AddressID, ref.ReferrerType, ref.ReferrerID, ref.ReferrerName).
		Scan(&out.AddressID, &out.ReferrerType, &out.ReferrerID, &out.ReferrerName, &out.AttachedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: address already referenced by another resource", helpers.ErrFailedPrecondition)
		}
		return nil, helpers.WrapPgErr(err, "Address", ref.AddressID)
	}
	return &out, nil
}

// ClearReference — DELETE referrer-row + used=false.
func (w *addressWriter) ClearReference(ctx context.Context, addressID string) error {
	tag, err := w.tx.Exec(ctx, `UPDATE addresses SET used = false WHERE id = $1`, addressID)
	if err != nil {
		return helpers.WrapPgErr(err, "Address", addressID)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: Address %s not found", helpers.ErrNotFound, addressID)
	}
	if _, err := w.tx.Exec(ctx, `DELETE FROM address_references WHERE address_id = $1`, addressID); err != nil {
		return helpers.WrapPgErr(err, "Address", addressID)
	}
	return nil
}

// ---- helpers ----

// marshalIPSpec — общий json marshaler для опциональных IP-spec'ов
// (nil → nil []byte → SQL NULL). Дублирует internal marshalExternalIPv4 etc. из
// legacy address_repo.go — выделим в общий helper после полной миграции.
func marshalIPSpec(v any, field string) ([]byte, error) {
	if v == nil {
		return nil, nil
	}
	// JSON marshaler в repo обходит nil-проверку через reflect; пробросим напрямую
	// через MarshalJSONB чтобы получить тот же error-mapping.
	switch s := v.(type) {
	case *domain.ExternalIpv4Spec:
		if s == nil {
			return nil, nil
		}
		return helpers.MarshalJSONB(s, field)
	case *domain.InternalIpv4Spec:
		if s == nil {
			return nil, nil
		}
		return helpers.MarshalJSONB(s, field)
	case *domain.InternalIpv6Spec:
		if s == nil {
			return nil, nil
		}
		return helpers.MarshalJSONB(s, field)
	case *domain.ExternalIpv6Spec:
		if s == nil {
			return nil, nil
		}
		return helpers.MarshalJSONB(s, field)
	}
	return helpers.MarshalJSONB(v, field)
}

// addOffsetToAddr — IP + offset (big.Int) = новый IP. Для IPv6 — 128-bit math.
// Дубль legacy address_repo_ipv6.go::addOffsetToAddr (не экспортирован).
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

// Compile-time assert: addressWriter implements AddressWriterIface.
var (
	_ kacho.AddressReaderIface = (*addressReader)(nil)
	_ kacho.AddressWriterIface = (*addressWriter)(nil)
)
