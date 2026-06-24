-- Copyright (c) PRO-Robotech
-- SPDX-License-Identifier: BUSL-1.1

-- +goose Up
-- +goose StatementBegin

-- =============================================================================
-- Kachō VPC — базовая схема
-- =============================================================================
-- Все таблицы / constraint / индексы / триггеры / helper-функции живут в схеме
-- `kacho_vpc`. CHECK constraints объявляются inline в CREATE TABLE — никаких
-- post-hoc ALTER … ADD CONSTRAINT. Схема описывает только control-plane VPC,
-- без data-plane-полей.
--
-- Состав:
--   - extension btree_gist (в public — extension-owned).
--   - helper-функции: kacho_labels_valid, vpc_outbox_notify, rt_auto_assoc_subnets,
--     subnet_auto_pick_rt, subnets_outbox_emit_route_table_change.
--   - tables (14):
--       operations, networks, subnets, addresses, address_references,
--       route_tables, security_groups, gateways,
--       network_interfaces, vpc_outbox, vpc_watch_cursors,
--       address_pools, address_pool_address_override, address_pool_network_default,
--       cloud_pool_selector, address_pool_free_ips,
--       ipv6_pool_cursors, ipv6_allocated_ips, ipv6_released_offsets.
--   - sequences: vpc_outbox_sequence_no_seq (owned by vpc_outbox.sequence_no).
--   - triggers: vpc_outbox_notify_trg, rt_auto_assoc_subnets_trg,
--     subnet_auto_pick_rt_trg, subnets_outbox_emit_route_table_change_trg.
-- =============================================================================

CREATE SCHEMA IF NOT EXISTS kacho_vpc;
SET search_path TO kacho_vpc, public;

-- Extension btree_gist — extension-owned, остается в public (pg_dump/restore
-- ожидает ее там по умолчанию).
CREATE EXTENSION IF NOT EXISTS btree_gist WITH SCHEMA public;

-- +goose StatementEnd

-- =============================================================================
-- Helper functions
-- =============================================================================

-- +goose StatementBegin
-- kacho_labels_valid — проверка labels JSONB (cardinality ≤64, key regex,
-- value length ≤63). Используется в CHECK constraint всех 8 ресурсов с labels.
CREATE OR REPLACE FUNCTION kacho_vpc.kacho_labels_valid(lbls jsonb) RETURNS boolean
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
        IF k !~ '^[a-z][-_./\\@a-z0-9]{0,62}$' THEN
            RETURN false;
        END IF;
        IF length(v) > 63 THEN
            RETURN false;
        END IF;
    END LOOP;
    RETURN true;
END;
$fn$;
-- +goose StatementEnd

-- +goose StatementBegin
-- vpc_outbox_notify — pg_notify на каждом INSERT в vpc_outbox.
-- Используется InternalWatchService для realtime event push (LISTEN/NOTIFY).
CREATE OR REPLACE FUNCTION kacho_vpc.vpc_outbox_notify() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    PERFORM pg_notify('vpc_outbox', NEW.sequence_no::text);
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- rt_auto_assoc_subnets — AFTER INSERT ON route_tables: новая RT в сети
-- применяется к Subnet'ам этой сети, у которых route_table_id еще не задан.
CREATE OR REPLACE FUNCTION kacho_vpc.rt_auto_assoc_subnets() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    UPDATE kacho_vpc.subnets
       SET route_table_id = NEW.id
     WHERE network_id = NEW.network_id
       AND route_table_id IS NULL;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- subnet_auto_pick_rt — BEFORE INSERT ON subnets: если клиент не задал
