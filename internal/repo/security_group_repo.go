package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
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
	labelsJSON, err := marshalJSONB(sg.Labels, "SecurityGroup.labels")
	if err != nil {
		return nil, err
	}
	rulesJSON, err := marshalJSONB(sg.Rules, "SecurityGroup.rules")
	if err != nil {
		return nil, err
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, service.ErrInternal
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const q = `
		INSERT INTO security_groups (id, folder_id, network_id, created_at, name, description, labels, status, default_for_network, rules)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING ` + sgCols
	row := tx.QueryRow(ctx, q,
		sg.ID, sg.FolderID, sg.NetworkID, sg.CreatedAt, sg.Name, sg.Description, labelsJSON, sg.Status, sg.DefaultForNetwork, rulesJSON,
	)
	result, err := scanSG(row)
	if err != nil {
		return nil, wrapPgErr(err, "SecurityGroup", sg.Name)
	}
	if err := emitVPC(ctx, tx, "SecurityGroup", result.ID, "CREATED", securityGroupPayload(result)); err != nil {
		return nil, service.ErrInternal
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, wrapPgErr(err, "SecurityGroup", sg.Name)
	}
	return result, nil
}

func (r *SecurityGroupRepo) Update(ctx context.Context, sg *domain.SecurityGroup) (*domain.SecurityGroup, error) {
	labelsJSON, err := marshalJSONB(sg.Labels, "SecurityGroup.labels")
	if err != nil {
		return nil, err
	}
	rulesJSON, err := marshalJSONB(sg.Rules, "SecurityGroup.rules")
	if err != nil {
		return nil, err
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, service.ErrInternal
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const q = `
		UPDATE security_groups SET name=$2, description=$3, labels=$4, rules=$5, status=$6
		WHERE id=$1
		RETURNING ` + sgCols
	row := tx.QueryRow(ctx, q, sg.ID, sg.Name, sg.Description, labelsJSON, rulesJSON, sg.Status)
	result, err := scanSG(row)
	if err != nil {
		return nil, wrapPgErr(err, "SecurityGroup", sg.ID)
	}
	if err := emitVPC(ctx, tx, "SecurityGroup", result.ID, "UPDATED", securityGroupPayload(result)); err != nil {
		return nil, service.ErrInternal
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, wrapPgErr(err, "SecurityGroup", sg.ID)
	}
	return result, nil
}

func (r *SecurityGroupRepo) Delete(ctx context.Context, id string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return service.ErrInternal
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx, `DELETE FROM security_groups WHERE id = $1`, id)
	if err != nil {
		return wrapPgErr(err, "SecurityGroup", id)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: SecurityGroup %s not found", service.ErrNotFound, id)
	}
	if err := emitVPC(ctx, tx, "SecurityGroup", id, "DELETED", map[string]any{"id": id}); err != nil {
		return service.ErrInternal
	}
	if err := tx.Commit(ctx); err != nil {
		return wrapPgErr(err, "SecurityGroup", id)
	}
	return nil
}

// UpdateRules атомарно меняет набор правил SG: удаляет правила с id ∈ deleteIDs
// и добавляет новые add. Возвращает обновлённый SG.
//
// Optimistic concurrency: SELECT захватывает текущий resource_version, UPDATE
// проверяет его в WHERE. Concurrent UpdateRules → один из вызовов получит
// 0 rows → ErrFailedPrecondition "concurrent modification, please retry"
// (защита от lost-update, см. TODO #22).
func (r *SecurityGroupRepo) UpdateRules(ctx context.Context, sgID string, deleteIDs []string, add []domain.SecurityGroupRule) (*domain.SecurityGroup, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, service.ErrInternal
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Загрузить текущий список rules + xmin (txid версия row для optimistic CC).
	// xmin — Postgres system column, меняется на каждый UPDATE; не требует
	// дополнительной колонки в схеме.
	var rulesJSON []byte
	var rowXmin string
	err = tx.QueryRow(ctx, `SELECT rules, xmin::text FROM security_groups WHERE id = $1`, sgID).Scan(&rulesJSON, &rowXmin)
	if err != nil {
		return nil, wrapPgErr(err, "SecurityGroup", sgID)
	}
	var rules []domain.SecurityGroupRule
	if rulesJSON != nil {
		if err := json.Unmarshal(rulesJSON, &rules); err != nil {
			return nil, fmt.Errorf("%w: corrupted rules JSONB for SG %s: %v", service.ErrInternal, sgID, err)
		}
	}
	// фильтруем удаляемые
	if len(deleteIDs) > 0 {
		toDel := make(map[string]struct{}, len(deleteIDs))
		for _, id := range deleteIDs {
			toDel[id] = struct{}{}
		}
		filtered := rules[:0]
		for _, r := range rules {
			if _, drop := toDel[r.ID]; drop {
				continue
			}
			filtered = append(filtered, r)
		}
		rules = filtered
	}
	// добавляем новые
	rules = append(rules, add...)
	newRulesJSON, err := marshalJSONB(rules, "SecurityGroup.rules")
	if err != nil {
		return nil, err
	}

	q := fmt.Sprintf(`UPDATE security_groups SET rules = $2 WHERE id = $1 AND xmin::text = $3 RETURNING %s`, sgCols)
	row := tx.QueryRow(ctx, q, sgID, newRulesJSON, rowXmin)
	sg, err := scanSG(row)
	if err != nil {
		// pgx.ErrNoRows → concurrent modification (xmin не совпал).
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: SecurityGroup %s was modified concurrently, please retry",
				service.ErrFailedPrecondition, sgID)
		}
		return nil, wrapPgErr(err, "SecurityGroup", sgID)
	}
	if err := emitVPC(ctx, tx, "SecurityGroup", sg.ID, "UPDATED", securityGroupPayload(sg)); err != nil {
		return nil, service.ErrInternal
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, wrapPgErr(err, "SecurityGroup", sgID)
	}
	return sg, nil
}

