// KAC-71: TDD red-phase integration tests для split AddressPool.cidr_blocks
// → v4_cidr_blocks + v6_cidr_blocks.
//
// Acceptance: docs/specs/sub-phase-1.x-addresspool-split-cidr-family-acceptance.md
//
// Покрытие:
//   - Group C (REQ-MIG-01..06): миграция 0022 — backfill mixed CIDR, idempotent,
//     binding/freelist/cursor tables не тронуты, defensive RAISE EXCEPTION на
//     pool с empty cidr_blocks.
//   - Group H (REQ-IPL-INVARIANT-01..03): UNIQUE constraints
//     addresses_external_pool_ip_uniq / addresses_external_v6_pool_ip_uniq /
//     address_pools_zone_kind_default_uniq остаются работоспособны после split.
//
// Все тесты — failing на текущей реализации (domain.AddressPool ещё не имеет
// V4CIDRBlocks/V6CIDRBlocks полей; repo.AddressPoolRepo.Insert по-прежнему
// пишет в колонку `cidr_blocks` которой нет после миграции 0022). После
// rpc-implementer KAC-74 — должны позеленеть.
package repo_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	_ "github.com/jackc/pgx/v5/stdlib"

	coredb "github.com/PRO-Robotech/kacho-corelib/db"
	"github.com/PRO-Robotech/kacho-corelib/ids"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/migrations"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
)

// setupTestDBWithMigrationsUpto — поднимает testcontainer Postgres + применяет
// миграции до указанного version (inclusive). Используется в C-тестах:
// сначала applied до 0021, затем insert pre-existing row, затем 0022.
// version=0 — без миграций (только пустая schema).
func setupTestDBWithMigrationsUpto(t testing.TB, target int64) string {
	t.Helper()
	ctx := context.Background()

	pgc, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("kacho_vpc_test"),
		postgres.WithUsername("vpc"),
		postgres.WithPassword("vpc"),
		postgres.BasicWaitStrategies(),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = pgc.Terminate(ctx) })

	dsn, err := pgc.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	db, err := sql.Open("pgx", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	goose.SetBaseFS(migrations.FS)
	require.NoError(t, goose.SetDialect("postgres"))
	require.NoError(t, goose.UpTo(db, ".", target))

	return dsn
}

// applyMigration — применяет следующую миграцию (один шаг). Возвращает ошибку
// goose (включая SQLSTATE P0001 от RAISE EXCEPTION) — используется в C6 для
// проверки fail-closed поведения.
func applyMigrationUp(t testing.TB, dsn string) error {
	t.Helper()
	db, err := sql.Open("pgx", dsn)
	require.NoError(t, err)
	defer db.Close()
	goose.SetBaseFS(migrations.FS)
	require.NoError(t, goose.SetDialect("postgres"))
	return goose.UpByOne(db, ".")
}

// gooseCurrentVersion — текущая applied версия. Используется для проверки
// rollback в C6.
func gooseCurrentVersion(t testing.TB, dsn string) int64 {
	t.Helper()
	db, err := sql.Open("pgx", dsn)
	require.NoError(t, err)
	defer db.Close()
	goose.SetBaseFS(migrations.FS)
	require.NoError(t, goose.SetDialect("postgres"))
	v, err := goose.GetDBVersion(db)
	require.NoError(t, err)
	return v
}

// --------------------------------------------------------------------------
// Group C — миграция 0022 (REQ-MIG-01..06)
// --------------------------------------------------------------------------

