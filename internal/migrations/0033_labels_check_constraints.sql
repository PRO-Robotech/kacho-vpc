-- KAC-94 (Wave 2 batch D, skill evgeniy §5 E.2): DB-уровневые CHECK constraints
-- на JSONB-поле `labels` для всех 8 VPC-ресурсов (networks, subnets, addresses,
-- route_tables, security_groups, gateways, private_endpoints,
-- network_interfaces) — financial backstop поверх domain.ValidateLabels (skill
-- §5 E.1, "БД — последний рубеж от внешних writers / bugs в app-коде").
-- Симметрично 0025/0026/0027/0028/0029/0030/0031/0032, но трогает только
-- labels — name/description/прочее уже покрыты предыдущими миграциями.
--
-- Параметры (источник истины — internal/domain/types.go::LabelKey/LabelVal/
-- ValidateLabels + const MaxLabels=64 / MaxLabelKeyLen=63 / MaxLabelValueLen=63):
--   * cardinality   — ≤ 64 пар (domain.MaxLabels).
--   * label key     — regex Go `^[a-z][-_./\\@a-z0-9]{0,62}$` (длина 1..63,
--                     первый символ — lowercase letter, далее — lowercase
--                     letter, digit, `-`, `_`, `.`, `/`, `\`, `@`).
--                     Postgres POSIX regex с standard_conforming_strings=on:
--                     literal `\\` в строке = 2 байта `\\`, что POSIX regex
--                     внутри класса трактует как single literal `\`.
--   * label value   — длина ≤ 63 байт (domain.MaxLabelValueLen; пустая строка
--                     OK). regex на value нет — только length check.
--
-- Helper-функция public.kacho_labels_valid(jsonb) — IMMUTABLE, чтобы CHECK
-- мог её вызывать без проблем с планировщиком. SQL NULL labels → true (на
-- случай legacy/edge; в текущей схеме все 8 таблиц имеют NOT NULL DEFAULT
-- '{}'::jsonb, поэтому в практике SQL NULL не встретится). JSONB-значение
-- `null` (jsonb_typeof='null') → true тоже: repo маршалит пустой
-- map[string]string как Go nil → JSON `null`, это легитимный «labels не
-- заданы» case (LabelsToMap возвращает nil при пустой dict, см.
-- internal/domain/types.go). Не-object jsonb (string/array/number/bool) →
-- false. Пустой object `{}` → true.
--
-- Defensive RAISE EXCEPTION P0001 на legacy invalid rows — даёт actionable
-- error в логах миграции вместо непрозрачного 23514 от ALTER TABLE … VALIDATE.
--
-- +goose Up
-- +goose StatementBegin

CREATE OR REPLACE FUNCTION public.kacho_labels_valid(lbls jsonb) RETURNS boolean
LANGUAGE plpgsql IMMUTABLE AS $fn$
DECLARE
    k text;
    v text;
    n int;
BEGIN
    IF lbls IS NULL THEN
        RETURN true;
    END IF;
    -- JSONB-null (`'null'::jsonb`) — легитимная сериализация Go nil-map'а
    -- из repo (см. domain.LabelsToMap → nil → json.Marshal → "null").
    IF jsonb_typeof(lbls) = 'null' THEN
        RETURN true;
    END IF;
    IF jsonb_typeof(lbls) <> 'object' THEN
        RETURN false;
    END IF;
    SELECT count(*) INTO n FROM jsonb_object_keys(lbls);
    IF n > 64 THEN
        RETURN false;
    END IF;
    FOR k, v IN SELECT key, value FROM jsonb_each_text(lbls) LOOP
        -- key regex parity с domain.LabelKey.Validate (internal/domain/types.go).
        IF k !~ '^[a-z][-_./\\@a-z0-9]{0,62}$' THEN
            RETURN false;
        END IF;
        -- length value parity с domain.LabelVal.Validate (≤ 63 байт).
        IF length(v) > 63 THEN
            RETURN false;
        END IF;
    END LOOP;
    RETURN true;
END;
$fn$;

-- +goose StatementEnd

-- +goose StatementBegin
DO $$
DECLARE
    tbl text;
    bad_count int;
BEGIN
    FOREACH tbl IN ARRAY ARRAY[
        'networks','subnets','addresses','route_tables',
        'security_groups','gateways','private_endpoints','network_interfaces'
    ] LOOP
        EXECUTE format(
            'SELECT count(*) FROM public.%I WHERE NOT public.kacho_labels_valid(labels)',
            tbl
        ) INTO bad_count;
        IF bad_count > 0 THEN
            RAISE EXCEPTION
                '% has % rows with labels violating domain.ValidateLabels (cardinality ≤64, key regex ^[a-z][-_./\\@a-z0-9]{0,62}$, value length ≤63) — fix before applying CHECK (KAC-94)',
                tbl, bad_count
                USING ERRCODE = 'P0001';
        END IF;
    END LOOP;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE public.networks
    ADD CONSTRAINT networks_labels_valid CHECK (public.kacho_labels_valid(labels)) NOT VALID;
ALTER TABLE public.networks VALIDATE CONSTRAINT networks_labels_valid;

ALTER TABLE public.subnets
    ADD CONSTRAINT subnets_labels_valid CHECK (public.kacho_labels_valid(labels)) NOT VALID;
ALTER TABLE public.subnets VALIDATE CONSTRAINT subnets_labels_valid;

ALTER TABLE public.addresses
    ADD CONSTRAINT addresses_labels_valid CHECK (public.kacho_labels_valid(labels)) NOT VALID;
ALTER TABLE public.addresses VALIDATE CONSTRAINT addresses_labels_valid;

ALTER TABLE public.route_tables
    ADD CONSTRAINT route_tables_labels_valid CHECK (public.kacho_labels_valid(labels)) NOT VALID;
ALTER TABLE public.route_tables VALIDATE CONSTRAINT route_tables_labels_valid;

ALTER TABLE public.security_groups
    ADD CONSTRAINT security_groups_labels_valid CHECK (public.kacho_labels_valid(labels)) NOT VALID;
ALTER TABLE public.security_groups VALIDATE CONSTRAINT security_groups_labels_valid;

ALTER TABLE public.gateways
    ADD CONSTRAINT gateways_labels_valid CHECK (public.kacho_labels_valid(labels)) NOT VALID;
ALTER TABLE public.gateways VALIDATE CONSTRAINT gateways_labels_valid;

ALTER TABLE public.private_endpoints
    ADD CONSTRAINT private_endpoints_labels_valid CHECK (public.kacho_labels_valid(labels)) NOT VALID;
ALTER TABLE public.private_endpoints VALIDATE CONSTRAINT private_endpoints_labels_valid;

ALTER TABLE public.network_interfaces
    ADD CONSTRAINT network_interfaces_labels_valid CHECK (public.kacho_labels_valid(labels)) NOT VALID;
ALTER TABLE public.network_interfaces VALIDATE CONSTRAINT network_interfaces_labels_valid;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE public.network_interfaces DROP CONSTRAINT IF EXISTS network_interfaces_labels_valid;
ALTER TABLE public.private_endpoints DROP CONSTRAINT IF EXISTS private_endpoints_labels_valid;
ALTER TABLE public.gateways DROP CONSTRAINT IF EXISTS gateways_labels_valid;
ALTER TABLE public.security_groups DROP CONSTRAINT IF EXISTS security_groups_labels_valid;
ALTER TABLE public.route_tables DROP CONSTRAINT IF EXISTS route_tables_labels_valid;
ALTER TABLE public.addresses DROP CONSTRAINT IF EXISTS addresses_labels_valid;
ALTER TABLE public.subnets DROP CONSTRAINT IF EXISTS subnets_labels_valid;
ALTER TABLE public.networks DROP CONSTRAINT IF EXISTS networks_labels_valid;

DROP FUNCTION IF EXISTS public.kacho_labels_valid(jsonb);
-- +goose StatementEnd
