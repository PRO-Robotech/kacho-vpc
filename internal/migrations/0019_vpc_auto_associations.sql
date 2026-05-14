-- KAC-56: auto-association ресурсов VPC на стороне БД (PL/pgSQL trigger'ы).
--
-- Закрывает TOCTOU-риски в service-слое (запрет #10 workspace CLAUDE.md /
-- эпик KAC-52: within-service refs/инварианты — DB-уровень обязателен).
--
-- Scope этой миграции (см. KAC-56 § "Финальный scope"):
--   (2) AFTER INSERT ON route_tables — auto-assoc Subnet'ов сети
--       (UPDATE subnets SET route_table_id=NEW.id WHERE network_id=NEW.network_id
--                                                  AND (route_table_id IS NULL OR route_table_id='')).
--   (3) BEFORE INSERT ON subnets — auto-pick RT, если в сети уже есть RouteTable
--       и клиент не задал NEW.route_table_id (выбираем самую раннюю, по created_at ASC).
--   (4) FK subnets.route_table_id → route_tables(id) ON DELETE SET NULL.
--   (5) AFTER UPDATE OF route_table_id ON subnets — outbox-эмит Subnet.UPDATED.
--
-- Не входит:
--   (1) Network → default SG через trigger — оставлено inline в service-слое из-за
--       id-generation (crockford-base32 в PL/pgSQL — отдельный тикет).
--
-- Note: subnets.route_table_id уже nullable (см. 0001_initial.sql:233:
-- `route_table_id text,` — без NOT NULL). Repo использует *string + COALESCE,
-- DROP NOT NULL не требуется. Backfill '' → NULL делаем для FK-совместимости.
--
-- +goose Up
-- +goose StatementBegin

-- ============================================================================
-- (4) backfill + FK subnets.route_table_id → route_tables(id) ON DELETE SET NULL
-- ============================================================================

-- Backfill dangling refs (route_table_id указывает на удалённую RT) → NULL.
-- Без этого ADD CONSTRAINT падает на 23503 (foreign_key_violation).
UPDATE subnets SET route_table_id = NULL
 WHERE route_table_id IS NOT NULL
   AND route_table_id <> ''
   AND NOT EXISTS (SELECT 1 FROM route_tables WHERE id = subnets.route_table_id);

-- Backfill empty-string → NULL (legacy: repo передавал nullableStr, но какие-то
-- строки могли остаться как ''). FK rejects '' (пустая строка != reference).
UPDATE subnets SET route_table_id = NULL WHERE route_table_id = '';

-- FK ON DELETE SET NULL. NOT VALID + VALIDATE — два прохода для длинных таблиц
-- (короткий exclusive lock на ADD, последующий VALIDATE — shared).
ALTER TABLE subnets
  ADD CONSTRAINT subnets_route_table_id_fkey
    FOREIGN KEY (route_table_id)
    REFERENCES route_tables(id)
    ON DELETE SET NULL
    NOT VALID;
ALTER TABLE subnets VALIDATE CONSTRAINT subnets_route_table_id_fkey;

-- ============================================================================
-- (2) RouteTable.Create → auto-assoc Subnet'ов сети
-- ============================================================================
-- AFTER INSERT trigger: новая RT в сети применяется к её Subnet'ам, у которых
-- route_table_id ещё не задан (NULL). Subnet с уже задаваемым route_table_id
-- (explicit user choice) НЕ перетирается — приоритет explicit.
CREATE OR REPLACE FUNCTION rt_auto_assoc_subnets() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  UPDATE subnets
     SET route_table_id = NEW.id
   WHERE network_id = NEW.network_id
     AND route_table_id IS NULL;
  RETURN NEW;
END
$$;

DROP TRIGGER IF EXISTS rt_auto_assoc_subnets_trg ON route_tables;
CREATE TRIGGER rt_auto_assoc_subnets_trg
  AFTER INSERT ON route_tables
  FOR EACH ROW
  EXECUTE FUNCTION rt_auto_assoc_subnets();

-- ============================================================================
-- (3) Subnet.Create → auto-pick RT, если в сети уже есть RouteTable
-- ============================================================================
-- BEFORE INSERT trigger: если клиент не задал NEW.route_table_id (NULL), а в
-- сети уже существует одна или несколько RouteTable, подставляем id самой
-- ранней по created_at. Симметрично (2): «Subnet в сети с RT обязан иметь
-- route_table_id». Если RT нет — оставляем NULL (auto-assoc сработает позже,
-- когда RT появится — см. (2)).
CREATE OR REPLACE FUNCTION subnet_auto_pick_rt() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.route_table_id IS NULL THEN
    SELECT id INTO NEW.route_table_id
      FROM route_tables
     WHERE network_id = NEW.network_id
     ORDER BY created_at ASC, id ASC
     LIMIT 1;
    -- Если RT нет — NEW.route_table_id остаётся NULL (FK позволяет).
  END IF;
  RETURN NEW;
END
$$;

DROP TRIGGER IF EXISTS subnet_auto_pick_rt_trg ON subnets;
CREATE TRIGGER subnet_auto_pick_rt_trg
  BEFORE INSERT ON subnets
  FOR EACH ROW
  EXECUTE FUNCTION subnet_auto_pick_rt();

-- ============================================================================
-- (5) Outbox-эмит для triggered UPDATE'ов subnets
-- ============================================================================
-- AFTER UPDATE OF route_table_id ON subnets — эмитит Subnet.UPDATED в vpc_outbox.
-- Watch-клиенты должны видеть изменения, вызванные (2) RT-auto-assoc и (4)
-- FK ON DELETE SET NULL (RT.Delete обнуляет route_table_id).
--
-- vpc_outbox columns (см. 0001_initial.sql:252):
--   sequence_no bigint NOT NULL (default nextval),
--   resource_kind text NOT NULL,
--   resource_id text NOT NULL,
--   event_type text NOT NULL,
--   payload jsonb DEFAULT '{}'::jsonb NOT NULL,
--   created_at timestamp with time zone DEFAULT now() NOT NULL,
--   processed_at timestamp with time zone.
--
-- Payload — упрощённый jsonb_build_object с ключевыми полями. Отличается от
-- service-side `subnetPayload(s)` (где payload computed Go-кодом), но watch-
-- клиент идемпотентен по `(resource_kind, resource_id, event_type)` — клиент
-- сделает Get для полного state, payload здесь — просто маркер «нужно
-- ресинхронизироваться».
--
-- WHEN-clause: эмитим только если значение реально изменилось (OLD IS DISTINCT
-- FROM NEW — учитывает NULL ↔ value переходы).
CREATE OR REPLACE FUNCTION subnets_outbox_emit_route_table_change() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  INSERT INTO vpc_outbox (resource_kind, resource_id, event_type, payload)
  VALUES (
    'Subnet',
    NEW.id,
    'UPDATED',
    jsonb_build_object(
      'id', NEW.id,
      'folder_id', NEW.folder_id,
      'network_id', NEW.network_id,
      'route_table_id', NEW.route_table_id,
      'name', NEW.name,
      'auto_association', true
    )
  );
  RETURN NEW;
END
$$;

DROP TRIGGER IF EXISTS subnets_outbox_emit_route_table_change_trg ON subnets;
CREATE TRIGGER subnets_outbox_emit_route_table_change_trg
  AFTER UPDATE OF route_table_id ON subnets
  FOR EACH ROW
  WHEN (OLD.route_table_id IS DISTINCT FROM NEW.route_table_id)
  EXECUTE FUNCTION subnets_outbox_emit_route_table_change();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS subnets_outbox_emit_route_table_change_trg ON subnets;
DROP FUNCTION IF EXISTS subnets_outbox_emit_route_table_change();

DROP TRIGGER IF EXISTS subnet_auto_pick_rt_trg ON subnets;
DROP FUNCTION IF EXISTS subnet_auto_pick_rt();

DROP TRIGGER IF EXISTS rt_auto_assoc_subnets_trg ON route_tables;
DROP FUNCTION IF EXISTS rt_auto_assoc_subnets();

ALTER TABLE subnets DROP CONSTRAINT IF EXISTS subnets_route_table_id_fkey;
-- +goose StatementEnd