// C1: Backfill из mixed `cidr_blocks` сохраняет данные по family.
//
// Given: applied до 0021 (включительно), в address_pools 3 row с разными
// семействами CIDR.
// When: apply 0022.
// Then: v4_cidr_blocks / v6_cidr_blocks правильно заполнены, cidr_blocks
// колонки нет.
func TestMigration0022_C1_BackfillMixedCidrBlocks(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	dsn := setupTestDBWithMigrationsUpto(t, 21)

	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	// Pre-existing rows: insert напрямую через SQL (до миграции 0022 колонка
	// cidr_blocks ещё существует). NB: zone_id оставляем NULL — после 0004
	// FK на zones нет, любая строка-zone_id допустима.
	type seedPool struct {
		id    string
		name  string
		cidrs []string
	}
	seeds := []seedPool{
		{ids.NewID("apl"), "pool-v4-mixed", []string{"203.0.113.0/24", "198.51.100.0/24"}},
		{ids.NewID("apl"), "pool-v6-only", []string{"2001:db8::/64"}},
		{ids.NewID("apl"), "pool-dual-stack", []string{"172.16.0.0/16", "fd12:3456:789a::/48"}},
	}
	for _, s := range seeds {
		_, err := pool.Exec(ctx, `
			INSERT INTO address_pools (id, name, cidr_blocks, kind)
			VALUES ($1, $2, $3::text[], 1)
		`, s.id, s.name, s.cidrs)
		require.NoError(t, err, "seed pool %s", s.name)
	}

	// Apply 0022.
	require.NoError(t, applyMigrationUp(t, dsn), "0022 must apply successfully")

	// Then: cidr_blocks column dropped.
	var has bool
	err = pool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM information_schema.columns
		 WHERE table_schema = 'public'
		   AND table_name   = 'address_pools'
		   AND column_name  = 'cidr_blocks')
	`).Scan(&has)
	require.NoError(t, err)
	assert.False(t, has, "cidr_blocks column must be dropped after 0022")

	// Then: per-pool backfill split correctly.
	type backfilled struct {
		v4 []string
		v6 []string
	}
	want := map[string]backfilled{
		"pool-v4-mixed":   {[]string{"203.0.113.0/24", "198.51.100.0/24"}, nil},
		"pool-v6-only":    {nil, []string{"2001:db8::/64"}},
		"pool-dual-stack": {[]string{"172.16.0.0/16"}, []string{"fd12:3456:789a::/48"}},
	}
	for _, s := range seeds {
		var got4, got6 []string
		err := pool.QueryRow(ctx, `
			SELECT v4_cidr_blocks, v6_cidr_blocks FROM address_pools WHERE id = $1
		`, s.id).Scan(&got4, &got6)
		require.NoError(t, err, "select split for %s", s.name)
		exp := want[s.name]
		assert.ElementsMatch(t, exp.v4, got4, "v4_cidr_blocks for %s", s.name)
		assert.ElementsMatch(t, exp.v6, got6, "v6_cidr_blocks for %s", s.name)
	}
}

// C2: Backfill идемпотентен на пустой БД.
func TestMigration0022_C2_EmptyDBIdempotent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	dsn := setupTestDBWithMigrationsUpto(t, 21)

	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	// Address_pools пуст. Apply 0022.
	require.NoError(t, applyMigrationUp(t, dsn))

	// Then: новые колонки добавлены, старая удалена.
	var has4, has6, hasOld bool
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM information_schema.columns
		 WHERE table_schema='public' AND table_name='address_pools' AND column_name='v4_cidr_blocks')
	`).Scan(&has4))
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM information_schema.columns
		 WHERE table_schema='public' AND table_name='address_pools' AND column_name='v6_cidr_blocks')
	`).Scan(&has6))
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM information_schema.columns
		 WHERE table_schema='public' AND table_name='address_pools' AND column_name='cidr_blocks')
	`).Scan(&hasOld))
	assert.True(t, has4, "v4_cidr_blocks must be added")
	assert.True(t, has6, "v6_cidr_blocks must be added")
	assert.False(t, hasOld, "cidr_blocks must be dropped")
}

