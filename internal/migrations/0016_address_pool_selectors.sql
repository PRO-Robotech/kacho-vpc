-- +goose Up
--
-- Address pool label-selectors + per-Network admin-controlled selector binding.
-- Расширяет 0015_address_pools.sql.
--
-- Match-семантика (нормативно): network's selector ⊆ pool's selector_labels.
-- То есть pool описывает «whitelist разрешённых labels»; network с любым
-- unlisted label не попадает в этот pool через label-cascade. Это inverse
-- vs k8s NodeSelector — выбран для **safe-by-default** admin-routing'а.
--
-- См. ResolvePoolForAddress cascade в kacho-vpc/internal/service/address_pool_service.go.

-- 1. Pool selectors.
ALTER TABLE address_pools
  ADD COLUMN selector_labels    JSONB    NOT NULL DEFAULT '{}'::jsonb,
  ADD COLUMN selector_priority  INTEGER  NOT NULL DEFAULT 0;

-- GIN index для containment-query: WHERE selector_labels @> $1
-- (network_selector ⊆ pool.selector_labels). jsonb_path_ops быстрее для
-- containment чем default jsonb_ops.
CREATE INDEX address_pools_selector_labels_gin
  ON address_pools USING gin (selector_labels jsonb_path_ops)
  WHERE selector_labels <> '{}'::jsonb;

-- 2. Per-Network admin-controlled selector binding (internal-only, отдельная
--    таблица — не загрязняем flat-schema networks).
--
--    Одна row per Network. Set/Unset через InternalNetworkService.{Set,Unset}PoolSelector.
--    Пустой selector ('{}'::jsonb) семантически равен отсутствию binding'а;
--    в обоих случаях label-cascade-step skip'ается, идём на default.
CREATE TABLE network_pool_selector (
  network_id  TEXT         PRIMARY KEY REFERENCES networks(id) ON DELETE CASCADE,
  selector    JSONB        NOT NULL DEFAULT '{}'::jsonb,
  set_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
  set_by      TEXT         NOT NULL DEFAULT ''   -- audit: каким actor (admin / system)
);

-- GIN index для reverse-lookup: «какие networks попадают в этот pool?»
-- Полезно для tooling (kachoctl ipam list-networks-for-pool).
CREATE INDEX network_pool_selector_gin
  ON network_pool_selector USING gin (selector jsonb_path_ops)
  WHERE selector <> '{}'::jsonb;

-- +goose Down

DROP INDEX IF EXISTS network_pool_selector_gin;
DROP TABLE IF EXISTS network_pool_selector;
DROP INDEX IF EXISTS address_pools_selector_labels_gin;
ALTER TABLE address_pools DROP COLUMN IF EXISTS selector_priority;
ALTER TABLE address_pools DROP COLUMN IF EXISTS selector_labels;
