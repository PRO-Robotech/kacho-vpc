-- Copyright (c) PRO-Robotech
-- SPDX-License-Identifier: BUSL-1.1

-- +goose Up
-- +goose StatementBegin

-- =============================================================================
-- NIC↔Instance attach slot: used_by_index (device-index на инстансе, eth0=0…).
-- =============================================================================
-- Оживляет отложенную в KAC-266 явную привязку NIC↔Instance. Привязка живёт на
-- строке network_interfaces flat-колонками used_by_type/id/name (уже есть) +
-- used_by_index (слот в инстансе), выставляется InternalNetworkInterfaceService.
-- Attach (атомарный CAS на used_by_id), очищается Detach.
--
-- used_by_index:
--   - NULLable: пусто у неприаттаченного NIC (used_by_id=''); задаётся при Attach.
--   - integer: 0-based слот (eth0=0, eth1=1, …), уникален в пределах инстанса.
--
-- Слот-уникальность — partial UNIQUE(used_by_id, used_by_index) WHERE used_by_id<>'':
--   - «несколько NIC на инстанс» держится тем, что уникальность — на ПАРЕ
--     (used_by_id, used_by_index), а НЕ глобальный UNIQUE(used_by_id) (тот ложно
--     запретил бы multi-NIC — урок откаченной миграции 0016/0017 из ретроспективы).
--   - NIC → ≤1 инстанс держит CAS `used_by_id='' OR =$instance` (single-statement
--     row-lock), а НЕ этот индекс.
--   - partial WHERE used_by_id<>'' исключает свободные NIC (used_by_id='',
--     used_by_index NULL) из уникальности — их «пустой» слот не конфликтует.

SET search_path TO kacho_vpc, public;

ALTER TABLE kacho_vpc.network_interfaces
    ADD COLUMN IF NOT EXISTS used_by_index integer;

CREATE UNIQUE INDEX IF NOT EXISTS ni_used_by_index_uniq
    ON kacho_vpc.network_interfaces (used_by_id, used_by_index) WHERE used_by_id <> '';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET search_path TO kacho_vpc, public;
DROP INDEX IF EXISTS kacho_vpc.ni_used_by_index_uniq;
ALTER TABLE kacho_vpc.network_interfaces DROP COLUMN IF EXISTS used_by_index;
-- +goose StatementEnd
