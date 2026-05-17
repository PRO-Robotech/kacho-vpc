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

// networkInterfaceReader — Get/List/ListBySubnet поверх произвольной pgx.Tx
// (read-only или RW).
//
// Wave 5 (KAC-94, skill evgeniy §6 G.1-G.7): NIC переезжает на CQRS поверх
// единой writer-TX (как Network — Wave 5 pilot, и SG — batch 33/34). Это
// нужно для двух кейсов: (1) Compute-flow «Create NIC + AttachToInstance»
// захочет атомарно создать ресурс и сразу заняться им; (2) Address-attach
// при NIC.Create — `addresses.used`/`address_references` обновляются в той
// же writer-TX, что INSERT(NIC), чтобы address не оставался помеченным как
// used, если INSERT(NIC) откатился. (Pilot — это рефакторинг repo-слоя; pilot-
// use-case'ы пока остаются с two-TX-шаблоном, переход на single writer-TX —
// отдельный шаг.)
//
// SQL/scan-семантика — parity с legacy `*repo.NetworkInterfaceRepo`:
// см. `internal/repo/network_interface_repo.go` (scanNI / niCols /
// niStatusName / niStatusFromName / isNICMacCollision / networkInterfacePayload
// — частично уже экспонированы через `internal/repo/shim_kacho_ni.go` для нужд
// этого пакета; остальные мы пере-используем через type-alias или re-export
// ниже).
type networkInterfaceReader struct {
	tx pgx.Tx
}

// Get — verbatim YC: well-formed-but-absent → NotFound с
// "Network interface <id> not found" (через WrapPgErr).
func (r *networkInterfaceReader) Get(ctx context.Context, id string) (*kacho.NetworkInterfaceRecord, error) {
	q := fmt.Sprintf(`SELECT %s FROM network_interfaces WHERE id = $1`, helpers.NICCols)
	row := r.tx.QueryRow(ctx, q, id)
	n, err := helpers.ScanNIRec(row)
	if err != nil {
		return nil, helpers.WrapPgErr(err, "Network interface", id)
	}
	return n, nil
}

// List — project_id required + cursor-based pagination + denormalised instance_id
// filter (used_by_type='compute_instance' AND used_by_id=$instance). NetworkID
// игнорируется (NIC не хранит network_id; legacy-репо тоже игнорировал — see
// `internal/repo/network_interface_repo.go::List`).
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
		n, err := helpers.ScanNIRec(rows)
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

// ListBySubnet возвращает все NIC, привязанные к указанной подсети. Нужен
// Subnet.Delete precondition (FK RESTRICT на subnets — миграция 0012, KAC-33).
// Не paginated (Subnet с >1000 NIC — edge-case; legacy-репо тоже без paging).
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
		n, err := helpers.ScanNIRec(rows)
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
// networkInterfaceReader (G.2 — writer видит свои writes).
//
// Особенность CQRS: writer НЕ emit'ит outbox сам — caller (use-case) делает
// `RepositoryWriter.Outbox().Emit(...)` явно после успешного DML. Это
// гарантирует, что outbox-write идёт в той же pgx.Tx (G.5).
type networkInterfaceWriter struct {
	networkInterfaceReader
	emitter kacho.OutboxEmitter // не используется здесь; держим для consistency
}

// Insert — INSERT network_interfaces RETURNING. MAC должен быть проставлен
// caller'ом (use-case аллоцирует через `macutil.GenerateMAC`).
//
// Cloud-wide UNIQUE на mac_address (constraint `network_interfaces_mac_address_key`,
// миграция 0014/KAC-48) — при коллизии возвращаем `helpers.ErrMacCollision`
// (caller retry'ит с новым MAC). Прочие нарушения (folder/name UNIQUE, FK
// subnet_id) — `WrapPgErr` → ErrAlreadyExists / ErrFailedPrecondition.
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
	rec, err := helpers.ScanNIRec(row)
	if err != nil {
		if helpers.IsNICMacCollision(err) {
			return nil, helpers.ErrMacCollision
		}
		return nil, helpers.WrapPgErr(err, "Network interface", string(n.Name))
	}
	return rec, nil
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
	rec, err := helpers.ScanNIRec(row)
	if err != nil {
		return nil, helpers.WrapPgErr(err, "Network interface", n.ID)
	}
	return rec, nil
}

