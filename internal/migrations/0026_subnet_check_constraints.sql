-- KAC-94 (Wave 2 batch A, skill evgeniy §5 E.2): DB-уровневые CHECK
-- constraints для subnets — financial backstop поверх domain.Validate (skill
-- §5 E.1 — "БД — последний рубеж от внешних writers / bugs в app-коде").
--
-- Параметры (источник — kacho-corelib/validate + internal/domain/types.go):
--   * name        — regex `^([a-zA-Z]([-_a-zA-Z0-9]{0,61}[a-zA-Z0-9])?)?$`
--                   (verbatim YC permissive: empty / uppercase / underscore allowed,
--                   длина 0..63). Тот же RcNameVPC что и для Network/Address/RT.
--   * description — UTF-8 character length ≤ 256.
--
-- Defensive: до ALTER TABLE мы прогоняем диагностический SELECT по существующим
-- rows и RAISE EXCEPTION P0001 если есть нарушения — это даёт явный
-- actionable error в логах миграции вместо непрозрачного 23514 от ALTER.
--
-- +goose Up
-- +goose StatementBegin

DO $$
DECLARE
    bad_name_count int;
    bad_desc_count int;
BEGIN
    SELECT COUNT(*) INTO bad_name_count
      FROM subnets
     WHERE name !~ '^([a-zA-Z]([-_a-zA-Z0-9]{0,61}[a-zA-Z0-9])?)?$';
    IF bad_name_count > 0 THEN
        RAISE EXCEPTION 'subnets has % rows with name violating VPC permissive regex — fix before applying CHECK (KAC-94)', bad_name_count
            USING ERRCODE = 'P0001';
    END IF;

    SELECT COUNT(*) INTO bad_desc_count
      FROM subnets
     WHERE length(description) > 256;
    IF bad_desc_count > 0 THEN
        RAISE EXCEPTION 'subnets has % rows with description longer than 256 chars — fix before applying CHECK (KAC-94)', bad_desc_count
            USING ERRCODE = 'P0001';
    END IF;
END $$;

ALTER TABLE subnets
    ADD CONSTRAINT subnets_name_check
    CHECK (name ~ '^([a-zA-Z]([-_a-zA-Z0-9]{0,61}[a-zA-Z0-9])?)?$') NOT VALID;
ALTER TABLE subnets VALIDATE CONSTRAINT subnets_name_check;

ALTER TABLE subnets
    ADD CONSTRAINT subnets_description_check
    CHECK (length(description) <= 256) NOT VALID;
ALTER TABLE subnets VALIDATE CONSTRAINT subnets_description_check;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE subnets DROP CONSTRAINT IF EXISTS subnets_description_check;
ALTER TABLE subnets DROP CONSTRAINT IF EXISTS subnets_name_check;

-- +goose StatementEnd
