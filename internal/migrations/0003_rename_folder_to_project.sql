-- +goose Up
-- +goose StatementBegin
-- =============================================================================
-- KAC-106 (E1) — hard rename folder_id -> project_id on all 8 VPC resources.
-- Strategy: ALTER TABLE RENAME COLUMN + ALTER INDEX RENAME (metadata-only,
-- instant, no data-rewrite). Acceptance D-1 (Option A) + D-2 simplification
-- (RENAME equivalent semantically when types match and no concurrent
-- migration needed). Includes recreation of trigger function
-- subnets_outbox_emit_route_table_change which referenced NEW.folder_id.
-- =============================================================================

-- 1) Rename columns
ALTER TABLE kacho_vpc.networks            RENAME COLUMN folder_id TO project_id;
ALTER TABLE kacho_vpc.subnets             RENAME COLUMN folder_id TO project_id;
ALTER TABLE kacho_vpc.addresses           RENAME COLUMN folder_id TO project_id;
ALTER TABLE kacho_vpc.route_tables        RENAME COLUMN folder_id TO project_id;
ALTER TABLE kacho_vpc.security_groups     RENAME COLUMN folder_id TO project_id;
ALTER TABLE kacho_vpc.gateways            RENAME COLUMN folder_id TO project_id;
ALTER TABLE kacho_vpc.private_endpoints   RENAME COLUMN folder_id TO project_id;
ALTER TABLE kacho_vpc.network_interfaces  RENAME COLUMN folder_id TO project_id;

-- 2) Rename indexes (UNIQUE + secondary)
ALTER INDEX kacho_vpc.networks_folder_id_name_key            RENAME TO networks_project_id_name_key;
ALTER INDEX kacho_vpc.networks_folder_idx                    RENAME TO networks_project_idx;

ALTER INDEX kacho_vpc.subnets_folder_id_name_key             RENAME TO subnets_project_id_name_key;
ALTER INDEX kacho_vpc.subnets_folder_idx                     RENAME TO subnets_project_idx;

ALTER INDEX kacho_vpc.addresses_folder_id_name_key           RENAME TO addresses_project_id_name_key;
ALTER INDEX kacho_vpc.addresses_folder_idx                   RENAME TO addresses_project_idx;

ALTER INDEX kacho_vpc.route_tables_folder_id_name_key        RENAME TO route_tables_project_id_name_key;
ALTER INDEX kacho_vpc.route_tables_folder_idx                RENAME TO route_tables_project_idx;

ALTER INDEX kacho_vpc.security_groups_folder_id_name_key     RENAME TO security_groups_project_id_name_key;
ALTER INDEX kacho_vpc.sg_folder_idx                          RENAME TO sg_project_idx;

ALTER INDEX kacho_vpc.gateways_folder_id_name_key            RENAME TO gateways_project_id_name_key;
ALTER INDEX kacho_vpc.gateways_folder_idx                    RENAME TO gateways_project_idx;

ALTER INDEX kacho_vpc.private_endpoints_folder_id_name_key   RENAME TO private_endpoints_project_id_name_key;
ALTER INDEX kacho_vpc.private_endpoints_folder_idx           RENAME TO private_endpoints_project_idx;

ALTER INDEX kacho_vpc.network_interfaces_folder_id_name_key  RENAME TO network_interfaces_project_id_name_key;
ALTER INDEX kacho_vpc.network_interfaces_folder_idx          RENAME TO network_interfaces_project_idx;
-- +goose StatementEnd

-- +goose StatementBegin
-- 3) Recreate trigger function subnets_outbox_emit_route_table_change with
--    NEW.project_id (it referenced NEW.folder_id in payload).
CREATE OR REPLACE FUNCTION kacho_vpc.subnets_outbox_emit_route_table_change() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO kacho_vpc.vpc_outbox (resource_kind, resource_id, event_type, payload)
    VALUES (
        'Subnet',
        NEW.id,
        'UPDATED',
        jsonb_build_object(
            'id', NEW.id,
            'project_id', NEW.project_id,
            'network_id', NEW.network_id,
            'route_table_id', NEW.route_table_id,
            'name', NEW.name,
            'auto_association', true
        )
    );
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Symmetric rollback: project_id -> folder_id

CREATE OR REPLACE FUNCTION kacho_vpc.subnets_outbox_emit_route_table_change() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO kacho_vpc.vpc_outbox (resource_kind, resource_id, event_type, payload)
    VALUES (
        'Subnet',
        NEW.id,
        'UPDATED',
        jsonb_build_object(
            'id', NEW.id,
            'folder_id', NEW.folder_id,
            'network_id', NEW.network_id,
            'route_table_id', NEW.route_table_id,
            'name', NEW.name,
            'auto_association', true
        )
    );
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER INDEX kacho_vpc.networks_project_id_name_key           RENAME TO networks_folder_id_name_key;
ALTER INDEX kacho_vpc.networks_project_idx                   RENAME TO networks_folder_idx;
ALTER INDEX kacho_vpc.subnets_project_id_name_key            RENAME TO subnets_folder_id_name_key;
ALTER INDEX kacho_vpc.subnets_project_idx                    RENAME TO subnets_folder_idx;
ALTER INDEX kacho_vpc.addresses_project_id_name_key          RENAME TO addresses_folder_id_name_key;
ALTER INDEX kacho_vpc.addresses_project_idx                  RENAME TO addresses_folder_idx;
ALTER INDEX kacho_vpc.route_tables_project_id_name_key       RENAME TO route_tables_folder_id_name_key;
ALTER INDEX kacho_vpc.route_tables_project_idx               RENAME TO route_tables_folder_idx;
ALTER INDEX kacho_vpc.security_groups_project_id_name_key    RENAME TO security_groups_folder_id_name_key;
ALTER INDEX kacho_vpc.sg_project_idx                         RENAME TO sg_folder_idx;
ALTER INDEX kacho_vpc.gateways_project_id_name_key           RENAME TO gateways_folder_id_name_key;
ALTER INDEX kacho_vpc.gateways_project_idx                   RENAME TO gateways_folder_idx;
ALTER INDEX kacho_vpc.private_endpoints_project_id_name_key  RENAME TO private_endpoints_folder_id_name_key;
ALTER INDEX kacho_vpc.private_endpoints_project_idx          RENAME TO private_endpoints_folder_idx;
ALTER INDEX kacho_vpc.network_interfaces_project_id_name_key RENAME TO network_interfaces_folder_id_name_key;
ALTER INDEX kacho_vpc.network_interfaces_project_idx         RENAME TO network_interfaces_folder_idx;

ALTER TABLE kacho_vpc.network_interfaces  RENAME COLUMN project_id TO folder_id;
ALTER TABLE kacho_vpc.private_endpoints   RENAME COLUMN project_id TO folder_id;
ALTER TABLE kacho_vpc.gateways            RENAME COLUMN project_id TO folder_id;
ALTER TABLE kacho_vpc.security_groups     RENAME COLUMN project_id TO folder_id;
ALTER TABLE kacho_vpc.route_tables        RENAME COLUMN project_id TO folder_id;
ALTER TABLE kacho_vpc.addresses           RENAME COLUMN project_id TO folder_id;
ALTER TABLE kacho_vpc.subnets             RENAME COLUMN project_id TO folder_id;
ALTER TABLE kacho_vpc.networks            RENAME COLUMN project_id TO folder_id;
-- +goose StatementEnd
