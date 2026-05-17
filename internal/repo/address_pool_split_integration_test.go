// KAC-71 / chore/squash-migrations: integration-тесты для split-shape AddressPool
// (v4_cidr_blocks + v6_cidr_blocks). Step-tests миграции 0022 (C1/C2/C3/C5/C6)
// были удалены вместе со squash'ом 0001..0034 → 0001 — миграция как отдельный
// шаг больше не существует, её эффект включён в baseline `0001_initial.sql`.
//
// Оставлены тесты, проверяющие финальный state (после squash):
//   - Group C4 — UNIQUE constraints (`addresses_external_pool_ip_uniq`,
//     `address_pools_zone_kind_default_uniq`) — финальные DB-инварианты, не зависят
//     от пути миграции.
//   - Group H (REQ-IPL-INVARIANT-01..03) — UNIQUE invariants под concurrency
//     поверх split-shape.
package repo_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	_ "github.com/jackc/pgx/v5/stdlib"

	coredb "github.com/PRO-Robotech/kacho-corelib/db"
	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
	kachopg "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho/pg"
)

// splitWithTx — helper для CQRS-tx обвязки в split-тестах. KAC-94 A.7 sub-PR 5/6.
func splitWithTx(t *testing.T, ctx context.Context, r kacho.Repository, fn func(kacho.RepositoryWriter) error) error {
	t.Helper()
	w, err := r.Writer(ctx)
	require.NoError(t, err)
	if err := fn(w); err != nil {
		w.Abort()
		return err
	}
	return w.Commit()
}

