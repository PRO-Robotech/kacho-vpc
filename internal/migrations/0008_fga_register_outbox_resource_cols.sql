-- Copyright (c) PRO-Robotech
-- SPDX-License-Identifier: BUSL-1.1

-- 0008: additive-колонки resource_kind / resource_id на
-- kacho_vpc.fga_register_outbox.
--
-- ЗАЧЕМ: corelib outbox/reconciler адресует register-intent строки по
-- (resource_kind, resource_id) — его derive-from-state backfill (insertIntent) и
-- inverse-orphan GC (intendedRegistered) читают/пишут эти колонки. Базовая
-- таблица fga_register_outbox их не имела (только event_type/payload), поэтому
-- reconciler не мог перечислить или переотправить намерения по ресурсу. Колонки
-- additive и backfill-safe (NOT NULL DEFAULT ''), так что существующие строки и
-- single-statement writer INSERT (который теперь тоже их заполняет) продолжают
-- работать.
--
-- Сам drainer эти колонки НЕ читает (он забирает строки по id / event_type /
-- payload / attempt_count); они нужны reconciler'у и observability/tracing.

-- +goose Up
ALTER TABLE kacho_vpc.fga_register_outbox
    ADD COLUMN resource_kind text NOT NULL DEFAULT '',
    ADD COLUMN resource_id   text NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE kacho_vpc.fga_register_outbox
    DROP COLUMN IF EXISTS resource_id,
    DROP COLUMN IF EXISTS resource_kind;
