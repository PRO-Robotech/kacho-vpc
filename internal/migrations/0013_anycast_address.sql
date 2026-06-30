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
-- Два GENERATED-столбца под cross-ресурсные FK (зеркаль internal_subnet_id из 0001;
-- expression-индекс нельзя сделать FK-таргетом — поэтому именно колонки):
--   - anycast_network_id — network-scope: FK→networks ON DELETE RESTRICT не даёт
--     удалить сеть, пока в ней есть anycast-аллокации.
--   - anycast_pool_id     — pool-scope: FK→anycast_address_pools ON DELETE RESTRICT
--     не даёт удалить пул, пока из него есть живая anycast-аллокация. Закрывает
--     is_default-пул, доступный без pivot-attach (software CountAttachments по
--     pivot-строкам его не ловит → DB-backstop).
-- Глобально-уникальный host — partial EXPRESSION-индекс по anycast->>'address'
-- (как addresses_external_ip_uniq в 0001): без второй STORED-колонки и второго
-- table-rewrite.

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

-- anycast_pool_id — извлекаем pool_id из anycast-spec (NULL для не-anycast
-- адресов). FK→anycast_address_pools ON DELETE RESTRICT: пул с живой аллокацией
-- удалить нельзя. DB-backstop к software CountAttachments — закрывает is_default-пул,
-- который auto-available без pivot-attach (CountAttachments=0 не ловит orphan).
ALTER TABLE kacho_vpc.addresses
    ADD COLUMN IF NOT EXISTS anycast_pool_id text GENERATED ALWAYS AS (
        CASE
            WHEN anycast IS NOT NULL
                 AND anycast ? 'pool_id'
                 AND length(anycast->>'pool_id') > 0
            THEN anycast->>'pool_id'
            ELSE NULL
        END
    ) STORED REFERENCES kacho_vpc.anycast_address_pools(id) ON DELETE RESTRICT;

-- Глобально-уникальный host: один и тот же anycast-IP не может быть выдан дважды
-- (gate против двойной аллокации). partial EXPRESSION-индекс по anycast->>'address'
-- (зеркаль addresses_external_ip_uniq из 0001): не-anycast строки и
-- ещё-не-выделенные anycast-адреса (пустой address) не участвуют.
CREATE UNIQUE INDEX IF NOT EXISTS addresses_anycast_host_uniq
    ON kacho_vpc.addresses ((anycast ->> 'address'))
    WHERE anycast IS NOT NULL AND (anycast ->> 'address') <> '';

-- Композитный partial-индекс под detach-guard CountAllocationsInNetwork:
-- COUNT(*) WHERE anycast_pool_id = $pool AND anycast_network_id = $net. Покрывает
-- обе реальные generated-колонки одним индексом (leading anycast_network_id служит
-- и scope-проверке network FK) — отдельные одиночные partial-индексы не нужны.
CREATE INDEX IF NOT EXISTS addresses_anycast_alloc_idx
    ON kacho_vpc.addresses (anycast_network_id, anycast_pool_id)
    WHERE anycast IS NOT NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

SET search_path TO kacho_vpc, public;

DROP INDEX IF EXISTS kacho_vpc.addresses_anycast_alloc_idx;
DROP INDEX IF EXISTS kacho_vpc.addresses_anycast_host_uniq;
ALTER TABLE kacho_vpc.addresses DROP COLUMN IF EXISTS anycast_pool_id;
ALTER TABLE kacho_vpc.addresses DROP COLUMN IF EXISTS anycast_network_id;
ALTER TABLE kacho_vpc.addresses DROP COLUMN IF EXISTS anycast;

-- +goose StatementEnd
