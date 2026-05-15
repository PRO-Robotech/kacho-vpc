package repo

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PRO-Robotech/kacho-corelib/filter"
	"github.com/PRO-Robotech/kacho-corelib/validate"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/service"
)

// PrivateEndpointRepo — реализация service.PrivateEndpointRepo поверх pgxpool.
type PrivateEndpointRepo struct {
	pool *pgxpool.Pool
}

// NewPrivateEndpointRepo создаёт PrivateEndpointRepo.
func NewPrivateEndpointRepo(pool *pgxpool.Pool) *PrivateEndpointRepo {
	return &PrivateEndpointRepo{pool: pool}
}

const peCols = `id, folder_id, created_at, name, description, labels, network_id, subnet_id, address_id, ip_address, service_type, dns_options, status`

func (r *PrivateEndpointRepo) Get(ctx context.Context, id string) (*domain.PrivateEndpoint, error) {
	q := fmt.Sprintf(`SELECT %s FROM private_endpoints WHERE id = $1`, peCols)
	row := r.pool.QueryRow(ctx, q, id)
	pe, err := scanPrivateEndpoint(row)
	if err != nil {
		return nil, wrapPgErr(err, "PrivateEndpoint", id)
	}
	return pe, nil
}

func (r *PrivateEndpointRepo) List(ctx context.Context, f service.PrivateEndpointFilter, p service.Pagination) ([]*domain.PrivateEndpoint, string, error) {
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
	q := fmt.Sprintf(`SELECT %s FROM private_endpoints %s ORDER BY created_at ASC, id ASC LIMIT $%d`, peCols, where, argIdx)
	args = append(args, pageSize+1)

	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, "", wrapPgErr(err, "PrivateEndpoint", "")
	}
	defer rows.Close()

	var result []*domain.PrivateEndpoint
	for rows.Next() {
		pe, err := scanPrivateEndpoint(rows)
		if err != nil {
			return nil, "", wrapPgErr(err, "PrivateEndpoint", "")
		}
		result = append(result, pe)
	}
	if err := rows.Err(); err != nil {
		return nil, "", wrapPgErr(err, "PrivateEndpoint", "")
	}

	var nextToken string
	if int64(len(result)) > pageSize {
		last := result[pageSize-1]
		nextToken = encodePageToken(last.CreatedAt, last.ID)
		result = result[:pageSize]
	}
	return result, nextToken, nil
}

