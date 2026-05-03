-- +goose Up

CREATE TABLE subnets (
  id             UUID        PRIMARY KEY,
  folder_id      TEXT        NOT NULL,
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  name           TEXT        NOT NULL,
  description    TEXT        NOT NULL DEFAULT '',
  labels         JSONB       NOT NULL DEFAULT '{}',
  network_id     UUID        NOT NULL REFERENCES networks(id),
  zone_id        TEXT        NOT NULL DEFAULT '',
  v4_cidr_blocks TEXT[]      NOT NULL DEFAULT '{}',
  v6_cidr_blocks TEXT[]      NOT NULL DEFAULT '{}',
  route_table_id TEXT,
  dhcp_options   JSONB
);

CREATE INDEX subnets_folder_idx ON subnets (folder_id);
CREATE INDEX subnets_network_idx ON subnets (network_id);
CREATE INDEX subnets_created_at_idx ON subnets (created_at);

-- +goose Down

DROP TABLE IF EXISTS subnets;