-- route_table_id, подставляем самую раннюю RT этой сети.
CREATE OR REPLACE FUNCTION kacho_vpc.subnet_auto_pick_rt() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.route_table_id IS NULL THEN
        SELECT id INTO NEW.route_table_id
          FROM kacho_vpc.route_tables
         WHERE network_id = NEW.network_id
         ORDER BY created_at ASC, id ASC
         LIMIT 1;
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- subnets_outbox_emit_route_table_change — AFTER UPDATE OF route_table_id:
-- эмитим Subnet.UPDATED в vpc_outbox при auto-assoc / RT.Delete (SET NULL).
CREATE OR REPLACE FUNCTION kacho_vpc.subnets_outbox_emit_route_table_change() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO kacho_vpc.vpc_outbox (resource_kind, resource_id, event_type, payload)
    VALUES (
        'Subnet',
        NEW.id,
        'UPDATED',
        jsonb_build_object(
            'id', NEW.id,
            'project_id', NEW.project_id,
            'network_id', NEW.network_id,
            'route_table_id', NEW.route_table_id,
            'name', NEW.name,
            'auto_association', true
        )
    );
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin

-- =============================================================================
-- operations (LRO — Long-Running Operations, kacho-corelib pattern)
-- =============================================================================

CREATE TABLE kacho_vpc.operations (
    id            text         PRIMARY KEY,
    description   text         NOT NULL,
    created_at    timestamptz  NOT NULL DEFAULT now(),
    created_by    text         NOT NULL DEFAULT 'anonymous',
    modified_at   timestamptz  NOT NULL DEFAULT now(),
    done          boolean      NOT NULL DEFAULT false,
    metadata_type text,
    metadata_data bytea,
    resource_id   text,
    error_code    integer,
    error_message text,
    error_details bytea,
    response_type text,
    response_data bytea,
    -- principal-поля: кто инициировал операцию. Без AuthN — DEFAULT-stub
    -- 'system'/'bootstrap'/'System'.
    principal_type         text         NOT NULL DEFAULT 'system',
    principal_id           text         NOT NULL DEFAULT 'bootstrap',
    principal_display_name text         NOT NULL DEFAULT 'System'
);

CREATE INDEX operations_resource_idx   ON kacho_vpc.operations (resource_id);
CREATE INDEX operations_done_idx       ON kacho_vpc.operations (done);
CREATE INDEX operations_created_at_idx ON kacho_vpc.operations (created_at);

-- =============================================================================
-- networks
-- =============================================================================

CREATE TABLE kacho_vpc.networks (
    id                        text         PRIMARY KEY,
    project_id                 text         NOT NULL,
    created_at                timestamptz  NOT NULL DEFAULT now(),
    name                      text         NOT NULL,
    description               text         NOT NULL DEFAULT '',
    labels                    jsonb        NOT NULL DEFAULT '{}'::jsonb,
    default_security_group_id text         NOT NULL DEFAULT '',
    route_distinguisher       text         NOT NULL DEFAULT '',

    CONSTRAINT networks_name_check
        CHECK (name ~ '^([a-zA-Z]([-_a-zA-Z0-9]{0,61}[a-zA-Z0-9])?)?$'),
    CONSTRAINT networks_description_check
        CHECK (length(description) <= 256),
    CONSTRAINT networks_labels_valid
        CHECK (kacho_vpc.kacho_labels_valid(labels))
);

CREATE UNIQUE INDEX networks_project_id_name_key ON kacho_vpc.networks (project_id, name);
CREATE INDEX networks_project_idx     ON kacho_vpc.networks (project_id);
CREATE INDEX networks_created_at_idx ON kacho_vpc.networks (created_at);

-- =============================================================================
-- route_tables
-- =============================================================================

CREATE TABLE kacho_vpc.route_tables (
    id             text         PRIMARY KEY,
    project_id      text         NOT NULL,
    created_at     timestamptz  NOT NULL DEFAULT now(),
    name           text         NOT NULL,
    description    text         NOT NULL DEFAULT '',
    labels         jsonb        NOT NULL DEFAULT '{}'::jsonb,
    network_id     text         NOT NULL REFERENCES kacho_vpc.networks(id),
    static_routes  jsonb        NOT NULL DEFAULT '[]'::jsonb,

    CONSTRAINT route_tables_name_check
        CHECK (name ~ '^([a-zA-Z]([-_a-zA-Z0-9]{0,61}[a-zA-Z0-9])?)?$'),
    CONSTRAINT route_tables_description_check
        CHECK (length(description) <= 256),
    CONSTRAINT route_tables_labels_valid
        CHECK (kacho_vpc.kacho_labels_valid(labels))
);