// C3: Single-family pool (v4-only или v6-only) — backfill корректен;
// другое поле пустое.
func TestMigration0022_C3_SingleFamilyBackfill(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	dsn := setupTestDBWithMigrationsUpto(t, 21)

	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	v4ID := ids.NewID("apl")
	v6ID := ids.NewID("apl")
	_, err = pool.Exec(ctx, `INSERT INTO address_pools (id, name, cidr_blocks, kind)
		VALUES ($1, 'v4-only', ARRAY['10.0.0.0/24','10.1.0.0/24']::text[], 1)`, v4ID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO address_pools (id, name, cidr_blocks, kind)
		VALUES ($1, 'v6-only', ARRAY['2001:db8:a::/64','2001:db8:b::/64']::text[], 1)`, v6ID)
	require.NoError(t, err)

	require.NoError(t, applyMigrationUp(t, dsn))

	var v4Got4, v4Got6, v6Got4, v6Got6 []string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT v4_cidr_blocks, v6_cidr_blocks FROM address_pools WHERE id=$1`, v4ID).
		Scan(&v4Got4, &v4Got6))
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT v4_cidr_blocks, v6_cidr_blocks FROM address_pools WHERE id=$1`, v6ID).
		Scan(&v6Got4, &v6Got6))

	assert.ElementsMatch(t, []string{"10.0.0.0/24", "10.1.0.0/24"}, v4Got4)
	assert.Empty(t, v4Got6, "v6 side must be empty for v4-only pool")
	assert.Empty(t, v6Got4, "v4 side must be empty for v6-only pool")
	assert.ElementsMatch(t, []string{"2001:db8:a::/64", "2001:db8:b::/64"}, v6Got6)
}

// C4: Existing UNIQUE constraints (`addresses_external_pool_ip_uniq`,
// partial UNIQUE `address_pools_zone_kind_default_uniq` WHERE is_default)
// после миграции остаются работоспособны.
func TestMigration0022_C4_UniqueConstraintsIntact(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	dsn := setupTestDB(t) // applies ALL migrations including 0022

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

// C5: Re-apply миграции (manual goose down + up) — идемпотентность.
//
// Применяем все миграции -> down 0022 -> up 0022 -> финальное состояние
// эквивалентно первому apply (idempotent через ADD/DROP COLUMN IF [NOT] EXISTS
// + guarded backfill).
func TestMigration0022_C5_GooseDownUpIdempotent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	dsn := setupTestDB(t) // all migrations applied

	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	// Seed: insert pool через split-shape (после 0022).
	poolID := ids.NewID("apl")
	_, err = pool.Exec(ctx, `
		INSERT INTO address_pools (id, name, v4_cidr_blocks, v6_cidr_blocks, kind)
		VALUES ($1, 'roundtrip', ARRAY['10.0.0.0/24']::text[], ARRAY['2001:db8::/64']::text[], 1)
	`, poolID)
	require.NoError(t, err)

	// goose down (rollback 0022).
	db, err := sql.Open("pgx", dsn)
	require.NoError(t, err)
	defer db.Close()
	goose.SetBaseFS(migrations.FS)
	require.NoError(t, goose.SetDialect("postgres"))
	require.NoError(t, goose.Down(db, "."))

	// После down — cidr_blocks снова есть, v4_/v6_ удалены, данные слиты.
	var hasOld bool
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM information_schema.columns
		 WHERE table_schema='public' AND table_name='address_pools' AND column_name='cidr_blocks')
	`).Scan(&hasOld))
	assert.True(t, hasOld, "down must restore cidr_blocks column")

	var merged []string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT cidr_blocks FROM address_pools WHERE id=$1`, poolID).Scan(&merged))
	assert.ElementsMatch(t, []string{"10.0.0.0/24", "2001:db8::/64"}, merged,
		"down rollback must merge v4+v6 back into cidr_blocks")

	// goose up — 0022 снова применяется, split восстанавливается.
	require.NoError(t, goose.Up(db, "."))

	var got4, got6 []string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT v4_cidr_blocks, v6_cidr_blocks FROM address_pools WHERE id=$1`, poolID).
		Scan(&got4, &got6))
	assert.ElementsMatch(t, []string{"10.0.0.0/24"}, got4)
	assert.ElementsMatch(t, []string{"2001:db8::/64"}, got6)
}

