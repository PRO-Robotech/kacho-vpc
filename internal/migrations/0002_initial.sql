-- +goose Up

CREATE TABLE networks (
  uid                UUID        PRIMARY KEY,
  folder_id          UUID        NOT NULL,
  cloud_id           UUID        NOT NULL,
  organization_id    UUID        NOT NULL,
  name               TEXT        NOT NULL,
  labels             JSONB       NOT NULL DEFAULT '{}'::jsonb,
  annotations        JSONB       NOT NULL DEFAULT '{}'::jsonb,
  creation_timestamp TIMESTAMPTZ NOT NULL DEFAULT now(),
  resource_version   BIGINT      NOT NULL DEFAULT nextval('resource_version_seq'),
  generation         BIGINT      NOT NULL DEFAULT 1,
  deletion_timestamp TIMESTAMPTZ,
  finalizers         TEXT[]      NOT NULL DEFAULT '{}',
  spec               JSONB       NOT NULL DEFAULT '{}'::jsonb,
  status             JSONB       NOT NULL DEFAULT '{"state":"ACTIVE"}'::jsonb,
  UNIQUE (folder_id, name)
);
CREATE INDEX networks_labels_gin ON networks USING GIN (labels jsonb_path_ops);
CREATE INDEX networks_folder_idx ON networks (folder_id);
-- +goose StatementBegin
CREATE TRIGGER networks_bump_rv BEFORE UPDATE ON networks FOR EACH ROW EXECUTE FUNCTION bump_resource_version();
-- +goose StatementEnd

CREATE TABLE subnets (
  uid                UUID        PRIMARY KEY,
  network_id         UUID        NOT NULL REFERENCES networks(uid) ON DELETE RESTRICT,
  folder_id          UUID        NOT NULL,
  cloud_id           UUID        NOT NULL,
  organization_id    UUID        NOT NULL,
  name               TEXT        NOT NULL,
  labels             JSONB       NOT NULL DEFAULT '{}'::jsonb,
  annotations        JSONB       NOT NULL DEFAULT '{}'::jsonb,
  creation_timestamp TIMESTAMPTZ NOT NULL DEFAULT now(),
  resource_version   BIGINT      NOT NULL DEFAULT nextval('resource_version_seq'),
  generation         BIGINT      NOT NULL DEFAULT 1,
  deletion_timestamp TIMESTAMPTZ,
  finalizers         TEXT[]      NOT NULL DEFAULT '{}',
  spec               JSONB       NOT NULL DEFAULT '{}'::jsonb,
  status             JSONB       NOT NULL DEFAULT '{"state":"ACTIVE"}'::jsonb,
  UNIQUE (folder_id, name)
);
CREATE INDEX subnets_labels_gin ON subnets USING GIN (labels jsonb_path_ops);
CREATE INDEX subnets_network_idx ON subnets (network_id);
CREATE INDEX subnets_folder_idx ON subnets (folder_id);
-- +goose StatementBegin
CREATE TRIGGER subnets_bump_rv BEFORE UPDATE ON subnets FOR EACH ROW EXECUTE FUNCTION bump_resource_version();
-- +goose StatementEnd

CREATE TABLE security_groups (
  uid                UUID        PRIMARY KEY,
  network_id         UUID        NOT NULL REFERENCES networks(uid) ON DELETE RESTRICT,
  folder_id          UUID        NOT NULL,
  cloud_id           UUID        NOT NULL,
  organization_id    UUID        NOT NULL,
  name               TEXT        NOT NULL,
  labels             JSONB       NOT NULL DEFAULT '{}'::jsonb,
  annotations        JSONB       NOT NULL DEFAULT '{}'::jsonb,
  creation_timestamp TIMESTAMPTZ NOT NULL DEFAULT now(),
  resource_version   BIGINT      NOT NULL DEFAULT nextval('resource_version_seq'),
  generation         BIGINT      NOT NULL DEFAULT 1,
  deletion_timestamp TIMESTAMPTZ,
  finalizers         TEXT[]      NOT NULL DEFAULT '{}',
  spec               JSONB       NOT NULL DEFAULT '{}'::jsonb,
  status             JSONB       NOT NULL DEFAULT '{"state":"ACTIVE"}'::jsonb,
  UNIQUE (folder_id, name)
);
CREATE INDEX security_groups_labels_gin ON security_groups USING GIN (labels jsonb_path_ops);
CREATE INDEX security_groups_network_idx ON security_groups (network_id);
CREATE INDEX security_groups_folder_idx ON security_groups (folder_id);
-- +goose StatementBegin
CREATE TRIGGER security_groups_bump_rv BEFORE UPDATE ON security_groups FOR EACH ROW EXECUTE FUNCTION bump_resource_version();
-- +goose StatementEnd

