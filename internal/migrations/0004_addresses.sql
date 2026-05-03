-- +goose Up

CREATE TABLE addresses (
  id                  UUID        PRIMARY KEY,
  folder_id           TEXT        NOT NULL,
  created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
  name                TEXT        NOT NULL DEFAULT '',
  description         TEXT        NOT NULL DEFAULT '',
  labels              JSONB       NOT NULL DEFAULT '{}',
  addr_type           SMALLINT    NOT NULL DEFAULT 0,
  ip_version          SMALLINT    NOT NULL DEFAULT 0,
  reserved            BOOLEAN     NOT NULL DEFAULT false,
  used                BOOLEAN     NOT NULL DEFAULT false,
  deletion_protection BOOLEAN     NOT NULL DEFAULT false,
  external_ipv4       JSONB,        -- { "address": "203.0.113.X", "zone_id": "..." }
  internal_ipv4       JSONB,        -- { "address": "10.x.x.x", "subnet_id": "..." }
  deleted_at          TIMESTAMPTZ
);

CREATE INDEX addresses_folder_idx ON addresses (folder_id) WHERE deleted_at IS NULL;
CREATE INDEX addresses_created_at_idx ON addresses (created_at);
-- Уникальность allocated IP (только среди живых записей):
CREATE UNIQUE INDEX addresses_external_ip_uniq ON addresses ((external_ipv4->>'address'))
  WHERE deleted_at IS NULL AND external_ipv4 IS NOT NULL;

-- +goose Down

DROP TABLE IF EXISTS addresses;