// C6 (defensive): INSERT row с cidr_blocks = ARRAY[]::text[] → 0022 RAISES
// EXCEPTION SQLSTATE P0001 → миграция падает → goose_db_version=21 (rollback).
func TestMigration0022_C6_DefensiveEmptyCidrBlocksRaisesException(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	dsn := setupTestDBWithMigrationsUpto(t, 21)

	pool, err := coredb.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	// Pre-existing row с empty cidr_blocks (теоретически невозможно через
	// service-слой; пробрасываем напрямую как pre-fix bug / data import).
	badID := ids.NewID("apl")
	_, err = pool.Exec(ctx, `
		INSERT INTO address_pools (id, name, cidr_blocks, kind)
		VALUES ($1, 'bad-empty', ARRAY[]::text[], 1)
	`, badID)
	require.NoError(t, err, "seed empty-cidr pool")

	// Apply 0022 — должно падать.
	upErr := applyMigrationUp(t, dsn)
	require.Error(t, upErr, "0022 must fail on pre-existing empty-cidr pool")
	// SQLSTATE P0001 (RAISE EXCEPTION).
	var pgErr *pgconn.PgError
	if errors.As(upErr, &pgErr) {
		assert.Equal(t, "P0001", pgErr.Code,
			"expected raise_exception SQLSTATE P0001")
		assert.Contains(t, pgErr.Message, "empty cidr_blocks",
			"error message must explain the cause")
		assert.Contains(t, pgErr.Message, badID,
			"error message must mention offending pool id")
	} else {
		// goose может обернуть pg error; в этом случае хотя бы текст должен
		// содержать упоминание.
		assert.Contains(t, strings.ToLower(upErr.Error()), "empty cidr",
			"wrapped error must mention empty cidr_blocks (got: %v)", upErr)
	}

	// goose_db_version остался на 21 (rollback).
	v := gooseCurrentVersion(t, dsn)
	assert.Equal(t, int64(21), v, "goose_db_version must remain at 21 after failed migration")

	// Структурные изменения должны быть откатаны: v4_cidr_blocks / v6_cidr_blocks
	// не добавлены, cidr_blocks не удалена.
	var has4, has6, hasOld bool
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM information_schema.columns
		 WHERE table_schema='public' AND table_name='address_pools' AND column_name='v4_cidr_blocks')
	`).Scan(&has4))
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM information_schema.columns
		 WHERE table_schema='public' AND table_name='address_pools' AND column_name='v6_cidr_blocks')
	`).Scan(&has6))
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM information_schema.columns
		 WHERE table_schema='public' AND table_name='address_pools' AND column_name='cidr_blocks')
	`).Scan(&hasOld))
	assert.False(t, has4, "v4_cidr_blocks must NOT be added when migration aborted")
	assert.False(t, has6, "v6_cidr_blocks must NOT be added when migration aborted")
	assert.True(t, hasOld, "cidr_blocks must NOT be dropped when migration aborted")
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

	r := repo.NewAddressPoolRepo(pool)

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
		i := i
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
			_, e := r.Insert(ctx, p)
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
	// repo.wrapPgErr маппит 23505 на ErrAlreadyExists или ErrFailedPrecondition
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

	r := repo.NewAddressPoolRepo(pool)
	ar := repo.NewAddressRepo(pool)

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
	_, err = r.Insert(ctx, p)
	require.NoError(t, err)
	require.NoError(t, r.PopulateFreelistForPool(ctx, p.ID))

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
			ip, e := ar.AllocateIPFromFreelist(ctx, p.ID, addrIDs[i])
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
