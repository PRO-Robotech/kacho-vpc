-- Copyright (c) PRO-Robotech
-- SPDX-License-Identifier: BUSL-1.1

-- +goose Up
-- +goose StatementBegin

-- =============================================================================
-- DB-уровневая защита от пересечения CIDR в AddressPool (within-service
-- инвариант обязан быть на DB-уровне).
-- =============================================================================
-- Проблема: AddressPool.v4_cidr_blocks / v6_cidr_blocks (text[]) могли
-- пересекаться внутри пула и МЕЖДУ пулами без всякой валидации. IPAM аллоцирует
-- external-IP из CIDR пула; UNIQUE addresses_external_pool_ip_uniq — per-pool
-- (address_pool_id, address), поэтому два пула с пересекающимися CIDR аллоцируют
-- ОДИН external-IP дважды (разные pool_id → UNIQUE проходит).
--
-- Решение: нормализованная child-таблица address_pool_cidrs + EXCLUDE gist
-- (declarative, race-free by construction — паттерн зеркалит subnets_no_overlap_v4
-- из базовой схемы). Инвариант scope = per `kind`: cross-zone public CIDR
-- обязаны быть глобально непересекающимися → zone в exclusion-key НЕ включаем
-- (EXTERNAL_PUBLIC — единственный активный kind).
--
-- btree_gist уже создан в базовой схеме (в public, extension-owned). `block cidr`
-- + `inet_ops` + `&&`, `kind smallint WITH =` — точная калька subnets_no_overlap_v4.

SET search_path TO kacho_vpc, public;

-- Идемпотентность (IF NOT EXISTS / ON CONFLICT): защищает от повторного/частичного
-- применения и от гонки двух одновременных migrate-init (helm-rollout + rollout
-- restart запускают goose параллельно; без идемпотентности это давало
-- "v4 recorded, table missing"). На fresh-БД поведение идентично — таблица одна.
CREATE TABLE IF NOT EXISTS kacho_vpc.address_pool_cidrs (
    pool_id text     NOT NULL REFERENCES kacho_vpc.address_pools(id) ON DELETE CASCADE,
    kind    smallint NOT NULL,
    block   cidr     NOT NULL,
    PRIMARY KEY (pool_id, block),
    CONSTRAINT address_pool_cidrs_no_overlap
        EXCLUDE USING gist (kind WITH =, block inet_ops WITH &&)
);
CREATE INDEX IF NOT EXISTS address_pool_cidrs_pool_id_idx ON kacho_vpc.address_pool_cidrs (pool_id);

-- backfill из существующих пулов (arrays → нормализованные строки).
-- На чистых БД (CI/testcontainers) данных нет. На стенде с УЖЕ пересекающимися
-- пулами этот INSERT упадет по EXCLUDE — это by-design (вскрывает сам баг);
-- оператор чистит пересечения перед миграцией. ON CONFLICT — для re-run/race.
INSERT INTO kacho_vpc.address_pool_cidrs (pool_id, kind, block)
    SELECT id, kind, b::cidr FROM kacho_vpc.address_pools, unnest(v4_cidr_blocks) AS b WHERE b <> ''
    UNION
    SELECT id, kind, b::cidr FROM kacho_vpc.address_pools, unnest(v6_cidr_blocks) AS b WHERE b <> ''
ON CONFLICT (pool_id, block) DO NOTHING;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

SET search_path TO kacho_vpc, public;

DROP TABLE IF EXISTS kacho_vpc.address_pool_cidrs;

-- +goose StatementEnd
