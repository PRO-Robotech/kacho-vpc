-- Copyright (c) PRO-Robotech
-- SPDX-License-Identifier: BUSL-1.1

-- 0005: DB-уровневые инварианты для default Security Group.
--
-- FK на default-SG: networks.default_security_group_id не имел FK → прямое
--   удаление default-SG оставляло dangling-ссылку на удаленную SG. FK невозможен
--   на NOT NULL DEFAULT '' (нет SG с id '') — переводим колонку в nullable
--   (NULL = «нет default SG»), '' → NULL, и добавляем FK ON DELETE SET NULL.
--   Публичный контракт не меняется: repo читает колонку через COALESCE(...,'')
--   (NULL → "" в proto-выводе).
--
-- Один default-SG на сеть: не было гарантии «не более одного default-SG на сеть»
--   → конкурентные/ошибочные пути могли создать два. Partial UNIQUE закрывает
--   инвариант.

-- +goose Up
ALTER TABLE kacho_vpc.networks ALTER COLUMN default_security_group_id DROP DEFAULT;
ALTER TABLE kacho_vpc.networks ALTER COLUMN default_security_group_id DROP NOT NULL;
UPDATE kacho_vpc.networks SET default_security_group_id = NULL WHERE default_security_group_id = '';
ALTER TABLE kacho_vpc.networks
    ADD CONSTRAINT networks_default_security_group_fk
    FOREIGN KEY (default_security_group_id)
    REFERENCES kacho_vpc.security_groups (id) ON DELETE SET NULL;

CREATE UNIQUE INDEX security_groups_one_default_per_network
    ON kacho_vpc.security_groups (network_id) WHERE default_for_network;

-- +goose Down
DROP INDEX IF EXISTS kacho_vpc.security_groups_one_default_per_network;
ALTER TABLE kacho_vpc.networks DROP CONSTRAINT IF EXISTS networks_default_security_group_fk;
UPDATE kacho_vpc.networks SET default_security_group_id = '' WHERE default_security_group_id IS NULL;
ALTER TABLE kacho_vpc.networks ALTER COLUMN default_security_group_id SET DEFAULT '';
ALTER TABLE kacho_vpc.networks ALTER COLUMN default_security_group_id SET NOT NULL;
