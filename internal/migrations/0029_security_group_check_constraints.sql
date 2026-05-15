-- KAC-94 (Wave 2 batch B, skill evgeniy §5 E.2): DB-уровневые CHECK
-- constraints для security_groups — financial backstop поверх domain.Validate
-- (skill §5 E.1). Симметрично 0025/0026/0027/0028.
--
-- Параметры (источник — kacho-corelib/validate + internal/domain/types.go):
--   * name        — regex `^([a-zA-Z]([-_a-zA-Z0-9]{0,61}[a-zA-Z0-9])?)?$`
--                   (verbatim YC permissive: empty / uppercase / underscore allowed,
--                   длина 0..63).
--   * description — UTF-8 character length ≤ 256.
--   * status      — enum: одно из {'ACTIVE','CREATING','UPDATING','DELETING'}
--                   (verbatim YC SecurityGroup_Status, см. domain.SecurityGroupStatus).
--
-- Defensive RAISE EXCEPTION P0001 на legacy invalid rows.
--
-- +goose Up
-- +goose StatementBegin

DO $$
DECLARE
    bad_name_count int;
    bad_desc_count int;
    bad_status_count int;
BEGIN
    SELECT COUNT(*) INTO bad_name_count
      FROM security_groups
     WHERE name !~ '^([a-zA-Z]([-_a-zA-Z0-9]{0,61}[a-zA-Z0-9])?)?$';
    IF bad_name_count > 0 THEN
        RAISE EXCEPTION 'security_groups has % rows with name violating VPC permissive regex — fix before applying CHECK (KAC-94)', bad_name_count
            USING ERRCODE = 'P0001';
    END IF;

    SELECT COUNT(*) INTO bad_desc_count
      FROM security_groups
     WHERE length(description) > 256;
    IF bad_desc_count > 0 THEN
        RAISE EXCEPTION 'security_groups has % rows with description longer than 256 chars — fix before applying CHECK (KAC-94)', bad_desc_count
            USING ERRCODE = 'P0001';
    END IF;

    SELECT COUNT(*) INTO bad_status_count
      FROM security_groups
     WHERE status NOT IN ('ACTIVE','CREATING','UPDATING','DELETING');
    IF bad_status_count > 0 THEN
        RAISE EXCEPTION 'security_groups has % rows with status outside SecurityGroupStatus enum — fix before applying CHECK (KAC-94)', bad_status_count
            USING ERRCODE = 'P0001';
    END IF;
END $$;

ALTER TABLE security_groups
    ADD CONSTRAINT security_groups_name_check
    CHECK (name ~ '^([a-zA-Z]([-_a-zA-Z0-9]{0,61}[a-zA-Z0-9])?)?$') NOT VALID;
ALTER TABLE security_groups VALIDATE CONSTRAINT security_groups_name_check;

ALTER TABLE security_groups
    ADD CONSTRAINT security_groups_description_check
    CHECK (length(description) <= 256) NOT VALID;
ALTER TABLE security_groups VALIDATE CONSTRAINT security_groups_description_check;

ALTER TABLE security_groups
    ADD CONSTRAINT security_groups_status_check
    CHECK (status IN ('ACTIVE','CREATING','UPDATING','DELETING')) NOT VALID;
ALTER TABLE security_groups VALIDATE CONSTRAINT security_groups_status_check;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE security_groups DROP CONSTRAINT IF EXISTS security_groups_status_check;
ALTER TABLE security_groups DROP CONSTRAINT IF EXISTS security_groups_description_check;
ALTER TABLE security_groups DROP CONSTRAINT IF EXISTS security_groups_name_check;

-- +goose StatementEnd
