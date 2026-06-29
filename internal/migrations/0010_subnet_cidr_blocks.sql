-- Copyright (c) PRO-Robotech
-- SPDX-License-Identifier: BUSL-1.1

-- +goose Up
-- +goose StatementBegin

-- =============================================================================
-- DB-уровневая защита от пересечения ВСЕХ CIDR-блоков подсетей в пределах одной
-- Network (within-service инвариант обязан жить на DB-уровне).
-- =============================================================================
-- Проблема: baseline EXCLUDE subnets_no_overlap_v4 / subnets_no_overlap_v6
-- проверяет только PRIMARY-блок каждого семейства (generated-колонки
-- v4_cidr_primary / v6_cidr_primary = массив[1]). Вторичные блоки
-- (v4_cidr_blocks[2..N] / v6_cidr_blocks[2..N], добавляемые AddCidrBlocks) под
-- EXCLUDE не попадают → пересечение вторичных диапазонов двух подсетей одной сети
-- проходит, и IPAM может выделить один internal-IP дважды.
--
-- Решение: нормализованная child-таблица subnet_cidr_blocks + EXCLUDE gist
-- (declarative, race-free by construction — прямой аналог address_pool_cidrs).
-- Ключ исключения scope = network_id: подсети РАЗНЫХ сетей могут иметь
-- пересекающиеся CIDR (изоляция per-network), поэтому network_id денормализуется
-- в child-строку ради scope-ключа. v4 и v6 в одной cidr-колонке не дают ложных
-- пересечений (inet && между разными семействами = false). Строки поддерживаются
-- репозиторием в той же writer-TX, что и subnet DML (Insert / SetCidrBlocks);
-- Delete Subnet снимает их через FK ON DELETE CASCADE.
--
-- btree_gist уже создан в базовой схеме (в public, extension-owned).

SET search_path TO kacho_vpc, public;

-- Идемпотентность (IF NOT EXISTS / ON CONFLICT): защищает от повторного/частичного
-- применения и от гонки двух одновременных migrate-init (helm-rollout +
-- rollout-restart запускают goose параллельно). На fresh-БД поведение идентично.
CREATE TABLE IF NOT EXISTS kacho_vpc.subnet_cidr_blocks (
    subnet_id  text NOT NULL REFERENCES kacho_vpc.subnets(id) ON DELETE CASCADE,
    network_id text NOT NULL,
    block      cidr NOT NULL,
    PRIMARY KEY (subnet_id, block),
    CONSTRAINT subnet_cidr_blocks_no_overlap
        EXCLUDE USING gist (network_id WITH =, block inet_ops WITH &&)
);
CREATE INDEX IF NOT EXISTS subnet_cidr_blocks_subnet_id_idx  ON kacho_vpc.subnet_cidr_blocks (subnet_id);
CREATE INDEX IF NOT EXISTS subnet_cidr_blocks_network_id_idx ON kacho_vpc.subnet_cidr_blocks (network_id);

-- backfill из существующих подсетей (arrays → нормализованные строки). На чистых
-- БД (CI/testcontainers) данных нет → no-op. На стенде с УЖЕ пересекающимися
-- вторичными блоками backfill упадет по EXCLUDE — by-design (вскрывает сам баг;
-- оператор чистит пересечения до миграции). regex-guard повторяет условие
-- generated-колонок v*_cidr_primary, чтобы ::cidr cast не падал на пустых или
-- неполных значениях. ON CONFLICT — для re-run / параллельного migrate-init.
INSERT INTO kacho_vpc.subnet_cidr_blocks (subnet_id, network_id, block)
    SELECT id, network_id, b::cidr FROM kacho_vpc.subnets, unnest(v4_cidr_blocks) AS b
        WHERE b ~ '^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+/[0-9]+$'
    UNION
    SELECT id, network_id, b::cidr FROM kacho_vpc.subnets, unnest(v6_cidr_blocks) AS b
        WHERE b ~ '^[0-9a-fA-F:]+/[0-9]+$'
ON CONFLICT (subnet_id, block) DO NOTHING;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

SET search_path TO kacho_vpc, public;

DROP TABLE IF EXISTS kacho_vpc.subnet_cidr_blocks;

-- +goose StatementEnd
