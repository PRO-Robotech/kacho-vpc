// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package pg

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/PRO-Robotech/kacho-vpc/internal/repo/helpers"
)

// addressPoolBindingReader — чтение explicit-биндингов
// (`address_pool_network_default`) поверх произвольной pgx.Tx.
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
			return "", helpers.ErrNotFound
		}
		return "", helpers.WrapPgErr(err, "AddressPoolNetworkDefault", networkID)
	}
	return poolID, nil
}

// addressPoolBindingWriter — write-операции в writer-TX. Встраивает reader, чтобы
// writer видел собственные writes. outbox-write — не здесь, а в use-case через
// writer.Outbox().Emit(...).
type addressPoolBindingWriter struct {
	addressPoolBindingReader
}

func (w *addressPoolBindingWriter) SetNetworkDefault(ctx context.Context, networkID, poolID string) error {
	_, err := w.tx.Exec(ctx, `
		INSERT INTO address_pool_network_default (network_id, pool_id, bound_at)
		VALUES ($1, $2, now())
		ON CONFLICT (network_id) DO UPDATE SET pool_id = EXCLUDED.pool_id, bound_at = now()
	`, networkID, poolID)
	if err != nil {
		return helpers.WrapPgErr(err, "AddressPoolNetworkDefault", networkID)
	}
	return nil
}

func (w *addressPoolBindingWriter) UnsetNetworkDefault(ctx context.Context, networkID string) error {
	_, err := w.tx.Exec(ctx,
		`DELETE FROM address_pool_network_default WHERE network_id = $1`, networkID)
	if err != nil {
		return helpers.WrapPgErr(err, "AddressPoolNetworkDefault", networkID)
	}
	return nil
}
