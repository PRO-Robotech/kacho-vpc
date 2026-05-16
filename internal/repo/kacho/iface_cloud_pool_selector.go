package kacho

import (
	"context"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

// CloudPoolSelectorReaderIface — read-операция над `cloud_pool_selector`.
//
// Wave 5 replicate (KAC-94 A.7 sub-PR 1/6, skill evgeniy §6 G.1-G.7): admin-
// controlled routing-labels на уровне Cloud. Используется cascade-resolve
// Step 3 (label_selector match): folder_id → cloud_id (peer-call к
// resource-manager) → selector через этот port → FindBySelectorMatch.
//
// Возвращает ErrNotFound если selector не задан (cascade-resolver fall-through).
type CloudPoolSelectorReaderIface interface {
	Get(ctx context.Context, cloudID string) (*domain.CloudPoolSelector, error)
}

// CloudPoolSelectorWriterIface — write-операции + read (G.2).
//
// DML-методы НЕ открывают свою TX и НЕ emit'ят outbox — это делает caller через
// `RepositoryWriter.Outbox().Emit(...)`. Atomicity DML + outbox гарантируется
// одной pgx.Tx writer'а.
//
// Set — upsert (ON CONFLICT DO UPDATE). Unset — idempotent DELETE.
type CloudPoolSelectorWriterIface interface {
	CloudPoolSelectorReaderIface
	Set(ctx context.Context, cloudID string, selector map[string]string, setBy string) error
	Unset(ctx context.Context, cloudID string) error
}
