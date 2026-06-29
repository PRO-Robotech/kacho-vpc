// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package kacho

import "context"

// AddressPoolBindingReaderIface — read-операции над explicit-биндингами
// pool ↔ network (address_pool_network_default). Используется cascade-resolve
// (network_default step) и handler-методом BindAsNetworkDefault.
//
// Возвращает пустую строку + ErrNotFound если binding не задан (cascade-resolver
// использует это для fall-through к следующему шагу).
type AddressPoolBindingReaderIface interface {
	GetNetworkDefault(ctx context.Context, networkID string) (string, error)
}

// AddressPoolBindingWriterIface — write-операции + read (writer видит свои writes).
//
// DML-методы НЕ открывают свою TX и НЕ emit'ят outbox — это делает caller
// через RepositoryWriter.Outbox().Emit(...). Atomicity DML + outbox
// гарантируется одной pgx.Tx writer'а.
//
// Set-метод — upsert (ON CONFLICT DO UPDATE). Unset-метод — idempotent
// DELETE (no error если binding не задан).
type AddressPoolBindingWriterIface interface {
	AddressPoolBindingReaderIface
	SetNetworkDefault(ctx context.Context, networkID, poolID string) error
	UnsetNetworkDefault(ctx context.Context, networkID string) error
}
