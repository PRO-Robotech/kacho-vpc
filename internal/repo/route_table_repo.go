package repo

import (
	"context"
	"encoding/json"
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

const rtCols = `id, folder_id, network_id, name, description,
	created_at, labels, status, generation, resource_version, static_routes, deleted_at`

// RouteTableRepo реализует service.RouteTableRepo.
type RouteTableRepo struct {
	pool *pgxpool.Pool
}

// NewRouteTableRepo создаёт RouteTableRepo.
func NewRouteTableRepo(pool *pgxpool.Pool) *RouteTableRepo {
	return &RouteTableRepo{pool: pool}
}

func (r *RouteTableRepo) Get(ctx context.Context, id string) (*domain.RouteTable, error) {
	uid, err := strToUUID(id)
	if err != nil {
		return nil, coreerrors.InvalidArgument().AddFieldViolation("id", "invalid uuid").Err()
	}
	row := r.pool.QueryRow(ctx,
		`SELECT `+rtCols+` FROM route_tables WHERE id = $1 AND deleted_at IS NULL`, uid)
	return scanRT(row)
}

func (r *RouteTableRepo) GetByFolderAndName(ctx context.Context, folderID, name string) (*domain.RouteTable, error) {
	fid, err := strToUUID(folderID)
	if err != nil {
		return nil, coreerrors.InvalidArgument().AddFieldViolation("folder_id", "invalid uuid").Err()
	}
	row := r.pool.QueryRow(ctx,
		`SELECT `+rtCols+` FROM route_tables WHERE folder_id = $1 AND name = $2 AND deleted_at IS NULL`,
		fid, name)
	return scanRT(row)
}

func (r *RouteTableRepo) List(ctx context.Context, filter service.ListFilter) ([]domain.RouteTable, string, error) {
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

	q := fmt.Sprintf(`SELECT `+rtCols+` FROM route_tables %s %s LIMIT $%d`, where, orderBy, argIdx)
	args = append(args, pageSize+1)

	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()

	var result []domain.RouteTable
	for rows.Next() {
		rt, serr := scanRTRow(rows)
		if serr != nil {
			return nil, "", serr
		}
		result = append(result, *rt)
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

func (r *RouteTableRepo) Create(ctx context.Context, rt *domain.RouteTable) error {
	uid, err := strToUUID(rt.ID)
	if err != nil {
		return err
	}
	fid, err := strToUUID(rt.FolderID)
	if err != nil {
		return err
	}
	nid, err := strToUUID(rt.NetworkID)
	if err != nil {
		return err
	}
	statusStr := domain.RouteTableStatusString[rt.Status]
	if statusStr == "" {
		statusStr = "ROUTE_TABLE_STATUS_PROVISIONING"
	}
	routesJSON, merr := marshalRoutes(rt.StaticRoutes)
	if merr != nil {
		return merr
	}

	_, err = r.pool.Exec(ctx, `
		INSERT INTO route_tables (id, folder_id, network_id, name, description, labels, status, generation, static_routes)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		uid, fid, nid, rt.Name, rt.Description, mapToJSON(rt.Labels), statusStr, rt.Generation, routesJSON,
	)
	return err
}

func (r *RouteTableRepo) Update(ctx context.Context, rt *domain.RouteTable) error {
	uid, err := strToUUID(rt.ID)
	if err != nil {
		return err
	}
	statusStr := domain.RouteTableStatusString[rt.Status]
	if statusStr == "" {
		statusStr = "ROUTE_TABLE_STATUS_ACTIVE"
	}
	routesJSON, merr := marshalRoutes(rt.StaticRoutes)
	if merr != nil {
		return merr
	}

	tag, err := r.pool.Exec(ctx, `
		UPDATE route_tables
		SET name = $2, description = $3, labels = $4, status = $5, generation = $6,
		    static_routes = $7, resource_version = gen_random_uuid()::text
		WHERE id = $1 AND deleted_at IS NULL`,
		uid, rt.Name, rt.Description, mapToJSON(rt.Labels), statusStr, rt.Generation, routesJSON,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return coreerrors.NotFound("RouteTable", rt.ID).Err()
	}
	return nil
}

func (r *RouteTableRepo) SoftDelete(ctx context.Context, id string) error {
	uid, err := strToUUID(id)
	if err != nil {
		return err
	}
	tag, err := r.pool.Exec(ctx,
		`UPDATE route_tables SET deleted_at = now(), status = 'ROUTE_TABLE_STATUS_DELETING' WHERE id = $1 AND deleted_at IS NULL`,
		uid)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return coreerrors.NotFound("RouteTable", id).Err()
	}
	return nil
}

// ---- scan helpers ----

func scanRT(row pgx.Row) (*domain.RouteTable, error) {
	rt, err := scanRTFields(row.Scan)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return rt, err
}

func scanRTRow(rows pgx.Rows) (*domain.RouteTable, error) {
	return scanRTFields(rows.Scan)
}

func scanRTFields(scanFn func(...any) error) (*domain.RouteTable, error) {
	var (
		id, folderID, networkID, name, description string
		createdAt                                   pgtype.Timestamptz
		labelsJSON, routesJSON                      []byte
		statusStr                                   string
		generation                                  int64
		resourceVersion                             string
		deletedAt                                   pgtype.Timestamptz
	)
	err := scanFn(
		&id, &folderID, &networkID, &name, &description,
		&createdAt, &labelsJSON, &statusStr, &generation, &resourceVersion, &routesJSON, &deletedAt,
	)
	if err != nil {
		return nil, err
	}
	routes, _ := unmarshalRoutes(routesJSON)
	return &domain.RouteTable{
		ID:              id,
		FolderID:        folderID,
		NetworkID:       networkID,
		Name:            name,
		Description:     description,
		CreatedAt:       tsToTime(createdAt),
		Labels:          jsonToMap(labelsJSON),
		Status:          domain.ParseRouteTableStatus(statusStr),
		Generation:      generation,
		ResourceVersion: resourceVersion,
		StaticRoutes:    routes,
		DeletedAt:       tsToTimePtr(deletedAt),
	}, nil
}

// ---- JSON helpers for static_routes ----

type routeJSON struct {
	ID                string `json:"id"`
	DestinationPrefix string `json:"destination_prefix"`
	NextHopAddress    string `json:"next_hop_address"`
	Description       string `json:"description"`
}

func marshalRoutes(routes []domain.StaticRoute) ([]byte, error) {
	if len(routes) == 0 {
		return []byte("[]"), nil
	}
	items := make([]routeJSON, len(routes))
	for i, r := range routes {
		items[i] = routeJSON{
			ID:                r.ID,
			DestinationPrefix: r.DestinationPrefix,
			NextHopAddress:    r.NextHopAddress,
			Description:       r.Description,
		}
	}
	return json.Marshal(items)
}

func unmarshalRoutes(b []byte) ([]domain.StaticRoute, error) {
	if len(b) == 0 {
		return nil, nil
	}
	var items []routeJSON
	if err := json.Unmarshal(b, &items); err != nil {
		return nil, err
	}
	routes := make([]domain.StaticRoute, len(items))
	for i, r := range items {
		routes[i] = domain.StaticRoute{
			ID:                r.ID,
			DestinationPrefix: r.DestinationPrefix,
			NextHopAddress:    r.NextHopAddress,
			Description:       r.Description,
		}
	}
	return routes, nil
}