CREATE UNIQUE INDEX route_tables_project_id_name_key
    ON kacho_vpc.route_tables (project_id, name) WHERE name <> '';
CREATE INDEX route_tables_project_idx     ON kacho_vpc.route_tables (project_id);
CREATE INDEX route_tables_network_idx    ON kacho_vpc.route_tables (network_id);
CREATE INDEX route_tables_created_at_idx ON kacho_vpc.route_tables (created_at);

-- =============================================================================
-- subnets
-- =============================================================================
-- v4_cidr_primary / v6_cidr_primary — generated columns для EXCLUDE GIST overlap.
-- subnets.route_table_id — FK ON DELETE SET NULL (auto-assoc через PL/pgSQL).

CREATE TABLE kacho_vpc.subnets (
    id              text         PRIMARY KEY,
    project_id       text         NOT NULL,
    created_at      timestamptz  NOT NULL DEFAULT now(),
    name            text         NOT NULL,
    description     text         NOT NULL DEFAULT '',
    labels          jsonb        NOT NULL DEFAULT '{}'::jsonb,
    network_id      text         NOT NULL REFERENCES kacho_vpc.networks(id),
    zone_id         text         NOT NULL DEFAULT '',
    v4_cidr_blocks  text[]       NOT NULL DEFAULT '{}'::text[],
    v6_cidr_blocks  text[]       NOT NULL DEFAULT '{}'::text[],
    route_table_id  text         REFERENCES kacho_vpc.route_tables(id) ON DELETE SET NULL,
    dhcp_options    jsonb,

    v4_cidr_primary cidr GENERATED ALWAYS AS (
        CASE
            WHEN (array_length(v4_cidr_blocks, 1) >= 1)
                 AND (v4_cidr_blocks[1] ~ '^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+/[0-9]+$')
            THEN (v4_cidr_blocks[1])::cidr
            ELSE NULL::cidr
        END
    ) STORED,
    v6_cidr_primary cidr GENERATED ALWAYS AS (
        CASE
            WHEN (array_length(v6_cidr_blocks, 1) >= 1)
                 AND (v6_cidr_blocks[1] ~ '^[0-9a-fA-F:]+/[0-9]+$')
            THEN (v6_cidr_blocks[1])::cidr
            ELSE NULL::cidr
        END
    ) STORED,

    CONSTRAINT subnets_name_check
        CHECK (name ~ '^([a-zA-Z]([-_a-zA-Z0-9]{0,61}[a-zA-Z0-9])?)?$'),
    CONSTRAINT subnets_description_check
        CHECK (length(description) <= 256),
    CONSTRAINT subnets_labels_valid
        CHECK (kacho_vpc.kacho_labels_valid(labels)),
    CONSTRAINT subnets_no_overlap_v4
        EXCLUDE USING gist (network_id WITH =, v4_cidr_primary inet_ops WITH &&)
        WHERE (v4_cidr_primary IS NOT NULL),
    CONSTRAINT subnets_no_overlap_v6
        EXCLUDE USING gist (network_id WITH =, v6_cidr_primary inet_ops WITH &&)
        WHERE (v6_cidr_primary IS NOT NULL)
);

CREATE UNIQUE INDEX subnets_project_id_name_key
    ON kacho_vpc.subnets (project_id, name) WHERE name <> '';
CREATE INDEX subnets_project_idx     ON kacho_vpc.subnets (project_id);
CREATE INDEX subnets_network_idx    ON kacho_vpc.subnets (network_id);
CREATE INDEX subnets_created_at_idx ON kacho_vpc.subnets (created_at);

