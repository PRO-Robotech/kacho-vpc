package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

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

const networkCols = `id, folder_id, created_at, name, description, labels, default_security_group_id`

func (r *NetworkRepo) Get(ctx context.Context, id string) (*domain.Network, error) {
	q := fmt.Sprintf(`SELECT %s FROM networks WHERE id = $1`, networkCols)
	row := r.pool.QueryRow(ctx, q, id)
	n, err := scanNetwork(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, service.ErrNotFound
	}
	return n, err
}

func (r *NetworkRepo) List(ctx context.Context, f service.NetworkFilter, p service.Pagination) ([]*domain.Network, string, error) {
	pageSize := p.PageSize
	if pageSize <= 0 || pageSize > 1000 {
		pageSize = 50
	}

	args := []any{}
	conditions := []string{}
	argIdx := 1

	if f.FolderID != "" {
		conditions = append(conditions, fmt.Sprintf("folder_id = $%d", argIdx))
		args = append(args, f.FolderID)
		argIdx++
	}
	if p.PageToken != "" {
		ts, id, err := decodePageToken(p.PageToken)
		if err != nil {
			return nil, "", fmt.Errorf("invalid page_token: %w", err)
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
		return nil, "", err
	}
	defer rows.Close()

	var result []*domain.Network
	for rows.Next() {
		n, err := scanNetwork(rows)
		if err != nil {
			return nil, "", err
		}
		result = append(result, n)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
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
	labelsJSON, _ := json.Marshal(n.Labels)

	const q = `
		INSERT INTO networks (id, folder_id, created_at, name, description, labels, default_security_group_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING ` + networkCols

	row := r.pool.QueryRow(ctx, q,
		n.ID, n.FolderID, n.CreatedAt, n.Name, n.Description, labelsJSON, n.DefaultSecurityGroupID,
	)
	return scanNetwork(row)
}

func (r *NetworkRepo) Update(ctx context.Context, n *domain.Network) (*domain.Network, error) {
	labelsJSON, _ := json.Marshal(n.Labels)

	const q = `
		UPDATE networks SET name=$2, description=$3, labels=$4, default_security_group_id=$5
		WHERE id=$1
		RETURNING ` + networkCols

	row := r.pool.QueryRow(ctx, q,
		n.ID, n.Name, n.Description, labelsJSON, n.DefaultSecurityGroupID,
	)
	result, err := scanNetwork(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, service.ErrNotFound
	}
	return result, err
}

func (r *NetworkRepo) Delete(ctx context.Context, id string) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM networks WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return service.ErrNotFound
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

	err := row.Scan(
		&n.ID, &n.FolderID, &n.CreatedAt, &n.Name, &n.Description, &labelsJSON,
		&n.DefaultSecurityGroupID,
	)
	if err != nil {
		return nil, err
	}
	if labelsJSON != nil {
		_ = json.Unmarshal(labelsJSON, &n.Labels)
	}
	return &n, nil
}
