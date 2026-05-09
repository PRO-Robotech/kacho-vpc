package repo

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PRO-Robotech/kacho-corelib/validate"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/service"
)

// -- Region --

type RegionRepo struct{ pool *pgxpool.Pool }

func NewRegionRepo(pool *pgxpool.Pool) *RegionRepo { return &RegionRepo{pool: pool} }

func (r *RegionRepo) Get(ctx context.Context, id string) (*domain.Region, error) {
	row := r.pool.QueryRow(ctx, `SELECT id, name, created_at FROM regions WHERE id = $1`, id)
	out := &domain.Region{}
	if err := row.Scan(&out.ID, &out.Name, &out.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, service.ErrNotFound
		}
		return nil, wrapPgErr(err, "Region", id)
	}
	return out, nil
}

func (r *RegionRepo) List(ctx context.Context, p service.Pagination) ([]*domain.Region, string, error) {
	pageSize, err := validate.PageSize("page_size", p.PageSize)
	if err != nil {
		return nil, "", err
	}
	args := []any{pageSize + 1}
	where := ""
	if p.PageToken != "" {
		ts, id, err := decodePageToken(p.PageToken)
		if err != nil {
			return nil, "", invalidPageTokenErr(err)
		}
		where = "WHERE (created_at, id) > ($2, $3)"
		args = append(args, ts, id)
	}
	q := fmt.Sprintf(`SELECT id, name, created_at FROM regions %s ORDER BY created_at ASC, id ASC LIMIT $1`, where)
	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, "", wrapPgErr(err, "Region", "")
	}
	defer rows.Close()
	var out []*domain.Region
	for rows.Next() {
		v := &domain.Region{}
		if err := rows.Scan(&v.ID, &v.Name, &v.CreatedAt); err != nil {
			return nil, "", wrapPgErr(err, "Region", "")
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, "", wrapPgErr(err, "Region", "")
	}
	var nextToken string
	if int64(len(out)) > pageSize {
		last := out[pageSize-1]
		nextToken = encodePageToken(last.CreatedAt, last.ID)
		out = out[:pageSize]
	}
	return out, nextToken, nil
}

func (r *RegionRepo) Insert(ctx context.Context, v *domain.Region) (*domain.Region, error) {
	_, err := r.pool.Exec(ctx, `INSERT INTO regions (id, name) VALUES ($1, $2)`, v.ID, v.Name)
	if err != nil {
		return nil, wrapPgErr(err, "Region", v.ID)
	}
	return r.Get(ctx, v.ID)
}

func (r *RegionRepo) Update(ctx context.Context, v *domain.Region) (*domain.Region, error) {
	tag, err := r.pool.Exec(ctx, `UPDATE regions SET name = $2 WHERE id = $1`, v.ID, v.Name)
	if err != nil {
		return nil, wrapPgErr(err, "Region", v.ID)
	}
	if tag.RowsAffected() == 0 {
		return nil, fmt.Errorf("%w: Region %s", service.ErrNotFound, v.ID)
	}
	return r.Get(ctx, v.ID)
}

