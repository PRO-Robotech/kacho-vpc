// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package pg

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/PRO-Robotech/kacho-corelib/validate"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/helpers"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// nicColsNI — helpers.NICCols с алиасом ni. Нужен в RETURNING у attach-CAS
// (`UPDATE network_interfaces ni … FROM subnets s …`): без алиаса RETURNING id/
// project_id/name/created_at был бы ambiguous (subnets несёт те же имена колонок).
const nicColsNI = `ni.id, ni.project_id, ni.created_at, ni.name, ni.description, ni.labels, ni.subnet_id,
	ni.v4_address_ids, ni.v6_address_ids, ni.security_group_ids, ni.used_by_type, ni.used_by_id, ni.used_by_name, ni.mac_address, ni.status`

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

// ListByInstanceIDs — batched read NIC-привязок по набору instance_id
// (used_by_type='compute_instance' AND used_by_id = ANY). Один запрос на всё
// множество (не N+1) для compute-side зеркала Instance.Get/List. Каждая запись
// несёт instance-local Index (used_by_index) + денормализованное зеркало адресации
// (primary v4/v6 адрес резолвится LEFT JOIN на первый v4/v6 Address-ресурс NIC —
// его IP лежит в jsonb-spec `->>'address'`). Пустой набор → (nil, nil).
func (r *networkInterfaceReader) ListByInstanceIDs(ctx context.Context, instanceIDs []string) ([]*kacho.NetworkInterfaceAttachment, error) {
	if len(instanceIDs) == 0 {
		return nil, nil
	}
	const q = `
		SELECT ni.id, ni.used_by_id, COALESCE(ni.used_by_index, 0), ni.subnet_id,
		       ni.security_group_ids, ni.mac_address,
		       COALESCE(av4.internal_ipv4->>'address', av4.external_ipv4->>'address', ''),
		       COALESCE(av6.internal_ipv6->>'address', av6.external_ipv6->>'address', '')
		  FROM network_interfaces ni
		  LEFT JOIN addresses av4 ON av4.id = (ni.v4_address_ids->>0)
		  LEFT JOIN addresses av6 ON av6.id = (ni.v6_address_ids->>0)
		 WHERE ni.used_by_type = 'compute_instance' AND ni.used_by_id = ANY($1::text[])
		 ORDER BY ni.used_by_id ASC, ni.used_by_index ASC`
	rows, err := r.tx.Query(ctx, q, instanceIDs)
	if err != nil {
		return nil, helpers.WrapPgErr(err, "Network interface", "")
	}
	defer rows.Close()
	var out []*kacho.NetworkInterfaceAttachment
	for rows.Next() {
		var a kacho.NetworkInterfaceAttachment
		var sgJSON []byte
		if err := rows.Scan(&a.NICID, &a.InstanceID, &a.Index, &a.SubnetID, &sgJSON, &a.MAC,
			&a.PrimaryV4Address, &a.PrimaryV6Address); err != nil {
			return nil, helpers.WrapPgErr(err, "Network interface", "")
		}
		if err := helpers.UnmarshalJSONB(sgJSON, &a.SecurityGroupIDs, "NetworkInterface.security_group_ids"); err != nil {
			return nil, err
		}
		out = append(out, &a)
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

// AttachToInstance — атомарный CAS NIC↔Instance (self-describing; vpc валидирует
// СВОИ строки ni+subnet, НЕ зовёт compute — ацикличность, KAC-266). Single-statement
// UPDATE … FROM subnets:
//   - `used_by_id=” OR =$instance` — свободен ИЛИ уже наш (идемпотентный replay);
//   - `project_id=$project` — project-coherence (из self-describing payload);
//   - `placement_type='REGIONAL' OR zone_id=$instance_zone` — zone-coherence с
//     anycast-исключением (REGIONAL-subnet зоны не несёт → zone-check пропущен).
//
// used_by_index: CASE сохраняет существующий слот при replay (used_by_id уже наш),
// иначе назначает $slot (явный или вычисленный первый свободный). Слот-уникальность
// держит partial UNIQUE(used_by_id, used_by_index) → 23505 при коллизии →
// ErrNICIndexTaken (service retry для auto-index). 0 rows → disambiguation.
func (w *networkInterfaceWriter) AttachToInstance(ctx context.Context, p kacho.AttachNICParams) (*kacho.NetworkInterfaceRecord, error) {
	slot := p.Index
	if slot < 0 { // AutoIndex — первый свободный слот на инстансе (в этой же TX)
		free, err := w.firstFreeSlot(ctx, p.InstanceID)
		if err != nil {
			return nil, err
		}
		slot = free
	}
	q := fmt.Sprintf(`
		UPDATE network_interfaces ni
		   SET used_by_id    = $2,
		       used_by_type  = 'compute_instance',
		       used_by_name  = $3,
		       used_by_index = CASE WHEN ni.used_by_id = $2 THEN ni.used_by_index ELSE $6 END,
		       status        = 'ACTIVE'
		  FROM subnets s
		 WHERE ni.id = $1
		   AND s.id = ni.subnet_id
		   AND (ni.used_by_id = '' OR ni.used_by_id = $2)
		   AND ni.project_id = $4
		   AND (s.placement_type = 'REGIONAL' OR s.zone_id = $5)
		RETURNING %s`, nicColsNI)
	rec, err := helpers.ScanNI(w.tx.QueryRow(ctx, q,
		p.NICID, p.InstanceID, p.InstanceName, p.ProjectID, p.InstanceZoneID, slot))
	if err != nil {
		if helpers.IsNICIndexCollision(err) {
			return nil, helpers.ErrNICIndexTaken
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, w.disambiguateAttach(ctx, p)
		}
		return nil, helpers.WrapPgErr(err, "Network interface", p.NICID)
	}
	return rec, nil
}

// firstFreeSlot — первый свободный used_by_index на инстансе (0-based). Читает
// занятые слоты в текущей writer-TX; concurrency (два fresh NIC → один слот) держит
// partial UNIQUE + retry в service. min-gap: среди N занятых всегда есть свободный в 0..N.
func (w *networkInterfaceWriter) firstFreeSlot(ctx context.Context, instanceID string) (int32, error) {
	rows, err := w.tx.Query(ctx,
		`SELECT used_by_index FROM network_interfaces WHERE used_by_id = $1 AND used_by_index IS NOT NULL`,
		instanceID)
	if err != nil {
		return 0, helpers.WrapPgErr(err, "Network interface", "")
	}
	defer rows.Close()
	used := map[int32]bool{}
	for rows.Next() {
		var idx int32
		if err := rows.Scan(&idx); err != nil {
			return 0, helpers.WrapPgErr(err, "Network interface", "")
		}
		used[idx] = true
	}
	if err := rows.Err(); err != nil {
		return 0, helpers.WrapPgErr(err, "Network interface", "")
	}
	for s := int32(0); ; s++ {
		if !used[s] {
			return s, nil
		}
	}
}

// disambiguateAttach — разбор 0-row исхода attach-CAS в той же TX: читает NIC+subnet
// и определяет причину. Порядок: not-found → in-use → project-mismatch → zone-mismatch.
func (w *networkInterfaceWriter) disambiguateAttach(ctx context.Context, p kacho.AttachNICParams) error {
	var usedByID, projectID, placement, zoneID string
	err := w.tx.QueryRow(ctx,
		`SELECT ni.used_by_id, ni.project_id, s.placement_type, s.zone_id
		   FROM network_interfaces ni JOIN subnets s ON s.id = ni.subnet_id
		  WHERE ni.id = $1`, p.NICID).Scan(&usedByID, &projectID, &placement, &zoneID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: Network interface %s not found", helpers.ErrNotFound, p.NICID)
		}
		return helpers.WrapPgErr(err, "Network interface", p.NICID)
	}
	switch {
	case usedByID != "" && usedByID != p.InstanceID:
		return helpers.ErrNICInUse
	case projectID != p.ProjectID:
		// project-mismatch — обычно ловит object-scoped authz раньше (M5); здесь fail-closed.
		return fmt.Errorf("%w: network interface project mismatch", helpers.ErrFailedPrecondition)
	case placement == string(domain.PlacementZonal) && zoneID != p.InstanceZoneID:
		return &helpers.NICZoneMismatchError{SubnetZone: zoneID, InstanceZone: p.InstanceZoneID}
	default:
		return fmt.Errorf("%w: network interface attach precondition", helpers.ErrFailedPrecondition)
	}
}

// DetachFromInstance — идемпотентное снятие привязки NIC↔Instance. UPDATE clears
// used_by_* + used_by_index=NULL, status='AVAILABLE' WHERE id=$nic AND used_by_id=
// $instance. 1 row → отвязан; 0 rows → Get(nic): существует → идемпотентный OK
// (уже отвязан / привязан к другому — возвращается как есть); нет → ErrNotFound.
func (w *networkInterfaceWriter) DetachFromInstance(ctx context.Context, nicID, instanceID string) (*kacho.NetworkInterfaceRecord, error) {
	q := fmt.Sprintf(`
		UPDATE network_interfaces
		   SET used_by_id='', used_by_type='', used_by_name='', used_by_index=NULL, status='AVAILABLE'
		 WHERE id = $1 AND used_by_id = $2
		RETURNING %s`, helpers.NICCols)
	rec, err := helpers.ScanNI(w.tx.QueryRow(ctx, q, nicID, instanceID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Идемпотентно: NIC не привязан к этому инстансу (или уже отвязан). Возвращаем
			// текущее состояние; отсутствие NIC → ErrNotFound (через Get→WrapPgErr).
			return w.Get(ctx, nicID)
		}
		return nil, helpers.WrapPgErr(err, "Network interface", nicID)
	}
	return rec, nil
}
