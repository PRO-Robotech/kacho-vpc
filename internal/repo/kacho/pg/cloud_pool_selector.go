package pg

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// cloudPoolSelectorReader — read `cloud_pool_selector` поверх произвольной
// pgx.Tx. Wave 5 replicate (KAC-94 A.7 sub-PR 1/6).
type cloudPoolSelectorReader struct {
	tx pgx.Tx
}

func (r *cloudPoolSelectorReader) Get(ctx context.Context, cloudID string) (*domain.CloudPoolSelector, error) {
	var (
		out   domain.CloudPoolSelector
		js    []byte
		setAt time.Time
		setBy string
	)
	err := r.tx.QueryRow(ctx, `
		SELECT selector, set_at, set_by
		FROM cloud_pool_selector WHERE cloud_id = $1
	`, cloudID).Scan(&js, &setAt, &setBy)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, repo.ErrNotFound
		}
		return nil, repo.WrapPgErr(err, "CloudPoolSelector", cloudID)
	}
	out.CloudID = cloudID
	out.SetAt = setAt
	out.SetBy = setBy
	if err := repo.UnmarshalJSONB(js, &out.Selector, "cloud_pool_selector.selector"); err != nil {
		return nil, err
	}
	return &out, nil
}

// cloudPoolSelectorWriter — write в writer-TX. Embeds reader (G.2).
type cloudPoolSelectorWriter struct {
	cloudPoolSelectorReader
	emitter kacho.OutboxEmitter
}

func (w *cloudPoolSelectorWriter) Set(ctx context.Context, cloudID string, selector map[string]string, setBy string) error {
	js, err := json.Marshal(repo.NormalizeMap(selector))
	if err != nil {
		return err
	}
	_, err = w.tx.Exec(ctx, `
		INSERT INTO cloud_pool_selector (cloud_id, selector, set_at, set_by)
		VALUES ($1, $2::jsonb, now(), $3)
		ON CONFLICT (cloud_id) DO UPDATE
		SET selector = EXCLUDED.selector, set_at = now(), set_by = EXCLUDED.set_by
	`, cloudID, js, setBy)
	if err != nil {
		return repo.WrapPgErr(err, "CloudPoolSelector", cloudID)
	}
	return nil
}

func (w *cloudPoolSelectorWriter) Unset(ctx context.Context, cloudID string) error {
	_, err := w.tx.Exec(ctx, `DELETE FROM cloud_pool_selector WHERE cloud_id = $1`, cloudID)
	if err != nil {
		return repo.WrapPgErr(err, "CloudPoolSelector", cloudID)
	}
	return nil
}
