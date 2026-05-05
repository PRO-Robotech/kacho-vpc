-- +goose Up
--
-- PrivateEndpoints: PrivateLink endpoints для подключения к internal services.
-- См. https://yandex.cloud/ru/docs/vpc/api-ref/PrivateEndpoint/ — verbatim YC.

CREATE TABLE private_endpoints (
  id              TEXT PRIMARY KEY,
  folder_id       TEXT NOT NULL,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  name            TEXT NOT NULL DEFAULT '',
  description     TEXT NOT NULL DEFAULT '',
  labels          JSONB NOT NULL DEFAULT '{}',
  network_id      TEXT,
  subnet_id       TEXT,
  -- Address: либо ID существующего Address-ресурса, либо назначаемый IP в подсети.
  address_id      TEXT,
  ip_address      TEXT,
  -- Тип сервиса (только object_storage в текущем YC): 'object_storage'.
  service_type    TEXT,
  -- DNS-опции: enabled / disabled — храним JSON для расширяемости.
  dns_options     JSONB NOT NULL DEFAULT '{}',
  status          TEXT NOT NULL DEFAULT 'PENDING'
);

CREATE INDEX private_endpoints_folder_idx ON private_endpoints (folder_id);
CREATE INDEX private_endpoints_network_idx ON private_endpoints (network_id);
CREATE INDEX private_endpoints_created_at_idx ON private_endpoints (created_at);

-- +goose Down

DROP TABLE IF EXISTS private_endpoints;
