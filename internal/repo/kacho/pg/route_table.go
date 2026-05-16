package pg

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/PRO-Robotech/kacho-corelib/filter"
	"github.com/PRO-Robotech/kacho-corelib/validate"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/helpers"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// routeTableReader — Get/List поверх произвольной pgx.Tx (read-only или RW).
// Wave 5 replicate (KAC-94, skill evgeniy §6 G.1-G.7): RT переезжает на CQRS,
// чтобы Network.Delete мог в одной writer-TX проверять child-RT и/или
// (опционально) удалять — parity с CQRS-Network/SG. SQL-семантика — parity с
// legacy `*repo.RouteTableRepo`: shim'ы экспортированы как `helpers.RouteTableCols`
// / `helpers.ScanRouteTable` / `helpers.MarshalStaticRoutes`.
//
// ⚠️ Auto-association: миграция `0019_vpc_auto_associations.sql` (KAC-56)
// устанавливает DB-уровневые PL/pgSQL триггеры: AFTER INSERT ON route_tables
// auto-assoc'ит Subnet'ы той же сети с `route_table_id IS NULL`. Это DB-side,
// в use-case'ы не лезет — CQRS-Insert просто делает INSERT (как и legacy-репо),
// триггер срабатывает и эмитит дополнительные `Subnet.UPDATED` события с
// маркером `auto_association: true` в `vpc_outbox`.
type routeTableReader struct {
	tx pgx.Tx
}

// Get — verbatim YC: well-formed-but-absent → NotFound с
// "Route table <id> not found".
func (r *routeTableReader) Get(ctx context.Context, id string) (*kacho.RouteTableRecord, error) {
	q := fmt.Sprintf(`SELECT %s FROM route_tables WHERE id = $1`, helpers.RouteTableCols)
	row := r.tx.QueryRow(ctx, q, id)
	rt, err := helpers.ScanRouteTable(row)
	if err != nil {
		return nil, helpers.WrapPgErr(err, "Route table", id)
	}
	return rt, nil
}

// List — cursor-based pagination + filter.Parse (YC-syntax, whitelist
// `["name"]`). Parity с legacy `*repo.RouteTableRepo.List`.
func (r *routeTableReader) List(ctx context.Context, f kacho.RouteTableFilter, p kacho.Pagination) ([]*kacho.RouteTableRecord, string, error) {
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
	q := fmt.Sprintf(`SELECT %s FROM route_tables %s ORDER BY created_at ASC, id ASC LIMIT $%d`, helpers.RouteTableCols, where, argIdx)
	args = append(args, pageSize+1)

	rows, err := r.tx.Query(ctx, q, args...)
	if err != nil {
		return nil, "", helpers.WrapPgErr(err, "Route table", "")
	}
	defer rows.Close()

	var result []*kacho.RouteTableRecord
	for rows.Next() {
		rt, err := helpers.ScanRouteTable(rows)
		if err != nil {
			return nil, "", helpers.WrapPgErr(err, "Route table", "")
		}
		result = append(result, rt)
	}
	if err := rows.Err(); err != nil {
		return nil, "", helpers.WrapPgErr(err, "Route table", "")
	}

	var nextToken string
	if int64(len(result)) > pageSize {
		last := result[pageSize-1]
		nextToken = helpers.EncodePageToken(last.CreatedAt, last.ID)
		result = result[:pageSize]
	}
	return result, nextToken, nil
}

// ListByNetwork — узкий read для checkNetworkEmpty / ListRouteTables.
// Реализован поверх List с filter NetworkID — экономит дублирование SQL.
func (r *routeTableReader) ListByNetwork(ctx context.Context, networkID string, p kacho.Pagination) ([]*kacho.RouteTableRecord, string, error) {
	return r.List(ctx, kacho.RouteTableFilter{NetworkID: networkID}, p)
}

// routeTableWriter — DML над route_tables через writer-TX. Embeds
// routeTableReader (G.2 — writer видит свои writes).
//
// Особенность CQRS-pilot'а: writer НЕ emit'ит outbox самостоятельно. После
// успешного DML caller (use-case) вызывает `RepositoryWriter.Outbox().Emit(...)`
// — это явная точка G.5, видно из use-case-кода что происходит outbox-write.
//
// ⚠️ Auto-association (KAC-56, миграция 0019): AFTER INSERT ON route_tables
// trigger перебирает `subnets WHERE network_id = NEW.network_id AND route_table_id IS NULL`
// и проставляет им `route_table_id = NEW.id`. Каждое такое UPDATE эмитит
// `Subnet.UPDATED` в `vpc_outbox` (trigger AFTER UPDATE OF route_table_id ON
// subnets) с payload `auto_association: true`. Use-case-код эмитит только
// `RouteTable.CREATED` — Subnet-события пишет БД.
type routeTableWriter struct {
	routeTableReader
	emitter kacho.OutboxEmitter // не используется внутри writer-методов; держим для consistency с networkWriter/securityGroupWriter
}

