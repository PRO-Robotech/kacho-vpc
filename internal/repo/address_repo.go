package repo

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PRO-Robotech/kacho-corelib/filter"
	"github.com/PRO-Robotech/kacho-corelib/validate"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/service"
)

// AddressRepo — реализация service.AddressRepo поверх pgxpool.
type AddressRepo struct {
	pool *pgxpool.Pool
}

// NewAddressRepo создаёт AddressRepo.
func NewAddressRepo(pool *pgxpool.Pool) *AddressRepo {
	return &AddressRepo{pool: pool}
}

const addressCols = `id, folder_id, created_at, name, description, labels, addr_type, ip_version, reserved, used, deletion_protection, external_ipv4, internal_ipv4`

func (r *AddressRepo) Get(ctx context.Context, id string) (*domain.Address, error) {
	q := fmt.Sprintf(`SELECT %s FROM addresses WHERE id = $1`, addressCols)
	row := r.pool.QueryRow(ctx, q, id)
	a, err := scanAddress(row)
	if err != nil {
		return nil, wrapPgErr(err, "Address", id)
	}
	return a, nil
}

func (r *AddressRepo) List(ctx context.Context, f service.AddressFilter, p service.Pagination) ([]*domain.Address, string, error) {
	pageSize, err := validate.PageSize("page_size", p.PageSize)
	if err != nil {
		return nil, "", err
	}

	args := []any{}
	conditions := []string{}
	argIdx := 1

	if f.FolderID != "" {
		conditions = append(conditions, fmt.Sprintf("folder_id = $%d", argIdx))
		args = append(args, f.FolderID)
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
			return nil, "", invalidFilterErr(perr)
		}
		if ast != nil {
			frag, fargs := ast.ToSQL(argIdx)
			conditions = append(conditions, frag)
			args = append(args, fargs...)
			argIdx += len(fargs)
		}
	}
	if p.PageToken != "" {
		ts, id, err := decodePageToken(p.PageToken)
		if err != nil {
			return nil, "", invalidPageTokenErr(err)
		}
		conditions = append(conditions, fmt.Sprintf("(created_at, id) > ($%d, $%d)", argIdx, argIdx+1))
		args = append(args, ts, id)
		argIdx += 2
	}

	var where string
	if len(conditions) > 0 {
		where = "WHERE " + strings.Join(conditions, " AND ")
	}
	q := fmt.Sprintf(`SELECT %s FROM addresses %s ORDER BY created_at ASC, id ASC LIMIT $%d`, addressCols, where, argIdx)
	args = append(args, pageSize+1)

	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, "", wrapPgErr(err, "Address", "")
	}
	defer rows.Close()

	var result []*domain.Address
	for rows.Next() {
		a, err := scanAddress(rows)
		if err != nil {
			return nil, "", wrapPgErr(err, "Address", "")
		}
		result = append(result, a)
	}
	if err := rows.Err(); err != nil {
		return nil, "", wrapPgErr(err, "Address", "")
	}

	var nextToken string
	if int64(len(result)) > pageSize {
		last := result[pageSize-1]
		nextToken = encodePageToken(last.CreatedAt, last.ID)
		result = result[:pageSize]
	}
	return result, nextToken, nil
}

