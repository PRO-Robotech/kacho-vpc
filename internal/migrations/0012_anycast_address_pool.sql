-- Copyright (c) PRO-Robotech
-- SPDX-License-Identifier: BUSL-1.1

-- +goose Up
-- +goose StatementBegin

-- =============================================================================
-- AnycastAddressPool — tenant-facing project-scoped ресурс + единый DB-уровневый
-- инвариант непересечения CIDR-претензий сети (network_cidr_claims + EXCLUDE).
-- =============================================================================
-- Состав:
--   1. anycast_address_pools — flat-ресурс (scope/ip_version/cidr_blocks immutable),
--      CHECK на форму scope/ip_version/status; partial UNIQUE «один is_default на
--      (scope, ip_version)»; cidr_blocks text[] — денормализованный вид для reads.
--   1b. anycast_address_pool_cidrs — нормализованная child-таблица блоков пула
--      (block cidr): CHECK reserved-диапазона по family + EXCLUDE gist на
--      непересечение блоков ВНУТРИ пула (pool_id WITH =, block WITH &&).
--   2. anycast_pool_network_attachments — M:N pivot пул↔сеть (same-DB FK,
--      ON DELETE CASCADE c обеих сторон).
--   3. УНИФИКАЦИЯ subnet_cidr_blocks → network_cidr_claims: одна child-таблица
--      претензий сети для ОБОИХ источников (subnet-блоки И anycast-пулы), один
--      EXCLUDE gist (network_id WITH =, cidr_range WITH &&) закрывает overlap в
--      обе стороны атомарно (subnet поверх пула И пул поверх subnet'а, конкурентно).
--   4. Seed платформенного is_default IPv4-пула из reserved 100.64.0.0/10.
--
-- btree_gist и helper kacho_labels_valid уже созданы в базовой схеме.

SET search_path TO kacho_vpc, public;

-- -----------------------------------------------------------------------------
-- 1. anycast_address_pools
-- -----------------------------------------------------------------------------
-- scope/ip_version/cidr_blocks — immutable после Create (новый пул вместо изменения
-- адресного пространства); name/description/labels — mutable через update_mask.
-- cidr_blocks — денормализованный text[]-вид адресных блоков пула для быстрых reads;
-- источник истины и DB-инвариант непересечения блоков ВНУТРИ пула — нормализованная
-- child-таблица anycast_address_pool_cidrs (см. 1b). reserved-диапазон и family
-- блоков энфорсятся на child-таблице, на самой пуловой строке остаются только
-- инварианты формы scope/ip_version/status.
CREATE TABLE IF NOT EXISTS kacho_vpc.anycast_address_pools (
    id          text         PRIMARY KEY,
    project_id  text         NOT NULL,                 -- cross-service ref на iam, без FK
    created_at  timestamptz  NOT NULL DEFAULT now(),
    name        text         NOT NULL DEFAULT '',
    description text         NOT NULL DEFAULT '',
    labels      jsonb        NOT NULL DEFAULT '{}'::jsonb,
    scope       text         NOT NULL,                 -- enum AnycastScope; фаза 1 — только INTERNAL
    ip_version  text         NOT NULL,                 -- IpVersion: IPV4 | IPV6
    cidr_blocks text[]       NOT NULL DEFAULT '{}',    -- денорм-вид блоков (истина — child anycast_address_pool_cidrs)
    is_default  boolean      NOT NULL DEFAULT false,   -- платформенный admin-owned пул
    status      text         NOT NULL,                 -- CREATING | ACTIVE | DELETING

    CONSTRAINT anycast_address_pools_name_chk
        CHECK (name ~ '^([a-zA-Z]([-_a-zA-Z0-9]{0,61}[a-zA-Z0-9])?)?$'),
    CONSTRAINT anycast_address_pools_description_len_chk
        CHECK (char_length(description) <= 256),
    CONSTRAINT anycast_address_pools_labels_valid
        CHECK (kacho_vpc.kacho_labels_valid(labels)),
    CONSTRAINT anycast_address_pools_scope_chk
        CHECK (scope IN ('INTERNAL')),
    CONSTRAINT anycast_address_pools_ip_version_chk
        CHECK (ip_version IN ('IPV4', 'IPV6')),
    CONSTRAINT anycast_address_pools_status_chk
        CHECK (status IN ('CREATING', 'ACTIVE', 'DELETING'))
);

-- Один is_default на (scope, ip_version) — платформенный fallback ровно один на
-- семейство и scope (partial UNIQUE; tenant-пулы с is_default=false не участвуют).
CREATE UNIQUE INDEX IF NOT EXISTS anycast_address_pools_default_uniq
    ON kacho_vpc.anycast_address_pools (scope, ip_version) WHERE is_default;

-- Уникальность имени в проекте (пустое имя допускается) — parity c прочими ресурсами.
CREATE UNIQUE INDEX IF NOT EXISTS anycast_address_pools_project_name_uniq
    ON kacho_vpc.anycast_address_pools (project_id, name) WHERE name <> '';

CREATE INDEX IF NOT EXISTS anycast_address_pools_project_idx
    ON kacho_vpc.anycast_address_pools (project_id);
-- Композитный (created_at, id) под keyset-пагинацию api-conventions (cursor = (created_at, id)).
CREATE INDEX IF NOT EXISTS anycast_address_pools_created_at_idx
    ON kacho_vpc.anycast_address_pools (created_at, id);
CREATE INDEX IF NOT EXISTS anycast_address_pools_labels_idx
    ON kacho_vpc.anycast_address_pools USING gin (labels jsonb_path_ops);

-- -----------------------------------------------------------------------------
-- 1b. anycast_address_pool_cidrs — нормализованные блоки пула
-- -----------------------------------------------------------------------------
-- Источник истины адресных блоков пула + DB-инвариант непересечения блоков ВНУТРИ
-- одного пула: EXCLUDE gist (pool_id WITH =, block WITH &&) запрещает overlap двух
-- блоков одного пула (иначе пул не приаттачить — его блоки конфликтнут друг с другом
-- в network_cidr_claims на attach). Cross-pool пересечение допустимо: изоляция
-- per-network через network_cidr_claims на attach, а НЕ глобально. reserved-CHECK:
-- IPv4-блок внутри 100.64.0.0/10 (RFC 6598 Shared Address Space, provider-purpose),
-- IPv6-блок внутри provider-ULA fd00:ca00::/48. Стиль зеркалит address_pool_cidrs
-- (миграция 0004); btree_gist уже создан в базовой схеме.
CREATE TABLE IF NOT EXISTS kacho_vpc.anycast_address_pool_cidrs (
    pool_id text NOT NULL REFERENCES kacho_vpc.anycast_address_pools(id) ON DELETE CASCADE,
    block   cidr NOT NULL,
    PRIMARY KEY (pool_id, block),
    CONSTRAINT anycast_address_pool_cidrs_reserved_chk CHECK (
        (family(block) = 4 AND block <<= '100.64.0.0/10'::cidr) OR
        (family(block) = 6 AND block <<= 'fd00:ca00::/48'::cidr)),
    CONSTRAINT anycast_address_pool_cidrs_no_overlap
        EXCLUDE USING gist (pool_id WITH =, block inet_ops WITH &&)
);
CREATE INDEX IF NOT EXISTS anycast_address_pool_cidrs_pool_id_idx
    ON kacho_vpc.anycast_address_pool_cidrs (pool_id);

-- -----------------------------------------------------------------------------
-- 2. anycast_pool_network_attachments — M:N pivot пул↔сеть
-- -----------------------------------------------------------------------------
-- Same-DB FK с ON DELETE CASCADE: удаление пула или сети снимает pivot-строку.
-- Платформенный is_default авто-доступен каждой сети БЕЗ явной pivot-строки.
CREATE TABLE IF NOT EXISTS kacho_vpc.anycast_pool_network_attachments (
    pool_id    text        NOT NULL REFERENCES kacho_vpc.anycast_address_pools(id) ON DELETE CASCADE,
    network_id text        NOT NULL REFERENCES kacho_vpc.networks(id)              ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (pool_id, network_id)
);
CREATE INDEX IF NOT EXISTS anycast_pool_network_attachments_network_idx
    ON kacho_vpc.anycast_pool_network_attachments (network_id);

-- -----------------------------------------------------------------------------
-- 3. subnet_cidr_blocks → network_cidr_claims (унификация претензий сети)
-- -----------------------------------------------------------------------------
-- Прежняя subnet_cidr_blocks держала только subnet-блоки. Anycast-пул претендует
-- на CIDR в той же сети — чтобы ОДИН EXCLUDE покрывал и subnet-блоки, и pool-claim'ы
-- (overlap subnet↔pool в обе стороны, конкурентно), таблица переименовывается и
-- обобщается: kind ∈ {'subnet','anycast_pool'}, ref_id = id сабнета/пула.
--
-- subnet_id остаётся NULLABLE и сохраняет FK→subnets ON DELETE CASCADE: для
-- kind='subnet' он равен ref_id и обеспечивает авто-снятие claim при удалении
-- подсети; для kind='anycast_pool' subnet_id IS NULL (claim снимается репозиторием
-- в writer-TX :detachNetwork / Delete пула). EXCLUDE-констрейнт и его scope-ключ
-- network_id переезжают вместе с таблицей; rename колонки block→cidr_range
-- автоматически обновляет определение EXCLUDE на новое имя.
--
-- ВАЖНО (контракт для repo-слоя): claim в network_cidr_claims обязан писаться в
-- ТОЙ ЖЕ writer-TX, что и основной DML — kind='subnet' на Subnet.Create и
-- Subnet:addCidrBlocks (вторичные блоки исторически обходили baseline-EXCLUDE,
-- claim-INSERT обязателен и на этой ветке); kind='anycast_pool' на пул
-- :attachNetwork. SQLSTATE 23P01 (exclusion_violation) → FAILED_PRECONDITION.
-- DO-guard'ы делают rename идемпотентным к повторному/параллельному migrate-init.

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM information_schema.tables
               WHERE table_schema = 'kacho_vpc' AND table_name = 'subnet_cidr_blocks')
       AND NOT EXISTS (SELECT 1 FROM information_schema.tables
               WHERE table_schema = 'kacho_vpc' AND table_name = 'network_cidr_claims') THEN
        ALTER TABLE kacho_vpc.subnet_cidr_blocks RENAME TO network_cidr_claims;
    END IF;
