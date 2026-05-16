package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PRO-Robotech/kacho-corelib/filter"
	"github.com/PRO-Robotech/kacho-corelib/validate"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// SecurityGroup — type-alias на kachorepo.SecurityGroupRecord (repo-entity с
// DB-managed CreatedAt). Wave 5 D.1 (KAC-94): переехало в repo-leaf
// `internal/repo/kacho/entity_security_group.go`; этот alias остался для
// обратной совместимости с legacy `*repo.SecurityGroupRepo`-консьюмерами.
type SecurityGroup = kachorepo.SecurityGroupRecord

// SecurityGroupRepo — реализация SecurityGroupRepoIface поверх pgxpool.
type SecurityGroupRepo struct {
	pool *pgxpool.Pool
}

// NewSecurityGroupRepo создаёт SecurityGroupRepo.
func NewSecurityGroupRepo(pool *pgxpool.Pool) *SecurityGroupRepo {
	return &SecurityGroupRepo{pool: pool}
}

// wrapSGErr — как wrapPgErr, но для not-found использует verbatim-YC формат
// "Security group SecurityGroup.Id(value=<id>) not found" (probe 2026-05-11,
// kacho-vpc#10). Остальные классы ошибок — через wrapPgErr.
func wrapSGErr(err error, id string) error {
	if errors.Is(err, pgx.ErrNoRows) && id != "" {
		return fmt.Errorf("%w: Security group SecurityGroup.Id(value=%s) not found", ErrNotFound, id)
	}
	return wrapPgErr(err, "SecurityGroup", id)
}

const sgCols = `id, folder_id, network_id, created_at, name, description, labels, status, default_for_network, rules`

func (r *SecurityGroupRepo) Get(ctx context.Context, id string) (*SecurityGroup, error) {
	q := fmt.Sprintf(`SELECT %s FROM security_groups WHERE id = $1`, sgCols)
	row := r.pool.QueryRow(ctx, q, id)
	sg, err := scanSG(row)
	if err != nil {
		return nil, wrapSGErr(err, id)
	}
	return sg, nil
}

func (r *SecurityGroupRepo) List(ctx context.Context, f SecurityGroupFilter, p Pagination) ([]*SecurityGroup, string, error) {
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
		// network_id поддерживается в filter (см. security_group_service.proto:
		// "filter by network_id is here") — позволяет отделить unbound (NULL
		// network_id) SG от привязанных. NULL-строки в равенстве network_id='<id>'
		// не матчатся, что и требуется.
		ast, perr := filter.Parse(f.Filter, []string{"name", "network_id"})
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
		return nil, "", wrapSGErr(err, "")
	}
	defer rows.Close()

	var result []*SecurityGroup
	for rows.Next() {
		sg, err := scanSG(rows)
		if err != nil {
			return nil, "", wrapSGErr(err, "")
		}
		result = append(result, sg)
	}
	if err := rows.Err(); err != nil {
		return nil, "", wrapSGErr(err, "")
	}

	var nextToken string
	if int64(len(result)) > pageSize {
		last := result[pageSize-1]
		nextToken = encodePageToken(last.CreatedAt, last.ID)
		result = result[:pageSize]
	}
	return result, nextToken, nil
}

