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

// NetworkRepo — реализация service.NetworkRepo поверх pgxpool.
type NetworkRepo struct {
	pool *pgxpool.Pool
}

// NewNetworkRepo создаёт NetworkRepo.
func NewNetworkRepo(pool *pgxpool.Pool) *NetworkRepo {
	return &NetworkRepo{pool: pool}
}

const networkCols = `id, folder_id, created_at, name, description, labels, default_security_group_id, vpn_id`

func (r *NetworkRepo) Get(ctx context.Context, id string) (*domain.Network, error) {
	q := fmt.Sprintf(`SELECT %s FROM networks WHERE id = $1`, networkCols)
	row := r.pool.QueryRow(ctx, q, id)
	n, err := scanNetwork(row)
	if err != nil {
		return nil, wrapPgErr(err, "Network", id)
	}
	return n, nil
}

func (r *NetworkRepo) List(ctx context.Context, f service.NetworkFilter, p service.Pagination) ([]*domain.Network, string, error) {
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
	q := fmt.Sprintf(`SELECT %s FROM networks %s ORDER BY created_at ASC, id ASC LIMIT $%d`, networkCols, where, argIdx)
	args = append(args, pageSize+1)

	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, "", wrapPgErr(err, "Network", "")
	}
	defer rows.Close()

	var result []*domain.Network
	for rows.Next() {
		n, err := scanNetwork(rows)
		if err != nil {
			return nil, "", wrapPgErr(err, "Network", "")
		}
		result = append(result, n)
	}
	if err := rows.Err(); err != nil {
		return nil, "", wrapPgErr(err, "Network", "")
	}

	var nextToken string
	if int64(len(result)) > pageSize {
		last := result[pageSize-1]
		nextToken = encodePageToken(last.CreatedAt, last.ID)
		result = result[:pageSize]
	}
	return result, nextToken, nil
}

func (r *NetworkRepo) Insert(ctx context.Context, n *domain.Network) (*domain.Network, error) {
	labelsJSON, err := marshalJSONB(n.Labels, "Network.labels")
	if err != nil {
		return nil, err
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, service.ErrInternal
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// vpn_id аллоцируется атомарно в этом же INSERT: сначала пробуем
	// переиспользовать минимальный из vpn_id_free (popped CTE), иначе nextval
	// (COALESCE в Postgres коротко замыкается — nextval вызывается только если
	// free-list пуст). Конкурентные insert'ы безопасны: DELETE берёт row-lock,
	// проигравший пойдёт в nextval.
	const q = `
		WITH popped AS (
			DELETE FROM vpn_id_free WHERE id = (SELECT id FROM vpn_id_free ORDER BY id LIMIT 1) RETURNING id
		)
		INSERT INTO networks (id, folder_id, created_at, name, description, labels, default_security_group_id, vpn_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, COALESCE((SELECT id FROM popped), nextval('vpn_id_seq')::int))
		RETURNING ` + networkCols

	row := tx.QueryRow(ctx, q,
		n.ID, n.FolderID, n.CreatedAt, n.Name, n.Description, labelsJSON, n.DefaultSecurityGroupID,
	)
	result, err := scanNetwork(row)
	if err != nil {
		return nil, wrapPgErr(err, "Network", n.Name)
	}
	if err := emitVPC(ctx, tx, "Network", result.ID, "CREATED", networkPayload(result)); err != nil {
		return nil, service.ErrInternal
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, wrapPgErr(err, "Network", n.Name)
	}
	return result, nil
}

func (r *NetworkRepo) Update(ctx context.Context, n *domain.Network) (*domain.Network, error) {
	labelsJSON, err := marshalJSONB(n.Labels, "Network.labels")
	if err != nil {
		return nil, err
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, service.ErrInternal
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const q = `
		UPDATE networks SET name=$2, description=$3, labels=$4, default_security_group_id=$5
		WHERE id=$1
		RETURNING ` + networkCols

	row := tx.QueryRow(ctx, q,
		n.ID, n.Name, n.Description, labelsJSON, n.DefaultSecurityGroupID,
	)
	result, err := scanNetwork(row)
	if err != nil {
		return nil, wrapPgErr(err, "Network", n.ID)
	}
	if err := emitVPC(ctx, tx, "Network", result.ID, "UPDATED", networkPayload(result)); err != nil {
		return nil, service.ErrInternal
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, wrapPgErr(err, "Network", n.ID)
	}
	return result, nil
}

// SetFolderID меняет folder_id у Network (для :move).
func (r *NetworkRepo) SetFolderID(ctx context.Context, id, folderID string) (*domain.Network, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, service.ErrInternal
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const q = `
		UPDATE networks SET folder_id = $2
		WHERE id = $1
		RETURNING ` + networkCols
	row := tx.QueryRow(ctx, q, id, folderID)
	result, err := scanNetwork(row)
	if err != nil {
		return nil, wrapPgErr(err, "Network", id)
	}
	if err := emitVPC(ctx, tx, "Network", result.ID, "UPDATED", networkPayload(result)); err != nil {
		return nil, service.ErrInternal
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, wrapPgErr(err, "Network", id)
	}
	return result, nil
}

func (r *NetworkRepo) Delete(ctx context.Context, id string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return service.ErrInternal
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Удаляем сеть и возвращаем её vpn_id во free-list одной statement'ой.
	tag, err := tx.Exec(ctx, `
		WITH d AS (DELETE FROM networks WHERE id = $1 RETURNING vpn_id)
		INSERT INTO vpn_id_free (id) SELECT vpn_id FROM d ON CONFLICT DO NOTHING`, id)
	if err != nil {
		if isFKViolation(err) {
			// Network has dependent subnets/route-tables — verbatim YC error.
			return fmt.Errorf("%w: network is not empty", service.ErrFailedPrecondition)
		}
		// 22P02 → InvalidArgument "invalid network id 'X'" (verbatim YC).
		// pgx.ErrNoRows / unique-violation / etc. — стандартный wrapPgErr.
		return wrapPgErr(err, "Network", id)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: Network %s not found", service.ErrNotFound, id)
	}
	if err := emitVPC(ctx, tx, "Network", id, "DELETED", map[string]any{"id": id}); err != nil {
		return service.ErrInternal
	}
	if err := tx.Commit(ctx); err != nil {
		return wrapPgErr(err, "Network", id)
	}
	return nil
}

// ---- scan helpers ----

type scannable interface {
	Scan(dest ...any) error
}

func scanNetwork(row scannable) (*domain.Network, error) {
	var n domain.Network
	var labelsJSON []byte

	var vpnID int32
	err := row.Scan(
		&n.ID, &n.FolderID, &n.CreatedAt, &n.Name, &n.Description, &labelsJSON,
		&n.DefaultSecurityGroupID, &vpnID,
	)
	if err != nil {
		return nil, err
	}
	n.VPNID = uint32(vpnID)
	if err := unmarshalJSONB(labelsJSON, &n.Labels, "Network.labels"); err != nil {
		return nil, err
	}
	return &n, nil
}
