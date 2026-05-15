package repo

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PRO-Robotech/kacho-corelib/filter"
	"github.com/PRO-Robotech/kacho-corelib/validate"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/ports"
	"github.com/PRO-Robotech/kacho-vpc/internal/service"
)

// Address — type-alias на domain.AddressRecord (repo-entity с DB-managed
// CreatedAt). Wave 2 batch A (KAC-94), parity с repo.Network.
type Address = domain.AddressRecord

// AddressRepo — реализация service.AddressRepo поверх pgxpool.
type AddressRepo struct {
	pool *pgxpool.Pool
}

// NewAddressRepo создаёт AddressRepo.
func NewAddressRepo(pool *pgxpool.Pool) *AddressRepo {
	return &AddressRepo{pool: pool}
}

const addressCols = `id, folder_id, created_at, name, description, labels, addr_type, ip_version, reserved, used, deletion_protection, external_ipv4, internal_ipv4, internal_ipv6, external_ipv6`

func (r *AddressRepo) Get(ctx context.Context, id string) (*Address, error) {
	q := fmt.Sprintf(`SELECT %s FROM addresses WHERE id = $1`, addressCols)
	row := r.pool.QueryRow(ctx, q, id)
	a, err := scanAddress(row)
	if err != nil {
		return nil, wrapPgErr(err, "Address", id)
	}
	return a, nil
}

