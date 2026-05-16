// Deprecated: legacy concrete `*<Resource>Repo` struct, оставлен временно ради integration-тестов
// и узких port-адаптеров в admin-services (AddressPool/Address/NIC use-cases ещё не на CQRS).
// Финальное удаление — после полной миграции на kacho.Repository (KAC-94 / skill evgeniy A.7).
//

package repo

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PRO-Robotech/kacho-corelib/filter"
	"github.com/PRO-Robotech/kacho-corelib/validate"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// RouteTable — type-alias на kacho.RouteTableRecord (repo-entity с DB-managed
// CreatedAt). Wave 5 replicate (KAC-94): Record уехал из domain в repo-leaf,
// parity с repo.Network.
type RouteTable = kachorepo.RouteTableRecord

// RouteTableRepo — pgxpool-impl репозитория Route Tables. KAC-94 finalize:
// общий port `RouteTableRepoIface` удалён (skill evgeniy A.7 + G.6); use-case-
// слой — на CQRS-Repository, эта структура — для integration-тестов и
// Network.Delete children-check (передаётся как узкий port).
type RouteTableRepo struct {
	pool *pgxpool.Pool
}

// NewRouteTableRepo создаёт RouteTableRepo.
func NewRouteTableRepo(pool *pgxpool.Pool) *RouteTableRepo {
	return &RouteTableRepo{pool: pool}
}

const routeTableCols = `id, folder_id, created_at, name, description, labels, network_id, static_routes`

func (r *RouteTableRepo) Get(ctx context.Context, id string) (*RouteTable, error) {
	q := fmt.Sprintf(`SELECT %s FROM route_tables WHERE id = $1`, routeTableCols)
	row := r.pool.QueryRow(ctx, q, id)
	rt, err := scanRouteTable(row)
	if err != nil {
		return nil, wrapPgErr(err, "Route table", id)
	}
	return rt, nil
}

func (r *RouteTableRepo) List(ctx context.Context, f RouteTableFilter, p Pagination) ([]*RouteTable, string, error) {
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
	q := fmt.Sprintf(`SELECT %s FROM route_tables %s ORDER BY created_at ASC, id ASC LIMIT $%d`, routeTableCols, where, argIdx)
	args = append(args, pageSize+1)

	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, "", wrapPgErr(err, "Route table", "")
	}
	defer rows.Close()

	var result []*RouteTable
	for rows.Next() {
		rt, err := scanRouteTable(rows)
		if err != nil {
			return nil, "", wrapPgErr(err, "Route table", "")
		}
		result = append(result, rt)
	}
	if err := rows.Err(); err != nil {
		return nil, "", wrapPgErr(err, "Route table", "")
	}

	var nextToken string
	if int64(len(result)) > pageSize {
		last := result[pageSize-1]
		nextToken = encodePageToken(last.CreatedAt, last.ID)
		result = result[:pageSize]
	}
	return result, nextToken, nil
}

// Insert вставляет RouteTable. Принимает domain.RouteTable (без CreatedAt).
// Возвращает *RouteTable (= *domain.RouteTableRecord) с заполненным CreatedAt.
func (r *RouteTableRepo) Insert(ctx context.Context, rt *domain.RouteTable) (*RouteTable, error) {
	labelsJSON, err := marshalJSONB(domain.LabelsToMap(rt.Labels), "RouteTable.labels")
	if err != nil {
		return nil, err
	}
	routesJSON, err := marshalStaticRoutes(rt.StaticRoutes)
	if err != nil {
		return nil, err
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, ErrInternal
	}
	defer func() { _ = tx.Rollback(ctx) }()

	now := time.Now().UTC()
	const q = `
		INSERT INTO route_tables (id, folder_id, created_at, name, description, labels, network_id, static_routes)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING ` + routeTableCols

	row := tx.QueryRow(ctx, q,
		rt.ID, rt.FolderID, now, string(rt.Name), string(rt.Description), labelsJSON,
		rt.NetworkID, routesJSON,
	)
	result, err := scanRouteTable(row)
	if err != nil {
		return nil, wrapPgErr(err, "Route table", string(rt.Name))
	}
	if err := emitVPC(ctx, tx, "RouteTable", result.ID, "CREATED", routeTablePayload(result)); err != nil {
		return nil, ErrInternal
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, wrapPgErr(err, "Route table", string(rt.Name))
	}
	return result, nil
}

