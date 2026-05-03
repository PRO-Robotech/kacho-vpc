-- +goose Up

CREATE TABLE networks (
  id                        UUID        PRIMARY KEY,
  folder_id                 TEXT        NOT NULL,
  created_at                TIMESTAMPTZ NOT NULL DEFAULT now(),
  name                      TEXT        NOT NULL,
  description               TEXT        NOT NULL DEFAULT '',
  labels                    JSONB       NOT NULL DEFAULT '{}',
  default_security_group_id TEXT        NOT NULL DEFAULT '',
  deleted_at                TIMESTAMPTZ
);

CREATE INDEX networks_folder_idx ON networks (folder_id) WHERE deleted_at IS NULL;
CREATE INDEX networks_created_at_idx ON networks (created_at);

-- +goose Down

DROP TABLE IF EXISTS networks;