func (r *AddressRepo) Insert(ctx context.Context, a *domain.Address) (*domain.Address, error) {
	labelsJSON, err := marshalJSONB(a.Labels, "Address.labels")
	if err != nil {
		return nil, err
	}
	extJSON, err := marshalExternalIPv4(a.ExternalIpv4)
	if err != nil {
		return nil, err
	}
	intJSON, err := marshalInternalIPv4(a.InternalIpv4)
	if err != nil {
		return nil, err
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, service.ErrInternal
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const q = `
		INSERT INTO addresses (id, folder_id, created_at, name, description, labels, addr_type, ip_version, reserved, used, deletion_protection, external_ipv4, internal_ipv4)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		RETURNING ` + addressCols

	row := tx.QueryRow(ctx, q,
		a.ID, a.FolderID, a.CreatedAt, a.Name, a.Description, labelsJSON,
		int32(a.Type), int32(a.IpVersion), a.Reserved, a.Used, a.DeletionProtection,
		extJSON, intJSON,
	)
	result, err := scanAddress(row)
	if err != nil {
		return nil, wrapPgErr(err, "Address", a.Name)
	}
	if err := emitVPC(ctx, tx, "Address", result.ID, "CREATED", addressPayload(result)); err != nil {
		return nil, service.ErrInternal
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, wrapPgErr(err, "Address", a.Name)
	}
	return result, nil
}

func (r *AddressRepo) Update(ctx context.Context, a *domain.Address) (*domain.Address, error) {
	labelsJSON, err := marshalJSONB(a.Labels, "Address.labels")
	if err != nil {
		return nil, err
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, service.ErrInternal
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const q = `
		UPDATE addresses SET name=$2, description=$3, labels=$4, reserved=$5, used=$6, deletion_protection=$7
		WHERE id=$1
		RETURNING ` + addressCols

	row := tx.QueryRow(ctx, q,
		a.ID, a.Name, a.Description, labelsJSON, a.Reserved, a.Used, a.DeletionProtection,
	)
	result, err := scanAddress(row)
	if err != nil {
		return nil, wrapPgErr(err, "Address", a.ID)
	}
	if err := emitVPC(ctx, tx, "Address", result.ID, "UPDATED", addressPayload(result)); err != nil {
		return nil, service.ErrInternal
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, wrapPgErr(err, "Address", a.ID)
	}
	return result, nil
}

// SetIPSpec атомарно обновляет external_ipv4 / internal_ipv4 JSONB-spec.
// Передавайте nil для поля, которое не нужно менять.
func (r *AddressRepo) SetIPSpec(ctx context.Context, id string, ext *domain.ExternalIpv4Spec, intn *domain.InternalIpv4Spec) (*domain.Address, error) {
	if ext == nil && intn == nil {
		return r.Get(ctx, id)
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, service.ErrInternal
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := `UPDATE addresses SET `
	args := []any{id}
	switch {
	case ext != nil && intn == nil:
		extJSON, err := marshalJSONB(ext, "Address.external_ipv4")
		if err != nil {
			return nil, err
		}
		q += `external_ipv4 = $2::jsonb`
		args = append(args, extJSON)
	case ext == nil && intn != nil:
		intJSON, err := marshalJSONB(intn, "Address.internal_ipv4")
		if err != nil {
			return nil, err
		}
		q += `internal_ipv4 = $2::jsonb`
		args = append(args, intJSON)
	default:
		extJSON, err := marshalJSONB(ext, "Address.external_ipv4")
		if err != nil {
			return nil, err
		}
		intJSON, err := marshalJSONB(intn, "Address.internal_ipv4")
		if err != nil {
			return nil, err
		}
		q += `external_ipv4 = $2::jsonb, internal_ipv4 = $3::jsonb`
		args = append(args, extJSON, intJSON)
	}
	q += ` WHERE id = $1 RETURNING ` + addressCols

	row := tx.QueryRow(ctx, q, args...)
	a, err := scanAddress(row)
	if err != nil {
		return nil, wrapPgErr(err, "Address", id)
	}
	if err := emitVPC(ctx, tx, "Address", a.ID, "UPDATED", addressPayload(a)); err != nil {
		return nil, service.ErrInternal
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, wrapPgErr(err, "Address", id)
	}
	return a, nil
}

// SetFolderID меняет folder_id у Address.
func (r *AddressRepo) SetFolderID(ctx context.Context, id, folderID string) (*domain.Address, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, service.ErrInternal
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := fmt.Sprintf(`UPDATE addresses SET folder_id = $2 WHERE id = $1 RETURNING %s`, addressCols)
	row := tx.QueryRow(ctx, q, id, folderID)
	a, err := scanAddress(row)
	if err != nil {
		return nil, wrapPgErr(err, "Address", id)
	}
	if err := emitVPC(ctx, tx, "Address", a.ID, "UPDATED", addressPayload(a)); err != nil {
		return nil, service.ErrInternal
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, wrapPgErr(err, "Address", id)
	}
	return a, nil
}

func (r *AddressRepo) Delete(ctx context.Context, id string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return service.ErrInternal
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx, `DELETE FROM addresses WHERE id = $1`, id)
	if err != nil {
		if isFKViolation(err) {
			return fmt.Errorf("%w: address is in use", service.ErrFailedPrecondition)
		}
		// 22P02 → InvalidArgument "invalid address id 'X'" (verbatim YC).
		return wrapPgErr(err, "Address", id)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: Address %s not found", service.ErrNotFound, id)
	}
	if err := emitVPC(ctx, tx, "Address", id, "DELETED", map[string]any{"id": id}); err != nil {
		return service.ErrInternal
	}
	if err := tx.Commit(ctx); err != nil {
		return wrapPgErr(err, "Address", id)
	}
	return nil
}

// GetByValue возвращает Address по конкретному IP (external или internal).
// Если задан subnetID — фильтрует по нему.
//
// Поведение verbatim YC: внутри одной подсети IP уникален, поэтому LIMIT 1.
// При отсутствии возвращает ErrNotFound.
func (r *AddressRepo) GetByValue(ctx context.Context, externalIP, internalIP, subnetID string) (*domain.Address, error) {
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
		return nil, service.ErrInvalidArg
	}
	where := "(" + strings.Join(conds, " OR ") + ")"
	if subnetID != "" {
		where = fmt.Sprintf(`%s AND internal_ipv4->>'subnet_id' = $%d`, where, argIdx)
		args = append(args, subnetID)
	}
	q := fmt.Sprintf(`SELECT %s FROM addresses WHERE %s LIMIT 1`, addressCols, where)
	row := r.pool.QueryRow(ctx, q, args...)
	a, err := scanAddress(row)
	if err != nil {
		return nil, wrapPgErr(err, "Address", "")
	}
	return a, nil
}

// ExistsIP проверяет, занят ли IP-адрес (в external или internal).
func (r *AddressRepo) ExistsIP(ctx context.Context, ip string) (bool, error) {
	var count int
	err := r.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM addresses
		WHERE (
			(external_ipv4->>'address' = $1) OR
			(internal_ipv4->>'address' = $1)
		)
	`, ip).Scan(&count)
	if err != nil {
		return false, wrapPgErr(err, "Address", "")
	}
	return count > 0, nil
}

// ---- address references (referrer-tracking) ----

// SetReference upsert'ит referrer-row для address_id и выставляет
// addresses.used=true — всё в одной tx. ErrNotFound если address не существует.
func (r *AddressRepo) SetReference(ctx context.Context, ref *domain.AddressReference) (*domain.AddressReference, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, service.ErrInternal
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx, `UPDATE addresses SET used = true WHERE id = $1`, ref.AddressID)
	if err != nil {
		return nil, wrapPgErr(err, "Address", ref.AddressID)
	}
	if tag.RowsAffected() == 0 {
		return nil, fmt.Errorf("%w: Address %s not found", service.ErrNotFound, ref.AddressID)
	}

	const q = `
		INSERT INTO address_references (address_id, referrer_type, referrer_id, referrer_name, attached_at)
		VALUES ($1, $2, $3, $4, now())
		ON CONFLICT (address_id) DO UPDATE
		  SET referrer_type = EXCLUDED.referrer_type,
		      referrer_id   = EXCLUDED.referrer_id,
		      referrer_name = EXCLUDED.referrer_name,
		      attached_at   = now()
		RETURNING address_id, referrer_type, referrer_id, referrer_name, attached_at`
	var out domain.AddressReference
	if err := tx.QueryRow(ctx, q, ref.AddressID, ref.ReferrerType, ref.ReferrerID, ref.ReferrerName).
		Scan(&out.AddressID, &out.ReferrerType, &out.ReferrerID, &out.ReferrerName, &out.AttachedAt); err != nil {
		return nil, wrapPgErr(err, "Address", ref.AddressID)
	}
	if err := emitVPC(ctx, tx, "Address", ref.AddressID, "UPDATED", map[string]any{"id": ref.AddressID, "used": true}); err != nil {
		return nil, service.ErrInternal
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, wrapPgErr(err, "Address", ref.AddressID)
	}
	return &out, nil
}

// MarkEphemeralInUse атомарно (одна tx): выставляет addresses.reserved=false,
// addresses.used=true и upsert'ит referrer-row (= SetReference + сброс reserved).
// ErrNotFound если address не существует. Используется для эфемерных NIC/NAT
// Address-ресурсов, созданных kacho-compute через AddressService.Create
// (там reserved=true verbatim YC, но для авто-аллоцированного NIC-адреса это
// неверно — в YC он не reserved). Идемпотентно.
func (r *AddressRepo) MarkEphemeralInUse(ctx context.Context, ref *domain.AddressReference) (*domain.AddressReference, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, service.ErrInternal
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx, `UPDATE addresses SET reserved = false, used = true WHERE id = $1`, ref.AddressID)
	if err != nil {
		return nil, wrapPgErr(err, "Address", ref.AddressID)
	}
	if tag.RowsAffected() == 0 {
		return nil, fmt.Errorf("%w: Address %s not found", service.ErrNotFound, ref.AddressID)
	}

	const q = `
		INSERT INTO address_references (address_id, referrer_type, referrer_id, referrer_name, attached_at)
		VALUES ($1, $2, $3, $4, now())
		ON CONFLICT (address_id) DO UPDATE
		  SET referrer_type = EXCLUDED.referrer_type,
		      referrer_id   = EXCLUDED.referrer_id,
		      referrer_name = EXCLUDED.referrer_name,
		      attached_at   = now()
		RETURNING address_id, referrer_type, referrer_id, referrer_name, attached_at`
	var out domain.AddressReference
	if err := tx.QueryRow(ctx, q, ref.AddressID, ref.ReferrerType, ref.ReferrerID, ref.ReferrerName).
		Scan(&out.AddressID, &out.ReferrerType, &out.ReferrerID, &out.ReferrerName, &out.AttachedAt); err != nil {
		return nil, wrapPgErr(err, "Address", ref.AddressID)
	}
	if err := emitVPC(ctx, tx, "Address", ref.AddressID, "UPDATED", map[string]any{"id": ref.AddressID, "reserved": false, "used": true}); err != nil {
		return nil, service.ErrInternal
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, wrapPgErr(err, "Address", ref.AddressID)
	}
	return &out, nil
}

// ClearReference удаляет referrer-row адреса (no-op если нет) и выставляет
// addresses.used=false. ErrNotFound если address не существует.
func (r *AddressRepo) ClearReference(ctx context.Context, addressID string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return service.ErrInternal
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx, `UPDATE addresses SET used = false WHERE id = $1`, addressID)
	if err != nil {
		return wrapPgErr(err, "Address", addressID)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: Address %s not found", service.ErrNotFound, addressID)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM address_references WHERE address_id = $1`, addressID); err != nil {
		return wrapPgErr(err, "Address", addressID)
	}
	if err := emitVPC(ctx, tx, "Address", addressID, "UPDATED", map[string]any{"id": addressID, "used": false}); err != nil {
		return service.ErrInternal
	}
	if err := tx.Commit(ctx); err != nil {
		return wrapPgErr(err, "Address", addressID)
	}
	return nil
}

