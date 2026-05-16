-- KAC-94 / skill evgeniy §5 E.4: переезд всех таблиц/функций kacho-vpc из
-- схемы `public` в схему `kacho_vpc` (parity с naming convention из workspace
-- CLAUDE.md: «Postgres database / schema — kacho_<domain>»).
--
-- До этой миграции все таблицы жили в `public`. Это нарушает E.4 «Schema = НЕ
-- public, а kacho_<svc>» и засоряет default-схему сервиса — любой ad-hoc psql
-- видел business-tables под голым именем без квалификатора, что плохо для
-- ревью dump'ов и для multi-tenant Postgres-хостов.
--
-- Эффект миграции (atomic — одна транзакция goose-StatementBegin):
--   1. CREATE SCHEMA kacho_vpc.
--   2. ALTER TABLE … SET SCHEMA kacho_vpc — для всех 21 owned-таблиц
--      (включая goose_db_version!) + autoMoved owned-sequences (PG автоматически
--      переносит OWNED-BY-sequence вслед за таблицей; проверено локально на
--      goose_db_version_id_seq + vpc_outbox_sequence_no_seq).
--   3. ALTER FUNCTION … SET SCHEMA kacho_vpc — для 5 user-функций (триггерные
--      и helper kacho_labels_valid). Триггеры ссылаются на функции по имени без
--      schema-quallifier — поэтому функция должна быть findable через
--      search_path сессии (см. ниже).
--   4. Расширение `btree_gist` остаётся в `public` — extension-owned, не наш
--      объект, и pg_dump/restore ожидает её там по умолчанию.
--
-- Application-сторона: pgxpool и migrate-conn (cmd/migrator) обязаны установить
-- `search_path TO kacho_vpc, public` на каждом соединении. Это сделано через
-- DSN-параметр `options=-c search_path=kacho_vpc,public` (libpq-стандарт,
-- поддерживается и pgx, и database/sql/pgx-stdlib). См. config.DSN() /
-- config.MigrateDSN() — там этот параметр добавляется в URL автоматически.
-- search_path необходим:
--   * чтобы unqualified-references из app-кода (`FROM networks`) резолвились
--     в `kacho_vpc.networks`;
--   * чтобы триггерные функции (rt_auto_assoc_subnets и др.), которые
--     ссылаются на таблицы без квалификатора, продолжали работать;
--   * чтобы CHECK-constraint `kacho_labels_valid(labels)` (миграция 0033)
--     находил функцию через search_path при INSERT/UPDATE.
--
-- Migration safety:
--   * Все ALTER … SET SCHEMA — metadata-only операции, fast на любом размере
--     данных (ACCESS EXCLUSIVE на каждую таблицу на time моментом каталог-write,
--     но без перезаписи строк).
--   * Goose сам отслеживает `goose_db_version` через search_path: после move
--     таблицы в `kacho_vpc` он её там и найдёт (требует search_path с
--     `kacho_vpc` впереди — см. DSN).
--   * Откат — зеркальный (Down): ALTER … SET SCHEMA public для всех объектов +
--     DROP SCHEMA kacho_vpc.
--
-- +goose Up
-- +goose StatementBegin

CREATE SCHEMA IF NOT EXISTS kacho_vpc;

-- 1. Tables (21) — owned sequences переезжают автоматически (PG semantics).
--    Порядок не важен: ALTER TABLE SET SCHEMA не trigger'ит FK-revalidation.
ALTER TABLE public.address_pool_address_override SET SCHEMA kacho_vpc;
ALTER TABLE public.address_pool_free_ips         SET SCHEMA kacho_vpc;
ALTER TABLE public.address_pool_network_default  SET SCHEMA kacho_vpc;
ALTER TABLE public.address_pools                 SET SCHEMA kacho_vpc;
ALTER TABLE public.address_references            SET SCHEMA kacho_vpc;
ALTER TABLE public.addresses                     SET SCHEMA kacho_vpc;
ALTER TABLE public.cloud_pool_selector           SET SCHEMA kacho_vpc;
ALTER TABLE public.gateways                      SET SCHEMA kacho_vpc;
ALTER TABLE public.goose_db_version              SET SCHEMA kacho_vpc;
ALTER TABLE public.ipv6_allocated_ips            SET SCHEMA kacho_vpc;
ALTER TABLE public.ipv6_pool_cursors             SET SCHEMA kacho_vpc;
ALTER TABLE public.ipv6_released_offsets         SET SCHEMA kacho_vpc;
ALTER TABLE public.network_interfaces            SET SCHEMA kacho_vpc;
ALTER TABLE public.networks                      SET SCHEMA kacho_vpc;
ALTER TABLE public.operations                    SET SCHEMA kacho_vpc;
ALTER TABLE public.private_endpoints             SET SCHEMA kacho_vpc;
ALTER TABLE public.route_tables                  SET SCHEMA kacho_vpc;
ALTER TABLE public.security_groups               SET SCHEMA kacho_vpc;
ALTER TABLE public.subnets                       SET SCHEMA kacho_vpc;
ALTER TABLE public.vpc_outbox                    SET SCHEMA kacho_vpc;
ALTER TABLE public.vpc_watch_cursors             SET SCHEMA kacho_vpc;

