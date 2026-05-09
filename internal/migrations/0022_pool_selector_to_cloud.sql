-- +goose Up
--
-- Перевод pool-selector с Network на Cloud:
--   external IP не имеет network_id, поэтому label-cascade резолва (Step 3)
--   через NetworkPoolSelector никогда не срабатывал для external Address.
--   Селектор должен висеть на Cloud, чтобы покрыть и external и internal IP
--   (cascade lookup идёт через folder_id → cloud_id).
--
-- Также: UNIQUE(region_id, name) для zones — нельзя создать две зоны с одним
-- именем в пределах одного региона.

-- 1. Удаляем старый network_pool_selector (был привязан к Network, не работал
-- для external IP).
DROP TABLE IF EXISTS network_pool_selector;

-- 2. Создаём cloud_pool_selector — admin-controlled labels на Cloud.
CREATE TABLE cloud_pool_selector (
  cloud_id   TEXT         PRIMARY KEY,            -- FK на kacho-resource-manager.clouds.id
  selector   JSONB        NOT NULL DEFAULT '{}'::jsonb,
  set_at     TIMESTAMPTZ  NOT NULL DEFAULT now(),
  set_by     TEXT         NOT NULL DEFAULT ''     -- audit (kachoctl, IAM-actor)
);

-- GIN индекс для @> containment-запросов (cascade resolve).
CREATE INDEX cloud_pool_selector_gin
  ON cloud_pool_selector USING gin (selector jsonb_path_ops)
  WHERE selector <> '{}'::jsonb;

-- 3. UNIQUE(region_id, name) для zones — prevent дубль-имена в пределах региона.
CREATE UNIQUE INDEX zones_region_id_name_key ON zones (region_id, name)
  WHERE name <> '';

-- +goose Down

DROP INDEX IF EXISTS zones_region_id_name_key;
DROP INDEX IF EXISTS cloud_pool_selector_gin;
DROP TABLE IF EXISTS cloud_pool_selector;

CREATE TABLE network_pool_selector (
  network_id TEXT         PRIMARY KEY,
  selector   JSONB        NOT NULL DEFAULT '{}'::jsonb,
  set_at     TIMESTAMPTZ  NOT NULL DEFAULT now(),
  set_by     TEXT         NOT NULL DEFAULT ''
);
