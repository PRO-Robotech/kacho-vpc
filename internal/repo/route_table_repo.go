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

// RouteTableRepo — реализация service.RouteTableRepo поверх pgxpool.
type RouteTableRepo struct {
	pool *pgxpool.Pool
}

// NewRouteTableRepo создаёт RouteTableRepo.
func NewRouteTableRepo(pool *pgxpool.Pool) *RouteTableRepo {
	return &RouteTableRepo{pool: pool}
}

const routeTableCols = `id, folder_id, created_at, name, description, labels, network_id, static_routes`

func (r *RouteTableRepo) Get(ctx context.Context, id string) (*domain.RouteTable, error) {
	q := fmt.Sprintf(`SELECT %s FROM route_tables WHERE id = $1`, routeTableCols)
	row := r.pool.QueryRow(ctx, q, id)
	rt, err := scanRouteTable(row)
	if err != nil {
		return nil, wrapPgErr(err, "RouteTable", id)
	}
	return rt, nil
}

func (r *RouteTableRepo) List(ctx context.Context, f service.RouteTableFilter, p service.Pagination) ([]*domain.RouteTable, string, error) {
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
	q := fmt.Sprintf(`SELECT %s FROM route_tables %s ORDER BY created_at ASC, id ASC LIMIT $%d`, routeTableCols, where, argIdx)
	args = append(args, pageSize+1)

	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, "", wrapPgErr(err, "RouteTable", "")
	}
	defer rows.Close()

	var result []*domain.RouteTable
	for rows.Next() {
		rt, err := scanRouteTable(rows)
		if err != nil {
			return nil, "", wrapPgErr(err, "RouteTable", "")
		}
		result = append(result, rt)
	}
	if err := rows.Err(); err != nil {
		return nil, "", wrapPgErr(err, "RouteTable", "")
	}

	var nextToken string
	if int64(len(result)) > pageSize {
		last := result[pageSize-1]
		nextToken = encodePageToken(last.CreatedAt, last.ID)
		result = result[:pageSize]
	}
	return result, nextToken, nil
}

func (r *RouteTableRepo) Insert(ctx context.Context, rt *domain.RouteTable) (*domain.RouteTable, error) {
	labelsJSON, err := marshalJSONB(rt.Labels, "RouteTable.labels")
	if err != nil {
		return nil, err
	}
	routesJSON, err := marshalStaticRoutes(rt.StaticRoutes)
	if err != nil {
		return nil, err
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, service.ErrInternal
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const q = `
		INSERT INTO route_tables (id, folder_id, created_at, name, description, labels, network_id, static_routes)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING ` + routeTableCols

	row := tx.QueryRow(ctx, q,
		rt.ID, rt.FolderID, rt.CreatedAt, rt.Name, rt.Description, labelsJSON,
		rt.NetworkID, routesJSON,
	)
	result, err := scanRouteTable(row)
	if err != nil {
		return nil, wrapPgErr(err, "RouteTable", rt.Name)
	}
	if err := emitVPC(ctx, tx, "RouteTable", result.ID, "CREATED", routeTablePayload(result)); err != nil {
		return nil, service.ErrInternal
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, wrapPgErr(err, "RouteTable", rt.Name)
	}
	return result, nil
}

func (r *RouteTableRepo) Update(ctx context.Context, rt *domain.RouteTable) (*domain.RouteTable, error) {
	labelsJSON, err := marshalJSONB(rt.Labels, "RouteTable.labels")
	if err != nil {
		return nil, err
	}
	routesJSON, err := marshalStaticRoutes(rt.StaticRoutes)
	if err != nil {
		return nil, err
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, service.ErrInternal
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const q = `
		UPDATE route_tables SET name=$2, description=$3, labels=$4, static_routes=$5
		WHERE id=$1
		RETURNING ` + routeTableCols

	row := tx.QueryRow(ctx, q,
		rt.ID, rt.Name, rt.Description, labelsJSON, routesJSON,
	)
	result, err := scanRouteTable(row)
	if err != nil {
		return nil, wrapPgErr(err, "RouteTable", rt.ID)
	}
	if err := emitVPC(ctx, tx, "RouteTable", result.ID, "UPDATED", routeTablePayload(result)); err != nil {
		return nil, service.ErrInternal
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, wrapPgErr(err, "RouteTable", rt.ID)
	}
	return result, nil
}

// SetFolderID меняет folder_id у RouteTable.
func (r *RouteTableRepo) SetFolderID(ctx context.Context, id, folderID string) (*domain.RouteTable, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, service.ErrInternal
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := fmt.Sprintf(`UPDATE route_tables SET folder_id = $2 WHERE id = $1 RETURNING %s`, routeTableCols)
	row := tx.QueryRow(ctx, q, id, folderID)
	rt, err := scanRouteTable(row)
	if err != nil {
		return nil, wrapPgErr(err, "RouteTable", id)
	}
	if err := emitVPC(ctx, tx, "RouteTable", rt.ID, "UPDATED", routeTablePayload(rt)); err != nil {
		return nil, service.ErrInternal
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, wrapPgErr(err, "RouteTable", id)
	}
	return rt, nil
}

func (r *RouteTableRepo) Delete(ctx context.Context, id string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return service.ErrInternal
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx, `DELETE FROM route_tables WHERE id = $1`, id)
	if err != nil {
		if isFKViolation(err) {
			return fmt.Errorf("%w: route table is in use", service.ErrFailedPrecondition)
		}
		// 22P02 → InvalidArgument "invalid routetable id 'X'" (verbatim YC).
		return wrapPgErr(err, "RouteTable", id)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: RouteTable %s not found", service.ErrNotFound, id)
	}
	if err := emitVPC(ctx, tx, "RouteTable", id, "DELETED", map[string]any{"id": id}); err != nil {
		return service.ErrInternal
	}
	if err := tx.Commit(ctx); err != nil {
		return wrapPgErr(err, "RouteTable", id)
	}
	return nil
}

// ---- scan helpers ----

func scanRouteTable(row scannable) (*domain.RouteTable, error) {
	var rt domain.RouteTable
	var labelsJSON, routesJSON []byte

	err := row.Scan(
		&rt.ID, &rt.FolderID, &rt.CreatedAt, &rt.Name, &rt.Description, &labelsJSON,
		&rt.NetworkID, &routesJSON,
	)
	if err != nil {
		return nil, err
	}
	if err := unmarshalJSONB(labelsJSON, &rt.Labels, "RouteTable.labels"); err != nil {
		return nil, err
	}
	if err := unmarshalJSONB(routesJSON, &rt.StaticRoutes, "RouteTable.static_routes"); err != nil {
		return nil, err
	}
	return &rt, nil
}

func marshalStaticRoutes(routes []domain.StaticRoute) ([]byte, error) {
	if routes == nil {
		return []byte("[]"), nil
	}
	return marshalJSONB(routes, "RouteTable.static_routes")
}
