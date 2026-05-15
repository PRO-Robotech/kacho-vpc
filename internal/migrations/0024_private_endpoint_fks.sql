-- KAC-89 (G2 из vpc audit KAC-84): добавляем FK на private_endpoints для
-- network_id / subnet_id / address_id. Раньше эти колонки были обычным TEXT
-- без FK — within-service ссылки проверялись только software-side (sync-Get
-- network/subnet перед Operation), что нарушает запрет #10 workspace CLAUDE.md
-- (within-service refs обязаны быть на DB-уровне). Без FK Network/Subnet/Address
-- можно было удалить из-под PE → orphan rows в private_endpoints.
--
-- Семантика:
--   * network_id  — NOT NULL в коде (sync-validated required), FK ON DELETE RESTRICT
--                   → Network с PE удалить нельзя.
--   * subnet_id   — optional; нормализуем '' → NULL и FK ON DELETE RESTRICT.
--   * address_id  — optional; нормализуем '' → NULL и FK ON DELETE RESTRICT.
--
-- Postgres FK с default MATCH SIMPLE пропускает NULL-значения (не проверяет
-- referential integrity на NULL), поэтому конвертация empty-string → NULL
-- нужна обязательно, иначе FK не примет empty-string-ссылки.
--
-- Zero-downtime: ADD CONSTRAINT … NOT VALID (мгновенный, без сканирования
-- таблицы) → VALIDATE CONSTRAINT (full scan, но без AccessExclusiveLock).
--
-- +goose Up
-- +goose StatementBegin

-- 1. Нормализация: пустые строки → NULL (FK не работает с empty-string).
UPDATE private_endpoints SET subnet_id  = NULL WHERE subnet_id  = '';
UPDATE private_endpoints SET address_id = NULL WHERE address_id = '';

-- 2. Defensive orphan-check: если есть существующие dangling-rows — миграция
--    падает с понятной ошибкой, а не валится позднее на VALIDATE CONSTRAINT.
--    Очистку orphan'ов делает оператор (DELETE или recreate parent) перед
--    повторным прогоном миграции.
DO $$
DECLARE
    n_orphans int;
BEGIN
    SELECT COUNT(*) INTO n_orphans
      FROM private_endpoints pe
     WHERE NOT EXISTS (SELECT 1 FROM networks n WHERE n.id = pe.network_id)
        OR (pe.subnet_id  IS NOT NULL AND NOT EXISTS (SELECT 1 FROM subnets   s WHERE s.id = pe.subnet_id))
        OR (pe.address_id IS NOT NULL AND NOT EXISTS (SELECT 1 FROM addresses a WHERE a.id = pe.address_id));
    IF n_orphans > 0 THEN
        RAISE EXCEPTION 'private_endpoints has % orphan rows — cleanup before applying FK constraints (KAC-89)', n_orphans
            USING ERRCODE = 'P0001';
    END IF;
END $$;

-- 3a. FK network_id (NOT NULL в коде, RESTRICT).
ALTER TABLE private_endpoints
    ADD CONSTRAINT private_endpoints_network_id_fkey
    FOREIGN KEY (network_id) REFERENCES networks(id) ON DELETE RESTRICT NOT VALID;
ALTER TABLE private_endpoints VALIDATE CONSTRAINT private_endpoints_network_id_fkey;

-- 3b. FK subnet_id (NULL-able, RESTRICT). После нормализации п.1 — empty-string
--     уже не встречается, FK проверяет только non-NULL.
ALTER TABLE private_endpoints
    ADD CONSTRAINT private_endpoints_subnet_id_fkey
    FOREIGN KEY (subnet_id) REFERENCES subnets(id) ON DELETE RESTRICT NOT VALID;
ALTER TABLE private_endpoints VALIDATE CONSTRAINT private_endpoints_subnet_id_fkey;

-- 3c. FK address_id (NULL-able, RESTRICT).
ALTER TABLE private_endpoints
    ADD CONSTRAINT private_endpoints_address_id_fkey
    FOREIGN KEY (address_id) REFERENCES addresses(id) ON DELETE RESTRICT NOT VALID;
ALTER TABLE private_endpoints VALIDATE CONSTRAINT private_endpoints_address_id_fkey;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- Best-effort откат. Empty-string нормализация (п.1 в Up) не откатывается:
-- empty-string-семантика была багом (within-service ref не на DB-уровне), и
-- repo.Insert после этой миграции тоже пишет NULL вместо '' (см. PrivateEndpointRepo.Insert).
ALTER TABLE private_endpoints DROP CONSTRAINT IF EXISTS private_endpoints_address_id_fkey;
ALTER TABLE private_endpoints DROP CONSTRAINT IF EXISTS private_endpoints_subnet_id_fkey;
ALTER TABLE private_endpoints DROP CONSTRAINT IF EXISTS private_endpoints_network_id_fkey;

-- +goose StatementEnd
