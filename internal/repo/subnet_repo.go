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

const subnetCols = `id, folder_id, network_id, zone_id, cidr_block, name, description,
	created_at, labels, status, generation, resource_version, observed_generation, status_last_transition_at, deleted_at`

// SubnetRepo реализует service.SubnetRepo.
type SubnetRepo struct {
	pool *pgxpool.Pool
}

// NewSubnetRepo создаёт SubnetRepo.
func NewSubnetRepo(pool *pgxpool.Pool) *SubnetRepo {
	return &SubnetRepo{pool: pool}
}

func (r *SubnetRepo) Get(ctx context.Context, id string) (*domain.Subnet, error) {
	uid, err := strToUUID(id)
	if err != nil {
		return nil, coreerrors.InvalidArgument().AddFieldViolation("id", "invalid uuid").Err()
	}
	row := r.pool.QueryRow(ctx,
		`SELECT `+subnetCols+` FROM subnets WHERE id = $1 AND deleted_at IS NULL`, uid)
	return scanSubnet(row)
}

func (r *SubnetRepo) GetByFolderAndName(ctx context.Context, folderID, name string) (*domain.Subnet, error) {
	fid, err := strToUUID(folderID)
	if err != nil {
		return nil, coreerrors.InvalidArgument().AddFieldViolation("folder_id", "invalid uuid").Err()
	}
	row := r.pool.QueryRow(ctx,
		`SELECT `+subnetCols+` FROM subnets WHERE folder_id = $1 AND name = $2 AND deleted_at IS NULL`,
		fid, name)
	return scanSubnet(row)
}

func (r *SubnetRepo) List(ctx context.Context, filter service.ListFilter) ([]domain.Subnet, string, error) {
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

	q := fmt.Sprintf(`SELECT `+subnetCols+` FROM subnets %s %s LIMIT $%d`, where, orderBy, argIdx)
	args = append(args, pageSize+1)

	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()

	var result []domain.Subnet
	for rows.Next() {
		s, serr := scanSubnetRow(rows)
		if serr != nil {
			return nil, "", serr
		}
		result = append(result, *s)
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

func (r *SubnetRepo) Create(ctx context.Context, s *domain.Subnet) error {
	uid, err := strToUUID(s.ID)
	if err != nil {
		return err
	}
	fid, err := strToUUID(s.FolderID)
	if err != nil {
		return err
	}
	nid, err := strToUUID(s.NetworkID)
	if err != nil {
		return err
	}
	statusStr := domain.SubnetStatusString[s.Status]
	if statusStr == "" {
		statusStr = "SUBNET_STATUS_PROVISIONING"
	}

	_, err = r.pool.Exec(ctx, `
		INSERT INTO subnets (id, folder_id, network_id, zone_id, cidr_block, name, description, labels, status, generation)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		uid, fid, nid, s.ZoneID, s.CIDRBlock, s.Name, s.Description, mapToJSON(s.Labels), statusStr, s.Generation,
	)
	return err
}

func (r *SubnetRepo) Update(ctx context.Context, s *domain.Subnet) error {
	uid, err := strToUUID(s.ID)
	if err != nil {
		return err
	}
	statusStr := domain.SubnetStatusString[s.Status]
	if statusStr == "" {
		statusStr = "SUBNET_STATUS_ACTIVE"
	}

	tag, err := r.pool.Exec(ctx, `
		UPDATE subnets
		SET name = $2, description = $3, labels = $4, status = $5, generation = $6,
		    resource_version = gen_random_uuid()::text,
		    status_last_transition_at = now()
		WHERE id = $1 AND deleted_at IS NULL`,
		uid, s.Name, s.Description, mapToJSON(s.Labels), statusStr, s.Generation,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return coreerrors.NotFound("Subnet", s.ID).Err()
	}
	return nil
}

func (r *SubnetRepo) SoftDelete(ctx context.Context, id string) error {
	uid, err := strToUUID(id)
	if err != nil {
		return err
	}
	tag, err := r.pool.Exec(ctx,
		`UPDATE subnets SET deleted_at = now(), status = 'SUBNET_STATUS_DELETING' WHERE id = $1 AND deleted_at IS NULL`,
		uid)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return coreerrors.NotFound("Subnet", id).Err()
	}
	return nil
}

func scanSubnet(row pgx.Row) (*domain.Subnet, error) {
	s, err := scanSubnetFields(row.Scan)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return s, err
}

func scanSubnetRow(rows pgx.Rows) (*domain.Subnet, error) {
	return scanSubnetFields(rows.Scan)
}

func scanSubnetFields(scanFn func(...any) error) (*domain.Subnet, error) {
	var (
		id, folderID, networkID, zoneID, cidrBlock, name, description string
		createdAt                                                       pgtype.Timestamptz
		labelsJSON                                                      []byte
		statusStr                                                       string
		generation                                                      int64
		resourceVersion                                                 string
		observedGeneration                                              int64
		statusLastTransition                                            pgtype.Timestamptz
		deletedAt                                                       pgtype.Timestamptz
	)
	err := scanFn(
		&id, &folderID, &networkID, &zoneID, &cidrBlock, &name, &description,
		&createdAt, &labelsJSON, &statusStr, &generation, &resourceVersion,
		&observedGeneration, &statusLastTransition, &deletedAt,
	)
	if err != nil {
		return nil, err
	}
	return &domain.Subnet{
		ID:                     id,
		FolderID:               folderID,
		NetworkID:              networkID,
		ZoneID:                 zoneID,
		CIDRBlock:              cidrBlock,
		Name:                   name,
		Description:            description,
		CreatedAt:              tsToTime(createdAt),
		Labels:                 jsonToMap(labelsJSON),
		Status:                 domain.ParseSubnetStatus(statusStr),
		Generation:             generation,
		ResourceVersion:        resourceVersion,
		ObservedGeneration:     observedGeneration,
		StatusLastTransitionAt: tsToTime(statusLastTransition),
		DeletedAt:              tsToTimePtr(deletedAt),
	}, nil
}
