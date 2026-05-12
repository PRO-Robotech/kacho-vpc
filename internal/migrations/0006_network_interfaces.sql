-- +goose Up
-- NetworkInterface (NIC) — first-class сетевой интерфейс (AWS-ENI-style; epic KAC-2).
-- Публичные колонки + internal data-plane-проекция (hv_id/sid/sid_seq/host_iface/netns/
-- gateway_ip/container_id/status_error/dataplane_revision — заполняется kacho-vpc-implement
-- через InternalNetworkInterfaceService.ReportNiDataplane; ИНФРА-ЧУВСТВИТЕЛЬНОЕ, не на
-- публичной поверхности — см. workspace CLAUDE.md §«Инфра-чувствительные данные»).
-- instance_id — loose-ref на kacho-compute Instance (без FK — это ресурс другого сервиса).
CREATE TABLE network_interfaces (
  id                   TEXT        PRIMARY KEY,
  folder_id            TEXT        NOT NULL,
  created_at           timestamptz NOT NULL DEFAULT now(),
  name                 TEXT        NOT NULL DEFAULT '',
  description          TEXT        NOT NULL DEFAULT '',
  labels               jsonb       NOT NULL DEFAULT '{}'::jsonb,
  subnet_id            TEXT        NOT NULL REFERENCES subnets(id) ON DELETE RESTRICT,
  network_id           TEXT        NOT NULL DEFAULT '',
  primary_v4_address   TEXT        NOT NULL,
  security_group_ids   jsonb       NOT NULL DEFAULT '[]'::jsonb,
  instance_id          TEXT        NOT NULL DEFAULT '',
  ni_index             TEXT        NOT NULL DEFAULT '',
  status               TEXT        NOT NULL DEFAULT 'AVAILABLE',  -- PROVISIONING|ACTIVE|AVAILABLE|FAILED|DELETING
  hv_id                TEXT        NOT NULL DEFAULT '',
  sid                  TEXT        NOT NULL DEFAULT '',
  sid_seq              integer     NOT NULL DEFAULT 0,
  host_iface           TEXT        NOT NULL DEFAULT '',
  netns                TEXT        NOT NULL DEFAULT '',
  gateway_ip           TEXT        NOT NULL DEFAULT '',
  container_id         TEXT        NOT NULL DEFAULT '',
  status_error         TEXT        NOT NULL DEFAULT '',
  dataplane_revision   bigint      NOT NULL DEFAULT 0,
  dataplane_updated_at timestamptz
);
CREATE UNIQUE INDEX network_interfaces_folder_id_name_key ON network_interfaces (folder_id, name) WHERE name <> '';
CREATE UNIQUE INDEX network_interfaces_subnet_addr_key   ON network_interfaces (subnet_id, primary_v4_address);
CREATE INDEX network_interfaces_folder_idx   ON network_interfaces (folder_id);
CREATE INDEX network_interfaces_subnet_idx   ON network_interfaces (subnet_id);
CREATE INDEX network_interfaces_network_idx  ON network_interfaces (network_id);
CREATE INDEX network_interfaces_instance_idx ON network_interfaces (instance_id) WHERE instance_id <> '';
CREATE INDEX network_interfaces_hv_idx       ON network_interfaces (hv_id) WHERE hv_id <> '';

-- +goose Down
DROP TABLE network_interfaces;
