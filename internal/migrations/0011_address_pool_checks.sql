-- Copyright (c) PRO-Robotech
-- SPDX-License-Identifier: BUSL-1.1

-- +goose Up
-- +goose StatementBegin

-- =============================================================================
-- DB CHECK-parity для address_pools: форма ресурса (name/description/kind/
-- selector_priority) обязана держаться на DB-уровне (within-service инвариант —
-- DB-уровень, ban #10), а не только software-валидацией в use-case.
-- =============================================================================
-- Таблица address_pools создана в 0001 без единого CHECK; domain.AddressPool.
-- Validate() теперь проверяет ту же форму на use-case-слое — этот файл добавляет
-- DB-backstop с тем же набором правил. Партиал-UNIQUE «один is_default на
-- (zone_id, kind)» (address_pools_zone_kind_default_uniq) уже есть в 0001 —
-- здесь НЕ дублируется (второй индекс с тем же предикатом запрещен).
--
-- Только ADD CONSTRAINT ... CHECK; ни одного нового индекса. Существующие
-- (валидные) строки проходят проверку при ADD CONSTRAINT — иначе миграция
-- падает, вскрывая невалидные данные (by-design).

SET search_path TO kacho_vpc, public;

-- Идемпотентность (IF NOT EXISTS через pg_constraint): ALTER TABLE ADD CONSTRAINT
-- не поддерживает IF NOT EXISTS для CHECK, поэтому оборачиваем в DO-guard —
-- защита от повторного/частичного применения и гонки параллельного migrate-init
-- (helm-rollout + rollout-restart запускают goose параллельно).
DO $$
BEGIN
    -- name — тот же разрешительный regex, что у прочих VPC-ресурсов (networks):
    -- буквы/цифры/дефисы/подчеркивания, начинается с буквы, до 63 символов,
    -- пустая строка допустима.
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'address_pools_name_chk') THEN
        ALTER TABLE kacho_vpc.address_pools
            ADD CONSTRAINT address_pools_name_chk
            CHECK (name ~ '^([a-zA-Z]([-_a-zA-Z0-9]{0,61}[a-zA-Z0-9])?)?$');
    END IF;

    -- description — длина не больше 256 символов.
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'address_pools_description_len_chk') THEN
        ALTER TABLE kacho_vpc.address_pools
            ADD CONSTRAINT address_pools_description_len_chk
            CHECK (char_length(description) <= 256);
    END IF;

    -- kind — единственное определенное значение AddressPoolKind = EXTERNAL_PUBLIC (1).
    -- Расширяется в lockstep с domain-enum + новой миграцией.
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'address_pools_kind_chk') THEN
        ALTER TABLE kacho_vpc.address_pools
            ADD CONSTRAINT address_pools_kind_chk
            CHECK (kind = 1);
    END IF;

    -- selector_priority — неотрицательный tie-break weight (HIGHER-wins).
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'address_pools_selector_priority_chk') THEN
        ALTER TABLE kacho_vpc.address_pools
            ADD CONSTRAINT address_pools_selector_priority_chk
            CHECK (selector_priority >= 0);
    END IF;
END $$;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET search_path TO kacho_vpc, public;
ALTER TABLE kacho_vpc.address_pools
    DROP CONSTRAINT IF EXISTS address_pools_name_chk,
    DROP CONSTRAINT IF EXISTS address_pools_description_len_chk,
    DROP CONSTRAINT IF EXISTS address_pools_kind_chk,
    DROP CONSTRAINT IF EXISTS address_pools_selector_priority_chk;
-- +goose StatementEnd
