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
)

// Network — type-alias на domain.NetworkRecord (repo-entity с DB-managed
// CreatedAt). Имя `repo.Network` сохранено для читаемости call-site'ов
// (`*repo.Network` встречается в service/handler-коде), а сама структура
// объявлена в `domain` чтобы её мог типизировать ещё и `internal/repo`
// без import-cycle. Skill evgeniy §4 D.1 / §7 H.1: CreatedAt живёт в
// repo-entity, не в domain.Network.
type Network = domain.NetworkRecord

// NetworkRepo — реализация NetworkRepoIface поверх pgxpool.
type NetworkRepo struct {
	pool *pgxpool.Pool
}

// NewNetworkRepo создаёт NetworkRepo.
func NewNetworkRepo(pool *pgxpool.Pool) *NetworkRepo {
	return &NetworkRepo{pool: pool}
}

const networkCols = `id, folder_id, created_at, name, description, labels, default_security_group_id`

func (r *NetworkRepo) Get(ctx context.Context, id string) (*Network, error) {
	q := fmt.Sprintf(`SELECT %s FROM networks WHERE id = $1`, networkCols)
	row := r.pool.QueryRow(ctx, q, id)
	n, err := scanNetwork(row)
	if err != nil {
		return nil, wrapPgErr(err, "Network", id)
	}
	return n, nil
}

func (r *NetworkRepo) List(ctx context.Context, f NetworkFilter, p Pagination) ([]*Network, string, error) {
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

	var result []*Network
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

// Insert вставляет Network. Принимает domain.Network (без CreatedAt — repo сам
// выставит `now()` в БД через DEFAULT; здесь явно прокидываем UTC-now для
// детерминированности тестов, но source of truth — БД-колонка).
//
// Возвращает *repo.Network с заполненным CreatedAt.
func (r *NetworkRepo) Insert(ctx context.Context, n *domain.Network) (*Network, error) {
	labelsJSON, err := marshalJSONB(domain.LabelsToMap(n.Labels), "Network.labels")
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
		INSERT INTO networks (id, folder_id, created_at, name, description, labels, default_security_group_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING ` + networkCols

	row := tx.QueryRow(ctx, q,
		n.ID, n.FolderID, now, string(n.Name), string(n.Description), labelsJSON, n.DefaultSecurityGroupID,
	)
	result, err := scanNetwork(row)
	if err != nil {
		return nil, wrapPgErr(err, "Network", string(n.Name))
	}
	if err := emitVPC(ctx, tx, "Network", result.ID, "CREATED", networkPayload(result)); err != nil {
		return nil, ErrInternal
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, wrapPgErr(err, "Network", string(n.Name))
	}
	return result, nil
}

// Update обновляет mutable-поля Network. Принимает domain.Network (без CreatedAt).
func (r *NetworkRepo) Update(ctx context.Context, n *domain.Network) (*Network, error) {
	labelsJSON, err := marshalJSONB(domain.LabelsToMap(n.Labels), "Network.labels")
	if err != nil {
		return nil, err
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, ErrInternal
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const q = `
		UPDATE networks SET name=$2, description=$3, labels=$4, default_security_group_id=$5
		WHERE id=$1
		RETURNING ` + networkCols

	row := tx.QueryRow(ctx, q,
		n.ID, string(n.Name), string(n.Description), labelsJSON, n.DefaultSecurityGroupID,
	)
	result, err := scanNetwork(row)
	if err != nil {
		return nil, wrapPgErr(err, "Network", n.ID)
	}
	if err := emitVPC(ctx, tx, "Network", result.ID, "UPDATED", networkPayload(result)); err != nil {
		return nil, ErrInternal
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, wrapPgErr(err, "Network", n.ID)
	}
	return result, nil
}

// SetFolderID меняет folder_id у Network (для :move).
func (r *NetworkRepo) SetFolderID(ctx context.Context, id, folderID string) (*Network, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, ErrInternal
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
		return nil, ErrInternal
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, wrapPgErr(err, "Network", id)
	}
	return result, nil
}

func (r *NetworkRepo) Delete(ctx context.Context, id string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return ErrInternal
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx, `DELETE FROM networks WHERE id = $1`, id)
	if err != nil {
		if isFKViolation(err) {
			// Network has dependent subnets/route-tables — verbatim YC error.
			return fmt.Errorf("%w: network is not empty", ErrFailedPrecondition)
		}
		// 22P02 → InvalidArgument "invalid network id 'X'" (verbatim YC).
		// pgx.ErrNoRows / unique-violation / etc. — стандартный wrapPgErr.
		return wrapPgErr(err, "Network", id)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: Network %s not found", ErrNotFound, id)
	}
	if err := emitVPC(ctx, tx, "Network", id, "DELETED", map[string]any{"id": id}); err != nil {
		return ErrInternal
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

func scanNetwork(row scannable) (*Network, error) {
	var n Network
	var labelsJSON []byte
	var name string
	var description string

	err := row.Scan(
		&n.ID, &n.FolderID, &n.CreatedAt, &name, &description, &labelsJSON,
		&n.DefaultSecurityGroupID,
	)
	if err != nil {
		return nil, err
	}
	n.Name = domain.RcNameVPC(name)
	n.Description = domain.RcDescription(description)
	var labels map[string]string
	if err := unmarshalJSONB(labelsJSON, &labels, "Network.labels"); err != nil {
		return nil, err
	}
	n.Labels = domain.LabelsFromMap(labels)
	return &n, nil
}
