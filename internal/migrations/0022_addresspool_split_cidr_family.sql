-- +goose Up
-- +goose StatementBegin

-- KAC-71: split address_pools.cidr_blocks → v4_cidr_blocks + v6_cidr_blocks
-- (parity с Subnet shape; делает family-фильтрацию IPAM cascade явной).
--
-- Acceptance: docs/specs/sub-phase-1.x-addresspool-split-cidr-family-acceptance.md
--   group C (REQ-MIG-01..06): C1 backfill сохраняет данные, C2 идемпотент на
--   пустой БД, C3 single-family pools, C4 binding/freelist/cursor таблицы не
--   тронуты, C5 re-apply идемпотентность (ADD COLUMN IF NOT EXISTS + backfill
--   guarded по information_schema.columns), C6 defensive RAISE EXCEPTION при
--   pool с пустым cidr_blocks (fail-closed; goose_db_version остаётся на 21).
--
-- Шаги:
--   1. Defensive guard (REQ-MIG-06 / C6) — RAISE EXCEPTION SQLSTATE P0001
--      если есть pool с cardinality(cidr_blocks)=0. Транзакция rollback'нута,
--      structural изменений нет, оператор fix'ит данные и retry'ит.
--   2. ADD COLUMN IF NOT EXISTS v4_cidr_blocks / v6_cidr_blocks (idempotent
--      при ручном re-run; goose-уровень тоже no-op после первого apply'я).
--   3. Backfill через DO-блок: содержит ':' → v6, иначе v4. Guarded по
--      существованию старой колонки `cidr_blocks` — если её уже нет
--      (повторное ручное применение), backfill skip.
--   4. DROP COLUMN cidr_blocks (с IF EXISTS — idempotent).
--
-- Сохранность: после миграции для каждого pool
--   v4_cidr_blocks ∪ v6_cidr_blocks (как множество строк) == прежний cidr_blocks.
-- FK на address_pools(id) у address_pool_free_ips / ipv6_pool_cursors /
-- address_pool_network_default / address_pool_address_override / ipv6_allocated_ips
-- остаются валидными — мы не трогаем primary key и не пересоздаём таблицу.

-- 1) Defensive guard (REQ-MIG-06 / C6) ------------------------------------
-- Pool с empty cidr_blocks теоретически невозможен (service-слой это запрещает
-- ещё до KAC-71 через REQ-IPL-CR-04-предшественник), но row могла просочиться
-- прямым INSERT'ом / data import'ом / pre-fix bug. После split такой row дал
-- бы v4=[], v6=[] — нарушение post-migration инварианта v4∪v6≠∅ (любой
-- последующий Update упал бы с InvalidArgument). Перехватываем до структурных
-- изменений: fail-closed, без data-poisoning через невидимое "оба пустые".
DO $$
DECLARE
    bad_pool_id TEXT;
BEGIN
    -- Если старая колонка `cidr_blocks` уже DROP'нута (ручной re-run после
    -- успешного apply'я) — проверять нечего, guard skip.
    IF NOT EXISTS (
        SELECT 1
        FROM information_schema.columns
        WHERE table_schema = 'public'
          AND table_name   = 'address_pools'
          AND column_name  = 'cidr_blocks'
    ) THEN
        RETURN;
    END IF;

    SELECT id INTO bad_pool_id
    FROM address_pools
    WHERE cardinality(cidr_blocks) = 0
    LIMIT 1;

    IF bad_pool_id IS NOT NULL THEN
        RAISE EXCEPTION
            'address_pool % has empty cidr_blocks; refusing to migrate (would violate post-migration invariant v4∪v6 ≠ ∅) (KAC-71)',
            bad_pool_id
        USING ERRCODE = 'P0001';
    END IF;
END
$$;

-- 2) ADD COLUMN IF NOT EXISTS (REQ-MIG-02 / C5) ---------------------------
-- IF NOT EXISTS делает шаг идемпотентным для ручного re-run; goose сам не
-- даст re-apply'ить уже applied миграцию, но при отладке (drop goose row,
-- replay вручную) — без этого было бы SQLSTATE 42701 duplicate_column.
ALTER TABLE address_pools ADD COLUMN IF NOT EXISTS v4_cidr_blocks text[] NOT NULL DEFAULT '{}'::text[];
ALTER TABLE address_pools ADD COLUMN IF NOT EXISTS v6_cidr_blocks text[] NOT NULL DEFAULT '{}'::text[];

-- 3) Backfill (REQ-MIG-01 / C1, REQ-MIG-03 / C3) --------------------------
-- Правило family detection: содержит ':' → v6, иначе v4. Это согласуется с
-- API-слоем (`netip.ParsePrefix` + `Addr().Is6()`) — IPv6-префиксы всегда
-- содержат ':' (separator octet groups), IPv4-префиксы — никогда. Backfill
-- идемпотентен per derivation: одинаковый input → одинаковый output.
--
-- Guarded по существованию старой колонки — если она уже DROP'нута (re-run
-- после успешного apply'я), backfill skip, no-op. UPDATE завёрнут в EXECUTE
-- внутри DO, иначе PL/pgSQL-планировщик попытается резолвить колонку
-- `cidr_blocks` при компиляции функции, даже если IF-ветка не сработает →
-- SQLSTATE 42703 undefined_column в re-run-сценарии.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM information_schema.columns
        WHERE table_schema = 'public'
          AND table_name   = 'address_pools'
          AND column_name  = 'cidr_blocks'
    ) THEN
        EXECUTE $sql$
            UPDATE address_pools
               SET v4_cidr_blocks = ARRAY(
                       SELECT c FROM unnest(cidr_blocks) AS u(c)
                        WHERE POSITION(':' IN c) = 0
                   ),
                   v6_cidr_blocks = ARRAY(
                       SELECT c FROM unnest(cidr_blocks) AS u(c)
                        WHERE POSITION(':' IN c) > 0
                   )
        $sql$;
    END IF;
END
$$;

-- 4) DROP COLUMN cidr_blocks ----------------------------------------------
-- IF EXISTS — idempotent при ручном re-run. Колонка не упомянута ни в одном
-- constraint, index или generated-column БД (проверено через grep по
-- internal/migrations/0001_initial.sql … 0021_external_ipv6.sql) — DROP
-- проходит чисто, без CASCADE.
ALTER TABLE address_pools DROP COLUMN IF EXISTS cidr_blocks;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- Best-effort rollback: восстановить `cidr_blocks` как объединение
-- v4_cidr_blocks + v6_cidr_blocks. Порядок элементов в объединении —
-- "v4 затем v6" (детерминирован, но может отличаться от исходного, если
-- pre-migration массив был mixed-order типа [v6, v4]). Это нормально:
-- семантика address_pools.cidr_blocks — set-like (порядок не используется
-- ни в одном service-/repo-методе; family определяется per-element).
--
-- Acceptance §«Скоуп — не делаем» помечает Down как "не входит в DoD"
-- (миграция split необратима на boilerplate level), но мы предоставляем
-- rollback path для local dev / отладки.

ALTER TABLE address_pools ADD COLUMN IF NOT EXISTS cidr_blocks text[] NOT NULL DEFAULT '{}'::text[];

UPDATE address_pools
   SET cidr_blocks = COALESCE(v4_cidr_blocks, '{}'::text[]) || COALESCE(v6_cidr_blocks, '{}'::text[]);

ALTER TABLE address_pools DROP COLUMN IF EXISTS v6_cidr_blocks;
ALTER TABLE address_pools DROP COLUMN IF EXISTS v4_cidr_blocks;

-- +goose StatementEnd
