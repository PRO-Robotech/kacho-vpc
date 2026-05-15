package repo

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PRO-Robotech/kacho-vpc/internal/ports"
)

// AddressPoolBindingRepo — реализация ports.AddressPoolBindingRepo
// (explicit binding pool ↔ network/address).
type AddressPoolBindingRepo struct {
	pool *pgxpool.Pool
}

func NewAddressPoolBindingRepo(pool *pgxpool.Pool) *AddressPoolBindingRepo {
	return &AddressPoolBindingRepo{pool: pool}
}

func (r *AddressPoolBindingRepo) SetNetworkDefault(ctx context.Context, networkID, poolID string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return ports.ErrInternal
	}
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, `
		INSERT INTO address_pool_network_default (network_id, pool_id, bound_at)
		VALUES ($1, $2, now())
		ON CONFLICT (network_id) DO UPDATE SET pool_id = EXCLUDED.pool_id, bound_at = now()
	`, networkID, poolID)
	if err != nil {
		return wrapPgErr(err, "AddressPoolNetworkDefault", networkID)
	}
	if err := emitVPC(ctx, tx, "AddressPoolNetworkDefault", networkID, "UPDATED",
		map[string]any{"network_id": networkID, "pool_id": poolID}); err != nil {
		return ports.ErrInternal
	}
	return tx.Commit(ctx)
}

func (r *AddressPoolBindingRepo) GetNetworkDefault(ctx context.Context, networkID string) (string, error) {
	var poolID string
	err := r.pool.QueryRow(ctx,
		`SELECT pool_id FROM address_pool_network_default WHERE network_id = $1`,
		networkID).Scan(&poolID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ports.ErrNotFound
		}
		return "", wrapPgErr(err, "AddressPoolNetworkDefault", networkID)
	}
	return poolID, nil
}

func (r *AddressPoolBindingRepo) UnsetNetworkDefault(ctx context.Context, networkID string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return ports.ErrInternal
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx,
		`DELETE FROM address_pool_network_default WHERE network_id = $1`, networkID)
	if err != nil {
		return wrapPgErr(err, "AddressPoolNetworkDefault", networkID)
	}
	if tag.RowsAffected() == 0 {
		return tx.Commit(ctx) // idempotent: nothing to do
	}
	_ = emitVPC(ctx, tx, "AddressPoolNetworkDefault", networkID, "DELETED",
		map[string]any{"network_id": networkID})
	return tx.Commit(ctx)
}

func (r *AddressPoolBindingRepo) SetAddressOverride(ctx context.Context, addressID, poolID string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return ports.ErrInternal
	}
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, `
		INSERT INTO address_pool_address_override (address_id, pool_id, bound_at)
		VALUES ($1, $2, now())
		ON CONFLICT (address_id) DO UPDATE SET pool_id = EXCLUDED.pool_id, bound_at = now()
	`, addressID, poolID)
	if err != nil {
		return wrapPgErr(err, "AddressPoolAddressOverride", addressID)
	}
	_ = emitVPC(ctx, tx, "AddressPoolAddressOverride", addressID, "UPDATED",
		map[string]any{"address_id": addressID, "pool_id": poolID})
	return tx.Commit(ctx)
}

func (r *AddressPoolBindingRepo) GetAddressOverride(ctx context.Context, addressID string) (string, error) {
	var poolID string
	err := r.pool.QueryRow(ctx,
		`SELECT pool_id FROM address_pool_address_override WHERE address_id = $1`,
		addressID).Scan(&poolID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ports.ErrNotFound
		}
		return "", wrapPgErr(err, "AddressPoolAddressOverride", addressID)
	}
	return poolID, nil
}

func (r *AddressPoolBindingRepo) UnsetAddressOverride(ctx context.Context, addressID string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return ports.ErrInternal
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx,
		`DELETE FROM address_pool_address_override WHERE address_id = $1`, addressID)
	if err != nil {
		return wrapPgErr(err, "AddressPoolAddressOverride", addressID)
	}
	if tag.RowsAffected() == 0 {
		return tx.Commit(ctx)
	}
	_ = emitVPC(ctx, tx, "AddressPoolAddressOverride", addressID, "DELETED",
		map[string]any{"address_id": addressID})
	return tx.Commit(ctx)
}

var _ = fmt.Sprintf
