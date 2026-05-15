-- +goose Up
-- KAC-79/KAC-36 (post-kube-ovn): убираем data-plane проекции, которыми kacho-vpc
-- больше не управляет. Раньше vpn_id был 24-bit data-plane id сети (KAC-2), и
-- network_interfaces несли свою data-plane проекцию (hv_id/sid/.../dataplane_revision),
-- которую заполнял kacho-vpc-implement через ReportNiDataplane. Теперь underlay
-- управляется kube-ovn, эти поля больше не используются ни кодом, ни клиентами.

-- networks.vpn_id + free-list.
ALTER TABLE networks DROP CONSTRAINT IF EXISTS networks_vpn_id_key;
ALTER TABLE networks DROP COLUMN IF EXISTS vpn_id;
DROP TABLE IF EXISTS vpn_id_free;
DROP SEQUENCE IF EXISTS vpn_id_seq;

-- network_interfaces.data-plane колонки + индекс по hv_id.
DROP INDEX IF EXISTS network_interfaces_hv_idx;
ALTER TABLE network_interfaces DROP COLUMN IF EXISTS hv_id;
ALTER TABLE network_interfaces DROP COLUMN IF EXISTS sid;
ALTER TABLE network_interfaces DROP COLUMN IF EXISTS sid_seq;
ALTER TABLE network_interfaces DROP COLUMN IF EXISTS host_iface;
ALTER TABLE network_interfaces DROP COLUMN IF EXISTS netns;
ALTER TABLE network_interfaces DROP COLUMN IF EXISTS gateway_ip;
ALTER TABLE network_interfaces DROP COLUMN IF EXISTS container_id;
ALTER TABLE network_interfaces DROP COLUMN IF EXISTS status_error;
ALTER TABLE network_interfaces DROP COLUMN IF EXISTS dataplane_revision;
ALTER TABLE network_interfaces DROP COLUMN IF EXISTS dataplane_updated_at;

-- +goose Down
-- Best-effort откат: re-add колонки с пустыми значениями. Free-list / hv_id index не
-- восстанавливаются точечно — для downgrade достаточно, чтобы схема снова приняла
-- старый код.

CREATE SEQUENCE IF NOT EXISTS vpn_id_seq START 1 MINVALUE 1 MAXVALUE 16777215;
ALTER TABLE networks ADD COLUMN IF NOT EXISTS vpn_id integer;
UPDATE networks SET vpn_id = nextval('vpn_id_seq') WHERE vpn_id IS NULL;
ALTER TABLE networks ALTER COLUMN vpn_id SET NOT NULL;
ALTER TABLE networks ADD CONSTRAINT networks_vpn_id_key UNIQUE (vpn_id);
CREATE TABLE IF NOT EXISTS vpn_id_free (id integer PRIMARY KEY);

ALTER TABLE network_interfaces ADD COLUMN IF NOT EXISTS hv_id                TEXT      NOT NULL DEFAULT '';
ALTER TABLE network_interfaces ADD COLUMN IF NOT EXISTS sid                  TEXT      NOT NULL DEFAULT '';
ALTER TABLE network_interfaces ADD COLUMN IF NOT EXISTS sid_seq              INTEGER   NOT NULL DEFAULT 0;
ALTER TABLE network_interfaces ADD COLUMN IF NOT EXISTS host_iface           TEXT      NOT NULL DEFAULT '';
ALTER TABLE network_interfaces ADD COLUMN IF NOT EXISTS netns                TEXT      NOT NULL DEFAULT '';
ALTER TABLE network_interfaces ADD COLUMN IF NOT EXISTS gateway_ip           TEXT      NOT NULL DEFAULT '';
ALTER TABLE network_interfaces ADD COLUMN IF NOT EXISTS container_id         TEXT      NOT NULL DEFAULT '';
ALTER TABLE network_interfaces ADD COLUMN IF NOT EXISTS status_error         TEXT      NOT NULL DEFAULT '';
ALTER TABLE network_interfaces ADD COLUMN IF NOT EXISTS dataplane_revision   BIGINT    NOT NULL DEFAULT 0;
ALTER TABLE network_interfaces ADD COLUMN IF NOT EXISTS dataplane_updated_at TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS network_interfaces_hv_idx ON network_interfaces (hv_id) WHERE hv_id <> '';
