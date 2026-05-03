package repo

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	coreerrors "github.com/PRO-Robotech/kacho-corelib/errors"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/service"
)

const addrCols = `id, folder_id, name, description, created_at, labels,
	address_type, zone_id, allocated_ipv4, status, deleted_at`

// AddressRepo реализует service.AddressRepo.
type AddressRepo struct {
	pool *pgxpool.Pool
}

// NewAddressRepo создаёт AddressRepo.
func NewAddressRepo(pool *pgxpool.Pool) *AddressRepo {
	return &AddressRepo{pool: pool}
}

func (r *AddressRepo) Get(ctx context.Context, id string) (*domain.Address, error) {
	uid, err := strToUUID(id)
	if err != nil {
		return nil, coreerrors.InvalidArgument().AddFieldViolation("id", "invalid uuid").Err()
	}
	row := r.pool.QueryRow(ctx,
		`SELECT `+addrCols+` FROM addresses WHERE id = $1 AND deleted_at IS NULL`, uid)
	return scanAddress(row)
}

func (r *AddressRepo) GetByFolderAndName(ctx context.Context, folderID, name string) (*domain.Address, error) {
	fid, err := strToUUID(folderID)
	if err != nil {
		return nil, coreerrors.InvalidArgument().AddFieldViolation("folder_id", "invalid uuid").Err()
	}
	row := r.pool.QueryRow(ctx,
		`SELECT `+addrCols+` FROM addresses WHERE folder_id = $1 AND name = $2 AND deleted_at IS NULL`,
		fid, name)
	return scanAddress(row)
}

func (r *AddressRepo) List(ctx context.Context, filter service.ListFilter) ([]domain.Address, string, error) {
	pageSize := filter.PageSize
	if pageSize <= 0 || pageSize > 1000 {
		pageSize = 50
	}

	args := []any{}
	conds := []string{"deleted_at IS NULL"}
	argIdx := 1

	if filter.FolderID != "" {
		fid, err := strToUUID(filter.FolderID)
		if err != nil {
			return nil, "", coreerrors.InvalidArgument().AddFieldViolation("folder_id", "invalid uuid").Err()
		}
		conds = append(conds, fmt.Sprintf("folder_id = $%d", argIdx))
		args = append(args, fid)
		argIdx++
	}

	if filter.PageToken != "" {
		cur, decErr := decodeNetworkPageToken(filter.PageToken)
		if decErr == nil {
			conds = append(conds, fmt.Sprintf("(created_at, id::text) > ($%d, $%d)", argIdx, argIdx+1))
			args = append(args, cur.CreatedAt, cur.ID)
			argIdx += 2
		}
	}

	where := "WHERE " + strings.Join(conds, " AND ")
	orderBy := buildOrderBy(filter.OrderBy)

	q := fmt.Sprintf(`SELECT `+addrCols+` FROM addresses %s %s LIMIT $%d`, where, orderBy, argIdx)
	args = append(args, pageSize+1)

	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()

	var result []domain.Address
	for rows.Next() {
		a, serr := scanAddressRow(rows)
		if serr != nil {
			return nil, "", serr
		}
		result = append(result, *a)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}

	var nextToken string
	if int64(len(result)) > pageSize {
		last := result[pageSize-1]
		nextToken = encodeNetworkPageToken(last.CreatedAt, last.ID)
		result = result[:pageSize]
	}
	return result, nextToken, nil
}

func (r *AddressRepo) Create(ctx context.Context, a *domain.Address) error {
	uid, err := strToUUID(a.ID)
	if err != nil {
		return err
	}
	fid, err := strToUUID(a.FolderID)
	if err != nil {
		return err
	}
	statusStr := domain.AddressStatusString[a.Status]
	if statusStr == "" {
		statusStr = "ADDRESS_STATUS_RESERVED"
	}

	_, err = r.pool.Exec(ctx, `
		INSERT INTO addresses (id, folder_id, name, description, labels, address_type, zone_id, allocated_ipv4, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		uid, fid, a.Name, a.Description, mapToJSON(a.Labels),
		a.AddressType, a.ZoneID, a.AllocatedIPv4, statusStr,
	)
	return err
}

func (r *AddressRepo) Update(ctx context.Context, a *domain.Address) error {
	uid, err := strToUUID(a.ID)
	if err != nil {
		return err
	}
	statusStr := domain.AddressStatusString[a.Status]
	if statusStr == "" {
		statusStr = "ADDRESS_STATUS_RESERVED"
	}

	tag, err := r.pool.Exec(ctx, `
		UPDATE addresses
		SET name = $2, description = $3, labels = $4, status = $5
		WHERE id = $1 AND deleted_at IS NULL`,
		uid, a.Name, a.Description, mapToJSON(a.Labels), statusStr,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return coreerrors.NotFound("Address", a.ID).Err()
	}
	return nil
}

func (r *AddressRepo) SoftDelete(ctx context.Context, id string) error {
	uid, err := strToUUID(id)
	if err != nil {
		return err
	}
	tag, err := r.pool.Exec(ctx,
		`UPDATE addresses SET deleted_at = now(), status = 'ADDRESS_STATUS_RELEASED' WHERE id = $1 AND deleted_at IS NULL`,
		uid)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return coreerrors.NotFound("Address", id).Err()
	}
	return nil
}

// ---- scan helpers ----

func scanAddress(row pgx.Row) (*domain.Address, error) {
	a, err := scanAddressFields(row.Scan)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return a, err
}

func scanAddressRow(rows pgx.Rows) (*domain.Address, error) {
	return scanAddressFields(rows.Scan)
}

func scanAddressFields(scanFn func(...any) error) (*domain.Address, error) {
	var (
		id, folderID, name, description string
		createdAt                        pgtype.Timestamptz
		labelsJSON                       []byte
		addressType, zoneID              string
		allocatedIPv4                    *string
		statusStr                        string
		deletedAt                        pgtype.Timestamptz
	)
	err := scanFn(
		&id, &folderID, &name, &description, &createdAt, &labelsJSON,
		&addressType, &zoneID, &allocatedIPv4, &statusStr, &deletedAt,
	)
	if err != nil {
		return nil, err
	}
	ip := ""
	if allocatedIPv4 != nil {
		ip = *allocatedIPv4
	}
	return &domain.Address{
		ID:            id,
		FolderID:      folderID,
		Name:          name,
		Description:   description,
		CreatedAt:     tsToTime(createdAt),
		Labels:        jsonToMap(labelsJSON),
		AddressType:   addressType,
		ZoneID:        zoneID,
		AllocatedIPv4: ip,
		Status:        domain.ParseAddressStatus(statusStr),
		DeletedAt:     tsToTimePtr(deletedAt),
	}, nil
}
