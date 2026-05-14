package repo_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	coredb "github.com/PRO-Robotech/kacho-corelib/db"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
)

// BenchmarkAllocateExternalIP_Freelist — sequential measurement of the
// PG-native AllocateIPFromFreelist path (migration 0014). Drives a /16
// pool (~65k usable IPs) in a testcontainers Postgres so b.N может расти
// на десятки тысяч итераций без exhaustion.
//
// Цель — снять «ножку отсчёта» ns/op для одного allocate-вызова на голом
// SQL (без service / gRPC overhead) — для BASELINE.md в Phase 5.
//
// Запуск:
//
//	go test ./internal/repo/... -bench BenchmarkAllocateExternalIP_Freelist \
//	  -benchmem -count=1 -run=^$ -timeout 10m
func BenchmarkAllocateExternalIP_Freelist(b *testing.B) {
	if testing.Short() {
		b.Skip("skipping integration bench")
	}
	ctx := context.Background()
	dsn := setupTestDB(b)

	pgPool, err := coredb.NewPool(ctx, dsn)
	require.NoError(b, err)
	defer pgPool.Close()

	poolID := insertTestPoolForFreelist(b, ctx, pgPool, "10.10.0.0/16")
	poolRepo := repo.NewAddressPoolRepo(pgPool)
	require.NoError(b, poolRepo.PopulateFreelistForPool(ctx, poolID))

	addrRepo := repo.NewAddressRepo(pgPool)

	addrIDs := make([]string, b.N)
	for i := range addrIDs {
		addrIDs[i] = insertTestAddressFreelist(b, ctx, pgPool)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := addrRepo.AllocateIPFromFreelist(ctx, poolID, addrIDs[i]); err != nil {
			b.Fatalf("allocate iter %d: %v", i, err)
		}
	}
}

// BenchmarkAllocateExternalIP_Freelist_Parallel — concurrent measurement
// using b.RunParallel. Drives a /13 (~520k usable IPs) so concurrent goroutines
// can grind without exhausting the pool.
//
// Address rows нельзя пре-создать (b.N неизвестен внутри RunParallel) —
// каждый goroutine вставляет свой address в горячем цикле; это добавляет
// фиксированный overhead к каждой итерации, но он консистентен между
// прогонами и не маскирует contention в самом allocate-SQL.
//
// Запуск идентичен sequential bench'у (одно `-bench` regex покрывает обе).
func BenchmarkAllocateExternalIP_Freelist_Parallel(b *testing.B) {
	if testing.Short() {
		b.Skip("skipping integration bench")
	}
	ctx := context.Background()
	dsn := setupTestDB(b)

	pgPool, err := coredb.NewPool(ctx, dsn)
	require.NoError(b, err)
	defer pgPool.Close()

	poolID := insertTestPoolForFreelist(b, ctx, pgPool, "10.0.0.0/13")
	poolRepo := repo.NewAddressPoolRepo(pgPool)
	require.NoError(b, poolRepo.PopulateFreelistForPool(ctx, poolID))

	addrRepo := repo.NewAddressRepo(pgPool)

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			addrID := insertTestAddressFreelist(b, ctx, pgPool)
			if _, err := addrRepo.AllocateIPFromFreelist(ctx, poolID, addrID); err != nil {
				b.Fatalf("parallel allocate: %v", err)
			}
		}
	})
}
