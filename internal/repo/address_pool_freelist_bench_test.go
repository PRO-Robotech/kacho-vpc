package repo_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	coredb "github.com/PRO-Robotech/kacho-corelib/db"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
	kachopg "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/pg"
)

// BenchmarkAllocateExternalIP_Freelist — sequential measurement of the
// PG-native AllocateIPFromFreelist path (migration 0014). Drives a /16
// pool (~65k usable IPs) in a testcontainers Postgres so b.N может расти
// на десятки тысяч итераций без exhaustion.
//
// KAC-94 A.7 sub-PR 6/6: переписан на CQRS Writer (был на legacy repo).
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

	r := kachopg.New(pgPool, nil)
	defer r.Close()

	withTx := func(fn func(kacho.RepositoryWriter) error) error {
		w, err := r.Writer(ctx)
		if err != nil {
			return err
		}
		if err := fn(w); err != nil {
			w.Abort()
			return err
		}
		return w.Commit()
	}

	poolID := insertTestPoolForFreelist(b, ctx, pgPool, "10.10.0.0/16")
	require.NoError(b, withTx(func(w kacho.RepositoryWriter) error {
		return w.AddressPools().PopulateFreelistForPool(ctx, poolID)
	}))

	addrIDs := make([]string, b.N)
	for i := range addrIDs {
		addrIDs[i] = insertTestAddressFreelist(b, ctx, pgPool)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		err := withTx(func(w kacho.RepositoryWriter) error {
			_, e := w.Addresses().AllocateIPFromFreelist(ctx, poolID, addrIDs[i])
			return e
		})
		if err != nil {
			b.Fatalf("allocate iter %d: %v", i, err)
		}
	}
}

// BenchmarkAllocateExternalIP_Freelist_Parallel — concurrent measurement
// using b.RunParallel.
func BenchmarkAllocateExternalIP_Freelist_Parallel(b *testing.B) {
	if testing.Short() {
		b.Skip("skipping integration bench")
	}
	ctx := context.Background()
	dsn := setupTestDB(b)

	pgPool, err := coredb.NewPool(ctx, dsn)
	require.NoError(b, err)
	defer pgPool.Close()

	r := kachopg.New(pgPool, nil)
	defer r.Close()

	withTx := func(fn func(kacho.RepositoryWriter) error) error {
		w, err := r.Writer(ctx)
		if err != nil {
			return err
		}
		if err := fn(w); err != nil {
			w.Abort()
			return err
		}
		return w.Commit()
	}

	poolID := insertTestPoolForFreelist(b, ctx, pgPool, "10.0.0.0/13")
	require.NoError(b, withTx(func(w kacho.RepositoryWriter) error {
		return w.AddressPools().PopulateFreelistForPool(ctx, poolID)
	}))

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			addrID := insertTestAddressFreelist(b, ctx, pgPool)
			err := withTx(func(w kacho.RepositoryWriter) error {
				_, e := w.Addresses().AllocateIPFromFreelist(ctx, poolID, addrID)
				return e
			})
			if err != nil {
				b.Fatalf("parallel allocate: %v", err)
			}
		}
	})
}