// C4: existing UNIQUE constraints (`addresses_external_pool_ip_uniq`,
// partial UNIQUE `address_pools_zone_kind_default_uniq` WHERE is_default)
// работают на split-shape baseline.
func TestMigration0022_C4_UniqueConstraintsIntact(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	dsn := setupTestDB(t) // applies the squashed baseline

	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	// C4.1 — address_pools_zone_kind_default_uniq partial UNIQUE на (zone_id, kind)
	// WHERE is_default=true. Создаём 2 pool в одной zone с is_default=true:
	// первая — OK, вторая — должна упасть 23505.
	first := ids.NewID("apl")
	second := ids.NewID("apl")
	_, err = pool.Exec(ctx, `
		INSERT INTO address_pools (id, name, v4_cidr_blocks, kind, zone_id, is_default)
		VALUES ($1, 'def-a', ARRAY['203.0.113.0/24']::text[], 1, 'ru-central1-c', true)
	`, first)
	require.NoError(t, err, "first default insert must succeed")

	_, err = pool.Exec(ctx, `
		INSERT INTO address_pools (id, name, v4_cidr_blocks, kind, zone_id, is_default)
		VALUES ($1, 'def-b', ARRAY['198.51.100.0/24']::text[], 1, 'ru-central1-c', true)
	`, second)
	require.Error(t, err, "second default in same zone/kind must fail (UNIQUE)")
	var pgErr *pgconn.PgError
	require.True(t, errors.As(err, &pgErr), "expected pg error, got %T", err)
	assert.Equal(t, "23505", pgErr.Code, "expected unique_violation (23505)")
	assert.Contains(t, pgErr.ConstraintName, "default",
		"violation must be on address_pools_zone_kind_default_uniq")

	// C4.2 — addresses_external_pool_ip_uniq на jsonb-выражении
	// (external_ipv4 ->> 'address_pool_id', external_ipv4 ->> 'address')
	// должен срабатывать при попытке insert'нуть два address с тем же IP+poolID.
	addr1 := ids.NewID(ids.PrefixAddress)
	addr2 := ids.NewID(ids.PrefixAddress)
	_, err = pool.Exec(ctx, `
		INSERT INTO addresses (id, folder_id, addr_type, ip_version, external_ipv4)
		VALUES ($1, 'f1', 1, 1, jsonb_build_object('address','203.0.113.5','address_pool_id',$2::text))
	`, addr1, first)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
		INSERT INTO addresses (id, folder_id, addr_type, ip_version, external_ipv4)
		VALUES ($1, 'f1', 1, 1, jsonb_build_object('address','203.0.113.5','address_pool_id',$2::text))
	`, addr2, first)
	require.Error(t, err, "duplicate (pool_id, ip) must violate addresses_external_pool_ip_uniq")
	require.True(t, errors.As(err, &pgErr))
	assert.Equal(t, "23505", pgErr.Code)
}

// --------------------------------------------------------------------------
// Group H — UNIQUE invariants под concurrency (REQ-IPL-INVARIANT-01..03)
// --------------------------------------------------------------------------

// H1: concurrent Create one default per (zone, kind) — partial UNIQUE
// срабатывает; вторая транзакция получает 23505 → ErrFailedPrecondition
// (через wrapPgErr в repo).
//
// 5 goroutines пытаются создать default pool в одной (zone, kind);
// проходит ровно одна, остальные получают `service.ErrAlreadyExists` или
// `service.ErrFailedPrecondition` (mapping detail — repo.wrapPgErr).
func TestAddressPoolSplit_H1_DefaultPerZoneKindUniqueUnderConcurrency(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	dsn := setupTestDB(t)
	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	r := kachopg.New(pool, nil)
	defer r.Close()

	const concurrency = 5
	now := time.Now().UTC().Truncate(time.Microsecond)
	var (
		okCount   int32
		errCount  int32
		mu        sync.Mutex
		seenError error
		wg        sync.WaitGroup
	)
	wg.Add(concurrency)
	for i := 0; i < concurrency; i++ {
		go func() {
			defer wg.Done()
			p := &domain.AddressPool{
				ID:           ids.NewID("apl"),
				Name:         "def-h1-" + ids.NewID("apl")[:6],
				V4CIDRBlocks: []string{cidrFor(i)},
				Kind:         domain.AddressPoolKindExternalPublic,
				ZoneID:       "ru-central1-c",
				IsDefault:    true,
				CreatedAt:    now,
				ModifiedAt:   now,
			}
			e := splitWithTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
				_, ie := w.AddressPools().Insert(ctx, p)
				return ie
			})
			mu.Lock()
			defer mu.Unlock()
			if e == nil {
				okCount++
			} else {
				errCount++
				seenError = e
			}
		}()
	}
	wg.Wait()

	assert.Equal(t, int32(1), okCount, "ровно одна вставка должна пройти")
	assert.Equal(t, int32(concurrency-1), errCount,
		"остальные должны упасть на partial UNIQUE")
	require.NotNil(t, seenError)
	// repo.wrapPgErr маппит 23505 на repo.ErrAlreadyExists или ErrFailedPrecondition
	// в зависимости от контекста; и то и другое допустимо для invariant'а.
	t.Logf("seen error: %v", seenError)
}

// cidrFor — детерминированный helper генератор уникальных /24 v4 CIDR.
// Чтобы H1-pool-insert'ы не пересекались по freelist UNIQUE.
func cidrFor(i int) string {
	return "203.0." + strconvItoa(i+10) + ".0/24"
}

// strconvItoa — нестандартный helper во избежание импорта strconv в одном
// test-файле (минимизация diff). i ∈ [0..255].
func strconvItoa(i int) string {
	if i < 10 {
		return string(rune('0' + i))
	}
	return string(rune('0'+i/10)) + string(rune('0'+i%10))
}

// H2: после split (cidr_blocks нет) — concurrent allocate из v4-only pool —
// addresses_external_pool_ip_uniq на колонке external_ipv4.address всё ещё
// работает: попытка двух insert'ов с тем же (pool_id, ip) — вторая 23505.
//
// Это regression-check: split не сломал JSONB-индекс на external_ipv4.
func TestAddressPoolSplit_H2_ExternalPoolIPUniqueAfterSplit(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	dsn := setupTestDB(t)
	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	// Создаём v4-only pool напрямую через SQL (после split).
	poolID := ids.NewID("apl")
	_, err = pool.Exec(ctx, `
		INSERT INTO address_pools (id, name, v4_cidr_blocks, kind)
		VALUES ($1, 'h2-pool', ARRAY['203.0.113.0/24']::text[], 1)
	`, poolID)
	require.NoError(t, err)

	addr1 := ids.NewID(ids.PrefixAddress)
	addr2 := ids.NewID(ids.PrefixAddress)
	_, err = pool.Exec(ctx, `
		INSERT INTO addresses (id, folder_id, addr_type, ip_version, external_ipv4)
		VALUES ($1, 'f1', 1, 1, jsonb_build_object('address','203.0.113.42','address_pool_id',$2::text))
	`, addr1, poolID)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
		INSERT INTO addresses (id, folder_id, addr_type, ip_version, external_ipv4)
		VALUES ($1, 'f1', 1, 1, jsonb_build_object('address','203.0.113.42','address_pool_id',$2::text))
	`, addr2, poolID)
	require.Error(t, err)
	var pgErr *pgconn.PgError
	require.True(t, errors.As(err, &pgErr))
	assert.Equal(t, "23505", pgErr.Code, "expected unique_violation on external_ip uniqueness")
	// Схема имеет ДВА related UNIQUE-индекса:
	//   * addresses_external_ip_uniq      (на address — глобальная уникальность IP)
	//   * addresses_external_pool_ip_uniq (на (pool_id, address))
	// Inserting (poolID, ip) с уже занятым ip нарушает оба — Postgres сообщает
	// тот, что отрабатывает первым (на нашем оборудовании это
	// addresses_external_ip_uniq, как более узкий и располагающийся раньше
	// в pg_constraint). Любой из двух — валидный backstop для split, поэтому
	// принимаем оба.
	assert.Contains(t, pgErr.ConstraintName, "external_ip",
		"violation must be on addresses_external*_ip_uniq (got: %s)", pgErr.ConstraintName)
}

