package pg

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/PRO-Robotech/kacho-corelib/filter"
	"github.com/PRO-Robotech/kacho-corelib/validate"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/helpers"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// subnetReader — Get/List/AddressesBySubnet поверх произвольной pgx.Tx
// (read-only или RW). Не имеет своего state кроме tx.
//
// Wave 5 replicate (KAC-94, skill evgeniy §6 G.1-G.7): Subnet переезжает на
// CQRS-Repository вслед за Network/SG. SQL/scan-семантика — parity с legacy
// `*repo.SubnetRepo` (helpers экспортированы как shim'ы — `helpers.SubnetCols` /
// `helpers.ScanSubnet` / `helpers.IsExclusionViolation` / `helpers.MarshalDhcp` /
// `helpers.AddressCols` / `helpers.ScanAddress`).
type subnetReader struct {
	tx pgx.Tx
}

// Get — verbatim YC: well-formed-but-absent → NotFound с "Subnet <id> not found".
func (r *subnetReader) Get(ctx context.Context, id string) (*kacho.SubnetRecord, error) {
	q := fmt.Sprintf(`SELECT %s FROM subnets WHERE id = $1`, helpers.SubnetCols)
	row := r.tx.QueryRow(ctx, q, id)
	s, err := helpers.ScanSubnet(row)
	if err != nil {
		return nil, helpers.WrapPgErr(err, "Subnet", id)
	}
	return s, nil
}

// List — cursor-based pagination + filter.Parse (YC-syntax). Логика идентична
// legacy `*repo.SubnetRepo.List`.
func (r *subnetReader) List(ctx context.Context, f kacho.SubnetFilter, p kacho.Pagination) ([]*kacho.SubnetRecord, string, error) {
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
	q := fmt.Sprintf(`SELECT %s FROM subnets %s ORDER BY created_at ASC, id ASC LIMIT $%d`, helpers.SubnetCols, where, argIdx)
	args = append(args, pageSize+1)

	rows, err := r.tx.Query(ctx, q, args...)
	if err != nil {
		return nil, "", helpers.WrapPgErr(err, "Subnet", "")
	}
	defer rows.Close()

	var result []*kacho.SubnetRecord
	for rows.Next() {
		s, err := helpers.ScanSubnet(rows)
		if err != nil {
			return nil, "", helpers.WrapPgErr(err, "Subnet", "")
		}
		result = append(result, s)
	}
	if err := rows.Err(); err != nil {
		return nil, "", helpers.WrapPgErr(err, "Subnet", "")
	}

	var nextToken string
	if int64(len(result)) > pageSize {
		last := result[pageSize-1]
		nextToken = helpers.EncodePageToken(last.CreatedAt, last.ID)
		result = result[:pageSize]
	}
	return result, nextToken, nil
}

