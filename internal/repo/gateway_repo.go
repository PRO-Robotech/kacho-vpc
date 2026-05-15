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
	"github.com/PRO-Robotech/kacho-vpc/internal/service"
)

// Gateway — type-alias на domain.GatewayRecord (repo-entity с DB-managed CreatedAt).
// Wave 2 batch B (KAC-94), parity с repo.Network.
type Gateway = domain.GatewayRecord

// GatewayRepo — реализация service.GatewayRepo поверх pgxpool.
type GatewayRepo struct {
	pool *pgxpool.Pool
}

// NewGatewayRepo создаёт GatewayRepo.
func NewGatewayRepo(pool *pgxpool.Pool) *GatewayRepo {
	return &GatewayRepo{pool: pool}
}

const gatewayCols = `id, folder_id, created_at, name, description, labels, gateway_type`

func (r *GatewayRepo) Get(ctx context.Context, id string) (*Gateway, error) {
	q := fmt.Sprintf(`SELECT %s FROM gateways WHERE id = $1`, gatewayCols)
	row := r.pool.QueryRow(ctx, q, id)
	g, err := scanGateway(row)
	if err != nil {
		return nil, wrapPgErr(err, "Gateway", id)
	}
	return g, nil
}

func (r *GatewayRepo) List(ctx context.Context, f service.GatewayFilter, p service.Pagination) ([]*Gateway, string, error) {
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
	q := fmt.Sprintf(`SELECT %s FROM gateways %s ORDER BY created_at ASC, id ASC LIMIT $%d`, gatewayCols, where, argIdx)
	args = append(args, pageSize+1)

	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, "", wrapPgErr(err, "Gateway", "")
	}
	defer rows.Close()

	var result []*Gateway
	for rows.Next() {
		g, err := scanGateway(rows)
		if err != nil {
			return nil, "", wrapPgErr(err, "Gateway", "")
		}
		result = append(result, g)
	}
	if err := rows.Err(); err != nil {
		return nil, "", wrapPgErr(err, "Gateway", "")
	}

	var nextToken string
	if int64(len(result)) > pageSize {
		last := result[pageSize-1]
		nextToken = encodePageToken(last.CreatedAt, last.ID)
		result = result[:pageSize]
	}
	return result, nextToken, nil
}

// Insert вставляет Gateway. Принимает domain.Gateway (без CreatedAt — repo сам
// выставит `now()`; source of truth — БД-колонка). Возвращает *Gateway
// (= *domain.GatewayRecord) с заполненным CreatedAt.
func (r *GatewayRepo) Insert(ctx context.Context, g *domain.Gateway) (*Gateway, error) {
	labelsJSON, err := marshalJSONB(domain.LabelsToMap(g.Labels), "Gateway.labels")
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
		INSERT INTO gateways (id, folder_id, created_at, name, description, labels, gateway_type)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING ` + gatewayCols

	row := tx.QueryRow(ctx, q,
		g.ID, g.FolderID, now, string(g.Name), string(g.Description), labelsJSON, string(g.GatewayType),
	)
	result, err := scanGateway(row)
	if err != nil {
		return nil, wrapPgErr(err, "Gateway", string(g.Name))
	}
	if err := emitVPC(ctx, tx, "Gateway", result.ID, "CREATED", gatewayPayload(result)); err != nil {
		return nil, service.ErrInternal
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, wrapPgErr(err, "Gateway", string(g.Name))
	}
	return result, nil
}

// Update обновляет mutable-поля Gateway. Принимает domain.Gateway (без CreatedAt).
func (r *GatewayRepo) Update(ctx context.Context, g *domain.Gateway) (*Gateway, error) {
	labelsJSON, err := marshalJSONB(domain.LabelsToMap(g.Labels), "Gateway.labels")
	if err != nil {
		return nil, err
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, service.ErrInternal
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const q = `
		UPDATE gateways SET name=$2, description=$3, labels=$4, gateway_type=$5
		WHERE id=$1
		RETURNING ` + gatewayCols

	row := tx.QueryRow(ctx, q,
		g.ID, string(g.Name), string(g.Description), labelsJSON, string(g.GatewayType),
	)
	result, err := scanGateway(row)
	if err != nil {
		return nil, wrapPgErr(err, "Gateway", g.ID)
	}
	if err := emitVPC(ctx, tx, "Gateway", result.ID, "UPDATED", gatewayPayload(result)); err != nil {
		return nil, service.ErrInternal
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, wrapPgErr(err, "Gateway", g.ID)
	}
	return result, nil
}

// SetFolderID меняет folder_id у Gateway (для :move).
func (r *GatewayRepo) SetFolderID(ctx context.Context, id, folderID string) (*Gateway, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, service.ErrInternal
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := fmt.Sprintf(`UPDATE gateways SET folder_id = $2 WHERE id = $1 RETURNING %s`, gatewayCols)
	row := tx.QueryRow(ctx, q, id, folderID)
	g, err := scanGateway(row)
	if err != nil {
		return nil, wrapPgErr(err, "Gateway", id)
	}
	if err := emitVPC(ctx, tx, "Gateway", g.ID, "UPDATED", gatewayPayload(g)); err != nil {
		return nil, service.ErrInternal
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, wrapPgErr(err, "Gateway", id)
	}
	return g, nil
}

func (r *GatewayRepo) Delete(ctx context.Context, id string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return service.ErrInternal
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx, `DELETE FROM gateways WHERE id = $1`, id)
	if err != nil {
		if isFKViolation(err) {
			return fmt.Errorf("%w: gateway is in use", service.ErrFailedPrecondition)
		}
		return wrapPgErr(err, "Gateway", id)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: Gateway %s not found", service.ErrNotFound, id)
	}
	if err := emitVPC(ctx, tx, "Gateway", id, "DELETED", map[string]any{"id": id}); err != nil {
		return service.ErrInternal
	}
	if err := tx.Commit(ctx); err != nil {
		return wrapPgErr(err, "Gateway", id)
	}
	return nil
}

// ---- scan helpers ----

func scanGateway(row scannable) (*Gateway, error) {
	var g Gateway
	var labelsJSON []byte
	var name, description, gatewayType string

	err := row.Scan(
		&g.ID, &g.FolderID, &g.CreatedAt, &name, &description, &labelsJSON,
		&gatewayType,
	)
	if err != nil {
		return nil, err
	}
	g.Name = domain.RcNameVPC(name)
	g.Description = domain.RcDescription(description)
	g.GatewayType = domain.GatewayType(gatewayType)
	var labels map[string]string
	if err := unmarshalJSONB(labelsJSON, &labels, "Gateway.labels"); err != nil {
		return nil, err
	}
	g.Labels = domain.LabelsFromMap(labels)
	return &g, nil
}

func gatewayPayload(g *Gateway) map[string]any {
	return domainToMap(g)
}