-- =============================================================================
-- addresses
-- =============================================================================
-- internal_subnet_id — generated column из subnet_id internal_ipv4 ИЛИ
-- internal_ipv6: internal-адрес (v4 или v6) блокирует свою подсеть через FK.

CREATE TABLE kacho_vpc.addresses (
    id                  text         PRIMARY KEY,
    project_id           text         NOT NULL,
    created_at          timestamptz  NOT NULL DEFAULT now(),
    name                text         NOT NULL DEFAULT '',
    description         text         NOT NULL DEFAULT '',
    labels              jsonb        NOT NULL DEFAULT '{}'::jsonb,
    addr_type           smallint     NOT NULL DEFAULT 0,
    ip_version          smallint     NOT NULL DEFAULT 0,
    reserved            boolean      NOT NULL DEFAULT false,
    used                boolean      NOT NULL DEFAULT false,
    deletion_protection boolean      NOT NULL DEFAULT false,
    external_ipv4       jsonb,
    internal_ipv4       jsonb,
    external_ipv6       jsonb,
    internal_ipv6       jsonb,

    internal_subnet_id text GENERATED ALWAYS AS (
        CASE
            WHEN internal_ipv4 IS NOT NULL
                 AND internal_ipv4 ? 'subnet_id'
                 AND length(internal_ipv4->>'subnet_id') > 0
            THEN internal_ipv4->>'subnet_id'
            WHEN internal_ipv6 IS NOT NULL
                 AND internal_ipv6 ? 'subnet_id'
                 AND length(internal_ipv6->>'subnet_id') > 0
            THEN internal_ipv6->>'subnet_id'
            ELSE NULL
        END
    ) STORED REFERENCES kacho_vpc.subnets(id) ON DELETE RESTRICT,

    CONSTRAINT addresses_name_check
        CHECK (name ~ '^([a-zA-Z]([-_a-zA-Z0-9]{0,61}[a-zA-Z0-9])?)?$'),
    CONSTRAINT addresses_description_check
        CHECK (length(description) <= 256),
    CONSTRAINT addresses_labels_valid
        CHECK (kacho_vpc.kacho_labels_valid(labels))
);

CREATE UNIQUE INDEX addresses_project_id_name_key
    ON kacho_vpc.addresses (project_id, name) WHERE name <> '';
CREATE UNIQUE INDEX addresses_external_ip_uniq
    ON kacho_vpc.addresses (((external_ipv4 ->> 'address')))
    WHERE external_ipv4 IS NOT NULL
      AND (external_ipv4 ->> 'address') <> '';
CREATE UNIQUE INDEX addresses_external_pool_ip_uniq
    ON kacho_vpc.addresses (((external_ipv4 ->> 'address_pool_id')),
                            ((external_ipv4 ->> 'address')))
    WHERE external_ipv4 IS NOT NULL
      AND (external_ipv4 ->> 'address') <> ''
      AND (external_ipv4 ->> 'address_pool_id') <> '';
CREATE UNIQUE INDEX addresses_internal_subnet_ip_uniq
    ON kacho_vpc.addresses (((internal_ipv4 ->> 'subnet_id')),
                            ((internal_ipv4 ->> 'address')))
    WHERE internal_ipv4 IS NOT NULL
      AND (internal_ipv4 ->> 'address') <> ''
      AND (internal_ipv4 ->> 'subnet_id') <> '';
CREATE UNIQUE INDEX addresses_internal_subnet_ipv6_uniq
    ON kacho_vpc.addresses (((internal_ipv6 ->> 'subnet_id')),
                            ((internal_ipv6 ->> 'address')))
    WHERE internal_ipv6 IS NOT NULL
      AND (internal_ipv6 ->> 'address') <> ''
      AND (internal_ipv6 ->> 'subnet_id') <> '';
CREATE UNIQUE INDEX addresses_external_v6_pool_ip_uniq
    ON kacho_vpc.addresses (((external_ipv6 ->> 'address_pool_id')),
                            ((external_ipv6 ->> 'address')))
    WHERE external_ipv6 IS NOT NULL
      AND (external_ipv6 ->> 'address') <> ''
      AND (external_ipv6 ->> 'address_pool_id') <> '';
