-- +goose Up

CREATE TABLE subnets (
    id                       UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    network_id               UUID        NOT NULL REFERENCES networks(id) ON DELETE RESTRICT,
    folder_id                UUID        NOT NULL,
    zone_id                  TEXT        NOT NULL DEFAULT '',
    cidr_block               TEXT        NOT NULL,
    name                     TEXT        NOT NULL,
    description              TEXT        NOT NULL DEFAULT '',
    labels                   JSONB       NOT NULL DEFAULT '{}',
    created_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    status                   TEXT        NOT NULL DEFAULT 'SUBNET_STATUS_PROVISIONING',
    generation               BIGINT      NOT NULL DEFAULT 1,
    resource_version         TEXT        NOT NULL DEFAULT gen_random_uuid()::text,
    observed_generation      BIGINT      NOT NULL DEFAULT 0,
    status_last_transition_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at               TIMESTAMPTZ,
    UNIQUE (folder_id, name)
);

CREATE INDEX subnets_labels_gin ON subnets USING GIN (labels jsonb_path_ops);
CREATE INDEX subnets_network_idx ON subnets (network_id);
CREATE INDEX subnets_folder_idx ON subnets (folder_id);

-- +goose Down

DROP TABLE IF EXISTS subnets;