END $$;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM information_schema.columns
               WHERE table_schema = 'kacho_vpc' AND table_name = 'network_cidr_claims'
                 AND column_name = 'block')
       AND NOT EXISTS (SELECT 1 FROM information_schema.columns
               WHERE table_schema = 'kacho_vpc' AND table_name = 'network_cidr_claims'
                 AND column_name = 'cidr_range') THEN
        ALTER TABLE kacho_vpc.network_cidr_claims RENAME COLUMN block TO cidr_range;
    END IF;
END $$;

ALTER TABLE kacho_vpc.network_cidr_claims
    ADD COLUMN IF NOT EXISTS kind   text NOT NULL DEFAULT 'subnet',
    ADD COLUMN IF NOT EXISTS ref_id text;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'network_cidr_claims_kind_chk') THEN
        ALTER TABLE kacho_vpc.network_cidr_claims
            ADD CONSTRAINT network_cidr_claims_kind_chk CHECK (kind IN ('subnet', 'anycast_pool'));
    END IF;
END $$;

-- backfill ref_id из subnet_id для существующих subnet-строк (на чистой БД — no-op).
UPDATE kacho_vpc.network_cidr_claims SET ref_id = subnet_id WHERE ref_id IS NULL AND subnet_id IS NOT NULL;

ALTER TABLE kacho_vpc.network_cidr_claims ALTER COLUMN ref_id SET NOT NULL;