CREATE INDEX addresses_project_idx          ON kacho_vpc.addresses (project_id);
CREATE INDEX addresses_internal_subnet_idx ON kacho_vpc.addresses (internal_subnet_id);
CREATE INDEX addresses_created_at_idx      ON kacho_vpc.addresses (created_at);

-- =============================================================================
-- address_references (referrer-tracking: Address.used + список ссылающихся)
-- =============================================================================

CREATE TABLE kacho_vpc.address_references (
    address_id    text         PRIMARY KEY REFERENCES kacho_vpc.addresses(id) ON DELETE CASCADE,
    referrer_type text         NOT NULL,
    referrer_id   text         NOT NULL,
    referrer_name text         NOT NULL DEFAULT '',
    attached_at   timestamptz  NOT NULL DEFAULT now()
);

CREATE INDEX address_references_referrer_idx
    ON kacho_vpc.address_references (referrer_type, referrer_id);

-- =============================================================================
-- security_groups
-- =============================================================================
-- network_id — обязателен и immutable после Create; см. COMMENT ниже.

CREATE TABLE kacho_vpc.security_groups (
    id                  text         PRIMARY KEY,
    project_id           text         NOT NULL,
    network_id          text         NOT NULL REFERENCES kacho_vpc.networks(id) ON DELETE RESTRICT,
    created_at          timestamptz  NOT NULL DEFAULT now(),
    name                text         NOT NULL,
    description         text         NOT NULL DEFAULT '',
    labels              jsonb        NOT NULL DEFAULT '{}'::jsonb,
    status              text         NOT NULL DEFAULT 'ACTIVE',
    default_for_network boolean      NOT NULL DEFAULT false,
    rules               jsonb        NOT NULL DEFAULT '[]'::jsonb,

    CONSTRAINT security_groups_name_check
        CHECK (name ~ '^([a-zA-Z]([-_a-zA-Z0-9]{0,61}[a-zA-Z0-9])?)?$'),
    CONSTRAINT security_groups_description_check
        CHECK (length(description) <= 256),
    CONSTRAINT security_groups_status_check
        CHECK (status IN ('ACTIVE','CREATING','UPDATING','DELETING')),
    CONSTRAINT security_groups_labels_valid
        CHECK (kacho_vpc.kacho_labels_valid(labels))
);

CREATE UNIQUE INDEX security_groups_project_id_name_key
    ON kacho_vpc.security_groups (project_id, name) WHERE name <> '';
CREATE INDEX sg_project_idx     ON kacho_vpc.security_groups (project_id);
CREATE INDEX sg_network_idx    ON kacho_vpc.security_groups (network_id);
CREATE INDEX sg_created_at_idx ON kacho_vpc.security_groups (created_at);

COMMENT ON COLUMN kacho_vpc.security_groups.network_id IS 'network_id — mandatory and immutable after Create';

-- =============================================================================
-- gateways (project-level, без network)
-- =============================================================================

CREATE TABLE kacho_vpc.gateways (
    id           text         PRIMARY KEY,
    project_id    text         NOT NULL,
    created_at   timestamptz  NOT NULL DEFAULT now(),
    name         text         NOT NULL DEFAULT '',
    description  text         NOT NULL DEFAULT '',
    labels       jsonb        NOT NULL DEFAULT '{}'::jsonb,
    gateway_type text         NOT NULL DEFAULT 'shared_egress',

    CONSTRAINT gateways_name_check
        CHECK (name ~ '^([a-zA-Z]([-_a-zA-Z0-9]{0,61}[a-zA-Z0-9])?)?$'),
    CONSTRAINT gateways_description_check
        CHECK (length(description) <= 256),
    CONSTRAINT gateways_labels_valid
        CHECK (kacho_vpc.kacho_labels_valid(labels))
);

