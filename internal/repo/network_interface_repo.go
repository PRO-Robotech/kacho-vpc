package repo

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PRO-Robotech/kacho-corelib/validate"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/service"
)

// NetworkInterfaceRepo — реализация service.NetworkInterfaceRepo поверх pgxpool.
type NetworkInterfaceRepo struct {
	pool *pgxpool.Pool
}

// NewNetworkInterfaceRepo создаёт NetworkInterfaceRepo.
func NewNetworkInterfaceRepo(pool *pgxpool.Pool) *NetworkInterfaceRepo {
	return &NetworkInterfaceRepo{pool: pool}
}

const niCols = `id, folder_id, created_at, name, description, labels, subnet_id, network_id,
	v4_address_ids, v6_address_ids, security_group_ids, instance_id, ni_index, status,
	hv_id, sid, sid_seq, host_iface, netns, gateway_ip, container_id, status_error, dataplane_revision, dataplane_updated_at`

func scanNI(row scannable) (*domain.NetworkInterface, error) {
	var n domain.NetworkInterface
	var labelsJSON, sgJSON, v4IDsJSON, v6IDsJSON []byte
	var statusName string
	var sidSeq int32
	if err := row.Scan(
		&n.ID, &n.FolderID, &n.CreatedAt, &n.Name, &n.Description, &labelsJSON, &n.SubnetID, &n.NetworkID,
		&v4IDsJSON, &v6IDsJSON, &sgJSON, &n.InstanceID, &n.Index, &statusName,
		&n.Dataplane.HVID, &n.Dataplane.SID, &sidSeq, &n.Dataplane.HostIface, &n.Dataplane.Netns, &n.Dataplane.GatewayIP,
		&n.Dataplane.ContainerID, &n.Dataplane.StatusError, &n.Dataplane.Revision, &n.Dataplane.UpdatedAt,
	); err != nil {
		return nil, err
	}
	if err := unmarshalJSONB(labelsJSON, &n.Labels, "NetworkInterface.labels"); err != nil {
		return nil, err
	}
	if err := unmarshalJSONB(v4IDsJSON, &n.V4AddressIDs, "NetworkInterface.v4_address_ids"); err != nil {
		return nil, err
	}
	if err := unmarshalJSONB(v6IDsJSON, &n.V6AddressIDs, "NetworkInterface.v6_address_ids"); err != nil {
		return nil, err
	}
	if err := unmarshalJSONB(sgJSON, &n.SecurityGroupIDs, "NetworkInterface.security_group_ids"); err != nil {
		return nil, err
	}
	n.Dataplane.SIDSeq = uint32(sidSeq)
	n.Status = niStatusFromName(statusName)
	return &n, nil
}

// resolveNIAddresses обогащает n.V4Addresses/n.V6Addresses резолвленными IP-строками
// из таблицы addresses по n.V4AddressIDs/n.V6AddressIDs (denorm для data-plane —
// см. workspace CLAUDE.md §«Инфра-чувствительные данные»; пустые refs → пустые срезы).
func (r *NetworkInterfaceRepo) resolveNIAddresses(ctx context.Context, n *domain.NetworkInterface) error {
	all := append(append([]string{}, n.V4AddressIDs...), n.V6AddressIDs...)
	if len(all) == 0 {
		return nil
	}
	rows, err := r.pool.Query(ctx,
		`SELECT id, COALESCE(internal_ipv4->>'address', external_ipv4->>'address', '') FROM addresses WHERE id = ANY($1)`, all)
	if err != nil {
		return wrapPgErr(err, "Address", "")
	}
	defer rows.Close()
	ipByID := make(map[string]string, len(all))
	for rows.Next() {
		var id, ip string
		if err := rows.Scan(&id, &ip); err != nil {
			return wrapPgErr(err, "Address", "")
		}
		ipByID[id] = ip
	}
	if err := rows.Err(); err != nil {
		return wrapPgErr(err, "Address", "")
	}
	collect := func(ids []string) []string {
		out := make([]string, 0, len(ids))
		for _, id := range ids {
			if ip := ipByID[id]; ip != "" {
				out = append(out, ip)
			}
		}
		return out
	}
	n.V4Addresses = collect(n.V4AddressIDs)
	n.V6Addresses = collect(n.V6AddressIDs)
	return nil
}