// Insert — INSERT route_tables RETURNING. CreatedAt проставляется явно (UTC),
// parity с legacy-репо (детерминизм для тестов). outbox-write — в use-case'е.
func (w *routeTableWriter) Insert(ctx context.Context, rt *domain.RouteTable) (*kacho.RouteTableRecord, error) {
	labelsJSON, err := helpers.MarshalJSONB(domain.LabelsToMap(rt.Labels), "RouteTable.labels")
	if err != nil {
		return nil, err
	}
	routesJSON, err := helpers.MarshalStaticRoutes(rt.StaticRoutes)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	q := fmt.Sprintf(`
		INSERT INTO route_tables (id, folder_id, created_at, name, description, labels, network_id, static_routes)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING %s`, helpers.RouteTableCols)

	row := w.tx.QueryRow(ctx, q,
		rt.ID, rt.FolderID, now, string(rt.Name), string(rt.Description), labelsJSON,
		rt.NetworkID, routesJSON,
	)
	result, err := helpers.ScanRouteTable(row)
	if err != nil {
		return nil, helpers.WrapPgErr(err, "Route table", string(rt.Name))
	}
	return result, nil
}

// Update — UPDATE route_tables RETURNING. Мутирует name/description/labels/
// static_routes (folder_id меняется через SetFolderID для :move; network_id —
// immutable).
//
// outbox-write — в use-case'е (см. Insert).
func (w *routeTableWriter) Update(ctx context.Context, rt *domain.RouteTable) (*kacho.RouteTableRecord, error) {
	labelsJSON, err := helpers.MarshalJSONB(domain.LabelsToMap(rt.Labels), "RouteTable.labels")
	if err != nil {
		return nil, err
	}
	routesJSON, err := helpers.MarshalStaticRoutes(rt.StaticRoutes)
	if err != nil {
		return nil, err
	}

	q := fmt.Sprintf(`
		UPDATE route_tables SET name=$2, description=$3, labels=$4, static_routes=$5
		WHERE id=$1
		RETURNING %s`, helpers.RouteTableCols)
	row := w.tx.QueryRow(ctx, q,
		rt.ID, string(rt.Name), string(rt.Description), labelsJSON, routesJSON,
	)
	result, err := helpers.ScanRouteTable(row)
	if err != nil {
		return nil, helpers.WrapPgErr(err, "Route table", rt.ID)
	}
	return result, nil
}

// SetFolderID меняет folder_id у RouteTable (для :move). outbox-write — в use-case'е.
func (w *routeTableWriter) SetFolderID(ctx context.Context, id, folderID string) (*kacho.RouteTableRecord, error) {
	q := fmt.Sprintf(`UPDATE route_tables SET folder_id = $2 WHERE id = $1 RETURNING %s`, helpers.RouteTableCols)
	row := w.tx.QueryRow(ctx, q, id, folderID)
	rt, err := helpers.ScanRouteTable(row)
	if err != nil {
		return nil, helpers.WrapPgErr(err, "Route table", id)
	}
	return rt, nil
}

// Delete — DELETE route_tables WHERE id = $1. FK violation (например, NetworkInterface
// или Subnet ссылается на RT) → ErrFailedPrecondition "route table is in use".
// row not affected → ErrNotFound "Route table <id> not found".
//
// ⚠️ Auto-association FK: `subnets.route_table_id → route_tables(id) ON DELETE
// SET NULL` (миграция 0019) — это значит Delete RT обнуляет route_table_id у
// привязанных Subnet'ов в той же tx-операции, FK не блокирует. Триггер AFTER
// UPDATE OF route_table_id эмитит соответствующие `Subnet.UPDATED` события в
// outbox.
//
// outbox-write (DELETED tombstone) — в use-case'е.
func (w *routeTableWriter) Delete(ctx context.Context, id string) error {
	tag, err := w.tx.Exec(ctx, `DELETE FROM route_tables WHERE id = $1`, id)
	if err != nil {
		if helpers.IsFKViolation(err) {
			return fmt.Errorf("%w: route table is in use", helpers.ErrFailedPrecondition)
		}
		return helpers.WrapPgErr(err, "Route table", id)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: Route table %s not found", helpers.ErrNotFound, id)
	}
	return nil
}
