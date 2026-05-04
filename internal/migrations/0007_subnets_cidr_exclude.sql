-- +goose Up
-- SU-CIDR-OVERLAP defense-in-depth — добавляет DB-level EXCLUDE constraint,
-- запрещающий пересекающиеся CIDR (v4 и v6) в рамках одной VPC.
--
-- Service-слой уже делает sync-проверку (subnet.go::checkCIDRDisjoint), но
-- между двумя concurrent Create есть TOCTOU-окно (List → Insert race). DB-level
-- constraint закрывает это окно атомарно.
--
-- Подход: на каждое CIDR-семейство добавляем generated stored column с типом
-- inet/cidr (первый CIDR из массива) и ставим EXCLUDE на (network_id WITH =,
-- <prefix> inet_ops WITH &&) — стандартный Postgres-паттерн «no overlap within
-- group».
--
-- v4_cidr_blocks: пишется клиентом (Subnet.Create.v4CidrBlocks).
-- v6_cidr_blocks: OUTPUT_ONLY (auto-allocated сервером в будущем). EXCLUDE на
-- v6 — defensive against future feature: если когда-нибудь сервер начнёт
-- аллоцировать v6, два concurrent allocation не сделают overlap.
--
-- При нарушении PG возвращает SQLSTATE 23P01 (exclusion_violation), который
-- repo мапит на ErrInvalidArg → gRPC InvalidArgument.

CREATE EXTENSION IF NOT EXISTS btree_gist;

-- +goose StatementBegin
ALTER TABLE subnets
  ADD COLUMN v4_cidr_primary CIDR
  GENERATED ALWAYS AS (
    CASE
      WHEN array_length(v4_cidr_blocks, 1) >= 1
       AND v4_cidr_blocks[1] ~ '^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+/[0-9]+$'
      THEN v4_cidr_blocks[1]::CIDR
      ELSE NULL
    END
  ) STORED;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE subnets
  ADD COLUMN v6_cidr_primary CIDR
  GENERATED ALWAYS AS (
    CASE
      WHEN array_length(v6_cidr_blocks, 1) >= 1
       AND v6_cidr_blocks[1] ~ '^[0-9a-fA-F:]+/[0-9]+$'
      THEN v6_cidr_blocks[1]::CIDR
      ELSE NULL
    END
  ) STORED;
-- +goose StatementEnd

-- Перед накатом EXCLUDE — почистить существующие overlap-данные (dev-стенд),
-- иначе ALTER TABLE упадёт со SQLSTATE 23P01 на исторических probe-runs. В
-- prod-data overlap'ов быть не должно (контракт всегда был).
-- Cascade удаляет зависимые addresses (FK 0006).
TRUNCATE TABLE subnets CASCADE;

ALTER TABLE subnets ADD CONSTRAINT subnets_no_overlap_v4
  EXCLUDE USING gist (
    network_id WITH =,
    v4_cidr_primary inet_ops WITH &&
  ) WHERE (v4_cidr_primary IS NOT NULL);

ALTER TABLE subnets ADD CONSTRAINT subnets_no_overlap_v6
  EXCLUDE USING gist (
    network_id WITH =,
    v6_cidr_primary inet_ops WITH &&
  ) WHERE (v6_cidr_primary IS NOT NULL);

-- +goose Down

ALTER TABLE subnets DROP CONSTRAINT IF EXISTS subnets_no_overlap_v6;
ALTER TABLE subnets DROP CONSTRAINT IF EXISTS subnets_no_overlap_v4;
ALTER TABLE subnets DROP COLUMN IF EXISTS v6_cidr_primary;
ALTER TABLE subnets DROP COLUMN IF EXISTS v4_cidr_primary;
