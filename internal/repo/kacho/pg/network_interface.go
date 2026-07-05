// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package pg

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/PRO-Robotech/kacho-corelib/validate"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/helpers"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// networkInterfaceReader — Get/List/ListBySubnet поверх произвольной pgx.Tx
// (read-only или RW). NIC ведется в CQRS-модели поверх единой writer-TX, чтобы
// при NIC.Create обновление `addresses.used`/`address_references` шло в той же
// TX, что INSERT(NIC) — address не остается помеченным как used, если INSERT(NIC)
// откатился.
type networkInterfaceReader struct {
	tx pgx.Tx
}

// Get — well-formed-but-absent → NotFound с "Network interface <id> not found"
// (через WrapPgErr).
func (r *networkInterfaceReader) Get(ctx context.Context, id string) (*kacho.NetworkInterfaceRecord, error) {
	q := fmt.Sprintf(`SELECT %s FROM network_interfaces WHERE id = $1`, helpers.NICCols)
	row := r.tx.QueryRow(ctx, q, id)
	n, err := helpers.ScanNI(row)
	if err != nil {
		return nil, helpers.WrapPgErr(err, "Network interface", id)
	}
	return n, nil
}

// List — project_id required + cursor-based pagination + denormalised instance_id
// filter (used_by_type='compute_instance' AND used_by_id=$instance). NetworkID
// игнорируется — NIC не хранит network_id.
func (r *networkInterfaceReader) List(ctx context.Context, f kacho.NetworkInterfaceFilter, p kacho.Pagination) ([]*kacho.NetworkInterfaceRecord, string, error) {
	pageSize, err := validate.PageSize("page_size", p.PageSize)
	if err != nil {
		return nil, "", err
	}

	args := []any{f.ProjectID}
	conds := []string{"project_id = $1"}
	add := func(col, val string) {
		if val == "" {
			return
		}
		args = append(args, val)
		conds = append(conds, fmt.Sprintf("%s = $%d", col, len(args)))
	}
	if f.InstanceID != "" {
		args = append(args, "compute_instance")
		conds = append(conds, fmt.Sprintf("used_by_type = $%d", len(args)))
		args = append(args, f.InstanceID)
		conds = append(conds, fmt.Sprintf("used_by_id = $%d", len(args)))
	}
	add("subnet_id", f.SubnetID)
	if p.PageToken != "" {
		ts, id, derr := helpers.DecodePageToken(p.PageToken)
		if derr != nil {
			return nil, "", helpers.InvalidPageTokenErr(derr)
		}
		args = append(args, ts, id)
		conds = append(conds, fmt.Sprintf("(created_at, id) > ($%d, $%d)", len(args)-1, len(args)))
	}
	args = append(args, pageSize+1)
	q := fmt.Sprintf(`SELECT %s FROM network_interfaces WHERE %s ORDER BY created_at ASC, id ASC LIMIT $%d`,
		helpers.NICCols, strings.Join(conds, " AND "), len(args))

	rows, err := r.tx.Query(ctx, q, args...)
	if err != nil {
		return nil, "", helpers.WrapPgErr(err, "Network interface", "")
	}
	defer rows.Close()
	var out []*kacho.NetworkInterfaceRecord
	for rows.Next() {
		n, err := helpers.ScanNI(rows)
		if err != nil {
			return nil, "", helpers.WrapPgErr(err, "Network interface", "")
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		return nil, "", helpers.WrapPgErr(err, "Network interface", "")
	}
	var next string
	if int64(len(out)) > pageSize {
		last := out[pageSize-1]
		next = helpers.EncodePageToken(last.CreatedAt, last.ID)
		out = out[:pageSize]
	}
	return out, next, nil
}

// ListByIDs — List с safety-net `WHERE id = ANY($allowedIDs)`.
//
// Семантика List (project_id required + instance_id denorm-фильтр на used_by +
// subnet_id + cursor) сохраняется; добавляется типизированный text[]-параметр
// (SQL-injection-safe). NetworkID игнорируется (как в List — NIC не хранит
// network_id). Pagination применяется к отфильтрованному набору. Пустой
// allowedIDs → (nil, "", nil).
func (r *networkInterfaceReader) ListByIDs(ctx context.Context, f kacho.NetworkInterfaceFilter, allowedIDs []string, p kacho.Pagination) ([]*kacho.NetworkInterfaceRecord, string, error) {
	if len(allowedIDs) == 0 {
		return nil, "", nil
	}
	pageSize, err := validate.PageSize("page_size", p.PageSize)
	if err != nil {
		return nil, "", err
	}

	args := []any{allowedIDs, f.ProjectID}
	conds := []string{"id = ANY($1::text[])", "project_id = $2"}
	add := func(col, val string) {
		if val == "" {
			return
		}
		args = append(args, val)
		conds = append(conds, fmt.Sprintf("%s = $%d", col, len(args)))
	}
	if f.InstanceID != "" {
		args = append(args, "compute_instance")
		conds = append(conds, fmt.Sprintf("used_by_type = $%d", len(args)))
		args = append(args, f.InstanceID)
		conds = append(conds, fmt.Sprintf("used_by_id = $%d", len(args)))
	}
	add("subnet_id", f.SubnetID)
	if p.PageToken != "" {
		ts, id, derr := helpers.DecodePageToken(p.PageToken)
		if derr != nil {
			return nil, "", helpers.InvalidPageTokenErr(derr)
		}
		args = append(args, ts, id)
		conds = append(conds, fmt.Sprintf("(created_at, id) > ($%d, $%d)", len(args)-1, len(args)))
	}
	args = append(args, pageSize+1)
	q := fmt.Sprintf(`SELECT %s FROM network_interfaces WHERE %s ORDER BY created_at ASC, id ASC LIMIT $%d`,
		helpers.NICCols, strings.Join(conds, " AND "), len(args))

	rows, err := r.tx.Query(ctx, q, args...)
	if err != nil {
		return nil, "", helpers.WrapPgErr(err, "Network interface", "")
	}
	defer rows.Close()
	var out []*kacho.NetworkInterfaceRecord
	for rows.Next() {
		n, err := helpers.ScanNI(rows)
		if err != nil {
			return nil, "", helpers.WrapPgErr(err, "Network interface", "")
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		return nil, "", helpers.WrapPgErr(err, "Network interface", "")
	}
	var next string
	if int64(len(out)) > pageSize {
		last := out[pageSize-1]
		next = helpers.EncodePageToken(last.CreatedAt, last.ID)
		out = out[:pageSize]
	}
	return out, next, nil
}

// ListBySubnet возвращает все NIC, привязанные к указанной подсети. Нужен для
// precondition Subnet.Delete (FK RESTRICT на subnets). Не paginated (Subnet с
// >1000 NIC — edge-case).
func (r *networkInterfaceReader) ListBySubnet(ctx context.Context, subnetID string) ([]*kacho.NetworkInterfaceRecord, error) {
	rows, err := r.tx.Query(ctx,
		fmt.Sprintf(`SELECT %s FROM network_interfaces WHERE subnet_id = $1 ORDER BY id ASC`, helpers.NICCols),
		subnetID)
	if err != nil {
		return nil, helpers.WrapPgErr(err, "Network interface", "")
	}
	defer rows.Close()
	var out []*kacho.NetworkInterfaceRecord
	for rows.Next() {
		n, err := helpers.ScanNI(rows)
		if err != nil {
			return nil, helpers.WrapPgErr(err, "Network interface", "")
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		return nil, helpers.WrapPgErr(err, "Network interface", "")
	}
	return out, nil
}

// networkInterfaceWriter — DML над network_interfaces через writer-TX. Embeds
// networkInterfaceReader, так что writer видит свои writes.
//
// Writer НЕ emit'ит outbox сам — caller (use-case) делает
// `RepositoryWriter.Outbox().Emit(...)` явно после успешного DML. Это
// гарантирует, что outbox-write идет в той же pgx.Tx.
type networkInterfaceWriter struct {
	networkInterfaceReader
}

// Insert — INSERT network_interfaces RETURNING. MAC должен быть проставлен
// caller'ом (use-case аллоцирует через `macutil.GenerateMAC`).
//
// Cloud-wide UNIQUE на mac_address (constraint `network_interfaces_mac_address_key`)
// — при коллизии возвращаем `helpers.ErrMacCollision` (caller retry'ит с новым
// MAC). Прочие нарушения (project/name UNIQUE, FK subnet_id) — `WrapPgErr` →
// ErrAlreadyExists / ErrFailedPrecondition.
//
// outbox-write — в use-case'е через `writer.Outbox().Emit(...)`.
func (w *networkInterfaceWriter) Insert(ctx context.Context, n *domain.NetworkInterface) (*kacho.NetworkInterfaceRecord, error) {
	labelsJSON, err := helpers.MarshalJSONB(domain.LabelsToMap(n.Labels), "NetworkInterface.labels")
	if err != nil {
		return nil, err
	}
	sgJSON, err := helpers.MarshalJSONB(helpers.OrEmptyStrSlice(n.SecurityGroupIDs), "NetworkInterface.security_group_ids")
	if err != nil {
		return nil, err
	}
	v4IDsJSON, err := helpers.MarshalJSONB(helpers.OrEmptyStrSlice(n.V4AddressIDs), "NetworkInterface.v4_address_ids")
	if err != nil {
		return nil, err
	}
	v6IDsJSON, err := helpers.MarshalJSONB(helpers.OrEmptyStrSlice(n.V6AddressIDs), "NetworkInterface.v6_address_ids")
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	q := fmt.Sprintf(`
		INSERT INTO network_interfaces (id, project_id, created_at, name, description, labels, subnet_id,
			v4_address_ids, v6_address_ids, security_group_ids, used_by_type, used_by_id, used_by_name, mac_address, status)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
		RETURNING %s`, helpers.NICCols)
	row := w.tx.QueryRow(ctx, q,
		n.ID, n.ProjectID, now, string(n.Name), string(n.Description), labelsJSON, n.SubnetID,
		v4IDsJSON, v6IDsJSON, sgJSON,
		n.UsedByType, n.UsedByID, n.UsedByName, n.MAC, helpers.NIStatusName(n.Status))
	rec, err := helpers.ScanNI(row)
	if err != nil {
		if helpers.IsNICMacCollision(err) {
			return nil, helpers.ErrMacCollision
		}
		return nil, helpers.WrapPgErr(err, "Network interface", string(n.Name))
	}
	return rec, nil
}

// GetForUpdate — Get с row-lock (`FOR UPDATE`) в writer-TX. Сериализует
// конкурентный read-modify-write в UpdateMeta (doUpdate): второй concurrent
// Update блокируется на GetForUpdate до commit первого, затем читает уже
// обновлённый row и применяет свою маску поверх — lost-update mutable-колонок
// NIC исключён (project-rule #10).
func (w *networkInterfaceWriter) GetForUpdate(ctx context.Context, id string) (*kacho.NetworkInterfaceRecord, error) {
	q := fmt.Sprintf(`SELECT %s FROM network_interfaces WHERE id = $1 FOR UPDATE`, helpers.NICCols)
	n, err := helpers.ScanNI(w.tx.QueryRow(ctx, q, id))
	if err != nil {
		return nil, helpers.WrapPgErr(err, "Network interface", id)
	}
	return n, nil
}

// UpdateMeta — UPDATE name/description/labels/security_group_ids/v4_address_ids/v6_address_ids.
// outbox-write — в use-case'е.
func (w *networkInterfaceWriter) UpdateMeta(ctx context.Context, n *domain.NetworkInterface) (*kacho.NetworkInterfaceRecord, error) {
	labelsJSON, err := helpers.MarshalJSONB(domain.LabelsToMap(n.Labels), "NetworkInterface.labels")
	if err != nil {
		return nil, err
	}
	sgJSON, err := helpers.MarshalJSONB(helpers.OrEmptyStrSlice(n.SecurityGroupIDs), "NetworkInterface.security_group_ids")
	if err != nil {
		return nil, err
	}
	v4IDsJSON, err := helpers.MarshalJSONB(helpers.OrEmptyStrSlice(n.V4AddressIDs), "NetworkInterface.v4_address_ids")
	if err != nil {
		return nil, err
	}
	v6IDsJSON, err := helpers.MarshalJSONB(helpers.OrEmptyStrSlice(n.V6AddressIDs), "NetworkInterface.v6_address_ids")
	if err != nil {
		return nil, err
	}
	q := fmt.Sprintf(`
		UPDATE network_interfaces SET name=$2, description=$3, labels=$4, security_group_ids=$5, v4_address_ids=$6, v6_address_ids=$7
		WHERE id=$1
		RETURNING %s`, helpers.NICCols)
	row := w.tx.QueryRow(ctx, q, n.ID, string(n.Name), string(n.Description), labelsJSON, sgJSON, v4IDsJSON, v6IDsJSON)
	rec, err := helpers.ScanNI(row)
	if err != nil {
		return nil, helpers.WrapPgErr(err, "Network interface", n.ID)
	}
	return rec, nil
}

// Delete — DELETE network_interfaces WHERE id = $1. row not affected →
// ErrNotFound. NIC не имеет children FK (нет ON DELETE cascade из NIC), но
// имеет parent FK на subnets — он срабатывает не при удалении NIC, а только
// при удалении Subnet (RESTRICT).
//
// outbox-write (DELETED tombstone) — в use-case'е.
func (w *networkInterfaceWriter) Delete(ctx context.Context, id string) error {
	tag, err := w.tx.Exec(ctx, `DELETE FROM network_interfaces WHERE id = $1`, id)
	if err != nil {
		return helpers.WrapPgErr(err, "Network interface", id)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: Network interface %s not found", helpers.ErrNotFound, id)
	}
	return nil
}
