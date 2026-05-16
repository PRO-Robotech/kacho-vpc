package pg

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// addressPoolBindingReader — read explicit-биндингов
// (`address_pool_network_default`, `address_pool_address_override`) поверх
// произвольной pgx.Tx. Wave 5 replicate (KAC-94 A.7 sub-PR 1/6).
type addressPoolBindingReader struct {
	tx pgx.Tx
}

func (r *addressPoolBindingReader) GetNetworkDefault(ctx context.Context, networkID string) (string, error) {
	var poolID string
	err := r.tx.QueryRow(ctx,
		`SELECT pool_id FROM address_pool_network_default WHERE network_id = $1`,
		networkID).Scan(&poolID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", repo.ErrNotFound
		}
		return "", repo.WrapPgErr(err, "AddressPoolNetworkDefault", networkID)
	}
	return poolID, nil
}

func (r *addressPoolBindingReader) GetAddressOverride(ctx context.Context, addressID string) (string, error) {
	var poolID string
	err := r.tx.QueryRow(ctx,
		`SELECT pool_id FROM address_pool_address_override WHERE address_id = $1`,
		addressID).Scan(&poolID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", repo.ErrNotFound
		}
		return "", repo.WrapPgErr(err, "AddressPoolAddressOverride", addressID)
	}
	return poolID, nil
}

// addressPoolBindingWriter — write-операции в writer-TX. Embeds reader (G.2).
// outbox-write — НЕ здесь, а в use-case через writer.Outbox().Emit(...).
type addressPoolBindingWriter struct {
	addressPoolBindingReader
	emitter kacho.OutboxEmitter
}

func (w *addressPoolBindingWriter) SetNetworkDefault(ctx context.Context, networkID, poolID string) error {
	_, err := w.tx.Exec(ctx, `
		INSERT INTO address_pool_network_default (network_id, pool_id, bound_at)
		VALUES ($1, $2, now())
		ON CONFLICT (network_id) DO UPDATE SET pool_id = EXCLUDED.pool_id, bound_at = now()
	`, networkID, poolID)
	if err != nil {
		return repo.WrapPgErr(err, "AddressPoolNetworkDefault", networkID)
	}
	return nil
}

func (w *addressPoolBindingWriter) UnsetNetworkDefault(ctx context.Context, networkID string) error {
	_, err := w.tx.Exec(ctx,
		`DELETE FROM address_pool_network_default WHERE network_id = $1`, networkID)
	if err != nil {
		return repo.WrapPgErr(err, "AddressPoolNetworkDefault", networkID)
	}
	return nil
}

func (w *addressPoolBindingWriter) SetAddressOverride(ctx context.Context, addressID, poolID string) error {
	_, err := w.tx.Exec(ctx, `
		INSERT INTO address_pool_address_override (address_id, pool_id, bound_at)
		VALUES ($1, $2, now())
		ON CONFLICT (address_id) DO UPDATE SET pool_id = EXCLUDED.pool_id, bound_at = now()
	`, addressID, poolID)
	if err != nil {
		return repo.WrapPgErr(err, "AddressPoolAddressOverride", addressID)
	}
	return nil
}

func (w *addressPoolBindingWriter) UnsetAddressOverride(ctx context.Context, addressID string) error {
	_, err := w.tx.Exec(ctx,
		`DELETE FROM address_pool_address_override WHERE address_id = $1`, addressID)
	if err != nil {
		return repo.WrapPgErr(err, "AddressPoolAddressOverride", addressID)
	}
	return nil
}