// AddressesBySubnet — Address-ресурсы, привязанные к subnet через
// internal_ipv4.subnet_id ИЛИ internal_ipv6.subnet_id (KAC-34 family-agnostic).
// Используется ListUsedAddresses и SubnetService.Delete (sync precheck) — поэтому
// предикат покрывает обе семьи. Парность с legacy `*repo.SubnetRepo.AddressesBySubnet`.
func (r *subnetReader) AddressesBySubnet(ctx context.Context, subnetID string, p kacho.Pagination) ([]*kacho.AddressRecord, string, error) {
	pageSize, err := validate.PageSize("page_size", p.PageSize)
	if err != nil {
		return nil, "", err
	}
	args := []any{subnetID}
	argIdx := 2
	tokenCond := ""
	if p.PageToken != "" {
		ts, id, derr := helpers.DecodePageToken(p.PageToken)
		if derr != nil {
			return nil, "", helpers.InvalidPageTokenErr(derr)
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
	  LIMIT $%d`, helpers.AddressCols, tokenCond, argIdx)
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

// subnetWriter — DML над subnets через writer-TX. Embeds subnetReader
// (G.2 — writer видит свои writes: Get/List/AddressesBySubnet доступны после
// Insert/Update в рамках той же TX).
//
// Особенность CQRS: writer **НЕ** emit'ит outbox самостоятельно. После
// успешного DML caller (use-case) вызывает `RepositoryWriter.Outbox().Emit(...)`
// — это явная точка G.5, видно из use-case-кода что происходит outbox-write.
type subnetWriter struct {
	subnetReader
	emitter kacho.OutboxEmitter // не используется напрямую; держим для composability с networkWriter
}

// Insert — INSERT subnets RETURNING. CreatedAt здесь явно проставляется в UTC
// (как в legacy-репо), хотя БД-колонка имеет DEFAULT now() — это нужно для
// детерминированности тестов.
//
// SU-CIDR-OVERLAP race-protection: service checkCIDRDisjoint могла пропустить
// параллельный Create, но EXCLUDE constraint в БД его поймал. Маппим
// SQLSTATE 23P01 на ErrFailedPrecondition (verbatim YC: code:9 "Subnet CIDRs
// can not overlap"). См. YC-DIFF-CIDR-OVERLAP-CODE.md / YC-DIFF-CIDR-ERROR-SHAPE.md.
//
// outbox-write — не здесь, а в use-case'е через `writer.Outbox().Emit(...)`.
func (w *subnetWriter) Insert(ctx context.Context, s *domain.Subnet) (*kacho.SubnetRecord, error) {
	labelsJSON, err := helpers.MarshalJSONB(domain.LabelsToMap(s.Labels), "Subnet.labels")
	if err != nil {
		return nil, err
	}
	dhcpJSON, err := helpers.MarshalDhcp(s.DhcpOptions)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	q := fmt.Sprintf(`
		INSERT INTO subnets (id, project_id, created_at, name, description, labels, network_id, zone_id, v4_cidr_blocks, v6_cidr_blocks, route_table_id, dhcp_options)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		RETURNING %s`, helpers.SubnetCols)

	row := w.tx.QueryRow(ctx, q,
		s.ID, s.ProjectID, now, string(s.Name), string(s.Description), labelsJSON,
		s.NetworkID, s.ZoneID,
		pgtype.Array[string]{Elements: s.V4CidrBlocks, Valid: true, Dims: []pgtype.ArrayDimension{{Length: int32(len(s.V4CidrBlocks)), LowerBound: 1}}},
		pgtype.Array[string]{Elements: s.V6CidrBlocks, Valid: true, Dims: []pgtype.ArrayDimension{{Length: int32(len(s.V6CidrBlocks)), LowerBound: 1}}},
		helpers.NullableStr(s.RouteTableID), dhcpJSON,
	)
	result, err := helpers.ScanSubnet(row)
	if helpers.IsExclusionViolation(err) {
		return nil, fmt.Errorf("%w: Subnet CIDRs can not overlap", helpers.ErrFailedPrecondition)
	}
	if err != nil {
		return nil, helpers.WrapPgErr(err, "Subnet", string(s.Name))
	}
	return result, nil
}

// Update — UPDATE subnets RETURNING. Мутирует name/description/labels/
// route_table_id/dhcp_options. v4_cidr_blocks НЕ обновляется здесь (immutable
// для Update path — verbatim YC soft-immutable; defensive depth — даже если
// service слой пропустит модифицированный s.V4CidrBlocks, репо его не
// перезапишет). Реальное изменение CIDR — через SetCidrBlocks (AddCidrBlocks/
// RemoveCidrBlocks).
//
// outbox-write — в use-case'е.
func (w *subnetWriter) Update(ctx context.Context, s *domain.Subnet) (*kacho.SubnetRecord, error) {
	labelsJSON, err := helpers.MarshalJSONB(domain.LabelsToMap(s.Labels), "Subnet.labels")
	if err != nil {
		return nil, err
	}
	dhcpJSON, err := helpers.MarshalDhcp(s.DhcpOptions)
	if err != nil {
		return nil, err
	}

	q := fmt.Sprintf(`
		UPDATE subnets SET name=$2, description=$3, labels=$4, route_table_id=$5, dhcp_options=$6
		WHERE id=$1
		RETURNING %s`, helpers.SubnetCols)

	row := w.tx.QueryRow(ctx, q,
		s.ID, string(s.Name), string(s.Description), labelsJSON,
		helpers.NullableStr(s.RouteTableID), dhcpJSON,
	)
	result, err := helpers.ScanSubnet(row)
	if err != nil {
		return nil, helpers.WrapPgErr(err, "Subnet", s.ID)
	}
	return result, nil
}

// SetCidrBlocks атомарно обновляет v4_cidr_blocks и v6_cidr_blocks у Subnet
// (для AddCidrBlocks/RemoveCidrBlocks). EXCLUDE constraint'ы
// subnets_no_overlap_v4 / subnets_no_overlap_v6 проверяют primary CIDR (array[1])
// каждого семейства на пересечение с другими подсетями той же сети.
//
// outbox-write — в use-case'е.
func (w *subnetWriter) SetCidrBlocks(ctx context.Context, id string, v4, v6 []string) (*kacho.SubnetRecord, error) {
	q := fmt.Sprintf(`UPDATE subnets SET v4_cidr_blocks = $2, v6_cidr_blocks = $3 WHERE id = $1 RETURNING %s`, helpers.SubnetCols)
	row := w.tx.QueryRow(ctx, q, id,
		pgtype.Array[string]{Elements: v4, Valid: true, Dims: []pgtype.ArrayDimension{{Length: int32(len(v4)), LowerBound: 1}}},
		pgtype.Array[string]{Elements: v6, Valid: true, Dims: []pgtype.ArrayDimension{{Length: int32(len(v6)), LowerBound: 1}}},
	)
	s, err := helpers.ScanSubnet(row)
	if helpers.IsExclusionViolation(err) {
		return nil, fmt.Errorf("%w: Subnet CIDRs can not overlap", helpers.ErrFailedPrecondition)
	}
	if err != nil {
		return nil, helpers.WrapPgErr(err, "Subnet", id)
	}
	return s, nil
}

// SetZoneID меняет zone_id у Subnet (для Relocate; verbatim YC: Relocate всё
// равно sync-FailedPrecondition'ит до этого вызова, метод оставлен для
// completeness / future-state). outbox-write — в use-case'е.
func (w *subnetWriter) SetZoneID(ctx context.Context, id, zoneID string) (*kacho.SubnetRecord, error) {
	q := fmt.Sprintf(`UPDATE subnets SET zone_id = $2 WHERE id = $1 RETURNING %s`, helpers.SubnetCols)
	row := w.tx.QueryRow(ctx, q, id, zoneID)
	s, err := helpers.ScanSubnet(row)
	if err != nil {
		return nil, helpers.WrapPgErr(err, "Subnet", id)
	}
	return s, nil
}

// SetProjectID меняет project_id у Subnet (для :move). outbox-write — в use-case'е.
func (w *subnetWriter) SetProjectID(ctx context.Context, id, folderID string) (*kacho.SubnetRecord, error) {
	q := fmt.Sprintf(`UPDATE subnets SET project_id = $2 WHERE id = $1 RETURNING %s`, helpers.SubnetCols)
	row := w.tx.QueryRow(ctx, q, id, folderID)
	s, err := helpers.ScanSubnet(row)
	if err != nil {
		return nil, helpers.WrapPgErr(err, "Subnet", id)
	}
	return s, nil
}

// Delete — DELETE subnets WHERE id = $1. FK violation (есть дети — addresses
// через generated internal_subnet_id, либо NICs) → ErrFailedPrecondition. row
// not affected → ErrNotFound "Subnet <id> not found".
//
// outbox-write (DELETED tombstone) — в use-case'е.
func (w *subnetWriter) Delete(ctx context.Context, id string) error {
	tag, err := w.tx.Exec(ctx, `DELETE FROM subnets WHERE id = $1`, id)
	if err != nil {
		if helpers.IsFKViolation(err) {
			return fmt.Errorf("%w: subnet has dependent resources", helpers.ErrFailedPrecondition)
		}
		return helpers.WrapPgErr(err, "Subnet", id)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: Subnet %s not found", helpers.ErrNotFound, id)
	}
	return nil
}
