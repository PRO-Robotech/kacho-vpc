-- Copyright (c) PRO-Robotech
-- SPDX-License-Identifier: BUSL-1.1

-- +goose Up
-- +goose StatementBegin

-- =============================================================================
-- Anycast-представление ресурса Address.
-- =============================================================================
-- Anycast-Address — это существующий ресурс Address (id-prefix adr) с anycast-spec:
-- network-scoped host /32 (IPv4) / /128 (IPv6), достижим только внутри VPC владельца.
-- Хранится в новой JSONB-колонке `anycast` ({network_id, address, pool_id}),
-- параллельно external/internal v4/v6-специям.
--
-- Два GENERATED-столбца (зеркаль паттерна internal_subnet_id из 0001):
--   - anycast_network_id — network-scope: FK→networks ON DELETE RESTRICT не даёт
--     удалить сеть, пока в ней есть anycast-аллокации.
--   - anycast_host       — host-адрес для глобально-уникального индекса: гейт
--     против двойной аллокации одного IP во всём кластере.

SET search_path TO kacho_vpc, public;

ALTER TABLE kacho_vpc.addresses
    ADD COLUMN IF NOT EXISTS anycast jsonb;

-- anycast_network_id — извлекаем network_id из anycast-spec (NULL для не-anycast
-- адресов). FK→networks ON DELETE RESTRICT: сеть с живой anycast-аллокацией
-- удалить нельзя. Зеркалит internal_subnet_id GENERATED.
ALTER TABLE kacho_vpc.addresses
    ADD COLUMN IF NOT EXISTS anycast_network_id text GENERATED ALWAYS AS (
        CASE
            WHEN anycast IS NOT NULL
                 AND anycast ? 'network_id'
                 AND length(anycast->>'network_id') > 0
            THEN anycast->>'network_id'
            ELSE NULL
        END
    ) STORED REFERENCES kacho_vpc.networks(id) ON DELETE RESTRICT;

-- anycast_host — host-адрес anycast-аллокации (NULL, пока адрес не выделен или
-- адрес не anycast). Глобально-уникальный индекс ниже использует этот столбец как
-- финальный DB-гейт против двойной аллокации одного IP.
ALTER TABLE kacho_vpc.addresses
    ADD COLUMN IF NOT EXISTS anycast_host text GENERATED ALWAYS AS (
        CASE
            WHEN anycast IS NOT NULL
                 AND anycast ? 'address'
                 AND length(anycast->>'address') > 0
            THEN anycast->>'address'
            ELSE NULL
        END
    ) STORED;

-- Глобально-уникальный host: один и тот же anycast-IP не может быть выдан дважды
-- (gate против двойной аллокации, D5). partial WHERE NOT NULL — не-anycast строки
-- и ещё-не-выделенные anycast-адреса (host пуст) не участвуют.
CREATE UNIQUE INDEX IF NOT EXISTS addresses_anycast_host_uniq
    ON kacho_vpc.addresses (anycast_host)
    WHERE anycast_host IS NOT NULL;

-- Индекс под scope-проверку network (FK-таргет) и detach-guard (COUNT аллокаций).
CREATE INDEX IF NOT EXISTS addresses_anycast_network_idx
    ON kacho_vpc.addresses (anycast_network_id)
    WHERE anycast_network_id IS NOT NULL;

-- Функциональный индекс под detach-guard CountAllocationsInNetwork:
-- COUNT(*) WHERE anycast->>'pool_id' = $pool AND anycast_network_id = $net.
CREATE INDEX IF NOT EXISTS addresses_anycast_pool_idx
    ON kacho_vpc.addresses ((anycast->>'pool_id'))
    WHERE anycast IS NOT NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

SET search_path TO kacho_vpc, public;

DROP INDEX IF EXISTS kacho_vpc.addresses_anycast_pool_idx;
DROP INDEX IF EXISTS kacho_vpc.addresses_anycast_network_idx;
DROP INDEX IF EXISTS kacho_vpc.addresses_anycast_host_uniq;
ALTER TABLE kacho_vpc.addresses DROP COLUMN IF EXISTS anycast_host;
ALTER TABLE kacho_vpc.addresses DROP COLUMN IF EXISTS anycast_network_id;
ALTER TABLE kacho_vpc.addresses DROP COLUMN IF EXISTS anycast;

-- +goose StatementEnd
