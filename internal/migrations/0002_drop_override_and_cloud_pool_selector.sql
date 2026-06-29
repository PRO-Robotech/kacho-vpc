-- Copyright (c) PRO-Robotech
-- SPDX-License-Identifier: BUSL-1.1

-- +goose Up
-- +goose StatementBegin

-- =============================================================================
-- Удаление неиспользуемых IPAM cascade-таблиц.
-- =============================================================================
-- BindAsAddressOverride / UnbindAddressOverride RPC и весь InternalCloudService
-- (CloudPoolSelector Set/Get/Unset) убраны из proto и impl. Cascade
-- ResolvePoolForAddress сведен к network_default → zone_default → global_default,
-- поэтому таблицы `address_pool_address_override` и `cloud_pool_selector`
-- больше не читаются и не пишутся — дропаем.
--
-- `address_pool_network_default` ОСТАЕТСЯ (BindAsNetworkDefault жив).

SET search_path TO kacho_vpc, public;

DROP TABLE IF EXISTS kacho_vpc.address_pool_address_override;
DROP TABLE IF EXISTS kacho_vpc.cloud_pool_selector;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

SET search_path TO kacho_vpc, public;

CREATE TABLE kacho_vpc.address_pool_address_override (
    address_id text         NOT NULL PRIMARY KEY REFERENCES kacho_vpc.addresses(id)     ON DELETE CASCADE,
    pool_id    text         NOT NULL          REFERENCES kacho_vpc.address_pools(id)   ON DELETE RESTRICT,
    bound_at   timestamptz  NOT NULL DEFAULT now()
);

CREATE INDEX address_pool_address_override_pool_idx
    ON kacho_vpc.address_pool_address_override (pool_id);

CREATE TABLE kacho_vpc.cloud_pool_selector (
    cloud_id text         NOT NULL PRIMARY KEY,
    selector jsonb        NOT NULL DEFAULT '{}'::jsonb,
    set_at   timestamptz  NOT NULL DEFAULT now(),
    set_by   text         NOT NULL DEFAULT ''
);

CREATE INDEX cloud_pool_selector_gin
    ON kacho_vpc.cloud_pool_selector USING gin (selector jsonb_path_ops)
    WHERE selector <> '{}'::jsonb;

-- +goose StatementEnd
