package repo

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/PRO-Robotech/kacho-vpc/internal/repo/helpers"
)

// KAC-94 A.7 sub-PR 4/6 + ultra-final: реальная реализация — в
// `internal/repo/helpers/outbox.go` (`helpers.EmitVPC` / `helpers.DomainToMap`
// / `helpers.NetworkPayload` etc.). Тонкие unexported алиасы оставлены для
// legacy `*_repo.go`, которые удалены в Sub-PR 6+. Однотипные payload-функции
// больше не нужны (legacy *Repo, использовавшие их через type-alias на
// repo-entity, удалены) — оставлены только generic emitVPC / domainToMap для
// будущих repo-уровневых helper'ов (если потребуются).

// emitVPC — alias на helpers.EmitVPC. Используется внутри пакета `repo` для
// raw-SQL bench-/integration-тестов (`address_pool_freelist_bench_test.go`).
func emitVPC(ctx context.Context, tx pgx.Tx, kind, id, eventType string, payload map[string]any) error {
	return helpers.EmitVPC(ctx, tx, kind, id, eventType, payload)
}

// domainToMap — alias на helpers.DomainToMap.
func domainToMap(v any) map[string]any {
	return helpers.DomainToMap(v)
}
