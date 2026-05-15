package repo

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PRO-Robotech/kacho-corelib/filter"
	"github.com/PRO-Robotech/kacho-corelib/validate"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/service"
)

// Subnet — type-alias на domain.SubnetRecord (repo-entity с DB-managed
// CreatedAt). Имя `repo.Subnet` сохранено для читаемости call-site'ов
// (`*repo.Subnet` в service/handler-коде), а сама структура объявлена в
// `domain` чтобы её мог типизировать ещё и `internal/ports` без import-cycle.
// Wave 2 batch A (KAC-94), parity с repo.Network.
type Subnet = domain.SubnetRecord

// SubnetRepo — реализация service.SubnetRepo поверх pgxpool.
type SubnetRepo struct {
	pool *pgxpool.Pool
}

// NewSubnetRepo создаёт SubnetRepo.
func NewSubnetRepo(pool *pgxpool.Pool) *SubnetRepo {
	return &SubnetRepo{pool: pool}
}

const subnetCols = `id, folder_id, created_at, name, description, labels, network_id, zone_id, v4_cidr_blocks, v6_cidr_blocks, route_table_id, dhcp_options`

func (r *SubnetRepo) Get(ctx context.Context, id string) (*Subnet, error) {
	q := fmt.Sprintf(`SELECT %s FROM subnets WHERE id = $1`, subnetCols)
	row := r.pool.QueryRow(ctx, q, id)
	s, err := scanSubnet(row)
	if err != nil {
		return nil, wrapPgErr(err, "Subnet", id)
	}
	return s, nil
}

func (r *SubnetRepo) List(ctx context.Context, f service.SubnetFilter, p service.Pagination) ([]*Subnet, string, error) {
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
	q := fmt.Sprintf(`SELECT %s FROM subnets %s ORDER BY created_at ASC, id ASC LIMIT $%d`, subnetCols, where, argIdx)
	args = append(args, pageSize+1)

	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, "", wrapPgErr(err, "Subnet", "")
	}
	defer rows.Close()

	var result []*Subnet
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

