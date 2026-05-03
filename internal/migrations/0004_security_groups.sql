-- +goose Up

CREATE TABLE security_groups (
    id               UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    network_id       UUID        NOT NULL REFERENCES networks(id) ON DELETE RESTRICT,
    folder_id        UUID        NOT NULL,
    name             TEXT        NOT NULL,
    description      TEXT        NOT NULL DEFAULT '',
    labels           JSONB       NOT NULL DEFAULT '{}',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    status           TEXT        NOT NULL DEFAULT 'SECURITY_GROUP_STATUS_PROVISIONING',
    generation       BIGINT      NOT NULL DEFAULT 1,
    resource_version TEXT        NOT NULL DEFAULT gen_random_uuid()::text,
    rules            JSONB       NOT NULL DEFAULT '[]',
    deleted_at       TIMESTAMPTZ,
    UNIQUE (folder_id, name)
);

CREATE INDEX security_groups_labels_gin ON security_groups USING GIN (labels jsonb_path_ops);
CREATE INDEX security_groups_network_idx ON security_groups (network_id);
CREATE INDEX security_groups_folder_idx ON security_groups (folder_id);

-- +goose Down

DROP TABLE IF EXISTS security_groups;