// GetReference возвращает referrer-row адреса. ErrNotFound если address
// не существует ИЛИ у него нет referrer'а.
func (r *AddressRepo) GetReference(ctx context.Context, addressID string) (*domain.AddressReference, error) {
	var out domain.AddressReference
	err := r.pool.QueryRow(ctx, `
		SELECT address_id, referrer_type, referrer_id, referrer_name, attached_at
		FROM address_references WHERE address_id = $1`, addressID).
		Scan(&out.AddressID, &out.ReferrerType, &out.ReferrerID, &out.ReferrerName, &out.AttachedAt)
	if err != nil {
		return nil, wrapPgErr(err, "Address", addressID)
	}
	return &out, nil
}

// ReferencesForAddresses возвращает referrer-row'ы для набора address-id.
func (r *AddressRepo) ReferencesForAddresses(ctx context.Context, addressIDs []string) (map[string]*domain.AddressReference, error) {
	out := make(map[string]*domain.AddressReference, len(addressIDs))
	if len(addressIDs) == 0 {
		return out, nil
	}
	rows, err := r.pool.Query(ctx, `
		SELECT address_id, referrer_type, referrer_id, referrer_name, attached_at
		FROM address_references WHERE address_id = ANY($1)`, addressIDs)
	if err != nil {
		return nil, wrapPgErr(err, "Address", "")
	}
	defer rows.Close()
	for rows.Next() {
		var ref domain.AddressReference
		if err := rows.Scan(&ref.AddressID, &ref.ReferrerType, &ref.ReferrerID, &ref.ReferrerName, &ref.AttachedAt); err != nil {
			return nil, wrapPgErr(err, "Address", "")
		}
		out[ref.AddressID] = &ref
	}
	if err := rows.Err(); err != nil {
		return nil, wrapPgErr(err, "Address", "")
	}
	return out, nil
}

