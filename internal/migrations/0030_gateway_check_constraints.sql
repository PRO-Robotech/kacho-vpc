-- KAC-94 (Wave 2 batch B, skill evgeniy §5 E.2): DB-уровневые CHECK
-- constraints для gateways — financial backstop поверх domain.Validate
-- (skill §5 E.1). Симметрично 0025/0026/0027/0028/0029.
--
-- Параметры (источник — kacho-corelib/validate + internal/domain/types.go):
--   * name        — regex `^([a-zA-Z]([-_a-zA-Z0-9]{0,61}[a-zA-Z0-9])?)?$`
--                   (verbatim YC permissive permission set — domain.Gateway.Name
--                   хранится как RcNameVPC в текущей фазе; service-слой
--                   дополнительно применяет strict-name контракт через
--                   corevalidate.NameGateway, но DB-CHECK match'ит permissive,
--                   чтобы избежать false-positive на исторических rows).
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
      FROM gateways
     WHERE name !~ '^([a-zA-Z]([-_a-zA-Z0-9]{0,61}[a-zA-Z0-9])?)?$';
    IF bad_name_count > 0 THEN
        RAISE EXCEPTION 'gateways has % rows with name violating VPC permissive regex — fix before applying CHECK (KAC-94)', bad_name_count
            USING ERRCODE = 'P0001';
    END IF;

    SELECT COUNT(*) INTO bad_desc_count
      FROM gateways
     WHERE length(description) > 256;
    IF bad_desc_count > 0 THEN
        RAISE EXCEPTION 'gateways has % rows with description longer than 256 chars — fix before applying CHECK (KAC-94)', bad_desc_count
            USING ERRCODE = 'P0001';
    END IF;
END $$;

ALTER TABLE gateways
    ADD CONSTRAINT gateways_name_check
    CHECK (name ~ '^([a-zA-Z]([-_a-zA-Z0-9]{0,61}[a-zA-Z0-9])?)?$') NOT VALID;
ALTER TABLE gateways VALIDATE CONSTRAINT gateways_name_check;

ALTER TABLE gateways
    ADD CONSTRAINT gateways_description_check
    CHECK (length(description) <= 256) NOT VALID;
ALTER TABLE gateways VALIDATE CONSTRAINT gateways_description_check;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE gateways DROP CONSTRAINT IF EXISTS gateways_description_check;
ALTER TABLE gateways DROP CONSTRAINT IF EXISTS gateways_name_check;

-- +goose StatementEnd
