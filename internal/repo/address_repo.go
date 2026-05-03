package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

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
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, service.ErrNotFound
	}
	return a, err
}

func (r *AddressRepo) List(ctx context.Context, f service.AddressFilter, p service.Pagination) ([]*domain.Address, string, error) {
	pageSize := p.PageSize
	if pageSize <= 0 || pageSize > 1000 {
		pageSize = 50
	}

	args := []any{}
	conditions := []string{}
	argIdx := 1

	if f.FolderID != "" {
		conditions = append(conditions, fmt.Sprintf("folder_id = $%d", argIdx))
		args = append(args, f.FolderID)
		argIdx++
	}
	if p.PageToken != "" {
		ts, id, err := decodePageToken(p.PageToken)
		if err != nil {
			return nil, "", fmt.Errorf("invalid page_token: %w", err)
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
		return nil, "", err
	}
	defer rows.Close()

	var result []*domain.Address
	for rows.Next() {
		a, err := scanAddress(rows)
		if err != nil {
			return nil, "", err
		}
		result = append(result, a)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
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
	labelsJSON, _ := json.Marshal(a.Labels)
	extJSON := marshalExternalIPv4(a.ExternalIpv4)
	intJSON := marshalInternalIPv4(a.InternalIpv4)

	const q = `
		INSERT INTO addresses (id, folder_id, created_at, name, description, labels, addr_type, ip_version, reserved, used, deletion_protection, external_ipv4, internal_ipv4)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		RETURNING ` + addressCols

	row := r.pool.QueryRow(ctx, q,
		a.ID, a.FolderID, a.CreatedAt, a.Name, a.Description, labelsJSON,
		int32(a.Type), int32(a.IpVersion), a.Reserved, a.Used, a.DeletionProtection,
		extJSON, intJSON,
	)
	return scanAddress(row)
}

func (r *AddressRepo) Update(ctx context.Context, a *domain.Address) (*domain.Address, error) {
	labelsJSON, _ := json.Marshal(a.Labels)

	const q = `
		UPDATE addresses SET name=$2, description=$3, labels=$4, reserved=$5, used=$6, deletion_protection=$7
		WHERE id=$1
		RETURNING ` + addressCols

	row := r.pool.QueryRow(ctx, q,
		a.ID, a.Name, a.Description, labelsJSON, a.Reserved, a.Used, a.DeletionProtection,
	)
	result, err := scanAddress(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, service.ErrNotFound
	}
	return result, err
}

func (r *AddressRepo) Delete(ctx context.Context, id string) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM addresses WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return service.ErrNotFound
	}
	return nil
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
		return false, err
	}
	return count > 0, nil
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

	if labelsJSON != nil {
		_ = json.Unmarshal(labelsJSON, &a.Labels)
	}
	if extJSON != nil {
		var ext domain.ExternalIpv4Spec
		if err := json.Unmarshal(extJSON, &ext); err == nil {
			a.ExternalIpv4 = &ext
		}
	}
	if intJSON != nil {
		var intSpec domain.InternalIpv4Spec
		if err := json.Unmarshal(intJSON, &intSpec); err == nil {
			a.InternalIpv4 = &intSpec
		}
	}
	return &a, nil
}

func marshalExternalIPv4(e *domain.ExternalIpv4Spec) []byte {
	if e == nil {
		return nil
	}
	b, _ := json.Marshal(e)
	return b
}

func marshalInternalIPv4(i *domain.InternalIpv4Spec) []byte {
	if i == nil {
		return nil
	}
	b, _ := json.Marshal(i)
	return b
}