// Get возвращает NIC по id.
func (r *NetworkInterfaceRepo) Get(ctx context.Context, id string) (*domain.NetworkInterface, error) {
	n, err := scanNI(r.pool.QueryRow(ctx, `SELECT `+niCols+` FROM network_interfaces WHERE id = $1`, id))
	if err != nil {
		return nil, wrapPgErr(err, "Network interface", id)
	}
	if err := r.resolveNIAddresses(ctx, n); err != nil {
		return nil, err
	}
	return n, nil
}

// List возвращает NIC фолдера (опц. фильтр по instance/subnet/network) с cursor-пагинацией.
func (r *NetworkInterfaceRepo) List(ctx context.Context, f service.NetworkInterfaceFilter, p service.Pagination) ([]*domain.NetworkInterface, string, error) {
	pageSize, err := validate.PageSize("page_size", p.PageSize)
	if err != nil {
		return nil, "", err
	}
	args := []any{f.FolderID}
	conds := []string{"folder_id = $1"}
	add := func(col, val string) {
		if val == "" {
			return
		}
		args = append(args, val)
		conds = append(conds, fmt.Sprintf("%s = $%d", col, len(args)))
	}
	add("instance_id", f.InstanceID)
	add("subnet_id", f.SubnetID)
	add("network_id", f.NetworkID)
	if p.PageToken != "" {
		ts, id, derr := decodePageToken(p.PageToken)
		if derr != nil {
			return nil, "", invalidPageTokenErr(derr)
		}
		args = append(args, ts, id)
		conds = append(conds, fmt.Sprintf("(created_at, id) > ($%d, $%d)", len(args)-1, len(args)))
	}
	args = append(args, pageSize+1)
	q := fmt.Sprintf(`SELECT %s FROM network_interfaces WHERE %s ORDER BY created_at ASC, id ASC LIMIT $%d`, niCols, strings.Join(conds, " AND "), len(args))
	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, "", wrapPgErr(err, "Network interface", "")
	}
	defer rows.Close()
	var out []*domain.NetworkInterface
	for rows.Next() {
		n, err := scanNI(rows)
		if err != nil {
			return nil, "", wrapPgErr(err, "Network interface", "")
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		return nil, "", wrapPgErr(err, "Network interface", "")
	}
	var next string
	if int64(len(out)) > pageSize {
		last := out[pageSize-1]
		next = encodePageToken(last.CreatedAt, last.ID)
		out = out[:pageSize]
	}
	return out, next, nil
}