// ---- scan helpers ----

func scanAddress(row scannable) (*domain.Address, error) {
	var a domain.Address
	var labelsJSON, extJSON, intJSON []byte
	var addrType, ipVersion int32

	err := row.Scan(
		&a.ID, &a.FolderID, &a.CreatedAt, &a.Name, &a.Description, &labelsJSON,
		&addrType, &ipVersion, &a.Reserved, &a.Used, &a.DeletionProtection,
		&extJSON, &intJSON,
	)
	if err != nil {
		return nil, err
	}
	a.Type = domain.AddressType(addrType)
	a.IpVersion = domain.IpVersion(ipVersion)

	if err := unmarshalJSONB(labelsJSON, &a.Labels, "Address.labels"); err != nil {
		return nil, err
	}
	if extJSON != nil {
		var ext domain.ExternalIpv4Spec
		if err := unmarshalJSONB(extJSON, &ext, "Address.external_ipv4"); err != nil {
			return nil, err
		}
		a.ExternalIpv4 = &ext
	}
	if intJSON != nil {
		var intSpec domain.InternalIpv4Spec
		if err := unmarshalJSONB(intJSON, &intSpec, "Address.internal_ipv4"); err != nil {
			return nil, err
		}
		a.InternalIpv4 = &intSpec
	}
	return &a, nil
}

func marshalExternalIPv4(e *domain.ExternalIpv4Spec) ([]byte, error) {
	if e == nil {
		return nil, nil
	}
	return marshalJSONB(e, "Address.external_ipv4")
}

func marshalInternalIPv4(i *domain.InternalIpv4Spec) ([]byte, error) {
	if i == nil {
		return nil, nil
	}
	return marshalJSONB(i, "Address.internal_ipv4")
}
