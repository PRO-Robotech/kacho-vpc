package kacho

import "context"

// AddressPoolBindingReaderIface — read-операции над explicit-биндингами
// pool ↔ network/address (`address_pool_network_default`, `address_pool_address_override`).
//
// Wave 5 replicate (KAC-94 A.7 sub-PR 1/6, skill evgeniy §6 G.1-G.7): CQRS-port
// для bindings, parity с AddressPoolReaderIface. Используется cascade-resolve
// (Step 1 address_override, Step 2 network_default) и handler-методами
// `BindAsNetworkDefault` / `BindAsAddressOverride`.
//
// Возвращает пустую строку + ErrNotFound если binding не задан (используется
// cascade-resolver для fall-through).
type AddressPoolBindingReaderIface interface {
	GetNetworkDefault(ctx context.Context, networkID string) (string, error)
	GetAddressOverride(ctx context.Context, addressID string) (string, error)
}

// AddressPoolBindingWriterIface — write-операции + read (G.2).
//
// DML-методы НЕ открывают свою TX и НЕ emit'ят outbox — это делает caller
// через `RepositoryWriter.Outbox().Emit(...)`. Atomicity DML + outbox
// гарантируется одной pgx.Tx writer'а.
//
// Set*-методы — upsert (ON CONFLICT DO UPDATE). Unset*-методы — idempotent
// DELETE (no error если binding не задан).
type AddressPoolBindingWriterIface interface {
	AddressPoolBindingReaderIface
	SetNetworkDefault(ctx context.Context, networkID, poolID string) error
	UnsetNetworkDefault(ctx context.Context, networkID string) error
	SetAddressOverride(ctx context.Context, addressID, poolID string) error
	UnsetAddressOverride(ctx context.Context, addressID string) error
}
