package pg

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/PRO-Robotech/kacho-corelib/filter"
	"github.com/PRO-Robotech/kacho-corelib/validate"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// privateEndpointReader — Get/List поверх произвольной pgx.Tx (read-only или RW).
// Wave 5 replicate (KAC-94, skill evgeniy §6 G.1-G.7): PrivateEndpoint переезжает
// на CQRS после Network/SG pilot'а.
//
// SQL/scan-семантика — parity с legacy `*repo.PrivateEndpointRepo` (helpers
// экспортированы как shim'ы в `internal/repo/shim_kacho.go` — `repo.PECols` /
// `repo.ScanPrivateEndpoint` / `repo.PrivateEndpointPayload`).
type privateEndpointReader struct {
	tx pgx.Tx
}

// Get — verbatim YC: well-formed-but-absent → NotFound с
// "PrivateEndpoint <id> not found".
func (r *privateEndpointReader) Get(ctx context.Context, id string) (*kacho.PrivateEndpointRecord, error) {
	q := fmt.Sprintf(`SELECT %s FROM private_endpoints WHERE id = $1`, repo.PECols)
	row := r.tx.QueryRow(ctx, q, id)
	pe, err := repo.ScanPrivateEndpoint(row)
	if err != nil {
		return nil, repo.WrapPgErr(err, "PrivateEndpoint", id)
	}
	return pe, nil
}

// List — cursor-based pagination + filter.Parse (YC-syntax). Логика идентична
// legacy `*repo.PrivateEndpointRepo.List`.
func (r *privateEndpointReader) List(ctx context.Context, f kacho.PrivateEndpointFilter, p kacho.Pagination) ([]*kacho.PrivateEndpointRecord, string, error) {
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
	if f.Name != "" {
		conditions = append(conditions, fmt.Sprintf("name = $%d", argIdx))
		args = append(args, f.Name)
		argIdx++
	}
	if f.Filter != "" {
		ast, perr := filter.Parse(f.Filter, []string{"name"})
		if perr != nil {
			return nil, "", repo.InvalidFilterErr(perr)
		}
		if ast != nil {
			frag, fargs := ast.ToSQL(argIdx)
			conditions = append(conditions, frag)
			args = append(args, fargs...)
			argIdx += len(fargs)
		}
	}
	if p.PageToken != "" {
		ts, id, derr := repo.DecodePageToken(p.PageToken)
		if derr != nil {
			return nil, "", repo.InvalidPageTokenErr(derr)
		}
		conditions = append(conditions, fmt.Sprintf("(created_at, id) > ($%d, $%d)", argIdx, argIdx+1))
		args = append(args, ts, id)
		argIdx += 2
	}

	var where string
	if len(conditions) > 0 {
		where = "WHERE " + strings.Join(conditions, " AND ")
	}
	q := fmt.Sprintf(`SELECT %s FROM private_endpoints %s ORDER BY created_at ASC, id ASC LIMIT $%d`, repo.PECols, where, argIdx)
	args = append(args, pageSize+1)

	rows, err := r.tx.Query(ctx, q, args...)
	if err != nil {
		return nil, "", repo.WrapPgErr(err, "PrivateEndpoint", "")
	}
	defer rows.Close()

	var result []*kacho.PrivateEndpointRecord
	for rows.Next() {
		pe, err := repo.ScanPrivateEndpoint(rows)
		if err != nil {
			return nil, "", repo.WrapPgErr(err, "PrivateEndpoint", "")
		}
		result = append(result, pe)
	}
	if err := rows.Err(); err != nil {
		return nil, "", repo.WrapPgErr(err, "PrivateEndpoint", "")
	}

	var nextToken string
	if int64(len(result)) > pageSize {
		last := result[pageSize-1]
		nextToken = repo.EncodePageToken(last.CreatedAt, last.ID)
		result = result[:pageSize]
	}
	return result, nextToken, nil
}

// privateEndpointWriter — DML над private_endpoints через writer-TX. Embeds
// privateEndpointReader (G.2 — writer видит свои writes).
//
// Особенность CQRS: writer НЕ emit'ит outbox сам — caller (use-case) делает
// `RepositoryWriter.Outbox().Emit(...)` явно после успешного DML. Это
// гарантирует, что outbox-write идёт в той же pgx.Tx (G.5).
//
// PE-specific: миграция 0024 добавила FK network_id/subnet_id/address_id (на
// networks/subnets/addresses) с ON DELETE RESTRICT. Optional колонки
// (subnet_id, address_id) нормализуются "" → NULL прямо в INSERT через
// `NULLIF($N, ”)` (FK с MATCH SIMPLE пропускает NULL, но empty-string трактует
// как реальный поиск → 23503).
type privateEndpointWriter struct {
	privateEndpointReader
	emitter kacho.OutboxEmitter // не используется здесь; держим для consistency с networkWriter
}

