package repo

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/PRO-Robotech/kacho-corelib/validate"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/service"
)

// SubnetRepo — реализация service.SubnetRepo поверх pgxpool.
type SubnetRepo struct {
	pool *pgxpool.Pool
}

// NewSubnetRepo создаёт SubnetRepo.
func NewSubnetRepo(pool *pgxpool.Pool) *SubnetRepo {
	return &SubnetRepo{pool: pool}
}

const subnetCols = `id, folder_id, created_at, name, description, labels, network_id, zone_id, v4_cidr_blocks, v6_cidr_blocks, route_table_id, dhcp_options`

func (r *SubnetRepo) Get(ctx context.Context, id string) (*domain.Subnet, error) {
	q := fmt.Sprintf(`SELECT %s FROM subnets WHERE id = $1`, subnetCols)
	row := r.pool.QueryRow(ctx, q, id)
	s, err := scanSubnet(row)
	if err != nil {
		return nil, wrapPgErr(err, "Subnet", id)
	}
	return s, nil
}

func (r *SubnetRepo) List(ctx context.Context, f service.SubnetFilter, p service.Pagination) ([]*domain.Subnet, string, error) {
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
	if f.NetworkID != "" {
		conditions = append(conditions, fmt.Sprintf("network_id = $%d", argIdx))
		args = append(args, f.NetworkID)
		argIdx++
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
	q := fmt.Sprintf(`SELECT %s FROM subnets %s ORDER BY created_at ASC, id ASC LIMIT $%d`, subnetCols, where, argIdx)
	args = append(args, pageSize+1)

	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, "", wrapPgErr(err, "Subnet", "")
	}
	defer rows.Close()

	var result []*domain.Subnet
	for rows.Next() {
		s, err := scanSubnet(rows)
		if err != nil {
			return nil, "", wrapPgErr(err, "Subnet", "")
		}
		result = append(result, s)
	}
	if err := rows.Err(); err != nil {
		return nil, "", wrapPgErr(err, "Subnet", "")
	}

	var nextToken string
	if int64(len(result)) > pageSize {
		last := result[pageSize-1]
		nextToken = encodePageToken(last.CreatedAt, last.ID)
		result = result[:pageSize]
	}
	return result, nextToken, nil
}

func (r *SubnetRepo) Insert(ctx context.Context, s *domain.Subnet) (*domain.Subnet, error) {
	labelsJSON, _ := json.Marshal(s.Labels)
	dhcpJSON := marshalDhcp(s.DhcpOptions)

	const q = `
		INSERT INTO subnets (id, folder_id, created_at, name, description, labels, network_id, zone_id, v4_cidr_blocks, v6_cidr_blocks, route_table_id, dhcp_options)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		RETURNING ` + subnetCols

	row := r.pool.QueryRow(ctx, q,
		s.ID, s.FolderID, s.CreatedAt, s.Name, s.Description, labelsJSON,
		s.NetworkID, s.ZoneID,
		pgtype.Array[string]{Elements: s.V4CidrBlocks, Valid: true, Dims: []pgtype.ArrayDimension{{Length: int32(len(s.V4CidrBlocks)), LowerBound: 1}}},
		pgtype.Array[string]{Elements: s.V6CidrBlocks, Valid: true, Dims: []pgtype.ArrayDimension{{Length: int32(len(s.V6CidrBlocks)), LowerBound: 1}}},
		nullableStr(s.RouteTableID), dhcpJSON,
	)
	result, err := scanSubnet(row)
	if isExclusionViolation(err) {
		// SU-CIDR-OVERLAP race-protection: service checkCIDRDisjoint могла
		// пропустить параллельный Create, но EXCLUDE constraint в БД его
		// поймал. Маппим на FailedPrecondition (verbatim YC: code:9 для
		// "Subnet CIDRs can not overlap"). См. YC-DIFF-CIDR-OVERLAP-CODE.md
		// и YC-DIFF-CIDR-ERROR-SHAPE.md (text — verbatim YC).
		return nil, fmt.Errorf("%w: Subnet CIDRs can not overlap",
			service.ErrFailedPrecondition)
	}
	if err != nil {
		return nil, wrapPgErr(err, "Subnet", s.Name)
	}
	return result, nil
}

func (r *SubnetRepo) Update(ctx context.Context, s *domain.Subnet) (*domain.Subnet, error) {
	labelsJSON, _ := json.Marshal(s.Labels)
	dhcpJSON := marshalDhcp(s.DhcpOptions)

	const q = `
		UPDATE subnets SET name=$2, description=$3, labels=$4, v4_cidr_blocks=$5, route_table_id=$6, dhcp_options=$7
		WHERE id=$1
		RETURNING ` + subnetCols

	row := r.pool.QueryRow(ctx, q,
		s.ID, s.Name, s.Description, labelsJSON,
		pgtype.Array[string]{Elements: s.V4CidrBlocks, Valid: true, Dims: []pgtype.ArrayDimension{{Length: int32(len(s.V4CidrBlocks)), LowerBound: 1}}},
		nullableStr(s.RouteTableID), dhcpJSON,
	)
	result, err := scanSubnet(row)
	if err != nil {
		return nil, wrapPgErr(err, "Subnet", s.ID)
	}
	return result, nil
}

// SetFolderID меняет folder_id у Subnet.
func (r *SubnetRepo) SetFolderID(ctx context.Context, id, folderID string) (*domain.Subnet, error) {
	q := fmt.Sprintf(`UPDATE subnets SET folder_id = $2 WHERE id = $1 RETURNING %s`, subnetCols)
	row := r.pool.QueryRow(ctx, q, id, folderID)
	s, err := scanSubnet(row)
	if err != nil {
		return nil, wrapPgErr(err, "Subnet", id)
	}
	return s, nil
}

func (r *SubnetRepo) Delete(ctx context.Context, id string) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM subnets WHERE id = $1`, id)
	if err != nil {
		if isFKViolation(err) {
			return fmt.Errorf("%w: subnet has dependent resources", service.ErrFailedPrecondition)
		}
		// 22P02 → InvalidArgument "invalid subnet id 'X'" (verbatim YC).
		return wrapPgErr(err, "Subnet", id)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: Subnet %s not found", service.ErrNotFound, id)
	}
	return nil
}

// ---- scan helpers ----

func scanSubnet(row scannable) (*domain.Subnet, error) {
	var s domain.Subnet
	var labelsJSON, dhcpJSON []byte
	var v4, v6 pgtype.Array[string]
	var routeTableID *string

	err := row.Scan(
		&s.ID, &s.FolderID, &s.CreatedAt, &s.Name, &s.Description, &labelsJSON,
		&s.NetworkID, &s.ZoneID, &v4, &v6, &routeTableID, &dhcpJSON,
	)
	if err != nil {
		return nil, err
	}
	if labelsJSON != nil {
		_ = json.Unmarshal(labelsJSON, &s.Labels)
	}
	if v4.Valid {
		s.V4CidrBlocks = v4.Elements
	}
	if v6.Valid {
		s.V6CidrBlocks = v6.Elements
	}
	if routeTableID != nil {
		s.RouteTableID = *routeTableID
	}
	if dhcpJSON != nil {
		var d domain.DhcpOptions
		if err := json.Unmarshal(dhcpJSON, &d); err == nil {
			s.DhcpOptions = &d
		}
	}
	return &s, nil
}

func marshalDhcp(d *domain.DhcpOptions) []byte {
	if d == nil {
		return nil
	}
	b, _ := json.Marshal(d)
	return b
}

func nullableStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
