-- Copyright (c) PRO-Robotech
-- SPDX-License-Identifier: BUSL-1.1

-- +goose Up
-- +goose StatementBegin

-- =============================================================================
-- address_references.owned: владеет ли referrer адресом (не просто ссылается).
-- =============================================================================
-- owned=true  — адрес заказан referrer'ом неявно (его lifecycle связан с
--               referrer'ом); освобождение = ClearReference + Delete адреса.
-- owned=false — tenant создал адрес заранее и залинковал; освобождение = только
--               ClearReference (адрес остается за tenant'ом).
--
-- Существующие referrer-строки (NIC-привязки) не владеют адресом — они лишь
-- ссылаются, поэтому бэкфилл значением false корректен. DEFAULT false оставляем:
-- referrer без явного owned — не владеющий (link-семантика по умолчанию).

SET search_path TO kacho_vpc, public;

ALTER TABLE kacho_vpc.address_references
    ADD COLUMN IF NOT EXISTS owned boolean NOT NULL DEFAULT false;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET search_path TO kacho_vpc, public;
ALTER TABLE kacho_vpc.address_references
    DROP COLUMN IF EXISTS owned;
-- +goose StatementEnd