// ListByHypervisor возвращает все NIC, размещённые на указанном гипервизоре.
func (r *NetworkInterfaceRepo) ListByHypervisor(ctx context.Context, hvID string) ([]*domain.NetworkInterface, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+niCols+` FROM network_interfaces WHERE hv_id = $1 ORDER BY id ASC`, hvID)
	if err != nil {
		return nil, wrapPgErr(err, "Network interface", "")
	}
	defer rows.Close()
	var out []*domain.NetworkInterface
	for rows.Next() {
		n, err := scanNI(rows)
		if err != nil {
			return nil, wrapPgErr(err, "Network interface", "")
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		return nil, wrapPgErr(err, "Network interface", "")
	}
	rows.Close()
	for _, n := range out {
		if err := r.resolveNIAddresses(ctx, n); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// Insert вставляет NIC.
func (r *NetworkInterfaceRepo) Insert(ctx context.Context, n *domain.NetworkInterface) (*domain.NetworkInterface, error) {
	labelsJSON, err := marshalJSONB(n.Labels, "NetworkInterface.labels")
	if err != nil {
		return nil, err
	}
	sgJSON, err := marshalJSONB(orEmptyStrSlice(n.SecurityGroupIDs), "NetworkInterface.security_group_ids")
	if err != nil {
		return nil, err
	}
	v4IDsJSON, err := marshalJSONB(orEmptyStrSlice(n.V4AddressIDs), "NetworkInterface.v4_address_ids")
	if err != nil {
		return nil, err
	}
	v6IDsJSON, err := marshalJSONB(orEmptyStrSlice(n.V6AddressIDs), "NetworkInterface.v6_address_ids")
	if err != nil {
		return nil, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, service.ErrInternal
	}
	defer func() { _ = tx.Rollback(ctx) }()
	const q = `
		INSERT INTO network_interfaces (id, folder_id, created_at, name, description, labels, subnet_id, network_id, v4_address_ids, v6_address_ids, security_group_ids, instance_id, ni_index, status)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		RETURNING ` + niCols
	res, err := scanNI(tx.QueryRow(ctx, q,
		n.ID, n.FolderID, n.CreatedAt, n.Name, n.Description, labelsJSON, n.SubnetID, n.NetworkID,
		v4IDsJSON, v6IDsJSON, sgJSON, n.InstanceID, n.Index, niStatusName(n.Status)))
	if err != nil {
		return nil, wrapPgErr(err, "Network interface", n.Name)
	}
	if err := emitVPC(ctx, tx, "NetworkInterface", res.ID, "CREATED", domainToMap(res)); err != nil {
		return nil, service.ErrInternal
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, wrapPgErr(err, "Network interface", n.Name)
	}
	return res, nil
}

// UpdateMeta обновляет name/description/labels/security_group_ids/v4_address_ids/v6_address_ids.
func (r *NetworkInterfaceRepo) UpdateMeta(ctx context.Context, n *domain.NetworkInterface) (*domain.NetworkInterface, error) {
	labelsJSON, err := marshalJSONB(n.Labels, "NetworkInterface.labels")
	if err != nil {
		return nil, err
	}
	sgJSON, err := marshalJSONB(orEmptyStrSlice(n.SecurityGroupIDs), "NetworkInterface.security_group_ids")
	if err != nil {
		return nil, err
	}
	v4IDsJSON, err := marshalJSONB(orEmptyStrSlice(n.V4AddressIDs), "NetworkInterface.v4_address_ids")
	if err != nil {
		return nil, err
	}
	v6IDsJSON, err := marshalJSONB(orEmptyStrSlice(n.V6AddressIDs), "NetworkInterface.v6_address_ids")
	if err != nil {
		return nil, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, service.ErrInternal
	}
	defer func() { _ = tx.Rollback(ctx) }()
	res, err := scanNI(tx.QueryRow(ctx,
		`UPDATE network_interfaces SET name=$2, description=$3, labels=$4, security_group_ids=$5, v4_address_ids=$6, v6_address_ids=$7 WHERE id=$1 RETURNING `+niCols,
		n.ID, n.Name, n.Description, labelsJSON, sgJSON, v4IDsJSON, v6IDsJSON))
	if err != nil {
		return nil, wrapPgErr(err, "Network interface", n.ID)
	}
	if err := emitVPC(ctx, tx, "NetworkInterface", res.ID, "UPDATED", domainToMap(res)); err != nil {
		return nil, service.ErrInternal
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, wrapPgErr(err, "Network interface", n.ID)
	}
	return res, nil
}

// SetInstance аттачит/детачит NIC (instanceID="" — детач) и выставляет status.
func (r *NetworkInterfaceRepo) SetInstance(ctx context.Context, id, instanceID, niIndex string, st domain.NetworkInterfaceStatus) (*domain.NetworkInterface, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, service.ErrInternal
	}
	defer func() { _ = tx.Rollback(ctx) }()
	res, err := scanNI(tx.QueryRow(ctx,
		`UPDATE network_interfaces SET instance_id=$2, ni_index=$3, status=$4 WHERE id=$1 RETURNING `+niCols,
		id, instanceID, niIndex, niStatusName(st)))
	if err != nil {
		return nil, wrapPgErr(err, "Network interface", id)
	}
	if err := emitVPC(ctx, tx, "NetworkInterface", res.ID, "UPDATED", domainToMap(res)); err != nil {
		return nil, service.ErrInternal
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, wrapPgErr(err, "Network interface", id)
	}
	return res, nil
}

// SetDataplane сохраняет write-back data-plane-проекцию и (опц.) меняет публичный status.
// Возвращает (ni, applied) — applied=false, если revision устарела.
func (r *NetworkInterfaceRepo) SetDataplane(ctx context.Context, id string, dp domain.NICDataplane, newStatus domain.NetworkInterfaceStatus, setStatus bool) (*domain.NetworkInterface, bool, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, false, service.ErrInternal
	}
	defer func() { _ = tx.Rollback(ctx) }()
	cur, err := scanNI(tx.QueryRow(ctx, `SELECT `+niCols+` FROM network_interfaces WHERE id = $1 FOR UPDATE`, id))
	if err != nil {
		return nil, false, wrapPgErr(err, "Network interface", id)
	}
	if dp.Revision < cur.Dataplane.Revision {
		_ = tx.Rollback(ctx)
		return cur, false, nil
	}
	statusCol := "status"
	statusVal := niStatusName(cur.Status)
	if setStatus {
		statusVal = niStatusName(newStatus)
	}
	res, err := scanNI(tx.QueryRow(ctx,
		`UPDATE network_interfaces SET hv_id=$2, sid=$3, sid_seq=$4, host_iface=$5, netns=$6, gateway_ip=$7, container_id=$8, status_error=$9, dataplane_revision=$10, dataplane_updated_at=now(), `+statusCol+`=$11
		 WHERE id=$1 RETURNING `+niCols,
		id, dp.HVID, dp.SID, int32(dp.SIDSeq), dp.HostIface, dp.Netns, dp.GatewayIP, dp.ContainerID, dp.StatusError, int64(dp.Revision), statusVal))
	if err != nil {
		return nil, false, wrapPgErr(err, "Network interface", id)
	}
	if err := emitVPC(ctx, tx, "NetworkInterface", res.ID, "UPDATED", domainToMap(res)); err != nil {
		return nil, false, service.ErrInternal
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, false, wrapPgErr(err, "Network interface", id)
	}
	return res, true, nil
}