func (r *PrivateEndpointRepo) Insert(ctx context.Context, pe *domain.PrivateEndpoint) (*domain.PrivateEndpoint, error) {
	labelsJSON, err := marshalJSONB(pe.Labels, "PrivateEndpoint.labels")
	if err != nil {
		return nil, err
	}
	dnsJSON, err := marshalJSONB(pe.DnsOptions, "PrivateEndpoint.dns_options")
	if err != nil {
		return nil, err
	}
	if dnsJSON == nil {
		dnsJSON = []byte("{}")
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, service.ErrInternal
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// KAC-89: subnet_id / address_id — optional within-service refs c FK на
	// subnets(id) / addresses(id) (миграция 0024). Postgres FK с MATCH SIMPLE
	// пропускает NULL, но empty-string '' трактуется как обычное значение и
	// FK попытается найти row с id='' → 23503. Конвертируем '' → NULL прямо
	// в INSERT через NULLIF, чтобы service-слой мог по-прежнему передавать
	// pe.SubnetID/pe.AddressID = "" для «не задано».
	const q = `
		INSERT INTO private_endpoints
		(id, folder_id, created_at, name, description, labels,
		 network_id, subnet_id, address_id, ip_address, service_type, dns_options, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NULLIF($8, ''), NULLIF($9, ''), $10, $11, $12, $13)
		RETURNING ` + peCols

	row := tx.QueryRow(ctx, q,
		pe.ID, pe.FolderID, pe.CreatedAt, pe.Name, pe.Description, labelsJSON,
		pe.NetworkID, pe.SubnetID, pe.AddressID, pe.IPAddress,
		pe.ServiceType, dnsJSON, pe.Status,
	)
	result, err := scanPrivateEndpoint(row)
	if err != nil {
		return nil, wrapPgErr(err, "PrivateEndpoint", pe.Name)
	}
	if err := emitVPC(ctx, tx, "PrivateEndpoint", result.ID, "CREATED", privateEndpointPayload(result)); err != nil {
		return nil, service.ErrInternal
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, wrapPgErr(err, "PrivateEndpoint", pe.Name)
	}
	return result, nil
}

func (r *PrivateEndpointRepo) Update(ctx context.Context, pe *domain.PrivateEndpoint) (*domain.PrivateEndpoint, error) {
	labelsJSON, err := marshalJSONB(pe.Labels, "PrivateEndpoint.labels")
	if err != nil {
		return nil, err
	}
	dnsJSON, err := marshalJSONB(pe.DnsOptions, "PrivateEndpoint.dns_options")
	if err != nil {
		return nil, err
	}
	if dnsJSON == nil {
		dnsJSON = []byte("{}")
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, service.ErrInternal
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const q = `
		UPDATE private_endpoints
		SET name=$2, description=$3, labels=$4, dns_options=$5
		WHERE id=$1
		RETURNING ` + peCols

	row := tx.QueryRow(ctx, q,
		pe.ID, pe.Name, pe.Description, labelsJSON, dnsJSON,
	)
	result, err := scanPrivateEndpoint(row)
	if err != nil {
		return nil, wrapPgErr(err, "PrivateEndpoint", pe.ID)
	}
	if err := emitVPC(ctx, tx, "PrivateEndpoint", result.ID, "UPDATED", privateEndpointPayload(result)); err != nil {
		return nil, service.ErrInternal
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, wrapPgErr(err, "PrivateEndpoint", pe.ID)
	}
	return result, nil
}

func (r *PrivateEndpointRepo) Delete(ctx context.Context, id string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return service.ErrInternal
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx, `DELETE FROM private_endpoints WHERE id = $1`, id)
	if err != nil {
		if isFKViolation(err) {
			return fmt.Errorf("%w: private endpoint is in use", service.ErrFailedPrecondition)
		}
		return wrapPgErr(err, "PrivateEndpoint", id)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: PrivateEndpoint %s not found", service.ErrNotFound, id)
	}
	if err := emitVPC(ctx, tx, "PrivateEndpoint", id, "DELETED", map[string]any{"id": id}); err != nil {
		return service.ErrInternal
	}
	if err := tx.Commit(ctx); err != nil {
		return wrapPgErr(err, "PrivateEndpoint", id)
	}
	return nil
}

// ---- scan helpers ----

func scanPrivateEndpoint(row scannable) (*domain.PrivateEndpoint, error) {
	var pe domain.PrivateEndpoint
	var labelsJSON, dnsJSON []byte
	var networkID, subnetID, addressID, ipAddress, serviceType *string

	err := row.Scan(
		&pe.ID, &pe.FolderID, &pe.CreatedAt, &pe.Name, &pe.Description, &labelsJSON,
		&networkID, &subnetID, &addressID, &ipAddress, &serviceType, &dnsJSON, &pe.Status,
	)
	if err != nil {
		return nil, err
	}
	if err := unmarshalJSONB(labelsJSON, &pe.Labels, "PrivateEndpoint.labels"); err != nil {
		return nil, err
	}
	if err := unmarshalJSONB(dnsJSON, &pe.DnsOptions, "PrivateEndpoint.dns_options"); err != nil {
		return nil, err
	}
	if networkID != nil {
		pe.NetworkID = *networkID
	}
	if subnetID != nil {
		pe.SubnetID = *subnetID
	}
	if addressID != nil {
		pe.AddressID = *addressID
	}
	if ipAddress != nil {
		pe.IPAddress = *ipAddress
	}
	if serviceType != nil {
		pe.ServiceType = *serviceType
	}
	return &pe, nil
}

func privateEndpointPayload(pe *domain.PrivateEndpoint) map[string]any {
	return domainToMap(pe)
}