CREATE UNIQUE INDEX gateways_project_id_name_key
    ON kacho_vpc.gateways (project_id, name) WHERE name <> '';
CREATE INDEX gateways_project_idx     ON kacho_vpc.gateways (project_id);
CREATE INDEX gateways_created_at_idx ON kacho_vpc.gateways (created_at);

-- =============================================================================
-- network_interfaces (NIC — самостоятельный сетевой интерфейс, first-class ресурс)
-- =============================================================================
-- mac_address — output-only, аллоцирует service-слой; cloud-wide UNIQUE.
-- used_by — flat-колонки (used_by_type/id/name), выставляются AttachToInstance.
-- Cardinality v4/v6 ≤ 1. Только control-plane, без data-plane-проекции.

CREATE TABLE kacho_vpc.network_interfaces (
    id                 text         PRIMARY KEY,
    project_id          text         NOT NULL,
    created_at         timestamptz  NOT NULL DEFAULT now(),
    name               text         NOT NULL DEFAULT '',
    description        text         NOT NULL DEFAULT '',
    labels             jsonb        NOT NULL DEFAULT '{}'::jsonb,
    subnet_id          text         NOT NULL REFERENCES kacho_vpc.subnets(id) ON DELETE RESTRICT,
    security_group_ids jsonb        NOT NULL DEFAULT '[]'::jsonb,
    v4_address_ids     jsonb        NOT NULL DEFAULT '[]'::jsonb,
    v6_address_ids     jsonb        NOT NULL DEFAULT '[]'::jsonb,
    status             text         NOT NULL DEFAULT 'AVAILABLE',
    mac_address        text         NOT NULL,
    used_by_type       text         NOT NULL DEFAULT '',
    used_by_id         text         NOT NULL DEFAULT '',
    used_by_name       text         NOT NULL DEFAULT '',

    CONSTRAINT network_interfaces_name_check
        CHECK (name ~ '^([a-zA-Z]([-_a-zA-Z0-9]{0,61}[a-zA-Z0-9])?)?$'),
    CONSTRAINT network_interfaces_description_check
        CHECK (length(description) <= 256),
    CONSTRAINT network_interfaces_labels_valid
        CHECK (kacho_vpc.kacho_labels_valid(labels)),
    CONSTRAINT network_interfaces_status_check
        CHECK (status IS NULL OR status IN ('PROVISIONING','ACTIVE','AVAILABLE','FAILED','DELETING','STATUS_UNSPECIFIED')),
    CONSTRAINT network_interfaces_mac_address_check
        CHECK (mac_address ~ '^[0-9a-f]{2}(:[0-9a-f]{2}){5}$'),
    CONSTRAINT network_interfaces_v4_addr_max1
        CHECK (jsonb_array_length(v4_address_ids) <= 1),
    CONSTRAINT network_interfaces_v6_addr_max1
        CHECK (jsonb_array_length(v6_address_ids) <= 1)
);

CREATE UNIQUE INDEX network_interfaces_project_id_name_key
    ON kacho_vpc.network_interfaces (project_id, name) WHERE name <> '';
CREATE UNIQUE INDEX network_interfaces_mac_address_key
    ON kacho_vpc.network_interfaces (mac_address);
CREATE INDEX network_interfaces_project_idx  ON kacho_vpc.network_interfaces (project_id);
CREATE INDEX network_interfaces_subnet_idx  ON kacho_vpc.network_interfaces (subnet_id);
CREATE INDEX network_interfaces_used_by_idx
    ON kacho_vpc.network_interfaces (used_by_type, used_by_id) WHERE used_by_id <> '';

-- =============================================================================
-- address_pools (IPAM, admin-resource; внутренний API)
-- =============================================================================
-- CIDR-блоки разделены по семействам: v4_cidr_blocks + v6_cidr_blocks.

