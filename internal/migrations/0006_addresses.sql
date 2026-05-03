-- +goose Up

CREATE TABLE addresses (
    id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    folder_id      UUID        NOT NULL,
    name           TEXT        NOT NULL,
    description    TEXT        NOT NULL DEFAULT '',
    labels         JSONB       NOT NULL DEFAULT '{}',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    address_type   TEXT        NOT NULL DEFAULT 'ADDRESS_TYPE_EXTERNAL',
    zone_id        TEXT        NOT NULL DEFAULT '',
    allocated_ipv4 TEXT        UNIQUE,
    status         TEXT        NOT NULL DEFAULT 'ADDRESS_STATUS_RESERVED',
    deleted_at     TIMESTAMPTZ,
    UNIQUE (folder_id, name)
);

CREATE INDEX addresses_labels_gin ON addresses USING GIN (labels jsonb_path_ops);
CREATE INDEX addresses_folder_idx ON addresses (folder_id);

-- +goose Down

DROP TABLE IF EXISTS addresses;
