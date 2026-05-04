package repo

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PRO-Robotech/kacho-corelib/filter"
	"github.com/PRO-Robotech/kacho-corelib/validate"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/service"
)

// SecurityGroupRepo — реализация service.SecurityGroupRepo поверх pgxpool.
type SecurityGroupRepo struct {
	pool *pgxpool.Pool
}

// NewSecurityGroupRepo создаёт SecurityGroupRepo.
func NewSecurityGroupRepo(pool *pgxpool.Pool) *SecurityGroupRepo {
	return &SecurityGroupRepo{pool: pool}
}

const sgCols = `id, folder_id, network_id, created_at, name, description, labels, status, default_for_network, rules`

func (r *SecurityGroupRepo) Get(ctx context.Context, id string) (*domain.SecurityGroup, error) {
	q := fmt.Sprintf(`SELECT %s FROM security_groups WHERE id = $1`, sgCols)
	row := r.pool.QueryRow(ctx, q, id)
	sg, err := scanSG(row)
	if err != nil {
		return nil, wrapPgErr(err, "SecurityGroup", id)
	}
	return sg, nil
}

func (r *SecurityGroupRepo) List(ctx context.Context, f service.SecurityGroupFilter, p service.Pagination) ([]*domain.SecurityGroup, string, error) {
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
	q := fmt.Sprintf(`SELECT %s FROM security_groups %s ORDER BY created_at ASC, id ASC LIMIT $%d`, sgCols, where, argIdx)
	args = append(args, pageSize+1)

	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, "", wrapPgErr(err, "SecurityGroup", "")
	}
	defer rows.Close()

	var result []*domain.SecurityGroup
	for rows.Next() {
		sg, err := scanSG(rows)
		if err != nil {
			return nil, "", wrapPgErr(err, "SecurityGroup", "")
		}
		result = append(result, sg)
	}
	if err := rows.Err(); err != nil {
		return nil, "", wrapPgErr(err, "SecurityGroup", "")
	}

	var nextToken string
	if int64(len(result)) > pageSize {
		last := result[pageSize-1]
		nextToken = encodePageToken(last.CreatedAt, last.ID)
		result = result[:pageSize]
	}
	return result, nextToken, nil
}

func (r *SecurityGroupRepo) Insert(ctx context.Context, sg *domain.SecurityGroup) (*domain.SecurityGroup, error) {
	labelsJSON, _ := json.Marshal(sg.Labels)
	rulesJSON, _ := json.Marshal(sg.Rules)

	const q = `
		INSERT INTO security_groups (id, folder_id, network_id, created_at, name, description, labels, status, default_for_network, rules)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING ` + sgCols
	row := r.pool.QueryRow(ctx, q,
		sg.ID, sg.FolderID, sg.NetworkID, sg.CreatedAt, sg.Name, sg.Description, labelsJSON, sg.Status, sg.DefaultForNetwork, rulesJSON,
	)
	result, err := scanSG(row)
	if err != nil {
		return nil, wrapPgErr(err, "SecurityGroup", sg.Name)
	}
	return result, nil
}

func (r *SecurityGroupRepo) Update(ctx context.Context, sg *domain.SecurityGroup) (*domain.SecurityGroup, error) {
	labelsJSON, _ := json.Marshal(sg.Labels)
	rulesJSON, _ := json.Marshal(sg.Rules)
	const q = `
		UPDATE security_groups SET name=$2, description=$3, labels=$4, rules=$5, status=$6
		WHERE id=$1
		RETURNING ` + sgCols
	row := r.pool.QueryRow(ctx, q, sg.ID, sg.Name, sg.Description, labelsJSON, rulesJSON, sg.Status)
	result, err := scanSG(row)
	if err != nil {
		return nil, wrapPgErr(err, "SecurityGroup", sg.ID)
	}
	return result, nil
}

func (r *SecurityGroupRepo) Delete(ctx context.Context, id string) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM security_groups WHERE id = $1`, id)
	if err != nil {
		return wrapPgErr(err, "SecurityGroup", id)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: SecurityGroup %s not found", service.ErrNotFound, id)
	}
	return nil
}

func (r *SecurityGroupRepo) SetFolderID(ctx context.Context, id, folderID string) (*domain.SecurityGroup, error) {
	q := fmt.Sprintf(`UPDATE security_groups SET folder_id = $2 WHERE id = $1 RETURNING %s`, sgCols)
	row := r.pool.QueryRow(ctx, q, id, folderID)
	sg, err := scanSG(row)
	if err != nil {
		return nil, wrapPgErr(err, "SecurityGroup", id)
	}
	return sg, nil
}

// ---- scan ----

func scanSG(row scannable) (*domain.SecurityGroup, error) {
	var sg domain.SecurityGroup
	var labelsJSON []byte
	var rulesJSON []byte

	err := row.Scan(
		&sg.ID, &sg.FolderID, &sg.NetworkID, &sg.CreatedAt, &sg.Name, &sg.Description, &labelsJSON, &sg.Status, &sg.DefaultForNetwork, &rulesJSON,
	)
	if err != nil {
		return nil, err
	}
	if labelsJSON != nil {
		_ = json.Unmarshal(labelsJSON, &sg.Labels)
	}
	if rulesJSON != nil {
		_ = json.Unmarshal(rulesJSON, &sg.Rules)
	}
	return &sg, nil
}