// UpdateRule обновляет описание/labels единичного правила в SG.
// Возвращает SG с обновлённым правилом.
//
// Optimistic concurrency: см. UpdateRules. Concurrent UpdateRule на ту же SG →
// FailedPrecondition "concurrent modification, please retry".
func (r *SecurityGroupRepo) UpdateRule(ctx context.Context, sgID, ruleID, description string, labels map[string]string, mask []string) (*domain.SecurityGroup, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, service.ErrInternal
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var rulesJSON []byte
	var rowXmin string
	err = tx.QueryRow(ctx, `SELECT rules, xmin::text FROM security_groups WHERE id = $1`, sgID).Scan(&rulesJSON, &rowXmin)
	if err != nil {
		return nil, wrapPgErr(err, "SecurityGroup", sgID)
	}
	var rules []domain.SecurityGroupRule
	if rulesJSON != nil {
		if err := json.Unmarshal(rulesJSON, &rules); err != nil {
			return nil, fmt.Errorf("%w: corrupted rules JSONB for SG %s: %v", service.ErrInternal, sgID, err)
		}
	}
	found := false
	applyMask := len(mask) > 0
	maskSet := map[string]struct{}{}
	for _, m := range mask {
		maskSet[m] = struct{}{}
	}
	for i := range rules {
		if rules[i].ID != ruleID {
			continue
		}
		found = true
		if !applyMask {
			rules[i].Description = description
			rules[i].Labels = labels
		} else {
			if _, ok := maskSet["description"]; ok {
				rules[i].Description = description
			}
			if _, ok := maskSet["labels"]; ok {
				rules[i].Labels = labels
			}
		}
		break
	}
	if !found {
		return nil, fmt.Errorf("%w: SecurityGroupRule %s not found in SecurityGroup %s",
			service.ErrNotFound, ruleID, sgID)
	}
	newRulesJSON, err := marshalJSONB(rules, "SecurityGroup.rules")
	if err != nil {
		return nil, err
	}

	q := fmt.Sprintf(`UPDATE security_groups SET rules = $2 WHERE id = $1 AND xmin::text = $3 RETURNING %s`, sgCols)
	row := tx.QueryRow(ctx, q, sgID, newRulesJSON, rowXmin)
	sg, err := scanSG(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: SecurityGroup %s was modified concurrently, please retry",
				service.ErrFailedPrecondition, sgID)
		}
		return nil, wrapPgErr(err, "SecurityGroup", sgID)
	}
	if err := emitVPC(ctx, tx, "SecurityGroup", sg.ID, "UPDATED", securityGroupPayload(sg)); err != nil {
		return nil, service.ErrInternal
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, wrapPgErr(err, "SecurityGroup", sgID)
	}
	return sg, nil
}

func (r *SecurityGroupRepo) SetFolderID(ctx context.Context, id, folderID string) (*domain.SecurityGroup, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, service.ErrInternal
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := fmt.Sprintf(`UPDATE security_groups SET folder_id = $2 WHERE id = $1 RETURNING %s`, sgCols)
	row := tx.QueryRow(ctx, q, id, folderID)
	sg, err := scanSG(row)
	if err != nil {
		return nil, wrapPgErr(err, "SecurityGroup", id)
	}
	if err := emitVPC(ctx, tx, "SecurityGroup", sg.ID, "UPDATED", securityGroupPayload(sg)); err != nil {
		return nil, service.ErrInternal
	}
	if err := tx.Commit(ctx); err != nil {
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
	if err := unmarshalJSONB(labelsJSON, &sg.Labels, "SecurityGroup.labels"); err != nil {
		return nil, err
	}
	if err := unmarshalJSONB(rulesJSON, &sg.Rules, "SecurityGroup.rules"); err != nil {
		return nil, err
	}
	return &sg, nil
}
