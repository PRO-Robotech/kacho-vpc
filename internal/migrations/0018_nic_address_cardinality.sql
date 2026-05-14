-- +goose Up
-- +goose StatementBegin

-- KAC-55: продуктовое решение — на одной NetworkInterface максимум один IPv4-
-- адрес и максимум один IPv6-адрес. Multi-IP на VM реализуется через
-- несколько NIC, а не через secondary addresses в одном NIC (упрощённая модель
-- vs AWS ENI; зеркалит verbatim YC compute API single primary_v4_address /
-- primary_v6_address).
--
-- До этой миграции `v4_address_ids` / `v6_address_ids` (JSONB-массивы)
-- ограничивались только software-валидацией в request-path. Согласно
-- workspace CLAUDE.md запрет #10 — within-service инварианты должны быть на
-- DB-уровне. Добавляем CHECK constraints как backstop: даже если service-слой
-- пропустит multi-address state (race / bypassed validator / direct repo
-- insert) — БД отдаст SQLSTATE 23514, repo маппит в InvalidArgument.
--
-- Pre-flight (2026-05-14): SELECT … WHERE jsonb_array_length(…) > 1 — 0 строк
-- на live стенде, можно применять без NOT VALID (existing rows не нарушают).

ALTER TABLE network_interfaces
    ADD CONSTRAINT network_interfaces_v4_addr_max1
    CHECK (jsonb_array_length(v4_address_ids) <= 1);

ALTER TABLE network_interfaces
    ADD CONSTRAINT network_interfaces_v6_addr_max1
    CHECK (jsonb_array_length(v6_address_ids) <= 1);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE network_interfaces DROP CONSTRAINT IF EXISTS network_interfaces_v4_addr_max1;
ALTER TABLE network_interfaces DROP CONSTRAINT IF EXISTS network_interfaces_v6_addr_max1;
-- +goose StatementEnd
