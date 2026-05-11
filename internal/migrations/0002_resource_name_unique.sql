-- +goose Up
-- FINDING-005: verbatim YC contract — name уникален в пределах folder для всех
-- VPC-ресурсов. В 0001 был только networks_folder_id_name_key; остальные ресурсы
-- допускали дубликаты name в folder (расхождение с YC).
--
-- Partial UNIQUE (WHERE name <> ''): VPC permissive policy разрешает empty name
-- для Subnet/Address/RouteTable/SecurityGroup/Gateway/PrivateEndpoint —
-- несколько ресурсов с пустым name допустимы (как в YC). Дубликат непустого
-- name в folder → 23505 → service.ErrAlreadyExists → gRPC ALREADY_EXISTS.

CREATE UNIQUE INDEX IF NOT EXISTS subnets_folder_id_name_key
    ON public.subnets USING btree (folder_id, name) WHERE (name <> ''::text);

CREATE UNIQUE INDEX IF NOT EXISTS route_tables_folder_id_name_key
    ON public.route_tables USING btree (folder_id, name) WHERE (name <> ''::text);

CREATE UNIQUE INDEX IF NOT EXISTS security_groups_folder_id_name_key
    ON public.security_groups USING btree (folder_id, name) WHERE (name <> ''::text);

CREATE UNIQUE INDEX IF NOT EXISTS gateways_folder_id_name_key
    ON public.gateways USING btree (folder_id, name) WHERE (name <> ''::text);

CREATE UNIQUE INDEX IF NOT EXISTS private_endpoints_folder_id_name_key
    ON public.private_endpoints USING btree (folder_id, name) WHERE (name <> ''::text);

CREATE UNIQUE INDEX IF NOT EXISTS addresses_folder_id_name_key
    ON public.addresses USING btree (folder_id, name) WHERE (name <> ''::text);

-- +goose Down
DROP INDEX IF EXISTS public.addresses_folder_id_name_key;
DROP INDEX IF EXISTS public.private_endpoints_folder_id_name_key;
DROP INDEX IF EXISTS public.gateways_folder_id_name_key;
DROP INDEX IF EXISTS public.security_groups_folder_id_name_key;
DROP INDEX IF EXISTS public.route_tables_folder_id_name_key;
DROP INDEX IF EXISTS public.subnets_folder_id_name_key;
