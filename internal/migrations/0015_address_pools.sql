-- +goose Up
--
-- AddressPool — internal-only resource (нет в public VPC API). Управляется
-- через InternalAddressPoolService, провижионится admin-tooling'ом.
-- См. proto/kacho/cloud/vpc/v1/internal_address_pool_service.proto.

CREATE TABLE address_pools (
  id           TEXT         PRIMARY KEY,
  folder_id    TEXT         NOT NULL,
  name         TEXT         NOT NULL DEFAULT '',
  description  TEXT         NOT NULL DEFAULT '',
  labels       JSONB        NOT NULL DEFAULT '{}',
  cidr_blocks  TEXT[]       NOT NULL DEFAULT '{}',
  -- 1 = EXTERNAL_PUBLIC, 2 = EXTERNAL_TEST, 100 = RESERVED_INTERNAL
  kind         SMALLINT     NOT NULL,
  region_id    TEXT         NOT NULL DEFAULT '',
  is_default   BOOLEAN      NOT NULL DEFAULT false,
  created_at   TIMESTAMPTZ  NOT NULL DEFAULT now(),
  modified_at  TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE INDEX address_pools_folder_idx ON address_pools (folder_id);
CREATE INDEX address_pools_region_idx ON address_pools (region_id);

-- В пределах (region_id, kind) допускается не более одного default pool
-- — partial UNIQUE.
CREATE UNIQUE INDEX address_pools_region_kind_default_uniq
  ON address_pools (region_id, kind) WHERE is_default = true;

-- BindAsNetworkDefault association (один pool per network).
CREATE TABLE address_pool_network_default (
  network_id   TEXT         PRIMARY KEY REFERENCES networks(id) ON DELETE CASCADE,
  pool_id      TEXT         NOT NULL REFERENCES address_pools(id) ON DELETE RESTRICT,
  bound_at     TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE INDEX address_pool_network_default_pool_idx
  ON address_pool_network_default (pool_id);

-- BindAsAddressOverride association (один override per address).
CREATE TABLE address_pool_address_override (
  address_id   TEXT         PRIMARY KEY REFERENCES addresses(id) ON DELETE CASCADE,
  pool_id      TEXT         NOT NULL REFERENCES address_pools(id) ON DELETE RESTRICT,
  bound_at     TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE INDEX address_pool_address_override_pool_idx
  ON address_pool_address_override (pool_id);

-- UNIQUE (pool_id, external_ip) для атомарного allocation external IP в рамках пула.
-- Cross-pool collision разрешён (разные пулы могут иметь overlapping CIDR'ов в
-- dev-стенде; на проде это ошибка администратора, но IPAM не должен молча падать).
CREATE UNIQUE INDEX addresses_external_pool_ip_uniq
  ON addresses (
    (external_ipv4->>'address_pool_id'),  -- denorm: pool_id хранится в external_ipv4 JSON для UNIQUE
    (external_ipv4->>'address')
  )
  WHERE external_ipv4 IS NOT NULL
    AND external_ipv4->>'address' <> ''
    AND external_ipv4->>'address_pool_id' <> '';

-- +goose Down

DROP INDEX IF EXISTS addresses_external_pool_ip_uniq;
DROP TABLE IF EXISTS address_pool_address_override;
DROP TABLE IF EXISTS address_pool_network_default;
DROP INDEX IF EXISTS address_pools_region_kind_default_uniq;
DROP TABLE IF EXISTS address_pools;
