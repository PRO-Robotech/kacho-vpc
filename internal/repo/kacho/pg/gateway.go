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
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// gatewayReader — Get/List поверх произвольной pgx.Tx (read-only или RW).
// Не имеет своего state кроме tx.
//
// Wave 5 replicate (KAC-94, skill evgeniy §6 G.1-G.7): Gateway-репо переезжает
// на CQRS вслед за pilot Network и batch 33/34 SecurityGroup. SQL/scan-семантика
// — parity с legacy *repo.GatewayRepo: см. internal/repo/gateway_repo.go
// (helpers экспортированы как shim-ы — repo.GatewayCols / repo.ScanGateway /
// repo.WrapGatewayErr / repo.MarshalJSONB / repo.GatewayPayload).
type gatewayReader struct {
	tx pgx.Tx
}

// Get — verbatim YC: well-formed-but-absent → NotFound с "Gateway <id> not found".
func (r *gatewayReader) Get(ctx context.Context, id string) (*kacho.GatewayRecord, error) {
	q := fmt.Sprintf(`SELECT %s FROM gateways WHERE id = $1`, repo.GatewayCols)
	row := r.tx.QueryRow(ctx, q, id)
	g, err := repo.ScanGateway(row)
	if err != nil {
		return nil, repo.WrapGatewayErr(err, id)
	}
	return g, nil
}

// List — cursor-based pagination + filter.Parse (YC-syntax с whitelist
// allowedFields=["name"]). Парность с legacy GatewayRepo.
func (r *gatewayReader) List(ctx context.Context, f kacho.GatewayFilter, p kacho.Pagination) ([]*kacho.GatewayRecord, string, error) {
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
			return nil, "", repo.InvalidFilterErr(perr)
		}
		if ast != nil {
			frag, fargs := ast.ToSQL(argIdx)
			conditions = append(conditions, frag)
			args = append(args, fargs...)
			argIdx += len(fargs)
		}
	}
	if p.PageToken != "" {
		ts, id, derr := repo.DecodePageToken(p.PageToken)
		if derr != nil {
			return nil, "", repo.InvalidPageTokenErr(derr)
		}
		conditions = append(conditions, fmt.Sprintf("(created_at, id) > ($%d, $%d)", argIdx, argIdx+1))
		args = append(args, ts, id)
		argIdx += 2
	}

	var where string
	if len(conditions) > 0 {
		where = "WHERE " + strings.Join(conditions, " AND ")
	}
	q := fmt.Sprintf(`SELECT %s FROM gateways %s ORDER BY created_at ASC, id ASC LIMIT $%d`, repo.GatewayCols, where, argIdx)
	args = append(args, pageSize+1)

	rows, err := r.tx.Query(ctx, q, args...)
	if err != nil {
		return nil, "", repo.WrapGatewayErr(err, "")
	}
	defer rows.Close()

	var result []*kacho.GatewayRecord
	for rows.Next() {
		g, err := repo.ScanGateway(rows)
		if err != nil {
			return nil, "", repo.WrapGatewayErr(err, "")
		}
		result = append(result, g)
	}
	if err := rows.Err(); err != nil {
		return nil, "", repo.WrapGatewayErr(err, "")
	}

	var nextToken string
	if int64(len(result)) > pageSize {
		last := result[pageSize-1]
		nextToken = repo.EncodePageToken(last.CreatedAt, last.ID)
		result = result[:pageSize]
	}
	return result, nextToken, nil
}

// gatewayWriter — DML над gateways через writer-TX. Embeds gatewayReader
// (G.2 — writer видит свои writes).
//
// Особенность CQRS: writer НЕ emit-ит outbox самостоятельно — caller (use-case)
// делает RepositoryWriter.Outbox().Emit(...) явно после успешного DML. Это
// гарантирует, что outbox-write идёт в той же pgx.Tx (G.5) и что
// последовательность outbox-событий принимается явным решением use-case-а, а
// не «из глубины» repo.
type gatewayWriter struct {
	gatewayReader
	emitter kacho.OutboxEmitter // не используется здесь; держим для consistency с networkWriter
}

// Insert — INSERT gateways RETURNING. CreatedAt явно проставляется в UTC (как
// в legacy-репо), хотя БД-колонка имеет DEFAULT now() — это нужно для
// детерминированности тестов и для возврата RETURNING без второго round-trip.
//
// outbox-write — не здесь, а в use-case-е через writer.Outbox().Emit(...).
func (w *gatewayWriter) Insert(ctx context.Context, g *domain.Gateway) (*kacho.GatewayRecord, error) {
	labelsJSON, err := repo.MarshalJSONB(domain.LabelsToMap(g.Labels), "Gateway.labels")
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	q := fmt.Sprintf(`
		INSERT INTO gateways (id, folder_id, created_at, name, description, labels, gateway_type)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING %s`, repo.GatewayCols)

	row := w.tx.QueryRow(ctx, q,
		g.ID, g.FolderID, now, string(g.Name), string(g.Description), labelsJSON, string(g.GatewayType),
	)
	result, err := repo.ScanGateway(row)
	if err != nil {
		return nil, repo.WrapGatewayErr(err, string(g.Name))
	}
	return result, nil
}

// Update — UPDATE gateways RETURNING name/description/labels/gateway_type.
// folder_id меняется через SetFolderID (для :move action).
//
// outbox-write — в use-case-е (см. Insert).
func (w *gatewayWriter) Update(ctx context.Context, g *domain.Gateway) (*kacho.GatewayRecord, error) {
	labelsJSON, err := repo.MarshalJSONB(domain.LabelsToMap(g.Labels), "Gateway.labels")
	if err != nil {
		return nil, err
	}

	q := fmt.Sprintf(`
		UPDATE gateways SET name=$2, description=$3, labels=$4, gateway_type=$5
		WHERE id=$1
		RETURNING %s`, repo.GatewayCols)

	row := w.tx.QueryRow(ctx, q,
		g.ID, string(g.Name), string(g.Description), labelsJSON, string(g.GatewayType),
	)
	result, err := repo.ScanGateway(row)
	if err != nil {
		return nil, repo.WrapGatewayErr(err, g.ID)
	}
	return result, nil
}

// SetFolderID меняет folder_id у Gateway (для :move). outbox-write — в use-case-е.
func (w *gatewayWriter) SetFolderID(ctx context.Context, id, folderID string) (*kacho.GatewayRecord, error) {
	q := fmt.Sprintf(`UPDATE gateways SET folder_id = $2 WHERE id = $1 RETURNING %s`, repo.GatewayCols)
	row := w.tx.QueryRow(ctx, q, id, folderID)
	g, err := repo.ScanGateway(row)
	if err != nil {
		return nil, repo.WrapGatewayErr(err, id)
	}
	return g, nil
}

// Delete — DELETE gateways WHERE id = $1. FK violation (gateway в использовании)
// → ErrFailedPrecondition с verbatim YC text "gateway is in use". row not
// affected → ErrNotFound "Gateway <id> not found".
//
// outbox-write (DELETED tombstone) — в use-case-е.
func (w *gatewayWriter) Delete(ctx context.Context, id string) error {
	tag, err := w.tx.Exec(ctx, `DELETE FROM gateways WHERE id = $1`, id)
	if err != nil {
		if repo.IsFKViolation(err) {
			return fmt.Errorf("%w: gateway is in use", repo.ErrFailedPrecondition)
		}
		return repo.WrapGatewayErr(err, id)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: Gateway %s not found", repo.ErrNotFound, id)
	}
	return nil
}