CREATE TABLE security_group_rules (
  id                UUID    PRIMARY KEY,
  security_group_id UUID    NOT NULL REFERENCES security_groups(uid) ON DELETE CASCADE,
  direction         TEXT    NOT NULL CHECK (direction IN ('INGRESS', 'EGRESS')),
  protocol          TEXT    NOT NULL,
  port_range_min    INT,
  port_range_max    INT,
  cidr_blocks       TEXT[]  NOT NULL DEFAULT '{}',
  description       TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX sgr_sg_idx ON security_group_rules (security_group_id);

CREATE TABLE route_tables (
  uid                UUID        PRIMARY KEY,
  network_id         UUID        NOT NULL REFERENCES networks(uid) ON DELETE RESTRICT,
  folder_id          UUID        NOT NULL,
  cloud_id           UUID        NOT NULL,
  organization_id    UUID        NOT NULL,
  name               TEXT        NOT NULL,
  labels             JSONB       NOT NULL DEFAULT '{}'::jsonb,
  annotations        JSONB       NOT NULL DEFAULT '{}'::jsonb,
  creation_timestamp TIMESTAMPTZ NOT NULL DEFAULT now(),
  resource_version   BIGINT      NOT NULL DEFAULT nextval('resource_version_seq'),
  generation         BIGINT      NOT NULL DEFAULT 1,
  deletion_timestamp TIMESTAMPTZ,
  finalizers         TEXT[]      NOT NULL DEFAULT '{}',
  spec               JSONB       NOT NULL DEFAULT '{}'::jsonb,
  status             JSONB       NOT NULL DEFAULT '{"state":"ACTIVE"}'::jsonb,
  UNIQUE (folder_id, name)
);
CREATE INDEX route_tables_labels_gin ON route_tables USING GIN (labels jsonb_path_ops);
CREATE INDEX route_tables_network_idx ON route_tables (network_id);
CREATE INDEX route_tables_folder_idx ON route_tables (folder_id);
-- +goose StatementBegin
CREATE TRIGGER route_tables_bump_rv BEFORE UPDATE ON route_tables FOR EACH ROW EXECUTE FUNCTION bump_resource_version();
-- +goose StatementEnd

CREATE TABLE static_routes (
  id                  UUID    PRIMARY KEY,
  route_table_id      UUID    NOT NULL REFERENCES route_tables(uid) ON DELETE CASCADE,
  destination_prefix  TEXT    NOT NULL,
  next_hop_address    TEXT    NOT NULL,
  description         TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX static_routes_rt_idx ON static_routes (route_table_id);

CREATE TABLE addresses (
  uid                UUID        PRIMARY KEY,
  folder_id          UUID        NOT NULL,
  cloud_id           UUID        NOT NULL,
  organization_id    UUID        NOT NULL,
  name               TEXT        NOT NULL,
  labels             JSONB       NOT NULL DEFAULT '{}'::jsonb,
  annotations        JSONB       NOT NULL DEFAULT '{}'::jsonb,
  creation_timestamp TIMESTAMPTZ NOT NULL DEFAULT now(),
  resource_version   BIGINT      NOT NULL DEFAULT nextval('resource_version_seq'),
  generation         BIGINT      NOT NULL DEFAULT 1,
  deletion_timestamp TIMESTAMPTZ,
  finalizers         TEXT[]      NOT NULL DEFAULT '{}',
  spec               JSONB       NOT NULL DEFAULT '{}'::jsonb,
  status             JSONB       NOT NULL DEFAULT '{"state":"RESERVED"}'::jsonb,
  allocated_ipv4     TEXT        UNIQUE,
  UNIQUE (folder_id, name)
);
CREATE INDEX addresses_labels_gin ON addresses USING GIN (labels jsonb_path_ops);
CREATE INDEX addresses_folder_idx ON addresses (folder_id);
-- +goose StatementBegin
CREATE TRIGGER addresses_bump_rv BEFORE UPDATE ON addresses FOR EACH ROW EXECUTE FUNCTION bump_resource_version();
-- +goose StatementEnd

-- +goose Down
DROP TABLE IF EXISTS addresses;
DROP TABLE IF EXISTS static_routes;
DROP TABLE IF EXISTS route_tables;
DROP TABLE IF EXISTS security_group_rules;
DROP TABLE IF EXISTS security_groups;
DROP TABLE IF EXISTS subnets;
DROP TABLE IF EXISTS networks;