func (r *AddressRepo) List(ctx context.Context, f service.AddressFilter, p service.Pagination) ([]*Address, string, error) {
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
	if f.SubnetID != "" {
		conditions = append(conditions, fmt.Sprintf("(internal_ipv4->>'subnet_id' = $%d OR internal_ipv6->>'subnet_id' = $%d)", argIdx, argIdx))
		args = append(args, f.SubnetID)
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

	var result []*Address
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

// Insert вставляет Address. Принимает domain.Address (без CreatedAt — DB-managed).
// Возвращает *Address (= *domain.AddressRecord) с заполненным CreatedAt.
func (r *AddressRepo) Insert(ctx context.Context, a *domain.Address) (*Address, error) {
	labelsJSON, err := marshalJSONB(domain.LabelsToMap(a.Labels), "Address.labels")
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
	int6JSON, err := marshalInternalIPv6(a.InternalIpv6)
	if err != nil {
		return nil, err
	}
	ext6JSON, err := marshalExternalIPv6(a.ExternalIpv6)
	if err != nil {
		return nil, err
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, service.ErrInternal
	}
	defer func() { _ = tx.Rollback(ctx) }()

	now := time.Now().UTC()
	const q = `
		INSERT INTO addresses (id, folder_id, created_at, name, description, labels, addr_type, ip_version, reserved, used, deletion_protection, external_ipv4, internal_ipv4, internal_ipv6, external_ipv6)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
		RETURNING ` + addressCols

	row := tx.QueryRow(ctx, q,
		a.ID, a.FolderID, now, string(a.Name), string(a.Description), labelsJSON,
		int32(a.Type), int32(a.IpVersion), a.Reserved, a.Used, a.DeletionProtection,
		extJSON, intJSON, int6JSON, ext6JSON,
	)
	result, err := scanAddress(row)
	if err != nil {
		return nil, wrapPgErr(err, "Address", string(a.Name))
	}
	if err := emitVPC(ctx, tx, "Address", result.ID, "CREATED", addressPayload(result)); err != nil {
		return nil, service.ErrInternal
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, wrapPgErr(err, "Address", string(a.Name))
	}
	return result, nil
}

// Update обновляет mutable-поля Address. Принимает domain.Address (без CreatedAt).
func (r *AddressRepo) Update(ctx context.Context, a *domain.Address) (*Address, error) {
	labelsJSON, err := marshalJSONB(domain.LabelsToMap(a.Labels), "Address.labels")
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
		a.ID, string(a.Name), string(a.Description), labelsJSON, a.Reserved, a.Used, a.DeletionProtection,
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
func (r *AddressRepo) SetIPSpec(ctx context.Context, id string, ext *domain.ExternalIpv4Spec, intn *domain.InternalIpv4Spec) (*Address, error) {
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

// SetInternalIPv6 атомарно обновляет internal_ipv6 JSONB-spec + emit outbox-event.
// Используется AllocateInternalIPv6 (random-pick + retry на UNIQUE-violation —
// constraint addresses_internal_subnet_ipv6_uniq → ErrAlreadyExists через wrapPgErr).
func (r *AddressRepo) SetInternalIPv6(ctx context.Context, id string, spec *domain.InternalIpv6Spec) (*Address, error) {
	if spec == nil {
		return r.Get(ctx, id)
	}
	int6JSON, err := marshalJSONB(spec, "Address.internal_ipv6")
	if err != nil {
		return nil, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, service.ErrInternal
	}
	defer func() { _ = tx.Rollback(ctx) }()

	row := tx.QueryRow(ctx, `UPDATE addresses SET internal_ipv6 = $2::jsonb WHERE id = $1 RETURNING `+addressCols, id, int6JSON)
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
func (r *AddressRepo) SetFolderID(ctx context.Context, id, folderID string) (*Address, error) {
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
func (r *AddressRepo) GetByValue(ctx context.Context, externalIP, internalIP, subnetID string) (*Address, error) {
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
//
// KAC-88 (G1 из audit KAC-84) — атомарный single-statement CAS на upsert:
// `ON CONFLICT (address_id) DO UPDATE … WHERE address_references.referrer_id
// = EXCLUDED.referrer_id`. Конфликт по адресу с ЧУЖИМ referrer'ом ⇒ DO UPDATE
// не применяется (предикат false), RETURNING отдаёт 0 строк, что pgx маппит в
// pgx.ErrNoRows → service.ErrFailedPrecondition.
//
// Это устраняет TOCTOU из service-слоя
// (NetworkInterfaceService.validateAddressRef делал Get → check used=false →
// SetReference), который раньше допускал second-writer-wins overwrite —
// точный parity-case с инцидентом 2026-05-14 (KAC-52, NIC-attach race).
//
// Idempotent re-attach к тому же referrer проходит (CAS matches → row пишется
// заново с новым referrer_name/attached_at, RETURNING возвращает row).
//
// См. workspace CLAUDE.md §«Within-service refs — DB-уровень обязателен»
// (запрет #10) — шаблон «атомарный single-statement CAS на одной row».
// Зеркало NetworkInterfaceRepo.SetUsedBy (network_interface_repo.go:262).
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
		  WHERE address_references.referrer_id = EXCLUDED.referrer_id
		RETURNING address_id, referrer_type, referrer_id, referrer_name, attached_at`
	var out domain.AddressReference
	if err := tx.QueryRow(ctx, q, ref.AddressID, ref.ReferrerType, ref.ReferrerID, ref.ReferrerName).
		Scan(&out.AddressID, &out.ReferrerType, &out.ReferrerID, &out.ReferrerName, &out.AttachedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// CAS failed — address already referenced by another resource.
			return nil, fmt.Errorf("%w: address already referenced by another resource", service.ErrFailedPrecondition)
		}
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
//
// KAC-88 (G1 из audit KAC-84) — атомарный single-statement CAS на upsert
// (parity с SetReference, см. её doc): попытка перепривязать адрес к ЧУЖОМУ
// referrer'у → service.ErrFailedPrecondition. Idempotent re-mark к тому же
// referrer проходит.
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
		  WHERE address_references.referrer_id = EXCLUDED.referrer_id
		RETURNING address_id, referrer_type, referrer_id, referrer_name, attached_at`
	var out domain.AddressReference
	if err := tx.QueryRow(ctx, q, ref.AddressID, ref.ReferrerType, ref.ReferrerID, ref.ReferrerName).
		Scan(&out.AddressID, &out.ReferrerType, &out.ReferrerID, &out.ReferrerName, &out.AttachedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: address already referenced by another resource", service.ErrFailedPrecondition)
		}
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

func scanAddress(row scannable) (*Address, error) {
	var a Address
	var labelsJSON, extJSON, intJSON, int6JSON, ext6JSON []byte
	var addrType, ipVersion int32
	var name string
	var description string

	err := row.Scan(
		&a.ID, &a.FolderID, &a.CreatedAt, &name, &description, &labelsJSON,
		&addrType, &ipVersion, &a.Reserved, &a.Used, &a.DeletionProtection,
		&extJSON, &intJSON, &int6JSON, &ext6JSON,
	)
	if err != nil {
		return nil, err
	}
	a.Name = domain.RcNameVPC(name)
	a.Description = domain.RcDescription(description)
	a.Type = domain.AddressType(addrType)
	a.IpVersion = domain.IpVersion(ipVersion)

	var labels map[string]string
	if err := unmarshalJSONB(labelsJSON, &labels, "Address.labels"); err != nil {
		return nil, err
	}
	a.Labels = domain.LabelsFromMap(labels)
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
	if int6JSON != nil {
		var int6Spec domain.InternalIpv6Spec
		if err := unmarshalJSONB(int6JSON, &int6Spec, "Address.internal_ipv6"); err != nil {
			return nil, err
		}
		a.InternalIpv6 = &int6Spec
	}
	if ext6JSON != nil {
		var ext6 domain.ExternalIpv6Spec
		if err := unmarshalJSONB(ext6JSON, &ext6, "Address.external_ipv6"); err != nil {
			return nil, err
		}
		a.ExternalIpv6 = &ext6
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

func marshalInternalIPv6(i *domain.InternalIpv6Spec) ([]byte, error) {
	if i == nil {
		return nil, nil
	}
	return marshalJSONB(i, "Address.internal_ipv6")
}

func marshalExternalIPv6(e *domain.ExternalIpv6Spec) ([]byte, error) {
	if e == nil {
		return nil, nil
	}
	return marshalJSONB(e, "Address.external_ipv6")
}

// ErrPoolExhausted — address_pool_free_ips empty for the given pool.
// Service-layer maps to gRPC FailedPrecondition. Re-exported from
// `internal/ports` so service-side `errors.Is(err, service.ErrPoolExhausted)`
// matches the same error-value the repo returns.
var ErrPoolExhausted = ports.ErrPoolExhausted

// allocateFromFreelistSQL — single-statement atomic IP allocation. SKIP LOCKED
// lets parallel workers grab different freelist rows without contention.
const allocateFromFreelistSQL = `
WITH picked AS (
    SELECT ip FROM address_pool_free_ips
    WHERE pool_id = $1
    ORDER BY ip
    LIMIT 1 FOR UPDATE SKIP LOCKED
), removed AS (
    DELETE FROM address_pool_free_ips f
    USING picked p
    WHERE f.pool_id = $1 AND f.ip = p.ip
    RETURNING f.ip
)
UPDATE addresses a
SET external_ipv4 = jsonb_set(
    jsonb_set(COALESCE(a.external_ipv4, '{}'::jsonb), '{address}', to_jsonb(host(r.ip))),
    '{address_pool_id}', to_jsonb($1::text)
)
FROM removed r
WHERE a.id = $2
RETURNING host(r.ip)::text;
`

// AllocateIPFromFreelist atomically:
//  1. pops one free IP from address_pool_free_ips for pool_id (SKIP LOCKED),
//  2. writes it to addresses.external_ipv4{address, address_pool_id} for address_id.
//
// Returns ErrPoolExhausted if the freelist for pool_id is empty.
func (r *AddressRepo) AllocateIPFromFreelist(ctx context.Context, poolID, addressID string) (string, error) {
	var ip string
	err := r.pool.QueryRow(ctx, allocateFromFreelistSQL, poolID, addressID).Scan(&ip)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrPoolExhausted
	}
	if err != nil {
		return "", fmt.Errorf("allocate from freelist: %w", err)
	}
	return ip, nil
}

// ReturnIPToFreelist puts an IP back into the pool's freelist. Idempotent:
// ON CONFLICT DO NOTHING. Called from AddressService.Delete (or compensation
// on allocation failure downstream) to release a previously-allocated IP.
func (r *AddressRepo) ReturnIPToFreelist(ctx context.Context, poolID, ip string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO address_pool_free_ips (pool_id, ip)
		VALUES ($1, $2::inet)
		ON CONFLICT (pool_id, ip) DO NOTHING
	`, poolID, ip)
	if err != nil {
		return fmt.Errorf("return ip to freelist: %w", err)
	}
	return nil
}
