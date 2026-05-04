-- +goose Up

CREATE TABLE security_groups (
  id                  UUID        PRIMARY KEY,
  folder_id           TEXT        NOT NULL,
  network_id          UUID        NOT NULL REFERENCES networks(id) ON DELETE RESTRICT,
  created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
  name                TEXT        NOT NULL,
  description         TEXT        NOT NULL DEFAULT '',
  labels              JSONB       NOT NULL DEFAULT '{}',
  status              TEXT        NOT NULL DEFAULT 'ACTIVE',
  default_for_network BOOLEAN     NOT NULL DEFAULT false,
  rules               JSONB       NOT NULL DEFAULT '[]'
);

CREATE INDEX sg_folder_idx ON security_groups (folder_id);
CREATE INDEX sg_network_idx ON security_groups (network_id);
CREATE INDEX sg_created_at_idx ON security_groups (created_at);

-- +goose Down

DROP TABLE IF EXISTS security_groups;
