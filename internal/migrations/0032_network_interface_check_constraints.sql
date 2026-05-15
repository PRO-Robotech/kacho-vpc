-- KAC-94 (Wave 2 batch C, skill evgeniy §5 E.2): DB-уровневые CHECK
-- constraints для network_interfaces — financial backstop поверх domain.Validate
-- (skill §5 E.1). Симметрично 0025/0026/0027/0028/0029/0030/0031.
--
-- Параметры (источник — kacho-corelib/validate + internal/domain/types.go +
-- internal/domain/constants.go + internal/domain/network_interface.go):
--   * name         — regex `^([a-zA-Z]([-_a-zA-Z0-9]{0,61}[a-zA-Z0-9])?)?$`
--                    (verbatim YC permissive: empty / uppercase / underscore allowed,
--                    длина 0..63).
--   * description  — UTF-8 character length ≤ 256.
--   * status       — enum: {'PROVISIONING','ACTIVE','AVAILABLE','FAILED','DELETING',
--                    'STATUS_UNSPECIFIED'} ИЛИ NULL (на случай legacy rows).
--   * mac_address  — regex `^[0-9a-f]{2}(:[0-9a-f]{2}){5}$` (lowercase, colon-separated,
--                    6 октетов). NULL не допускается миграцией 0014 (NOT NULL),
--                    но CHECK дополнительно отбивает невалидные форматы (старый
--                    md5-backfill из 0014 — 32 hex без двоеточий — отбивается);
--                    пустая строка не разрешена.
--
-- Defensive RAISE EXCEPTION P0001 на legacy invalid rows: если 0014 заполнил
-- mac_address через `md5(random()||id)` (32 hex без двоеточий), эта миграция
-- упадёт; нужно сначала backfill'ить mac_address в правильную форму либо
-- переинициализировать (admin SQL `UPDATE … SET mac_address = '0e:' || …`).
--
-- +goose Up
-- +goose StatementBegin

DO $$
DECLARE
    bad_name_count int;
    bad_desc_count int;
    bad_status_count int;
    bad_mac_count int;
BEGIN
    SELECT COUNT(*) INTO bad_name_count
      FROM network_interfaces
     WHERE name !~ '^([a-zA-Z]([-_a-zA-Z0-9]{0,61}[a-zA-Z0-9])?)?$';
    IF bad_name_count > 0 THEN
        RAISE EXCEPTION 'network_interfaces has % rows with name violating VPC permissive regex — fix before applying CHECK (KAC-94)', bad_name_count
            USING ERRCODE = 'P0001';
    END IF;

    SELECT COUNT(*) INTO bad_desc_count
      FROM network_interfaces
     WHERE length(description) > 256;
    IF bad_desc_count > 0 THEN
        RAISE EXCEPTION 'network_interfaces has % rows with description longer than 256 chars — fix before applying CHECK (KAC-94)', bad_desc_count
            USING ERRCODE = 'P0001';
    END IF;

    SELECT COUNT(*) INTO bad_status_count
      FROM network_interfaces
     WHERE status IS NOT NULL
       AND status NOT IN ('PROVISIONING','ACTIVE','AVAILABLE','FAILED','DELETING','STATUS_UNSPECIFIED');
    IF bad_status_count > 0 THEN
        RAISE EXCEPTION 'network_interfaces has % rows with status outside NetworkInterfaceStatus enum — fix before applying CHECK (KAC-94)', bad_status_count
            USING ERRCODE = 'P0001';
    END IF;

    SELECT COUNT(*) INTO bad_mac_count
      FROM network_interfaces
     WHERE mac_address !~ '^[0-9a-f]{2}(:[0-9a-f]{2}){5}$';
    IF bad_mac_count > 0 THEN
        RAISE EXCEPTION 'network_interfaces has % rows with mac_address not matching ^[0-9a-f]{2}(:[0-9a-f]{2}){5}$ — fix before applying CHECK (KAC-94/KAC-48)', bad_mac_count
            USING ERRCODE = 'P0001';
    END IF;
END $$;

ALTER TABLE network_interfaces
    ADD CONSTRAINT network_interfaces_name_check
    CHECK (name ~ '^([a-zA-Z]([-_a-zA-Z0-9]{0,61}[a-zA-Z0-9])?)?$') NOT VALID;
ALTER TABLE network_interfaces VALIDATE CONSTRAINT network_interfaces_name_check;

ALTER TABLE network_interfaces
    ADD CONSTRAINT network_interfaces_description_check
    CHECK (length(description) <= 256) NOT VALID;
ALTER TABLE network_interfaces VALIDATE CONSTRAINT network_interfaces_description_check;

ALTER TABLE network_interfaces
    ADD CONSTRAINT network_interfaces_status_check
    CHECK (status IS NULL OR status IN ('PROVISIONING','ACTIVE','AVAILABLE','FAILED','DELETING','STATUS_UNSPECIFIED')) NOT VALID;
ALTER TABLE network_interfaces VALIDATE CONSTRAINT network_interfaces_status_check;

ALTER TABLE network_interfaces
    ADD CONSTRAINT network_interfaces_mac_address_check
    CHECK (mac_address ~ '^[0-9a-f]{2}(:[0-9a-f]{2}){5}$') NOT VALID;
ALTER TABLE network_interfaces VALIDATE CONSTRAINT network_interfaces_mac_address_check;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE network_interfaces DROP CONSTRAINT IF EXISTS network_interfaces_mac_address_check;
ALTER TABLE network_interfaces DROP CONSTRAINT IF EXISTS network_interfaces_status_check;
ALTER TABLE network_interfaces DROP CONSTRAINT IF EXISTS network_interfaces_description_check;
ALTER TABLE network_interfaces DROP CONSTRAINT IF EXISTS network_interfaces_name_check;

-- +goose StatementEnd
