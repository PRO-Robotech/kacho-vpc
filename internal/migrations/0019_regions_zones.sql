-- +goose Up
--
-- Region/Zone first-class concepts.
-- Регионы и зоны теперь — явные сущности, на которые привязываются ресурсы
-- (AddressPool.zone_id, Subnet.zone_id, etc.). Раньше region_id был свободной
-- строкой → admin типичных ошибок («указал zone в region-поле»).
--
-- Lifecycle: regions/zones — admin-managed, провижионятся через миграции
-- (как seed ниже) или через future admin-RPC. Public API не меняется
-- (Address.external_ipv4_spec.zone_id остаётся string verbatim YC).

CREATE TABLE regions (
  id          TEXT         PRIMARY KEY,         -- "ru-central1"
  name        TEXT         NOT NULL DEFAULT '', -- human-readable
  created_at  TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE TABLE zones (
  id          TEXT         PRIMARY KEY,         -- "ru-central1-a"
  region_id   TEXT         NOT NULL REFERENCES regions(id) ON DELETE RESTRICT,
  name        TEXT         NOT NULL DEFAULT '',
  created_at  TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE INDEX zones_region_idx ON zones (region_id);

-- Seed: единственный регион ru-central1 с тремя зонами (matches YC convention).
INSERT INTO regions (id, name) VALUES ('ru-central1', 'Russia Central 1');

INSERT INTO zones (id, region_id, name) VALUES
  ('ru-central1-a', 'ru-central1', 'Zone A'),
  ('ru-central1-b', 'ru-central1', 'Zone B'),
  ('ru-central1-d', 'ru-central1', 'Zone D');

-- +goose Down

DROP TABLE IF EXISTS zones;
DROP TABLE IF EXISTS regions;