CREATE TABLE kacho_vpc.address_pools (
    id                text         PRIMARY KEY,
    name              text         NOT NULL DEFAULT '',
    description       text         NOT NULL DEFAULT '',
    labels            jsonb        NOT NULL DEFAULT '{}'::jsonb,
    v4_cidr_blocks    text[]       NOT NULL DEFAULT '{}'::text[],
    v6_cidr_blocks    text[]       NOT NULL DEFAULT '{}'::text[],
    kind              smallint     NOT NULL,
    is_default        boolean      NOT NULL DEFAULT false,
    created_at        timestamptz  NOT NULL DEFAULT now(),
    modified_at       timestamptz  NOT NULL DEFAULT now(),
    selector_labels   jsonb        NOT NULL DEFAULT '{}'::jsonb,
    selector_priority integer      NOT NULL DEFAULT 0,
    zone_id           text
);

CREATE INDEX address_pools_zone_idx ON kacho_vpc.address_pools (zone_id);
CREATE UNIQUE INDEX address_pools_zone_kind_default_uniq
    ON kacho_vpc.address_pools (COALESCE(zone_id, ''), kind) WHERE is_default = true;
CREATE INDEX address_pools_selector_labels_gin
    ON kacho_vpc.address_pools USING gin (selector_labels jsonb_path_ops)
    WHERE selector_labels <> '{}'::jsonb;

-- =============================================================================
-- address_pool bindings (per-network default + per-address override)
-- =============================================================================

CREATE TABLE kacho_vpc.address_pool_address_override (
    address_id text         NOT NULL PRIMARY KEY REFERENCES kacho_vpc.addresses(id)     ON DELETE CASCADE,
    pool_id    text         NOT NULL          REFERENCES kacho_vpc.address_pools(id)   ON DELETE RESTRICT,
    bound_at   timestamptz  NOT NULL DEFAULT now()
);

CREATE INDEX address_pool_address_override_pool_idx
    ON kacho_vpc.address_pool_address_override (pool_id);

CREATE TABLE kacho_vpc.address_pool_network_default (
    network_id text         NOT NULL PRIMARY KEY REFERENCES kacho_vpc.networks(id)       ON DELETE CASCADE,
    pool_id    text         NOT NULL          REFERENCES kacho_vpc.address_pools(id)    ON DELETE RESTRICT,
    bound_at   timestamptz  NOT NULL DEFAULT now()
);

CREATE INDEX address_pool_network_default_pool_idx
    ON kacho_vpc.address_pool_network_default (pool_id);

-- =============================================================================
-- cloud_pool_selector (admin label-selector per Cloud)
-- =============================================================================

CREATE TABLE kacho_vpc.cloud_pool_selector (
    cloud_id text         NOT NULL PRIMARY KEY,
    selector jsonb        NOT NULL DEFAULT '{}'::jsonb,
    set_at   timestamptz  NOT NULL DEFAULT now(),
    set_by   text         NOT NULL DEFAULT ''
);

CREATE INDEX cloud_pool_selector_gin
    ON kacho_vpc.cloud_pool_selector USING gin (selector jsonb_path_ops)
    WHERE selector <> '{}'::jsonb;

-- =============================================================================
-- IPAM materialized freelist (IPv4) + sparse counter (IPv6)
-- =============================================================================

CREATE TABLE kacho_vpc.address_pool_free_ips (
    pool_id text NOT NULL REFERENCES kacho_vpc.address_pools(id) ON DELETE CASCADE,
    ip      inet NOT NULL,
    PRIMARY KEY (pool_id, ip)
);

CREATE INDEX address_pool_free_ips_pool_idx
    ON kacho_vpc.address_pool_free_ips (pool_id);

CREATE TABLE kacho_vpc.ipv6_pool_cursors (
    pool_id     text          NOT NULL PRIMARY KEY REFERENCES kacho_vpc.address_pools(id) ON DELETE CASCADE,
    next_offset numeric(39,0) NOT NULL DEFAULT 1
);