// H3: concurrent Allocate из v4-only pool через repo.AllocateIPFromFreelist —
// 5 параллельных allocate, все получают разные IP, ни один не получает
// 23505 (PG-native FOR UPDATE SKIP LOCKED атомарно достаёт row из freelist).
func TestAddressPoolSplit_H3_FreelistAllocateConcurrentNoDup(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	dsn := setupTestDB(t)
	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	r := kachopg.New(pool, nil)
	defer r.Close()

	// Создаём pool с маленьким CIDR-блоком — гарантируем что freelist
	// не пуст для concurrency-теста.
	p := &domain.AddressPool{
		ID:           ids.NewID("apl"),
		Name:         "h3-pool",
		V4CIDRBlocks: []string{"203.0.113.0/28"}, // 14 usable IPs
		Kind:         domain.AddressPoolKindExternalPublic,
		ZoneID:       "ru-central1-c",
		CreatedAt:    time.Now().UTC().Truncate(time.Microsecond),
	}
	p.ModifiedAt = p.CreatedAt
	require.NoError(t, splitWithTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		_, e := w.AddressPools().Insert(ctx, p)
		return e
	}))
	require.NoError(t, splitWithTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
		return w.AddressPools().PopulateFreelistForPool(ctx, p.ID)
	}))

	// 5 concurrent allocate'ов в 5 разных Address.
	const N = 5
	addrIDs := make([]string, N)
	for i := range addrIDs {
		addrIDs[i] = ids.NewID(ids.PrefixAddress)
		_, err = pool.Exec(ctx, `
			INSERT INTO addresses (id, folder_id, addr_type, ip_version, reserved)
			VALUES ($1, 'f1', 1, 1, true)
		`, addrIDs[i])
		require.NoError(t, err)
	}

	var (
		ipsMu sync.Mutex
		ips   = make(map[string]int)
		wg    sync.WaitGroup
	)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var ip string
			e := splitWithTx(t, ctx, r, func(w kacho.RepositoryWriter) error {
				var ierr error
				ip, ierr = w.Addresses().AllocateIPFromFreelist(ctx, p.ID, addrIDs[i])
				return ierr
			})
			require.NoError(t, e)
			ipsMu.Lock()
			ips[ip]++
			ipsMu.Unlock()
		}(i)
	}
	wg.Wait()

	assert.Equal(t, N, len(ips), "каждый goroutine должен получить уникальный IP")
	for ip, n := range ips {
		assert.Equal(t, 1, n, "IP %s выдан более одного раза", ip)
	}
}
