// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package pg

import (
	"context"
	"encoding/json"
	"errors"
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

// securityGroupReader — Get/List поверх произвольной pgx.Tx (read-only или RW).
// SG построен на CQRS, чтобы Network.Create мог inline создать default-SG в
// одной writer-TX (Insert(Network) → Insert(SG) → SetDefaultSGID(Network)),
// закрывая orphan-SG window прежней схемы с раздельными TX (crash между ними
// оставлял либо orphan-SG, либо Network без default_security_group_id).
//
// SQL/scan-семантика опирается на общие shim'ы — `helpers.SGCols` /
// `helpers.ScanSG` / `helpers.WrapSGErr` / `helpers.NullableStr`.
type securityGroupReader struct {
	tx pgx.Tx
}

// Get — well-formed-but-absent → NotFound с
// "Security group SecurityGroup.Id(value=<id>) not found" (через WrapSGErr).
func (r *securityGroupReader) Get(ctx context.Context, id string) (*kacho.SecurityGroupRecord, error) {
	q := fmt.Sprintf(`SELECT %s FROM security_groups WHERE id = $1`, helpers.SGCols)
	row := r.tx.QueryRow(ctx, q, id)
	sg, err := helpers.ScanSG(row)
	if err != nil {
		return nil, helpers.WrapSGErr(err, id)
	}
	return sg, nil
}

// List — cursor-based pagination + filter.Parse (whitelist полей
// ["name","network_id"]).
func (r *securityGroupReader) List(ctx context.Context, f kacho.SecurityGroupFilter, p kacho.Pagination) ([]*kacho.SecurityGroupRecord, string, error) {
	pageSize, err := validate.PageSize("page_size", p.PageSize)
	if err != nil {
		return nil, "", err
	}

	args := []any{}
	conditions := []string{}
	argIdx := 1

	if f.ProjectID != "" {
		conditions = append(conditions, fmt.Sprintf("project_id = $%d", argIdx))
		args = append(args, f.ProjectID)
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
		ast, perr := filter.Parse(f.Filter, []string{"name", "network_id"})
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
	q := fmt.Sprintf(`SELECT %s FROM security_groups %s ORDER BY created_at ASC, id ASC LIMIT $%d`, helpers.SGCols, where, argIdx)
	args = append(args, pageSize+1)

	rows, err := r.tx.Query(ctx, q, args...)
	if err != nil {
		return nil, "", helpers.WrapSGErr(err, "")
	}
	defer rows.Close()

	var result []*kacho.SecurityGroupRecord
	for rows.Next() {
		sg, err := helpers.ScanSG(rows)
		if err != nil {
			return nil, "", helpers.WrapSGErr(err, "")
		}
		result = append(result, sg)
	}
	if err := rows.Err(); err != nil {
		return nil, "", helpers.WrapSGErr(err, "")
	}

	var nextToken string
	if int64(len(result)) > pageSize {
		last := result[pageSize-1]
		nextToken = helpers.EncodePageToken(last.CreatedAt, last.ID)
		result = result[:pageSize]
	}
	return result, nextToken, nil
}

// ListByIDs — List с safety-net `WHERE id = ANY($allowedIDs)` для listauthz-
// фильтрации. List-семантика (project_id/network_id/name/filter/cursor)
// сохраняется; allowed-set передается типизированным text[]-параметром
// (SQL-injection-safe). Pagination применяется к отфильтрованному набору. Пустой
// allowedIDs → (nil, "", nil) short-circuit первым стейтментом.
func (r *securityGroupReader) ListByIDs(ctx context.Context, f kacho.SecurityGroupFilter, allowedIDs []string, p kacho.Pagination) ([]*kacho.SecurityGroupRecord, string, error) {
	if len(allowedIDs) == 0 {
		return nil, "", nil
	}
	pageSize, err := validate.PageSize("page_size", p.PageSize)
	if err != nil {
		return nil, "", err
	}

	args := []any{allowedIDs}
	conditions := []string{"id = ANY($1::text[])"}
	argIdx := 2

	if f.ProjectID != "" {
		conditions = append(conditions, fmt.Sprintf("project_id = $%d", argIdx))
		args = append(args, f.ProjectID)
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
		ast, perr := filter.Parse(f.Filter, []string{"name", "network_id"})
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

	where := "WHERE " + strings.Join(conditions, " AND ")
	q := fmt.Sprintf(`SELECT %s FROM security_groups %s ORDER BY created_at ASC, id ASC LIMIT $%d`, helpers.SGCols, where, argIdx)
	args = append(args, pageSize+1)

	rows, err := r.tx.Query(ctx, q, args...)
	if err != nil {
		return nil, "", helpers.WrapSGErr(err, "")
	}
	defer rows.Close()

	var result []*kacho.SecurityGroupRecord
	for rows.Next() {
		sg, err := helpers.ScanSG(rows)
		if err != nil {
			return nil, "", helpers.WrapSGErr(err, "")
		}
		result = append(result, sg)
	}
	if err := rows.Err(); err != nil {
		return nil, "", helpers.WrapSGErr(err, "")
	}

	var nextToken string
	if int64(len(result)) > pageSize {
		last := result[pageSize-1]
		nextToken = helpers.EncodePageToken(last.CreatedAt, last.ID)
		result = result[:pageSize]
	}
	return result, nextToken, nil
}

// securityGroupWriter — DML над security_groups через writer-TX. Embeds
// securityGroupReader (writer видит свои writes).
//
// Writer НЕ emit'ит outbox сам — caller (use-case) делает
// `RepositoryWriter.Outbox().Emit(...)` явно после успешного DML. Это гарантирует,
// что outbox-write идет в той же pgx.Tx, и что Network.Create (которая делает 2
// DML — SG + Network.SetDefaultSGID — в одной writer-TX) эмитит правильную
// последовательность outbox-событий из use-case'а, не из «глубины» repo.
type securityGroupWriter struct {
	securityGroupReader
}

// Insert — INSERT security_groups RETURNING. network_id опционален: пустая строка
// → SQL NULL, иначе срабатывает FK на network.
//
// outbox-write — в use-case'е через `writer.Outbox().Emit(...)`.
func (w *securityGroupWriter) Insert(ctx context.Context, sg *domain.SecurityGroup) (*kacho.SecurityGroupRecord, error) {
	labelsJSON, err := helpers.MarshalJSONB(domain.LabelsToMap(sg.Labels), "SecurityGroup.labels")
	if err != nil {
		return nil, err
	}
	rulesJSON, err := helpers.MarshalJSONB(sg.Rules, "SecurityGroup.rules")
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	q := fmt.Sprintf(`
		INSERT INTO security_groups (id, project_id, network_id, created_at, name, description, labels, default_for_network, rules)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING %s`, helpers.SGCols)
	row := w.tx.QueryRow(ctx, q,
		sg.ID, sg.ProjectID, helpers.NullableStr(sg.NetworkID), now,
		string(sg.Name), string(sg.Description), labelsJSON,
		sg.DefaultForNetwork, rulesJSON,
	)
	result, err := helpers.ScanSG(row)
	if err != nil {
		return nil, helpers.WrapSGErr(err, string(sg.Name))
	}
	return result, nil
}

// Update — UPDATE security_groups RETURNING name/description/labels/rules.
// outbox-write — в use-case'е.
func (w *securityGroupWriter) Update(ctx context.Context, sg *domain.SecurityGroup) (*kacho.SecurityGroupRecord, error) {
	labelsJSON, err := helpers.MarshalJSONB(domain.LabelsToMap(sg.Labels), "SecurityGroup.labels")
	if err != nil {
		return nil, err
	}
	rulesJSON, err := helpers.MarshalJSONB(sg.Rules, "SecurityGroup.rules")
	if err != nil {
		return nil, err
	}

	q := fmt.Sprintf(`
		UPDATE security_groups SET name=$2, description=$3, labels=$4, rules=$5
		WHERE id=$1
		RETURNING %s`, helpers.SGCols)
	row := w.tx.QueryRow(ctx, q,
		sg.ID, string(sg.Name), string(sg.Description), labelsJSON, rulesJSON,
	)
	result, err := helpers.ScanSG(row)
	if err != nil {
		return nil, helpers.WrapSGErr(err, sg.ID)
	}
	return result, nil
}

// GetForUpdate — Get с row-lock (`FOR UPDATE`) в writer-TX. Сериализует
// конкурентный read-modify-write в Update: второй concurrent Update блокируется
// на GetForUpdate до commit первого, затем читает уже обновленный row (включая
// rules-JSONB) и применяет свою маску поверх — lost-update исключен.
// (UpdateRules/UpdateRule используют xmin-OCC отдельно.)
func (w *securityGroupWriter) GetForUpdate(ctx context.Context, id string) (*kacho.SecurityGroupRecord, error) {
	q := fmt.Sprintf(`SELECT %s FROM security_groups WHERE id = $1 FOR UPDATE`, helpers.SGCols)
	sg, err := helpers.ScanSG(w.tx.QueryRow(ctx, q, id))
	if err != nil {
		return nil, helpers.WrapSGErr(err, id)
	}
	return sg, nil
}

// Delete — DELETE security_groups WHERE id = $1. row not affected → ErrNotFound
// с текстом "Security group SecurityGroup.Id(value=<id>) not found".
//
// outbox-write (DELETED tombstone) — в use-case'е.
func (w *securityGroupWriter) Delete(ctx context.Context, id string) error {
	tag, err := w.tx.Exec(ctx, `DELETE FROM security_groups WHERE id = $1`, id)
	if err != nil {
		return helpers.WrapSGErr(err, id)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: Security group SecurityGroup.Id(value=%s) not found", helpers.ErrNotFound, id)
	}
	return nil
}

// UpdateRules атомарно меняет набор правил SG в текущей writer-TX. Optimistic
// concurrency через `xmin::text` snapshot — concurrent UpdateRules → один из
// вызовов получит 0 rows → ErrFailedPrecondition "concurrent modification".
//
// outbox-write — в use-case'е.
func (w *securityGroupWriter) UpdateRules(ctx context.Context, sgID string, deleteIDs []string, add []domain.SecurityGroupRule) (*kacho.SecurityGroupRecord, error) {
	var rulesJSON []byte
	var rowXmin string
	if err := w.tx.QueryRow(ctx, `SELECT rules, xmin::text FROM security_groups WHERE id = $1`, sgID).Scan(&rulesJSON, &rowXmin); err != nil {
		return nil, helpers.WrapSGErr(err, sgID)
	}
	var rules []domain.SecurityGroupRule
	if rulesJSON != nil {
		if err := json.Unmarshal(rulesJSON, &rules); err != nil {
			return nil, fmt.Errorf("%w: corrupted rules JSONB for SG %s: %v", helpers.ErrInternal, sgID, err)
		}
	}
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
	rules = append(rules, add...)
	newRulesJSON, err := helpers.MarshalJSONB(rules, "SecurityGroup.rules")
	if err != nil {
		return nil, err
	}

	q := fmt.Sprintf(`UPDATE security_groups SET rules = $2 WHERE id = $1 AND xmin::text = $3 RETURNING %s`, helpers.SGCols)
	row := w.tx.QueryRow(ctx, q, sgID, newRulesJSON, rowXmin)
	sg, err := helpers.ScanSG(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: SecurityGroup %s was modified concurrently, please retry",
				helpers.ErrFailedPrecondition, sgID)
		}
		return nil, helpers.WrapSGErr(err, sgID)
	}
	return sg, nil
}

// UpdateRule обновляет description/labels единичного правила в SG (xmin-OCC).
// Concurrent-modification → FailedPrecondition.
//
// outbox-write — в use-case'е.
func (w *securityGroupWriter) UpdateRule(ctx context.Context, sgID, ruleID, description string, labels map[string]string, mask []string) (*kacho.SecurityGroupRecord, error) {
	var rulesJSON []byte
	var rowXmin string
	if err := w.tx.QueryRow(ctx, `SELECT rules, xmin::text FROM security_groups WHERE id = $1`, sgID).Scan(&rulesJSON, &rowXmin); err != nil {
		return nil, helpers.WrapSGErr(err, sgID)
	}
	var rules []domain.SecurityGroupRule
	if rulesJSON != nil {
		if err := json.Unmarshal(rulesJSON, &rules); err != nil {
			return nil, fmt.Errorf("%w: corrupted rules JSONB for SG %s: %v", helpers.ErrInternal, sgID, err)
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
			helpers.ErrNotFound, ruleID, sgID)
	}
	newRulesJSON, err := helpers.MarshalJSONB(rules, "SecurityGroup.rules")
	if err != nil {
		return nil, err
	}

	q := fmt.Sprintf(`UPDATE security_groups SET rules = $2 WHERE id = $1 AND xmin::text = $3 RETURNING %s`, helpers.SGCols)
	row := w.tx.QueryRow(ctx, q, sgID, newRulesJSON, rowXmin)
	sg, err := helpers.ScanSG(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: SecurityGroup %s was modified concurrently, please retry",
				helpers.ErrFailedPrecondition, sgID)
		}
		return nil, helpers.WrapSGErr(err, sgID)
	}
	return sg, nil
}
