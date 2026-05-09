-- +goose Up
--
-- AddressPool: переход с region_id (строка) на zone_id (FK к zones).
-- AddressPool теперь привязан к **зоне**, не к региону — соответствует
-- тому, что Address.external_ipv4_spec.zone_id тоже хранит zone, не region.
--
-- Старый region_id заменяется. Existing rows с непустым region_id мы
-- интерпретируем: если значение match'ится с zone — записываем как zone_id;
-- если match'ится с region — берём первую zone в этом region (best-effort
-- для миграции). Если ни то, ни другое — обнуляем.

ALTER TABLE address_pools ADD COLUMN zone_id TEXT REFERENCES zones(id) ON DELETE RESTRICT;

-- Best-effort миграция existing данных:
-- 1) zone-id точное совпадение → как-есть.
UPDATE address_pools SET zone_id = region_id
  WHERE region_id IN (SELECT id FROM zones);

-- 2) region-id совпадение → берём первую zone этого региона.
UPDATE address_pools p SET zone_id = (
  SELECT z.id FROM zones z WHERE z.region_id = p.region_id ORDER BY z.id LIMIT 1
)
  WHERE p.zone_id IS NULL
    AND p.region_id IN (SELECT id FROM regions);

-- Удаляем старую колонку и старый partial UNIQUE на (region_id, kind).
DROP INDEX IF EXISTS address_pools_region_kind_default_uniq;
ALTER TABLE address_pools DROP COLUMN IF EXISTS region_id;

-- Новый partial UNIQUE: один is_default=true на (zone_id, kind).
-- zone_id может быть NULL — это global default; partial-index с
-- COALESCE для безопасного UNIQUE на NULL.
CREATE UNIQUE INDEX address_pools_zone_kind_default_uniq
  ON address_pools (COALESCE(zone_id, ''), kind)
  WHERE is_default = true;

CREATE INDEX address_pools_zone_idx ON address_pools (zone_id);

-- +goose Down

DROP INDEX IF EXISTS address_pools_zone_idx;
DROP INDEX IF EXISTS address_pools_zone_kind_default_uniq;
ALTER TABLE address_pools ADD COLUMN region_id TEXT NOT NULL DEFAULT '';
ALTER TABLE address_pools DROP COLUMN IF EXISTS zone_id;
CREATE UNIQUE INDEX address_pools_region_kind_default_uniq
  ON address_pools (region_id, kind) WHERE is_default = true;
