-- +goose Up

CREATE TABLE route_tables (
  id            UUID        PRIMARY KEY,
  folder_id     TEXT        NOT NULL,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  name          TEXT        NOT NULL,
  description   TEXT        NOT NULL DEFAULT '',
  labels        JSONB       NOT NULL DEFAULT '{}',
  network_id    UUID        NOT NULL REFERENCES networks(id),
  static_routes JSONB       NOT NULL DEFAULT '[]'
);

CREATE INDEX route_tables_folder_idx ON route_tables (folder_id);
CREATE INDEX route_tables_network_idx ON route_tables (network_id);
CREATE INDEX route_tables_created_at_idx ON route_tables (created_at);

-- +goose Down

DROP TABLE IF EXISTS route_tables;