// Insert — INSERT private_endpoints RETURNING. CreatedAt здесь явно проставляется
// в UTC для детерминированности тестов. Empty-string-id для optional FK
// (subnet_id/address_id) конвертируются в SQL NULL прямо в запросе (см. KAC-89
// и миграцию 0024).
//
// outbox-write — не здесь, а в use-case'е через `writer.Outbox().Emit(...)`.
func (w *privateEndpointWriter) Insert(ctx context.Context, pe *domain.PrivateEndpoint) (*kacho.PrivateEndpointRecord, error) {
	labelsJSON, err := repo.MarshalJSONB(domain.LabelsToMap(pe.Labels), "PrivateEndpoint.labels")
	if err != nil {
		return nil, err
	}
	dnsJSON, err := repo.MarshalJSONB(pe.DnsOptions, "PrivateEndpoint.dns_options")
	if err != nil {
		return nil, err
	}
	if dnsJSON == nil {
		dnsJSON = []byte("{}")
	}

	now := time.Now().UTC()
	q := fmt.Sprintf(`
		INSERT INTO private_endpoints
		(id, folder_id, created_at, name, description, labels,
		 network_id, subnet_id, address_id, ip_address, service_type, dns_options, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NULLIF($8, ''), NULLIF($9, ''), $10, $11, $12, $13)
		RETURNING %s`, repo.PECols)

	row := w.tx.QueryRow(ctx, q,
		pe.ID, pe.FolderID, now, string(pe.Name), string(pe.Description), labelsJSON,
		pe.NetworkID, pe.SubnetID, pe.AddressID, pe.IPAddress,
		string(pe.ServiceType), dnsJSON, string(pe.Status),
	)
	result, err := repo.ScanPrivateEndpoint(row)
	if err != nil {
		return nil, repo.WrapPgErr(err, "PrivateEndpoint", string(pe.Name))
	}
	return result, nil
}

// Update — UPDATE private_endpoints RETURNING. Мутирует name/description/labels/
// dns_options (verbatim YC: остальные поля immutable — service-слой проверяет
// update_mask).
//
// outbox-write — в use-case'е.
func (w *privateEndpointWriter) Update(ctx context.Context, pe *domain.PrivateEndpoint) (*kacho.PrivateEndpointRecord, error) {
	labelsJSON, err := repo.MarshalJSONB(domain.LabelsToMap(pe.Labels), "PrivateEndpoint.labels")
	if err != nil {
		return nil, err
	}
	dnsJSON, err := repo.MarshalJSONB(pe.DnsOptions, "PrivateEndpoint.dns_options")
	if err != nil {
		return nil, err
	}
	if dnsJSON == nil {
		dnsJSON = []byte("{}")
	}

	q := fmt.Sprintf(`
		UPDATE private_endpoints
		SET name=$2, description=$3, labels=$4, dns_options=$5
		WHERE id=$1
		RETURNING %s`, repo.PECols)

	row := w.tx.QueryRow(ctx, q,
		pe.ID, string(pe.Name), string(pe.Description), labelsJSON, dnsJSON,
	)
	result, err := repo.ScanPrivateEndpoint(row)
	if err != nil {
		return nil, repo.WrapPgErr(err, "PrivateEndpoint", pe.ID)
	}
	return result, nil
}

// SetFolderID меняет folder_id у PrivateEndpoint. PE не имеет Move RPC в YC
// verbatim API, но writer-iface поддерживает метод parity с другими ресурсами.
func (w *privateEndpointWriter) SetFolderID(ctx context.Context, id, folderID string) (*kacho.PrivateEndpointRecord, error) {
	q := fmt.Sprintf(`UPDATE private_endpoints SET folder_id = $2 WHERE id = $1 RETURNING %s`, repo.PECols)
	row := w.tx.QueryRow(ctx, q, id, folderID)
	pe, err := repo.ScanPrivateEndpoint(row)
	if err != nil {
		return nil, repo.WrapPgErr(err, "PrivateEndpoint", id)
	}
	return pe, nil
}

// Delete — DELETE private_endpoints WHERE id = $1. FK violation (PE используется
// другим ресурсом — теоретически возможно при будущих расширениях) →
// ErrFailedPrecondition. row not affected → ErrNotFound.
//
// outbox-write (DELETED tombstone) — в use-case'е.
func (w *privateEndpointWriter) Delete(ctx context.Context, id string) error {
	tag, err := w.tx.Exec(ctx, `DELETE FROM private_endpoints WHERE id = $1`, id)
	if err != nil {
		if repo.IsFKViolation(err) {
			return fmt.Errorf("%w: private endpoint is in use", repo.ErrFailedPrecondition)
		}
		return repo.WrapPgErr(err, "PrivateEndpoint", id)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: PrivateEndpoint %s not found", repo.ErrNotFound, id)
	}
	return nil
}