// Update обновляет mutable-поля RouteTable. Принимает domain.RouteTable (без CreatedAt).
func (r *RouteTableRepo) Update(ctx context.Context, rt *domain.RouteTable) (*RouteTable, error) {
	labelsJSON, err := marshalJSONB(domain.LabelsToMap(rt.Labels), "RouteTable.labels")
	if err != nil {
		return nil, err
	}
	routesJSON, err := marshalStaticRoutes(rt.StaticRoutes)
	if err != nil {
		return nil, err
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, ErrInternal
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const q = `
		UPDATE route_tables SET name=$2, description=$3, labels=$4, static_routes=$5
		WHERE id=$1
		RETURNING ` + routeTableCols

	row := tx.QueryRow(ctx, q,
		rt.ID, string(rt.Name), string(rt.Description), labelsJSON, routesJSON,
	)
	result, err := scanRouteTable(row)
	if err != nil {
		return nil, wrapPgErr(err, "Route table", rt.ID)
	}
	if err := emitVPC(ctx, tx, "RouteTable", result.ID, "UPDATED", routeTablePayload(result)); err != nil {
		return nil, ErrInternal
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, wrapPgErr(err, "Route table", rt.ID)
	}
	return result, nil
}

// SetFolderID меняет folder_id у RouteTable.
func (r *RouteTableRepo) SetFolderID(ctx context.Context, id, folderID string) (*RouteTable, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, ErrInternal
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := fmt.Sprintf(`UPDATE route_tables SET folder_id = $2 WHERE id = $1 RETURNING %s`, routeTableCols)
	row := tx.QueryRow(ctx, q, id, folderID)
	rt, err := scanRouteTable(row)
	if err != nil {
		return nil, wrapPgErr(err, "Route table", id)
	}
	if err := emitVPC(ctx, tx, "RouteTable", rt.ID, "UPDATED", routeTablePayload(rt)); err != nil {
		return nil, ErrInternal
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, wrapPgErr(err, "Route table", id)
	}
	return rt, nil
}

func (r *RouteTableRepo) Delete(ctx context.Context, id string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return ErrInternal
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx, `DELETE FROM route_tables WHERE id = $1`, id)
	if err != nil {
		if isFKViolation(err) {
			return fmt.Errorf("%w: route table is in use", ErrFailedPrecondition)
		}
		// 22P02 → InvalidArgument "invalid routetable id 'X'" (verbatim YC).
		return wrapPgErr(err, "Route table", id)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: Route table %s not found", ErrNotFound, id)
	}
	if err := emitVPC(ctx, tx, "RouteTable", id, "DELETED", map[string]any{"id": id}); err != nil {
		return ErrInternal
	}
	if err := tx.Commit(ctx); err != nil {
		return wrapPgErr(err, "Route table", id)
	}
	return nil
}

// ---- scan helpers ----

func scanRouteTable(row scannable) (*RouteTable, error) {
	var rt RouteTable
	var labelsJSON, routesJSON []byte
	var name string
	var description string

	err := row.Scan(
		&rt.ID, &rt.FolderID, &rt.CreatedAt, &name, &description, &labelsJSON,
		&rt.NetworkID, &routesJSON,
	)
	if err != nil {
		return nil, err
	}
	rt.Name = domain.RcNameVPC(name)
	rt.Description = domain.RcDescription(description)
	var labels map[string]string
	if err := unmarshalJSONB(labelsJSON, &labels, "RouteTable.labels"); err != nil {
		return nil, err
	}
	rt.Labels = domain.LabelsFromMap(labels)
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
