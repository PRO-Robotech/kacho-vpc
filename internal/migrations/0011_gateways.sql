-- +goose Up
--
-- Gateways: NAT Gateway (shared egress) ресурсы.
-- См. https://yandex.cloud/ru/docs/vpc/api-ref/Gateway/ — verbatim YC contract.

CREATE TABLE gateways (
  id            TEXT PRIMARY KEY,
  folder_id     TEXT NOT NULL,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  name          TEXT NOT NULL DEFAULT '',
  description   TEXT NOT NULL DEFAULT '',
  labels        JSONB NOT NULL DEFAULT '{}',
  -- gateway_type: 'shared_egress' (единственный поддерживаемый тип в YC).
  -- Другие типы (private/dedicated) не поддерживаются.
  gateway_type  TEXT NOT NULL DEFAULT 'shared_egress'
);

CREATE INDEX gateways_folder_idx ON gateways (folder_id);
CREATE INDEX gateways_created_at_idx ON gateways (created_at);

-- +goose Down

DROP TABLE IF EXISTS gateways;
