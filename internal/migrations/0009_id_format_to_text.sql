-- +goose Up
--
-- ID-FORMAT-MIGRATION: переход с UUID-id на YC-style "<3-char prefix><17-char
-- crockford-base32>" (всего 20 chars). См. findings/ID-FORMAT-MIGRATION.md.
--
-- Стратегия: dev-стенд, fresh start. TRUNCATE всех vpc-ресурсов CASCADE,
-- DROP FK, ALTER COLUMN id и FK-refs на TEXT, повторно ADD CONSTRAINT.
-- Generated stored column addresses.internal_subnet_id пересоздаётся с
-- TEXT-типом и без length=36 проверки (теперь длина 20).
--
-- ВНИМАНИЕ: эта миграция teardown-и все данные таблиц networks, subnets,
-- addresses, route_tables, security_groups. Таблица operations НЕ затрагивается
-- (там id уже TEXT — см. 0001_operations.sql).

-- 1. TRUNCATE с правильным порядком: сверху вниз по FK.
--    addresses → subnets/route_tables/security_groups → networks.
TRUNCATE addresses, subnets, route_tables, security_groups, networks RESTART IDENTITY CASCADE;

-- 2. DROP FK & generated columns которые мешают ALTER TYPE.

-- 2a. Drop EXCLUDE constraints на subnets.v4/v6_cidr_primary — они не
--     блокируют, но указанные generated stored columns ссылаются на
--     subnets table; при ALTER COLUMN id у нас нет конфликта, оставляем.
--     (Тем не менее: subnets.network_id type меняется на TEXT — это OK
--     для exclude indexes, тип network_id участвует в EXCLUDE и должен
--     быть совместим с btree_gist; TEXT поддерживается через btree_gist.)

-- 2b. Drop FKs которые блокируют ALTER TYPE родительской колонки.
ALTER TABLE addresses       DROP CONSTRAINT IF EXISTS addresses_internal_subnet_fkey;
ALTER TABLE subnets         DROP CONSTRAINT IF EXISTS subnets_network_id_fkey;
ALTER TABLE route_tables    DROP CONSTRAINT IF EXISTS route_tables_network_id_fkey;
ALTER TABLE security_groups DROP CONSTRAINT IF EXISTS security_groups_network_id_fkey;

-- 2c. Drop generated stored column addresses.internal_subnet_id (зависит от
--     internal_ipv4 JSONB и имеет length=36 check на UUID).
ALTER TABLE addresses DROP CONSTRAINT IF EXISTS addresses_internal_subnet_fkey;
DROP INDEX IF EXISTS addresses_internal_subnet_idx;
ALTER TABLE addresses DROP COLUMN IF EXISTS internal_subnet_id;

-- 3. ALTER COLUMN id и FK refs UUID → TEXT.

-- 3a. networks
ALTER TABLE networks ALTER COLUMN id TYPE TEXT;
-- default_security_group_id уже TEXT (см. 0002_networks.sql).

-- 3b. subnets
ALTER TABLE subnets ALTER COLUMN id TYPE TEXT;
ALTER TABLE subnets ALTER COLUMN network_id TYPE TEXT;
-- route_table_id уже TEXT (см. 0003_subnets.sql).

-- 3c. addresses
ALTER TABLE addresses ALTER COLUMN id TYPE TEXT;
-- folder_id уже TEXT.

-- 3d. route_tables
ALTER TABLE route_tables ALTER COLUMN id TYPE TEXT;
ALTER TABLE route_tables ALTER COLUMN network_id TYPE TEXT;

-- 3e. security_groups
ALTER TABLE security_groups ALTER COLUMN id TYPE TEXT;
ALTER TABLE security_groups ALTER COLUMN network_id TYPE TEXT;

-- 4. Re-add generated stored addresses.internal_subnet_id с TEXT-типом.
--    Ранее проверялось `length(...) = 36` (UUID-длина); новый формат может
--    иметь длину 20 (yc-style) или 36 (legacy UUID). Проверяем `length > 0`
--    и НЕ пустую строку — и FK-constraint доделает остальное (REFERENCES
--    subnets(id) ON DELETE RESTRICT).

-- +goose StatementBegin
ALTER TABLE addresses
  ADD COLUMN internal_subnet_id TEXT
  GENERATED ALWAYS AS (
    CASE WHEN internal_ipv4 IS NOT NULL
         AND internal_ipv4 ? 'subnet_id'
         AND length(internal_ipv4->>'subnet_id') > 0
    THEN internal_ipv4->>'subnet_id'
    ELSE NULL
    END
  ) STORED;
-- +goose StatementEnd

CREATE INDEX addresses_internal_subnet_idx ON addresses (internal_subnet_id);

-- 5. Re-add FK constraints.
ALTER TABLE addresses ADD CONSTRAINT addresses_internal_subnet_fkey
  FOREIGN KEY (internal_subnet_id) REFERENCES subnets(id) ON DELETE RESTRICT;
ALTER TABLE subnets ADD CONSTRAINT subnets_network_id_fkey
  FOREIGN KEY (network_id) REFERENCES networks(id);
ALTER TABLE route_tables ADD CONSTRAINT route_tables_network_id_fkey
  FOREIGN KEY (network_id) REFERENCES networks(id);
ALTER TABLE security_groups ADD CONSTRAINT security_groups_network_id_fkey
  FOREIGN KEY (network_id) REFERENCES networks(id) ON DELETE RESTRICT;

-- +goose Down
-- Rollback не предусмотрен (TRUNCATE необратим). Для отката:
--   1. Восстановить из backup.
--   2. Применить старые миграции 0006/0008 на пустых таблицах.
