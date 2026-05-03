-- +goose Up

CREATE TABLE networks (
    id                       UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    folder_id                UUID        NOT NULL,
    name                     TEXT        NOT NULL,
    description              TEXT        NOT NULL DEFAULT '',
    labels                   JSONB       NOT NULL DEFAULT '{}',
    created_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    status                   TEXT        NOT NULL DEFAULT 'NETWORK_STATUS_PROVISIONING',
    generation               BIGINT      NOT NULL DEFAULT 1,
    resource_version         TEXT        NOT NULL DEFAULT gen_random_uuid()::text,
    observed_generation      BIGINT      NOT NULL DEFAULT 0,
    status_last_transition_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at               TIMESTAMPTZ,
    UNIQUE (folder_id, name)
);

CREATE INDEX networks_labels_gin ON networks USING GIN (labels jsonb_path_ops);
CREATE INDEX networks_folder_idx ON networks (folder_id);

-- +goose Down

DROP TABLE IF EXISTS networks;
