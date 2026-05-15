package repo

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
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

// Data-plane колонки (hv_id/sid/sid_seq/host_iface/netns/gateway_ip/container_id/
// status_error/dataplane_revision/dataplane_updated_at) удалены в KAC-79/KAC-36
// (post-kube-ovn: kube-ovn управляет underlay сам, у kacho-vpc больше нет своей
// data-plane-проекции).
const niCols = `id, folder_id, created_at, name, description, labels, subnet_id,
	v4_address_ids, v6_address_ids, security_group_ids, used_by_type, used_by_id, used_by_name, mac_address, status`

func scanNI(row scannable) (*domain.NetworkInterface, error) {
	var n domain.NetworkInterface
	var labelsJSON, sgJSON, v4IDsJSON, v6IDsJSON []byte
	var statusName string
	if err := row.Scan(
		&n.ID, &n.FolderID, &n.CreatedAt, &n.Name, &n.Description, &labelsJSON, &n.SubnetID,
		&v4IDsJSON, &v6IDsJSON, &sgJSON, &n.UsedByType, &n.UsedByID, &n.UsedByName, &n.MAC, &statusName,
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
	n.Status = niStatusFromName(statusName)
	return &n, nil
}

// Get возвращает NIC по id.
func (r *NetworkInterfaceRepo) Get(ctx context.Context, id string) (*domain.NetworkInterface, error) {
	n, err := scanNI(r.pool.QueryRow(ctx, `SELECT `+niCols+` FROM network_interfaces WHERE id = $1`, id))
	if err != nil {
		return nil, wrapPgErr(err, "Network interface", id)
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
	// instance_id-фильтр маппится на denorm used_by (referrer={compute_instance,<id>}).
	if f.InstanceID != "" {
		args = append(args, "compute_instance")
		conds = append(conds, fmt.Sprintf("used_by_type = $%d", len(args)))
		args = append(args, f.InstanceID)
		conds = append(conds, fmt.Sprintf("used_by_id = $%d", len(args)))
	}
	add("subnet_id", f.SubnetID)
	// network_id-фильтр больше не поддерживается: NIC не хранит network_id
	// (выводится транзитивно через subnet). Игнорируется (no-op).
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

// ListBySubnet возвращает все NIC, привязанные к указанной подсети.
func (r *NetworkInterfaceRepo) ListBySubnet(ctx context.Context, subnetID string) ([]*domain.NetworkInterface, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+niCols+` FROM network_interfaces WHERE subnet_id = $1 ORDER BY id ASC`, subnetID)
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
		INSERT INTO network_interfaces (id, folder_id, created_at, name, description, labels, subnet_id, v4_address_ids, v6_address_ids, security_group_ids, used_by_type, used_by_id, used_by_name, mac_address, status)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
		RETURNING ` + niCols
	res, err := scanNI(tx.QueryRow(ctx, q,
		n.ID, n.FolderID, n.CreatedAt, n.Name, n.Description, labelsJSON, n.SubnetID,
		v4IDsJSON, v6IDsJSON, sgJSON, n.UsedByType, n.UsedByID, n.UsedByName, n.MAC, niStatusName(n.Status)))
	if err != nil {
		// MAC-collision — отдельная sentinel, чтобы service-слой мог retry'ить
		// с новым MAC, не путая её с (folder_id,name) UNIQUE → AlreadyExists.
		if isNICMacCollision(err) {
			return nil, service.ErrMacCollision
		}
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

// SetUsedBy выставляет/очищает denorm used_by-ссылку NIC (refID="" — очистка =
// detach) и публичный status. Зеркало AddressRepo.SetReference/ClearReference.
//
// Attach-путь (refID != "") — атомарный single-statement CAS:
// `WHERE id=$1 AND (used_by_id = ” OR used_by_id = $3)`. Это устраняет TOCTOU из service-слоя
// (NetworkInterfaceService.AttachToInstance), который раньше делал Get → check
// → unconditional UPDATE и допускал second-writer-wins race (инцидент 2026-05-14,
// KAC-52: два Compute.Instance.Create с одним existing_network_interface_id
// обе прошли software-guard, second UPDATE безусловно перезаписал ownership →
// два pod-а на одной NIC → Kube-OVN IP-allocation conflict).
//
// 0 rows из RETURNING → service.ErrFailedPrecondition. Single-statement UPDATE
// на одной row защищён row-level lock-ом Postgres: параллельный writer ждёт
// commit-а первого, видит обновлённый row, CAS не matches → 0 rows. Никакого
// дополнительного UNIQUE-индекса не нужно — миграция 0016 пыталась добавить
// `UNIQUE (used_by_id) WHERE used_by_id <> ”` как backstop, но это семантически
// запрещало multi-NIC instance (один Compute.Instance имеет N NetworkInterface —
// корректный AWS-ENI use case), и миграция падала на боевом стенде; откачено
// в 0017.
//
// Detach-путь (refID == "") — idempotent unconditional UPDATE: повторный
// detach уже-свободного NIC — no-op без error. Concurrent detach + attach —
// admin race, не критичен для correctness.
//
// См. workspace CLAUDE.md §«Within-service refs — DB-уровень обязателен»
// (запрет #10) — шаблон «атомарный single-statement CAS на одной row».
func (r *NetworkInterfaceRepo) SetUsedBy(ctx context.Context, id, refType, refID, refName string, st domain.NetworkInterfaceStatus) (*domain.NetworkInterface, error) {
	if refID == "" {
		refType, refName = "", ""
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, service.ErrInternal
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var (
		sql  string
		args []any
	)
	if refID == "" {
		// Detach: idempotent — clears ownership unconditionally.
		sql = `UPDATE network_interfaces SET used_by_type=$2, used_by_id=$3, used_by_name=$4, status=$5 WHERE id=$1 RETURNING ` + niCols
		args = []any{id, refType, refID, refName, niStatusName(st)}
	} else {
		// Attach: CAS — only if free OR already owned by the same referrer
		// (idempotent re-attach). Paired with partial UNIQUE
		// network_interfaces_used_by_uniq (migration 0016).
		sql = `UPDATE network_interfaces
                  SET used_by_type=$2, used_by_id=$3, used_by_name=$4, status=$5
                WHERE id=$1 AND (used_by_id = '' OR used_by_id = $3)
                RETURNING ` + niCols
		args = []any{id, refType, refID, refName, niStatusName(st)}
	}

	res, err := scanNI(tx.QueryRow(ctx, sql, args...))
	if err != nil {
		if refID != "" && errors.Is(err, pgx.ErrNoRows) {
			// CAS failed — someone else owns it (UPDATE matched 0 rows from RETURNING).
			return nil, service.ErrFailedPrecondition
		}
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

// SetDataplane / ReportNiDataplane / ListByHypervisor / NICDataplane — удалены в
// KAC-79/KAC-36 (post-kube-ovn: data-plane-проекция NIC + write-back от
// kacho-vpc-implement больше не нужны, kube-ovn управляет underlay сам).

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
