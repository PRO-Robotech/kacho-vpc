-- Copyright (c) PRO-Robotech
-- SPDX-License-Identifier: BUSL-1.1

-- +goose Up
-- +goose StatementBegin

-- =============================================================================
-- Subnet placement: дискриминатор размещения подсети (ZONAL | REGIONAL).
-- =============================================================================
-- ZONAL    — unicast-адреса, подсеть живет в одной зоне доступности (zone_id).
-- REGIONAL — anycast-префикс, анонсируется active-active из здоровых зон региона
--            (region_id); адреса region-scoped, без зональности.
--
-- Дискриминатор один (placement_type), payload плоский (zone_id / region_id).
-- Целостность пары держит DB-CHECK (within-service инвариант — DB-уровень, ban #10):
-- ровно одно из zone_id/region_id непусто, согласованно с placement_type. Пустого
-- placement_type в БД быть не может (CHECK перечисляет только ZONAL/REGIONAL).
--
-- Непересечение CIDR региональных подсетей с любыми (зональными или региональными)
-- подсетями той же сети уже держит per-network EXCLUDE child-таблицы
-- subnet_cidr_blocks (network_id WITH =, block WITH &&) — отдельный constraint не нужен.

SET search_path TO kacho_vpc, public;

-- placement_type: бэкфилл существующих строк значением 'ZONAL' (они зональны по
-- факту — zone_id обязателен до этой миграции), затем снятие DEFAULT, чтобы новый
-- INSERT задавал тип явно (без silent-fallback). Это корректная инициализация
-- колонки, а не legacy-режим: модель требует явного выбора на каждом Create.
ALTER TABLE kacho_vpc.subnets
    ADD COLUMN IF NOT EXISTS placement_type text NOT NULL DEFAULT 'ZONAL';
ALTER TABLE kacho_vpc.subnets
    ALTER COLUMN placement_type DROP DEFAULT;

-- region_id: непусто только у REGIONAL-подсетей (empty-string convention, как zone_id).
ALTER TABLE kacho_vpc.subnets
    ADD COLUMN IF NOT EXISTS region_id text NOT NULL DEFAULT '';

DO $$
BEGIN
    -- placement_type ограничен ровно двумя значениями; расширяется в lockstep с
    -- domain-enum + новой миграцией. Пустого/UNSPECIFIED значения в БД нет.
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'subnets_placement_type_chk') THEN
        ALTER TABLE kacho_vpc.subnets
            ADD CONSTRAINT subnets_placement_type_chk
            CHECK (placement_type IN ('ZONAL', 'REGIONAL'));
    END IF;

    -- Точная биусловная связь типа и payload'а: ZONAL ⟺ (zone_id задан, region_id пуст),
    -- REGIONAL ⟺ (region_id задан, zone_id пуст). Исключает обе вырожденные комбинации
    -- (оба пусты / оба заданы).
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'subnets_placement_payload_chk') THEN
        ALTER TABLE kacho_vpc.subnets
            ADD CONSTRAINT subnets_placement_payload_chk
            CHECK (
                (placement_type = 'ZONAL'    AND zone_id <> '' AND region_id = '') OR
                (placement_type = 'REGIONAL' AND zone_id = ''  AND region_id <> '')
            );
    END IF;
END $$;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET search_path TO kacho_vpc, public;
ALTER TABLE kacho_vpc.subnets
    DROP CONSTRAINT IF EXISTS subnets_placement_payload_chk,
    DROP CONSTRAINT IF EXISTS subnets_placement_type_chk;
ALTER TABLE kacho_vpc.subnets
    DROP COLUMN IF EXISTS region_id,
    DROP COLUMN IF EXISTS placement_type;
-- +goose StatementEnd
