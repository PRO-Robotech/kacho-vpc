// Deprecated: legacy concrete `*<Resource>Repo` struct, оставлен временно ради integration-тестов
// и узких port-адаптеров в admin-services (AddressPool/Address/NIC use-cases ещё не на CQRS).
// Финальное удаление — после полной миграции на kacho.Repository (KAC-94 / skill evgeniy A.7).
//

package repo

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// CloudPoolSelectorRepo — admin-controlled routing-labels for Cloud.
// См. domain.CloudPoolSelector + миграция 0022.
type CloudPoolSelectorRepo struct {
	pool *pgxpool.Pool
}

func NewCloudPoolSelectorRepo(pool *pgxpool.Pool) *CloudPoolSelectorRepo {
	return &CloudPoolSelectorRepo{pool: pool}
}

func (r *CloudPoolSelectorRepo) Set(ctx context.Context, cloudID string, selector map[string]string, setBy string) error {
	js, err := json.Marshal(normalizeMap(selector))
	if err != nil {
		return err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return ErrInternal
	}
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, `
		INSERT INTO cloud_pool_selector (cloud_id, selector, set_at, set_by)
		VALUES ($1, $2::jsonb, now(), $3)
		ON CONFLICT (cloud_id) DO UPDATE
		SET selector = EXCLUDED.selector, set_at = now(), set_by = EXCLUDED.set_by
	`, cloudID, js, setBy)
	if err != nil {
		return wrapPgErr(err, "CloudPoolSelector", cloudID)
	}
	_ = emitVPC(ctx, tx, "CloudPoolSelector", cloudID, "UPDATED", map[string]any{
		"cloud_id": cloudID, "selector": normalizeMap(selector), "set_by": setBy,
	})
	return tx.Commit(ctx)
}

func (r *CloudPoolSelectorRepo) Get(ctx context.Context, cloudID string) (*domain.CloudPoolSelector, error) {
	var (
		out   domain.CloudPoolSelector
		js    []byte
		setAt time.Time
		setBy string
	)
	err := r.pool.QueryRow(ctx, `
		SELECT selector, set_at, set_by
		FROM cloud_pool_selector WHERE cloud_id = $1
	`, cloudID).Scan(&js, &setAt, &setBy)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, wrapPgErr(err, "CloudPoolSelector", cloudID)
	}
	out.CloudID = cloudID
	out.SetAt = setAt
	out.SetBy = setBy
	if err := unmarshalJSONB(js, &out.Selector, "cloud_pool_selector.selector"); err != nil {
		return nil, err
	}
	return &out, nil
}

func (r *CloudPoolSelectorRepo) Unset(ctx context.Context, cloudID string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return ErrInternal
	}
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, `DELETE FROM cloud_pool_selector WHERE cloud_id = $1`, cloudID)
	if err != nil {
		return wrapPgErr(err, "CloudPoolSelector", cloudID)
	}
	_ = emitVPC(ctx, tx, "CloudPoolSelector", cloudID, "DELETED", map[string]any{"cloud_id": cloudID})
	return tx.Commit(ctx)
}

func normalizeMap(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}