func (r *RegionRepo) Delete(ctx context.Context, id string) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM regions WHERE id = $1`, id)
	if err != nil {
		return wrapPgErr(err, "Region", id)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: Region %s", service.ErrNotFound, id)
	}
	return nil
}

// CountZones — сколько Zone привязано к региону. Используется для
// FailedPrecondition в RegionService.Delete (cascade delete запрещён —
// admin обязан сначала снести zones).
func (r *RegionRepo) CountZones(ctx context.Context, regionID string) (int64, error) {
	var n int64
	err := r.pool.QueryRow(ctx, `SELECT count(*) FROM zones WHERE region_id = $1`, regionID).Scan(&n)
	if err != nil {
		return 0, wrapPgErr(err, "Region", regionID)
	}
	return n, nil
}

// -- Zone --

type ZoneRepo struct{ pool *pgxpool.Pool }

func NewZoneRepo(pool *pgxpool.Pool) *ZoneRepo { return &ZoneRepo{pool: pool} }

func (r *ZoneRepo) Get(ctx context.Context, id string) (*domain.Zone, error) {
	row := r.pool.QueryRow(ctx, `SELECT id, region_id, name, created_at FROM zones WHERE id = $1`, id)
	out := &domain.Zone{}
	if err := row.Scan(&out.ID, &out.RegionID, &out.Name, &out.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, service.ErrNotFound
		}
		return nil, wrapPgErr(err, "Zone", id)
	}
	return out, nil
}

func (r *ZoneRepo) List(ctx context.Context, regionID string, p service.Pagination) ([]*domain.Zone, string, error) {
	pageSize, err := validate.PageSize("page_size", p.PageSize)
	if err != nil {
		return nil, "", err
	}
	conds := []string{}
	args := []any{pageSize + 1}
	idx := 2
	if regionID != "" {
		conds = append(conds, fmt.Sprintf("region_id = $%d", idx))
		args = append(args, regionID)
		idx++
	}
	if p.PageToken != "" {
		ts, id, err := decodePageToken(p.PageToken)
		if err != nil {
			return nil, "", invalidPageTokenErr(err)
		}
		conds = append(conds, fmt.Sprintf("(created_at, id) > ($%d, $%d)", idx, idx+1))
		args = append(args, ts, id)
	}
	where := ""
	if len(conds) > 0 {
		where = "WHERE " + joinAnd(conds)
	}
	q := fmt.Sprintf(`SELECT id, region_id, name, created_at FROM zones %s ORDER BY created_at ASC, id ASC LIMIT $1`, where)
	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, "", wrapPgErr(err, "Zone", "")
	}
	defer rows.Close()
	var out []*domain.Zone
	for rows.Next() {
		v := &domain.Zone{}
		if err := rows.Scan(&v.ID, &v.RegionID, &v.Name, &v.CreatedAt); err != nil {
			return nil, "", wrapPgErr(err, "Zone", "")
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, "", wrapPgErr(err, "Zone", "")
	}
	var nextToken string
	if int64(len(out)) > pageSize {
		last := out[pageSize-1]
		nextToken = encodePageToken(last.CreatedAt, last.ID)
		out = out[:pageSize]
	}
	return out, nextToken, nil
}

func (r *ZoneRepo) Insert(ctx context.Context, v *domain.Zone) (*domain.Zone, error) {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO zones (id, region_id, name) VALUES ($1, $2, $3)`,
		v.ID, v.RegionID, v.Name)
	if err != nil {
		return nil, wrapPgErr(err, "Zone", v.ID)
	}
	return r.Get(ctx, v.ID)
}

func (r *ZoneRepo) Update(ctx context.Context, v *domain.Zone) (*domain.Zone, error) {
	tag, err := r.pool.Exec(ctx, `UPDATE zones SET name = $2 WHERE id = $1`, v.ID, v.Name)
	if err != nil {
		return nil, wrapPgErr(err, "Zone", v.ID)
	}
	if tag.RowsAffected() == 0 {
		return nil, fmt.Errorf("%w: Zone %s", service.ErrNotFound, v.ID)
	}
	return r.Get(ctx, v.ID)
}

func (r *ZoneRepo) Delete(ctx context.Context, id string) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM zones WHERE id = $1`, id)
	if err != nil {
		return wrapPgErr(err, "Zone", id)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: Zone %s", service.ErrNotFound, id)
	}
	return nil
}

// CountDependents — сколько ресурсов ссылается на zone (address_pools,
// subnets, addresses через external_ipv4_spec.zone_id). Используется
// в ZoneService.Delete для FailedPrecondition (subnets.zone_id и addresses
// JSONB-zone не имеют FK constraint — service-level guard обязателен).
func (r *ZoneRepo) CountDependents(ctx context.Context, zoneID string) (service.ZoneDeps, error) {
	var d service.ZoneDeps
	row := r.pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM address_pools WHERE zone_id = $1),
			(SELECT count(*) FROM subnets WHERE zone_id = $1),
			(SELECT count(*) FROM addresses WHERE external_ipv4 ->> 'zone_id' = $1)
	`, zoneID)
	if err := row.Scan(&d.AddressPools, &d.Subnets, &d.Addresses); err != nil {
		return d, wrapPgErr(err, "Zone", zoneID)
	}
	return d, nil
}
