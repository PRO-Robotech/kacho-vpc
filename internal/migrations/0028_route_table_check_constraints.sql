-- KAC-94 (Wave 2 batch A, skill evgeniy §5 E.2): DB-уровневые CHECK
-- constraints для route_tables — financial backstop поверх domain.Validate
-- (skill §5 E.1). Симметрично 0025/0026/0027.
--
-- Параметры (источник — kacho-corelib/validate + internal/domain/types.go):
--   * name        — regex `^([a-zA-Z]([-_a-zA-Z0-9]{0,61}[a-zA-Z0-9])?)?$`
--                   (verbatim YC permissive: empty / uppercase / underscore allowed,
--                   длина 0..63).
--   * description — UTF-8 character length ≤ 256.
--
-- Defensive RAISE EXCEPTION P0001 на legacy invalid rows.
--
-- +goose Up
-- +goose StatementBegin

DO $$
DECLARE
    bad_name_count int;
    bad_desc_count int;
BEGIN
    SELECT COUNT(*) INTO bad_name_count
      FROM route_tables
     WHERE name !~ '^([a-zA-Z]([-_a-zA-Z0-9]{0,61}[a-zA-Z0-9])?)?$';
    IF bad_name_count > 0 THEN
        RAISE EXCEPTION 'route_tables has % rows with name violating VPC permissive regex — fix before applying CHECK (KAC-94)', bad_name_count
            USING ERRCODE = 'P0001';
    END IF;

    SELECT COUNT(*) INTO bad_desc_count
      FROM route_tables
     WHERE length(description) > 256;
    IF bad_desc_count > 0 THEN
        RAISE EXCEPTION 'route_tables has % rows with description longer than 256 chars — fix before applying CHECK (KAC-94)', bad_desc_count
            USING ERRCODE = 'P0001';
    END IF;
END $$;

ALTER TABLE route_tables
    ADD CONSTRAINT route_tables_name_check
    CHECK (name ~ '^([a-zA-Z]([-_a-zA-Z0-9]{0,61}[a-zA-Z0-9])?)?$') NOT VALID;
ALTER TABLE route_tables VALIDATE CONSTRAINT route_tables_name_check;

ALTER TABLE route_tables
    ADD CONSTRAINT route_tables_description_check
    CHECK (length(description) <= 256) NOT VALID;
ALTER TABLE route_tables VALIDATE CONSTRAINT route_tables_description_check;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE route_tables DROP CONSTRAINT IF EXISTS route_tables_description_check;
ALTER TABLE route_tables DROP CONSTRAINT IF EXISTS route_tables_name_check;

-- +goose StatementEnd