// Insert вставляет Subnet. Принимает domain.Subnet (без CreatedAt — DB-managed
// через явный UTC-now для детерминированности тестов; source of truth — БД).
// Возвращает *Subnet (= *domain.SubnetRecord) с заполненным CreatedAt.
func (r *SubnetRepo) Insert(ctx context.Context, s *domain.Subnet) (*Subnet, error) {
	labelsJSON, err := marshalJSONB(domain.LabelsToMap(s.Labels), "Subnet.labels")
	if err != nil {
		return nil, err
	}
	dhcpJSON, err := marshalDhcp(s.DhcpOptions)
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
		INSERT INTO subnets (id, folder_id, created_at, name, description, labels, network_id, zone_id, v4_cidr_blocks, v6_cidr_blocks, route_table_id, dhcp_options)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		RETURNING ` + subnetCols

	row := tx.QueryRow(ctx, q,
		s.ID, s.FolderID, now, string(s.Name), string(s.Description), labelsJSON,
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
		return nil, wrapPgErr(err, "Subnet", string(s.Name))
	}
	if err := emitVPC(ctx, tx, "Subnet", result.ID, "CREATED", subnetPayload(result)); err != nil {
		return nil, service.ErrInternal
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, wrapPgErr(err, "Subnet", string(s.Name))
	}
	return result, nil
}

// Update обновляет mutable-поля Subnet. Принимает domain.Subnet (без CreatedAt).
func (r *SubnetRepo) Update(ctx context.Context, s *domain.Subnet) (*Subnet, error) {
	labelsJSON, err := marshalJSONB(domain.LabelsToMap(s.Labels), "Subnet.labels")
	if err != nil {
		return nil, err
	}
	dhcpJSON, err := marshalDhcp(s.DhcpOptions)
	if err != nil {
		return nil, err
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, service.ErrInternal
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// v4_cidr_blocks НЕ обновляется здесь — это immutable для Update path
	// (только service-layer SetCidrBlocks, см. AddCidrBlocks/RemoveCidrBlocks).
	// Defensive depth (TODO #27): убираем колонку из SET даже если service
	// слой пропустит модифицированный s.V4CidrBlocks по ошибке.
	const q = `
		UPDATE subnets SET name=$2, description=$3, labels=$4, route_table_id=$5, dhcp_options=$6
		WHERE id=$1
		RETURNING ` + subnetCols

	row := tx.QueryRow(ctx, q,
		s.ID, string(s.Name), string(s.Description), labelsJSON,
		nullableStr(s.RouteTableID), dhcpJSON,
	)
	result, err := scanSubnet(row)
	if err != nil {
		return nil, wrapPgErr(err, "Subnet", s.ID)
	}
	if err := emitVPC(ctx, tx, "Subnet", result.ID, "UPDATED", subnetPayload(result)); err != nil {
		return nil, service.ErrInternal
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, wrapPgErr(err, "Subnet", s.ID)
	}
	return result, nil
}

// SetCidrBlocks обновляет v4_cidr_blocks и v6_cidr_blocks у Subnet атомарно
// (для AddCidrBlocks/RemoveCidrBlocks). EXCLUDE constraint'ы
// subnets_no_overlap_v4 / subnets_no_overlap_v6 проверяют primary CIDR (array[1])
// каждого семейства на пересечение с другими подсетями той же сети.
func (r *SubnetRepo) SetCidrBlocks(ctx context.Context, id string, v4, v6 []string) (*Subnet, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, service.ErrInternal
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := fmt.Sprintf(`UPDATE subnets SET v4_cidr_blocks = $2, v6_cidr_blocks = $3 WHERE id = $1 RETURNING %s`, subnetCols)
	row := tx.QueryRow(ctx, q, id,
		pgtype.Array[string]{Elements: v4, Valid: true, Dims: []pgtype.ArrayDimension{{Length: int32(len(v4)), LowerBound: 1}}},
		pgtype.Array[string]{Elements: v6, Valid: true, Dims: []pgtype.ArrayDimension{{Length: int32(len(v6)), LowerBound: 1}}},
	)
	s, err := scanSubnet(row)
	if isExclusionViolation(err) {
		return nil, fmt.Errorf("%w: Subnet CIDRs can not overlap", service.ErrFailedPrecondition)
	}
	if err != nil {
		return nil, wrapPgErr(err, "Subnet", id)
	}
	if err := emitVPC(ctx, tx, "Subnet", s.ID, "UPDATED", subnetPayload(s)); err != nil {
		return nil, service.ErrInternal
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, wrapPgErr(err, "Subnet", id)
	}
	return s, nil
}

// SetZoneID меняет zone_id у Subnet (для Relocate).
func (r *SubnetRepo) SetZoneID(ctx context.Context, id, zoneID string) (*Subnet, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, service.ErrInternal
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := fmt.Sprintf(`UPDATE subnets SET zone_id = $2 WHERE id = $1 RETURNING %s`, subnetCols)
	row := tx.QueryRow(ctx, q, id, zoneID)
	s, err := scanSubnet(row)
	if err != nil {
		return nil, wrapPgErr(err, "Subnet", id)
	}
	if err := emitVPC(ctx, tx, "Subnet", s.ID, "UPDATED", subnetPayload(s)); err != nil {
		return nil, service.ErrInternal
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, wrapPgErr(err, "Subnet", id)
	}
	return s, nil
}

// AddressesBySubnet возвращает Address-ресурсы, привязанные к данному subnet
// через internal_ipv4.subnet_id ИЛИ internal_ipv6.subnet_id. Используется для
// ListUsedAddresses / ListBySubnet (см. AddressService) и для sync-precheck в
// SubnetService.Delete ("Subnet has allocated internal addresses") — поэтому
// предикат должен покрывать обе семьи (KAC-34: v6-only internal address тоже
// блокирует удаление своей подсети, как и v4).
func (r *SubnetRepo) AddressesBySubnet(ctx context.Context, subnetID string, p service.Pagination) ([]*Address, string, error) {
	pageSize, err := validate.PageSize("page_size", p.PageSize)
	if err != nil {
		return nil, "", err
	}
	args := []any{subnetID}
	argIdx := 2
	tokenCond := ""
	if p.PageToken != "" {
		ts, id, err := decodePageToken(p.PageToken)
		if err != nil {
			return nil, "", invalidPageTokenErr(err)
		}
		tokenCond = fmt.Sprintf(" AND (created_at, id) > ($%d, $%d)", argIdx, argIdx+1)
		args = append(args, ts, id)
		argIdx += 2
	}
	q := fmt.Sprintf(`SELECT %s FROM addresses
	  WHERE ((internal_ipv4 IS NOT NULL AND internal_ipv4->>'subnet_id' = $1)
	      OR (internal_ipv6 IS NOT NULL AND internal_ipv6->>'subnet_id' = $1))
	    %s
	  ORDER BY created_at ASC, id ASC
	  LIMIT $%d`, addressCols, tokenCond, argIdx)
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

// SetFolderID меняет folder_id у Subnet.
func (r *SubnetRepo) SetFolderID(ctx context.Context, id, folderID string) (*Subnet, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, service.ErrInternal
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := fmt.Sprintf(`UPDATE subnets SET folder_id = $2 WHERE id = $1 RETURNING %s`, subnetCols)
	row := tx.QueryRow(ctx, q, id, folderID)
	s, err := scanSubnet(row)
	if err != nil {
		return nil, wrapPgErr(err, "Subnet", id)
	}
	if err := emitVPC(ctx, tx, "Subnet", s.ID, "UPDATED", subnetPayload(s)); err != nil {
		return nil, service.ErrInternal
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, wrapPgErr(err, "Subnet", id)
	}
	return s, nil
}

func (r *SubnetRepo) Delete(ctx context.Context, id string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return service.ErrInternal
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx, `DELETE FROM subnets WHERE id = $1`, id)
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
	if err := emitVPC(ctx, tx, "Subnet", id, "DELETED", map[string]any{"id": id}); err != nil {
		return service.ErrInternal
	}
	if err := tx.Commit(ctx); err != nil {
		return wrapPgErr(err, "Subnet", id)
	}
	return nil
}

// ---- scan helpers ----

func scanSubnet(row scannable) (*Subnet, error) {
	var s Subnet
	var labelsJSON, dhcpJSON []byte
	var v4, v6 pgtype.Array[string]
	var routeTableID *string
	var name string
	var description string

	err := row.Scan(
		&s.ID, &s.FolderID, &s.CreatedAt, &name, &description, &labelsJSON,
		&s.NetworkID, &s.ZoneID, &v4, &v6, &routeTableID, &dhcpJSON,
	)
	if err != nil {
		return nil, err
	}
	s.Name = domain.RcNameVPC(name)
	s.Description = domain.RcDescription(description)
	var labels map[string]string
	if err := unmarshalJSONB(labelsJSON, &labels, "Subnet.labels"); err != nil {
		return nil, err
	}
	s.Labels = domain.LabelsFromMap(labels)
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
		if err := unmarshalJSONB(dhcpJSON, &d, "Subnet.dhcp_options"); err != nil {
			return nil, err
		}
		s.DhcpOptions = &d
	}
	return &s, nil
}

func marshalDhcp(d *domain.DhcpOptions) ([]byte, error) {
	if d == nil {
		return nil, nil
	}
	return marshalJSONB(d, "Subnet.dhcp_options")
}

func nullableStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