CREATE TABLE kacho_vpc.ipv6_allocated_ips (
    pool_id    text          NOT NULL REFERENCES kacho_vpc.address_pools(id) ON DELETE CASCADE,
    ip         inet          NOT NULL,
    "offset"   numeric(39,0) NOT NULL,
    address_id text          NOT NULL,
    created_at timestamptz   NOT NULL DEFAULT now(),
    PRIMARY KEY (pool_id, ip),
    UNIQUE (pool_id, "offset")
);

CREATE INDEX ipv6_allocated_ips_pool_idx ON kacho_vpc.ipv6_allocated_ips (pool_id);

CREATE TABLE kacho_vpc.ipv6_released_offsets (
    pool_id  text          NOT NULL REFERENCES kacho_vpc.address_pools(id) ON DELETE CASCADE,
    "offset" numeric(39,0) NOT NULL,
    PRIMARY KEY (pool_id, "offset")
);

CREATE INDEX ipv6_released_offsets_pool_idx ON kacho_vpc.ipv6_released_offsets (pool_id);

-- =============================================================================
-- vpc_outbox + sequence + LISTEN/NOTIFY trigger
-- =============================================================================

CREATE SEQUENCE kacho_vpc.vpc_outbox_sequence_no_seq
    START WITH 1 INCREMENT BY 1 NO MINVALUE NO MAXVALUE CACHE 1;

CREATE TABLE kacho_vpc.vpc_outbox (
    sequence_no   bigint       NOT NULL DEFAULT nextval('kacho_vpc.vpc_outbox_sequence_no_seq'::regclass) PRIMARY KEY,
    resource_kind text         NOT NULL,
    resource_id   text         NOT NULL,
    event_type    text         NOT NULL,
    payload       jsonb        NOT NULL DEFAULT '{}'::jsonb,
    created_at    timestamptz  NOT NULL DEFAULT now(),
    processed_at  timestamptz
);

ALTER SEQUENCE kacho_vpc.vpc_outbox_sequence_no_seq OWNED BY kacho_vpc.vpc_outbox.sequence_no;

CREATE INDEX vpc_outbox_seq_idx  ON kacho_vpc.vpc_outbox (sequence_no);
CREATE INDEX vpc_outbox_kind_idx ON kacho_vpc.vpc_outbox (resource_kind, sequence_no);

CREATE TABLE kacho_vpc.vpc_watch_cursors (
    subscriber_id    text         NOT NULL PRIMARY KEY,
    last_sequence_no bigint       NOT NULL DEFAULT 0,
    updated_at       timestamptz  NOT NULL DEFAULT now()
);

-- =============================================================================
-- Triggers (auto-association + outbox NOTIFY)
-- =============================================================================

CREATE TRIGGER vpc_outbox_notify_trg
    AFTER INSERT ON kacho_vpc.vpc_outbox
    FOR EACH ROW EXECUTE FUNCTION kacho_vpc.vpc_outbox_notify();

CREATE TRIGGER rt_auto_assoc_subnets_trg
    AFTER INSERT ON kacho_vpc.route_tables
    FOR EACH ROW EXECUTE FUNCTION kacho_vpc.rt_auto_assoc_subnets();

CREATE TRIGGER subnet_auto_pick_rt_trg
    BEFORE INSERT ON kacho_vpc.subnets
    FOR EACH ROW EXECUTE FUNCTION kacho_vpc.subnet_auto_pick_rt();

CREATE TRIGGER subnets_outbox_emit_route_table_change_trg
    AFTER UPDATE OF route_table_id ON kacho_vpc.subnets
    FOR EACH ROW
    WHEN (OLD.route_table_id IS DISTINCT FROM NEW.route_table_id)
    EXECUTE FUNCTION kacho_vpc.subnets_outbox_emit_route_table_change();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- Greenfield baseline: down — drop schema целиком (с CASCADE на все объекты).
-- btree_gist extension остается в public — общий объект, не наш.
DROP SCHEMA IF EXISTS kacho_vpc CASCADE;
SET search_path TO public;

-- +goose StatementEnd
