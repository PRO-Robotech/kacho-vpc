-- KAC-94 (Wave 2 batch B, skill evgeniy §5 E.2): DB-уровневые CHECK
-- constraints для private_endpoints — financial backstop поверх domain.Validate
-- (skill §5 E.1). Симметрично 0025/0026/0027/0028/0029/0030.
--
-- Параметры (источник — kacho-corelib/validate + internal/domain/types.go +
-- internal/domain/constants.go):
--   * name         — regex `^([a-zA-Z]([-_a-zA-Z0-9]{0,61}[a-zA-Z0-9])?)?$`
--                    (verbatim YC permissive: empty / uppercase / underscore allowed,
--                    длина 0..63).
--   * description  — UTF-8 character length ≤ 256.
--   * service_type — enum: {'object_storage'} ИЛИ NULL (на случай legacy rows).
--   * status       — enum: {'PENDING','AVAILABLE','DELETING'} ИЛИ NULL.
--
-- Defensive RAISE EXCEPTION P0001 на legacy invalid rows. service_type / status
-- допускают NULL — historical rows могут не иметь значения, и domain.Validate
-- их тоже не дёргает (oneof/enum-семантика — service-слой).
--
-- +goose Up
-- +goose StatementBegin

DO $$
DECLARE
    bad_name_count int;
    bad_desc_count int;
    bad_service_count int;
    bad_status_count int;
BEGIN
    SELECT COUNT(*) INTO bad_name_count
      FROM private_endpoints
     WHERE name !~ '^([a-zA-Z]([-_a-zA-Z0-9]{0,61}[a-zA-Z0-9])?)?$';
    IF bad_name_count > 0 THEN
        RAISE EXCEPTION 'private_endpoints has % rows with name violating VPC permissive regex — fix before applying CHECK (KAC-94)', bad_name_count
            USING ERRCODE = 'P0001';
    END IF;

    SELECT COUNT(*) INTO bad_desc_count
      FROM private_endpoints
     WHERE length(description) > 256;
    IF bad_desc_count > 0 THEN
        RAISE EXCEPTION 'private_endpoints has % rows with description longer than 256 chars — fix before applying CHECK (KAC-94)', bad_desc_count
            USING ERRCODE = 'P0001';
    END IF;

    SELECT COUNT(*) INTO bad_service_count
      FROM private_endpoints
     WHERE service_type IS NOT NULL
       AND service_type NOT IN ('object_storage');
    IF bad_service_count > 0 THEN
        RAISE EXCEPTION 'private_endpoints has % rows with service_type outside PrivateEndpointServiceType enum — fix before applying CHECK (KAC-94)', bad_service_count
            USING ERRCODE = 'P0001';
    END IF;

    SELECT COUNT(*) INTO bad_status_count
      FROM private_endpoints
     WHERE status IS NOT NULL
       AND status NOT IN ('PENDING','AVAILABLE','DELETING');
    IF bad_status_count > 0 THEN
        RAISE EXCEPTION 'private_endpoints has % rows with status outside PrivateEndpointStatus enum — fix before applying CHECK (KAC-94)', bad_status_count
            USING ERRCODE = 'P0001';
    END IF;
END $$;

ALTER TABLE private_endpoints
    ADD CONSTRAINT private_endpoints_name_check
    CHECK (name ~ '^([a-zA-Z]([-_a-zA-Z0-9]{0,61}[a-zA-Z0-9])?)?$') NOT VALID;
ALTER TABLE private_endpoints VALIDATE CONSTRAINT private_endpoints_name_check;

ALTER TABLE private_endpoints
    ADD CONSTRAINT private_endpoints_description_check
    CHECK (length(description) <= 256) NOT VALID;
ALTER TABLE private_endpoints VALIDATE CONSTRAINT private_endpoints_description_check;

ALTER TABLE private_endpoints
    ADD CONSTRAINT private_endpoints_service_type_check
    CHECK (service_type IS NULL OR service_type IN ('object_storage')) NOT VALID;
ALTER TABLE private_endpoints VALIDATE CONSTRAINT private_endpoints_service_type_check;

ALTER TABLE private_endpoints
    ADD CONSTRAINT private_endpoints_status_check
    CHECK (status IS NULL OR status IN ('PENDING','AVAILABLE','DELETING')) NOT VALID;
ALTER TABLE private_endpoints VALIDATE CONSTRAINT private_endpoints_status_check;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE private_endpoints DROP CONSTRAINT IF EXISTS private_endpoints_status_check;
ALTER TABLE private_endpoints DROP CONSTRAINT IF EXISTS private_endpoints_service_type_check;
ALTER TABLE private_endpoints DROP CONSTRAINT IF EXISTS private_endpoints_description_check;
ALTER TABLE private_endpoints DROP CONSTRAINT IF EXISTS private_endpoints_name_check;

-- +goose StatementEnd