-- kind backfilled (DEFAULT 'subnet') для существующих subnet-строк; дальше DEFAULT
-- снимается — каждый INSERT (repo) обязан задавать kind явно, чтобы anycast-claim,
-- забывший kind, не был молча классифицирован как 'subnet' (неверный cleanup).
ALTER TABLE kacho_vpc.network_cidr_claims ALTER COLUMN kind DROP DEFAULT;

-- PK переезжает с (subnet_id, block) на (network_id, kind, ref_id, cidr_range):
-- subnet_id больше не обязателен (NULL для anycast), а пул может быть приаттачен к
-- нескольким сетям с одним cidr → network_id в ключе обязателен. Старый PK снимаем
-- ДО DROP NOT NULL на subnet_id (колонка PK не может стать nullable).
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'subnet_cidr_blocks_pkey') THEN
        ALTER TABLE kacho_vpc.network_cidr_claims DROP CONSTRAINT subnet_cidr_blocks_pkey;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'network_cidr_claims_pkey') THEN
        ALTER TABLE kacho_vpc.network_cidr_claims
            ADD CONSTRAINT network_cidr_claims_pkey PRIMARY KEY (network_id, kind, ref_id, cidr_range);
    END IF;
    IF EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'subnet_cidr_blocks_no_overlap') THEN
        ALTER TABLE kacho_vpc.network_cidr_claims
            RENAME CONSTRAINT subnet_cidr_blocks_no_overlap TO network_cidr_claims_no_overlap;
    END IF;
END $$;

ALTER TABLE kacho_vpc.network_cidr_claims ALTER COLUMN subnet_id DROP NOT NULL;