// Delete удаляет NIC.
func (r *NetworkInterfaceRepo) Delete(ctx context.Context, id string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return service.ErrInternal
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `DELETE FROM network_interfaces WHERE id = $1`, id)
	if err != nil {
		return wrapPgErr(err, "Network interface", id)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: Network interface %s not found", service.ErrNotFound, id)
	}
	if err := emitVPC(ctx, tx, "NetworkInterface", id, "DELETED", map[string]any{"id": id}); err != nil {
		return service.ErrInternal
	}
	if err := tx.Commit(ctx); err != nil {
		return wrapPgErr(err, "Network interface", id)
	}
	return nil
}

func orEmptyStrSlice(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func niStatusName(s domain.NetworkInterfaceStatus) string {
	switch s {
	case domain.NIStatusProvisioning:
		return "PROVISIONING"
	case domain.NIStatusActive:
		return "ACTIVE"
	case domain.NIStatusAvailable:
		return "AVAILABLE"
	case domain.NIStatusFailed:
		return "FAILED"
	case domain.NIStatusDeleting:
		return "DELETING"
	default:
		return "STATUS_UNSPECIFIED"
	}
}

func niStatusFromName(s string) domain.NetworkInterfaceStatus {
	switch s {
	case "PROVISIONING":
		return domain.NIStatusProvisioning
	case "ACTIVE":
		return domain.NIStatusActive
	case "AVAILABLE":
		return domain.NIStatusAvailable
	case "FAILED":
		return domain.NIStatusFailed
	case "DELETING":
		return domain.NIStatusDeleting
	default:
		return domain.NIStatusUnspecified
	}
}