// Insert вставляет SG. Принимает domain.SecurityGroup (без CreatedAt — repo сам
// выставит `now()` для детерминированности тестов; source of truth — БД-колонка).
// Возвращает *SecurityGroup (= *kachorepo.SecurityGroupRecord).
func (r *SecurityGroupRepo) Insert(ctx context.Context, sg *domain.SecurityGroup) (*SecurityGroup, error) {
	labelsJSON, err := marshalJSONB(domain.LabelsToMap(sg.Labels), "SecurityGroup.labels")
	if err != nil {
		return nil, err
	}
	rulesJSON, err := marshalJSONB(sg.Rules, "SecurityGroup.rules")
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
		INSERT INTO security_groups (id, folder_id, network_id, created_at, name, description, labels, status, default_for_network, rules)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING ` + sgCols
	row := tx.QueryRow(ctx, q,
		// network_id опционален (kacho-proto#8): пустая строка → SQL NULL, иначе
		// FK security_groups_network_id_fkey сработал бы на '' (нет такой сети).
		sg.ID, sg.FolderID, nullableStr(sg.NetworkID), now, string(sg.Name), string(sg.Description), labelsJSON, string(sg.Status), sg.DefaultForNetwork, rulesJSON,
	)
	result, err := scanSG(row)
	if err != nil {
		return nil, wrapSGErr(err, string(sg.Name))
	}
	if err := emitVPC(ctx, tx, "SecurityGroup", result.ID, "CREATED", securityGroupPayload(result)); err != nil {
		return nil, ErrInternal
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, wrapSGErr(err, string(sg.Name))
	}
	return result, nil
}

// Update обновляет mutable-поля SG. Принимает domain.SecurityGroup (без CreatedAt).
func (r *SecurityGroupRepo) Update(ctx context.Context, sg *domain.SecurityGroup) (*SecurityGroup, error) {
	labelsJSON, err := marshalJSONB(domain.LabelsToMap(sg.Labels), "SecurityGroup.labels")
	if err != nil {
		return nil, err
	}
	rulesJSON, err := marshalJSONB(sg.Rules, "SecurityGroup.rules")
	if err != nil {
		return nil, err
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, ErrInternal
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const q = `
		UPDATE security_groups SET name=$2, description=$3, labels=$4, rules=$5, status=$6
		WHERE id=$1
		RETURNING ` + sgCols
	row := tx.QueryRow(ctx, q, sg.ID, string(sg.Name), string(sg.Description), labelsJSON, rulesJSON, string(sg.Status))
	result, err := scanSG(row)
	if err != nil {
		return nil, wrapSGErr(err, sg.ID)
	}
	if err := emitVPC(ctx, tx, "SecurityGroup", result.ID, "UPDATED", securityGroupPayload(result)); err != nil {
		return nil, ErrInternal
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, wrapSGErr(err, sg.ID)
	}
	return result, nil
}

func (r *SecurityGroupRepo) Delete(ctx context.Context, id string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return ErrInternal
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx, `DELETE FROM security_groups WHERE id = $1`, id)
	if err != nil {
		return wrapSGErr(err, id)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: Security group SecurityGroup.Id(value=%s) not found", ErrNotFound, id)
	}
	if err := emitVPC(ctx, tx, "SecurityGroup", id, "DELETED", map[string]any{"id": id}); err != nil {
		return ErrInternal
	}
	if err := tx.Commit(ctx); err != nil {
		return wrapSGErr(err, id)
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
func (r *SecurityGroupRepo) UpdateRules(ctx context.Context, sgID string, deleteIDs []string, add []domain.SecurityGroupRule) (*SecurityGroup, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, ErrInternal
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Загрузить текущий список rules + xmin (txid версия row для optimistic CC).
	// xmin — Postgres system column, меняется на каждый UPDATE; не требует
	// дополнительной колонки в схеме.
	var rulesJSON []byte
	var rowXmin string
	err = tx.QueryRow(ctx, `SELECT rules, xmin::text FROM security_groups WHERE id = $1`, sgID).Scan(&rulesJSON, &rowXmin)
	if err != nil {
		return nil, wrapSGErr(err, sgID)
	}
	var rules []domain.SecurityGroupRule
	if rulesJSON != nil {
		if err := json.Unmarshal(rulesJSON, &rules); err != nil {
			return nil, fmt.Errorf("%w: corrupted rules JSONB for SG %s: %v", ErrInternal, sgID, err)
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
				ErrFailedPrecondition, sgID)
		}
		return nil, wrapSGErr(err, sgID)
	}
	if err := emitVPC(ctx, tx, "SecurityGroup", sg.ID, "UPDATED", securityGroupPayload(sg)); err != nil {
		return nil, ErrInternal
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, wrapSGErr(err, sgID)
	}
	return sg, nil
}

// UpdateRule обновляет описание/labels единичного правила в SG.
// Возвращает SG с обновлённым правилом.
//
// Optimistic concurrency: см. UpdateRules. Concurrent UpdateRule на ту же SG →
// FailedPrecondition "concurrent modification, please retry".
func (r *SecurityGroupRepo) UpdateRule(ctx context.Context, sgID, ruleID, description string, labels map[string]string, mask []string) (*SecurityGroup, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, ErrInternal
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var rulesJSON []byte
	var rowXmin string
	err = tx.QueryRow(ctx, `SELECT rules, xmin::text FROM security_groups WHERE id = $1`, sgID).Scan(&rulesJSON, &rowXmin)
	if err != nil {
		return nil, wrapSGErr(err, sgID)
	}
	var rules []domain.SecurityGroupRule
	if rulesJSON != nil {
		if err := json.Unmarshal(rulesJSON, &rules); err != nil {
			return nil, fmt.Errorf("%w: corrupted rules JSONB for SG %s: %v", ErrInternal, sgID, err)
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
			rules[i].Description = domain.RcDescription(description)
			rules[i].Labels = labels
		} else {
			if _, ok := maskSet["description"]; ok {
				rules[i].Description = domain.RcDescription(description)
			}
			if _, ok := maskSet["labels"]; ok {
				rules[i].Labels = labels
			}
		}
		break
	}
	if !found {
		return nil, fmt.Errorf("%w: SecurityGroupRule %s not found in SecurityGroup %s",
			ErrNotFound, ruleID, sgID)
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
				ErrFailedPrecondition, sgID)
		}
		return nil, wrapSGErr(err, sgID)
	}
	if err := emitVPC(ctx, tx, "SecurityGroup", sg.ID, "UPDATED", securityGroupPayload(sg)); err != nil {
		return nil, ErrInternal
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, wrapSGErr(err, sgID)
	}
	return sg, nil
}

func (r *SecurityGroupRepo) SetFolderID(ctx context.Context, id, folderID string) (*SecurityGroup, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, ErrInternal
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := fmt.Sprintf(`UPDATE security_groups SET folder_id = $2 WHERE id = $1 RETURNING %s`, sgCols)
	row := tx.QueryRow(ctx, q, id, folderID)
	sg, err := scanSG(row)
	if err != nil {
		return nil, wrapSGErr(err, id)
	}
	if err := emitVPC(ctx, tx, "SecurityGroup", sg.ID, "UPDATED", securityGroupPayload(sg)); err != nil {
		return nil, ErrInternal
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, wrapSGErr(err, id)
	}
	return sg, nil
}

// ---- scan ----

func scanSG(row scannable) (*SecurityGroup, error) {
	var sg SecurityGroup
	var labelsJSON []byte
	var rulesJSON []byte
	var networkID *string // nullable (kacho-proto#8: unbound / folder-level SG)
	var name, description, statusStr string

	err := row.Scan(
		&sg.ID, &sg.FolderID, &networkID, &sg.CreatedAt, &name, &description, &labelsJSON, &statusStr, &sg.DefaultForNetwork, &rulesJSON,
	)
	if err != nil {
		return nil, err
	}
	if networkID != nil {
		sg.NetworkID = *networkID
	}
	sg.Name = domain.RcNameVPC(name)
	sg.Description = domain.RcDescription(description)
	sg.Status = domain.SecurityGroupStatus(statusStr)
	var labels map[string]string
	if err := unmarshalJSONB(labelsJSON, &labels, "SecurityGroup.labels"); err != nil {
		return nil, err
	}
	sg.Labels = domain.LabelsFromMap(labels)
	if err := unmarshalJSONB(rulesJSON, &sg.Rules, "SecurityGroup.rules"); err != nil {
		return nil, err
	}
	return &sg, nil
}