ALTER INDEX IF EXISTS kacho_vpc.subnet_cidr_blocks_subnet_id_idx  RENAME TO network_cidr_claims_subnet_id_idx;
ALTER INDEX IF EXISTS kacho_vpc.subnet_cidr_blocks_network_id_idx RENAME TO network_cidr_claims_network_id_idx;
CREATE INDEX IF NOT EXISTS network_cidr_claims_ref_idx ON kacho_vpc.network_cidr_claims (kind, ref_id);

-- -----------------------------------------------------------------------------
-- 4. Seed платформенного is_default IPv4-пула (одноразовый bootstrap)
-- -----------------------------------------------------------------------------
-- admin-owned пул в платформенном project (sentinel project_id 'kacho-system',
-- не tenant-enumerable: тенантский List фильтрует по своему project_id). Срез
-- 100.64.0.0/16 из reserved 100.64.0.0/10. Детерминированный id (prefix aap +
-- crockford-base32 тело) — фиксирован, ON CONFLICT DO NOTHING для re-run/race.
INSERT INTO kacho_vpc.anycast_address_pools
    (id, project_id, name, description, scope, ip_version, cidr_blocks, is_default, status)
VALUES
    ('aap00000000000000def', 'kacho-system', 'default-internal-ipv4',
     'Platform default INTERNAL anycast address pool (IPv4), auto-available to every network.',
     'INTERNAL', 'IPV4', ARRAY['100.64.0.0/16'], true, 'ACTIVE')
ON CONFLICT (id) DO NOTHING;

-- Нормализованная строка блока seed-пула (источник истины + EXCLUDE-инвариант).
INSERT INTO kacho_vpc.anycast_address_pool_cidrs (pool_id, block)
VALUES ('aap00000000000000def', '100.64.0.0/16')
ON CONFLICT (pool_id, block) DO NOTHING;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

SET search_path TO kacho_vpc, public;

-- Порядок DROP — FK-дети раньше родителя: pivot и child-блоки ссылаются на
-- anycast_address_pools (ON DELETE CASCADE), снимаем их первыми.
DROP TABLE IF EXISTS kacho_vpc.anycast_pool_network_attachments;
DROP TABLE IF EXISTS kacho_vpc.anycast_address_pool_cidrs;
DROP TABLE IF EXISTS kacho_vpc.anycast_address_pools;

-- Откат унификации: убрать anycast-претензии, вернуть subnet_cidr_blocks.
DELETE FROM kacho_vpc.network_cidr_claims WHERE kind = 'anycast_pool';

DROP INDEX IF EXISTS kacho_vpc.network_cidr_claims_ref_idx;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'network_cidr_claims_pkey') THEN
        ALTER TABLE kacho_vpc.network_cidr_claims DROP CONSTRAINT network_cidr_claims_pkey;
    END IF;
    IF EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'network_cidr_claims_no_overlap') THEN
        ALTER TABLE kacho_vpc.network_cidr_claims
            RENAME CONSTRAINT network_cidr_claims_no_overlap TO subnet_cidr_blocks_no_overlap;
    END IF;
END $$;

ALTER TABLE kacho_vpc.network_cidr_claims ALTER COLUMN subnet_id SET NOT NULL;
ALTER TABLE kacho_vpc.network_cidr_claims DROP COLUMN IF EXISTS kind;
ALTER TABLE kacho_vpc.network_cidr_claims DROP COLUMN IF EXISTS ref_id;

ALTER INDEX IF EXISTS kacho_vpc.network_cidr_claims_subnet_id_idx  RENAME TO subnet_cidr_blocks_subnet_id_idx;
ALTER INDEX IF EXISTS kacho_vpc.network_cidr_claims_network_id_idx RENAME TO subnet_cidr_blocks_network_id_idx;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM information_schema.columns
               WHERE table_schema = 'kacho_vpc' AND table_name = 'network_cidr_claims'
                 AND column_name = 'cidr_range') THEN
        ALTER TABLE kacho_vpc.network_cidr_claims RENAME COLUMN cidr_range TO block;
    END IF;
END $$;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM information_schema.tables
               WHERE table_schema = 'kacho_vpc' AND table_name = 'network_cidr_claims') THEN
        ALTER TABLE kacho_vpc.network_cidr_claims RENAME TO subnet_cidr_blocks;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'subnet_cidr_blocks_pkey') THEN
        ALTER TABLE kacho_vpc.subnet_cidr_blocks
            ADD CONSTRAINT subnet_cidr_blocks_pkey PRIMARY KEY (subnet_id, block);
    END IF;
END $$;

-- +goose StatementEnd
