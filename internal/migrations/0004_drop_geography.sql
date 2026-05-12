-- +goose Up
-- Geography (Region/Zone) moved out of kacho-vpc to kacho-compute (epic KAC-15).
-- kacho-vpc no longer owns these tables; subnets.zone_id / address_pools.zone_id /
-- addresses.external_ipv4 (zone in JSONB) keep zone-id as a plain string, validated
-- on the request path via compute.v1.ZoneService.Get (see internal/clients/compute_client.go,
-- workspace CLAUDE.md §«Кросс-доменные ссылки на ресурсы»). Dangling refs (a zone deleted
-- in kacho-compute) are tolerated gracefully on read.
ALTER TABLE address_pools DROP CONSTRAINT IF EXISTS address_pools_zone_id_fkey;
DROP TABLE IF EXISTS zones;
DROP TABLE IF EXISTS regions;

-- +goose Down
-- Best-effort restore (admin must re-seed regions/zones via kacho-compute, or run
-- the kacho-compute migration 0003 — the canonical owner now). zone_id columns are
-- left untouched.
CREATE TABLE IF NOT EXISTS regions (
  id         text        PRIMARY KEY,
  name       text        NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS zones (
  id         text        PRIMARY KEY,
  region_id  text        NOT NULL,
  name       text        NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now()
);
