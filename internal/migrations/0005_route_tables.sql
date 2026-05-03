-- +goose Up

CREATE TABLE route_tables (
    id               UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    network_id       UUID        NOT NULL REFERENCES networks(id) ON DELETE RESTRICT,
    folder_id        UUID        NOT NULL,
    name             TEXT        NOT NULL,
    description      TEXT        NOT NULL DEFAULT '',
    labels           JSONB       NOT NULL DEFAULT '{}',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    status           TEXT        NOT NULL DEFAULT 'ROUTE_TABLE_STATUS_PROVISIONING',
    generation       BIGINT      NOT NULL DEFAULT 1,
    resource_version TEXT        NOT NULL DEFAULT gen_random_uuid()::text,
    static_routes    JSONB       NOT NULL DEFAULT '[]',
    deleted_at       TIMESTAMPTZ,
    UNIQUE (folder_id, name)
);

CREATE INDEX route_tables_labels_gin ON route_tables USING GIN (labels jsonb_path_ops);
CREATE INDEX route_tables_network_idx ON route_tables (network_id);
CREATE INDEX route_tables_folder_idx ON route_tables (folder_id);

-- +goose Down

DROP TABLE IF EXISTS route_tables;