// SetProjectID меняет project_id у NIC. NIC сейчас не поддерживает Move RPC
// (NIC привязан к Subnet); метод оставлен для parity с другими writer-iface
// (NetworkWriterIface / SecurityGroupWriterIface), на случай admin-tooling.
func (w *networkInterfaceWriter) SetProjectID(ctx context.Context, id, folderID string) (*kacho.NetworkInterfaceRecord, error) {
	q := fmt.Sprintf(`UPDATE network_interfaces SET project_id = $2 WHERE id = $1 RETURNING %s`, helpers.NICCols)
	row := w.tx.QueryRow(ctx, q, id, folderID)
	rec, err := helpers.ScanNIRec(row)
	if err != nil {
		return nil, helpers.WrapPgErr(err, "Network interface", id)
	}
	return rec, nil
}

// AttachToInstance — атомарный CAS на used_by_* + status=ACTIVE.
//
// **Race-safety (KAC-52, workspace CLAUDE.md §«Within-service refs — DB-уровень
// обязателен», запрет #10):** single-statement conditional UPDATE на одной
// row защищён row-level lock-ом Postgres. CAS-условие — `(used_by_id = ” OR
// used_by_id = $refID)`: либо NIC свободен, либо уже attached к тому же
// owner-у (идемпотентный re-attach). 0 rows из RETURNING → ErrFailedPrecondition.
//
// Software-side `Get → check → Update` (TOCTOU) ЗАПРЕЩЁН — этот шаблон привёл
// к реальному инциденту 2026-05-14 (две Compute.Instance.Create указали один
// existing_network_interface_id, обе прошли software-guard, обе вызвали
// безусловный UPDATE → two pods on one NIC → kube-ovn IP-conflict). Атомарный
// CAS здесь — финальная защита; software fast-path в use-case остаётся как
// UX-улучшение (понятный error message с актуальным owner-id).
func (w *networkInterfaceWriter) AttachToInstance(ctx context.Context, id, refType, refID, refName string) (*kacho.NetworkInterfaceRecord, error) {
	q := fmt.Sprintf(`
		UPDATE network_interfaces
		   SET used_by_type=$2, used_by_id=$3, used_by_name=$4, status=$5
		 WHERE id=$1 AND (used_by_id = '' OR used_by_id = $3)
		RETURNING %s`, helpers.NICCols)
	row := w.tx.QueryRow(ctx, q, id, refType, refID, refName, helpers.NIStatusName(domain.NIStatusActive))
	rec, err := helpers.ScanNIRec(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// CAS failed — someone else owns it (UPDATE matched 0 rows).
			return nil, helpers.ErrFailedPrecondition
		}
		return nil, helpers.WrapPgErr(err, "Network interface", id)
	}
	return rec, nil
}

// DetachFromInstance — idempotent UPDATE: затирает used_by_* + status=AVAILABLE.
// Повторный detach уже-свободного NIC — no-op без error.
func (w *networkInterfaceWriter) DetachFromInstance(ctx context.Context, id string) (*kacho.NetworkInterfaceRecord, error) {
	q := fmt.Sprintf(`
		UPDATE network_interfaces SET used_by_type='', used_by_id='', used_by_name='', status=$2
		WHERE id=$1
		RETURNING %s`, helpers.NICCols)
	row := w.tx.QueryRow(ctx, q, id, helpers.NIStatusName(domain.NIStatusAvailable))
	rec, err := helpers.ScanNIRec(row)
	if err != nil {
		return nil, helpers.WrapPgErr(err, "Network interface", id)
	}
	return rec, nil
}

// Delete — DELETE network_interfaces WHERE id = $1. row not affected →
// ErrNotFound. NIC не имеет children FK (нет ON DELETE caskads из NIC), но
// имеет parent FK на subnets — это не trigger'ит при удалении NIC, только
// при удалении Subnet (RESTRICT — миграция 0012, KAC-33).
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
