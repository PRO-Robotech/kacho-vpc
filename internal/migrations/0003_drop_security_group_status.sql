-- Copyright (c) PRO-Robotech
-- SPDX-License-Identifier: BUSL-1.1

-- +goose Up
-- +goose StatementBegin

-- =============================================================================
-- Удаление поля SecurityGroup.status.
-- =============================================================================
-- Поле `status` (enum CREATING/ACTIVE/UPDATING/DELETING) убрано из контракта
-- SecurityGroup. У SG нет provisioning-lifecycle — статус никем не наблюдался.
-- Дропаем колонку и связанный CHECK-constraint.

SET search_path TO kacho_vpc, public;

ALTER TABLE kacho_vpc.security_groups DROP CONSTRAINT IF EXISTS security_groups_status_check;
ALTER TABLE kacho_vpc.security_groups DROP COLUMN IF EXISTS status;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

SET search_path TO kacho_vpc, public;

ALTER TABLE kacho_vpc.security_groups
    ADD COLUMN status text NOT NULL DEFAULT 'ACTIVE';

ALTER TABLE kacho_vpc.security_groups
    ADD CONSTRAINT security_groups_status_check
    CHECK (status IN ('ACTIVE', 'CREATING', 'UPDATING', 'DELETING'));

-- +goose StatementEnd