-- 2. User-defined functions (5). Триггерные привязаны по имени функции к
--    своим таблицам; имя резолвится через search_path сессии, поэтому
--    функцию обязательно перенести вслед за таблицами.
ALTER FUNCTION public.kacho_labels_valid(jsonb)                         SET SCHEMA kacho_vpc;
ALTER FUNCTION public.rt_auto_assoc_subnets()                           SET SCHEMA kacho_vpc;
ALTER FUNCTION public.subnet_auto_pick_rt()                             SET SCHEMA kacho_vpc;
ALTER FUNCTION public.subnets_outbox_emit_route_table_change()          SET SCHEMA kacho_vpc;
ALTER FUNCTION public.vpc_outbox_notify()                               SET SCHEMA kacho_vpc;

-- 3. Set session-level search_path. goose сразу после этого блока сделает
--    INSERT INTO goose_db_version — а таблица уже переехала в kacho_vpc.
--    Без SET search_path goose упадёт на 42P01 «relation goose_db_version
--    does not exist» (public.goose_db_version больше нет, search_path
--    указывает только в public). NOTE: это session-level (без LOCAL), чтобы
--    INSERT после COMMIT транзакции тоже видел новую схему.
SET search_path TO kacho_vpc, public;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- Зеркальный откат: возвращаем все объекты в `public`.
ALTER FUNCTION kacho_vpc.vpc_outbox_notify()                            SET SCHEMA public;
ALTER FUNCTION kacho_vpc.subnets_outbox_emit_route_table_change()       SET SCHEMA public;
ALTER FUNCTION kacho_vpc.subnet_auto_pick_rt()                          SET SCHEMA public;
ALTER FUNCTION kacho_vpc.rt_auto_assoc_subnets()                        SET SCHEMA public;
ALTER FUNCTION kacho_vpc.kacho_labels_valid(jsonb)                      SET SCHEMA public;

ALTER TABLE kacho_vpc.vpc_watch_cursors             SET SCHEMA public;
ALTER TABLE kacho_vpc.vpc_outbox                    SET SCHEMA public;
ALTER TABLE kacho_vpc.subnets                       SET SCHEMA public;
ALTER TABLE kacho_vpc.security_groups               SET SCHEMA public;
ALTER TABLE kacho_vpc.route_tables                  SET SCHEMA public;
ALTER TABLE kacho_vpc.private_endpoints             SET SCHEMA public;
ALTER TABLE kacho_vpc.operations                    SET SCHEMA public;
ALTER TABLE kacho_vpc.networks                      SET SCHEMA public;
ALTER TABLE kacho_vpc.network_interfaces            SET SCHEMA public;
ALTER TABLE kacho_vpc.ipv6_released_offsets         SET SCHEMA public;
ALTER TABLE kacho_vpc.ipv6_pool_cursors             SET SCHEMA public;
ALTER TABLE kacho_vpc.ipv6_allocated_ips            SET SCHEMA public;
ALTER TABLE kacho_vpc.goose_db_version              SET SCHEMA public;
ALTER TABLE kacho_vpc.gateways                      SET SCHEMA public;
ALTER TABLE kacho_vpc.cloud_pool_selector           SET SCHEMA public;
ALTER TABLE kacho_vpc.addresses                     SET SCHEMA public;
ALTER TABLE kacho_vpc.address_references            SET SCHEMA public;
ALTER TABLE kacho_vpc.address_pools                 SET SCHEMA public;
ALTER TABLE kacho_vpc.address_pool_network_default  SET SCHEMA public;
ALTER TABLE kacho_vpc.address_pool_free_ips         SET SCHEMA public;
ALTER TABLE kacho_vpc.address_pool_address_override SET SCHEMA public;

DROP SCHEMA IF EXISTS kacho_vpc;

-- Goose сейчас сделает DELETE FROM goose_db_version — а она снова в public.
-- Сбросим search_path обратно к default'у на случай, если сессия имела
-- предыдущий kacho_vpc впереди (например через DSN options).
SET search_path TO public;

-- +goose StatementEnd
