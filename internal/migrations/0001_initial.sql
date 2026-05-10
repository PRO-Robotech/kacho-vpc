-- +goose Up
-- Squashed initial schema: replaces 0001..0022 migrations of pre-1.1 era.
-- Generated from pg_dump of stable production schema 2026-05-11.
-- Includes: operations + outbox + 7 resource tables + IPAM (regions/zones/pools).

CREATE EXTENSION IF NOT EXISTS btree_gist WITH SCHEMA public;

-- +goose StatementBegin
CREATE FUNCTION public.vpc_outbox_notify() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
  PERFORM pg_notify('vpc_outbox', NEW.sequence_no::text);
  RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TABLE public.address_pool_address_override (
    address_id text NOT NULL,
    pool_id text NOT NULL,
    bound_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: address_pool_network_default; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.address_pool_network_default (
    network_id text NOT NULL,
    pool_id text NOT NULL,
    bound_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: address_pools; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.address_pools (
    id text NOT NULL,
    name text DEFAULT ''::text NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    labels jsonb DEFAULT '{}'::jsonb NOT NULL,
    cidr_blocks text[] DEFAULT '{}'::text[] NOT NULL,
    kind smallint NOT NULL,
    is_default boolean DEFAULT false NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    modified_at timestamp with time zone DEFAULT now() NOT NULL,
    selector_labels jsonb DEFAULT '{}'::jsonb NOT NULL,
    selector_priority integer DEFAULT 0 NOT NULL,
    zone_id text
);


--
-- Name: addresses; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.addresses (
    id text NOT NULL,
    folder_id text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    name text DEFAULT ''::text NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    labels jsonb DEFAULT '{}'::jsonb NOT NULL,
    addr_type smallint DEFAULT 0 NOT NULL,
    ip_version smallint DEFAULT 0 NOT NULL,
    reserved boolean DEFAULT false NOT NULL,
    used boolean DEFAULT false NOT NULL,
    deletion_protection boolean DEFAULT false NOT NULL,
    external_ipv4 jsonb,
    internal_ipv4 jsonb,
    internal_subnet_id text GENERATED ALWAYS AS (
CASE
    WHEN ((internal_ipv4 IS NOT NULL) AND (internal_ipv4 ? 'subnet_id'::text) AND (length((internal_ipv4 ->> 'subnet_id'::text)) > 0)) THEN (internal_ipv4 ->> 'subnet_id'::text)
    ELSE NULL::text
END) STORED
);


--
-- Name: cloud_pool_selector; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.cloud_pool_selector (
    cloud_id text NOT NULL,
    selector jsonb DEFAULT '{}'::jsonb NOT NULL,
    set_at timestamp with time zone DEFAULT now() NOT NULL,
    set_by text DEFAULT ''::text NOT NULL
);


--
-- Name: gateways; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.gateways (
    id text NOT NULL,
    folder_id text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    name text DEFAULT ''::text NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    labels jsonb DEFAULT '{}'::jsonb NOT NULL,
    gateway_type text DEFAULT 'shared_egress'::text NOT NULL
);






--
-- Name: networks; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.networks (
    id text NOT NULL,
    folder_id text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    name text NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    labels jsonb DEFAULT '{}'::jsonb NOT NULL,
    default_security_group_id text DEFAULT ''::text NOT NULL,
    route_distinguisher text DEFAULT ''::text NOT NULL
);


--
-- Name: operations; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.operations (
    id text NOT NULL,
    description text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    created_by text DEFAULT 'anonymous'::text NOT NULL,
    modified_at timestamp with time zone DEFAULT now() NOT NULL,
    done boolean DEFAULT false NOT NULL,
    metadata_type text,
    metadata_data bytea,
    resource_id text,
    error_code integer,
    error_message text,
    error_details bytea,
    response_type text,
    response_data bytea
);


--
-- Name: private_endpoints; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.private_endpoints (
    id text NOT NULL,
    folder_id text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    name text DEFAULT ''::text NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    labels jsonb DEFAULT '{}'::jsonb NOT NULL,
    network_id text,
    subnet_id text,
    address_id text,
    ip_address text,
    service_type text,
    dns_options jsonb DEFAULT '{}'::jsonb NOT NULL,
    status text DEFAULT 'PENDING'::text NOT NULL
);


--
-- Name: regions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.regions (
    id text NOT NULL,
    name text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: route_tables; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.route_tables (
    id text NOT NULL,
    folder_id text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    name text NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    labels jsonb DEFAULT '{}'::jsonb NOT NULL,
    network_id text NOT NULL,
    static_routes jsonb DEFAULT '[]'::jsonb NOT NULL
);


--
-- Name: security_groups; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.security_groups (
    id text NOT NULL,
    folder_id text NOT NULL,
    network_id text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    name text NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    labels jsonb DEFAULT '{}'::jsonb NOT NULL,
    status text DEFAULT 'ACTIVE'::text NOT NULL,
    default_for_network boolean DEFAULT false NOT NULL,
    rules jsonb DEFAULT '[]'::jsonb NOT NULL
);


--
-- Name: subnets; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.subnets (
    id text NOT NULL,
    folder_id text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    name text NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    labels jsonb DEFAULT '{}'::jsonb NOT NULL,
    network_id text NOT NULL,
    zone_id text DEFAULT ''::text NOT NULL,
    v4_cidr_blocks text[] DEFAULT '{}'::text[] NOT NULL,
    v6_cidr_blocks text[] DEFAULT '{}'::text[] NOT NULL,
    route_table_id text,
    dhcp_options jsonb,
    v4_cidr_primary cidr GENERATED ALWAYS AS (
CASE
    WHEN ((array_length(v4_cidr_blocks, 1) >= 1) AND (v4_cidr_blocks[1] ~ '^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+/[0-9]+$'::text)) THEN (v4_cidr_blocks[1])::cidr
    ELSE NULL::cidr
END) STORED,
    v6_cidr_primary cidr GENERATED ALWAYS AS (
CASE
    WHEN ((array_length(v6_cidr_blocks, 1) >= 1) AND (v6_cidr_blocks[1] ~ '^[0-9a-fA-F:]+/[0-9]+$'::text)) THEN (v6_cidr_blocks[1])::cidr
    ELSE NULL::cidr
END) STORED
);


--
-- Name: vpc_outbox; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.vpc_outbox (
    sequence_no bigint NOT NULL,
    resource_kind text NOT NULL,
    resource_id text NOT NULL,
    event_type text NOT NULL,
    payload jsonb DEFAULT '{}'::jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    processed_at timestamp with time zone
);


--
-- Name: vpc_outbox_sequence_no_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.vpc_outbox_sequence_no_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: vpc_outbox_sequence_no_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.vpc_outbox_sequence_no_seq OWNED BY public.vpc_outbox.sequence_no;


--
-- Name: vpc_watch_cursors; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.vpc_watch_cursors (
    subscriber_id text NOT NULL,
    last_sequence_no bigint DEFAULT 0 NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: zones; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.zones (
    id text NOT NULL,
    region_id text NOT NULL,
    name text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: vpc_outbox sequence_no; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.vpc_outbox ALTER COLUMN sequence_no SET DEFAULT nextval('public.vpc_outbox_sequence_no_seq'::regclass);


--
-- Name: address_pool_address_override address_pool_address_override_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.address_pool_address_override
    ADD CONSTRAINT address_pool_address_override_pkey PRIMARY KEY (address_id);


--
-- Name: address_pool_network_default address_pool_network_default_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.address_pool_network_default
    ADD CONSTRAINT address_pool_network_default_pkey PRIMARY KEY (network_id);


--
-- Name: address_pools address_pools_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.address_pools
    ADD CONSTRAINT address_pools_pkey PRIMARY KEY (id);


--
-- Name: addresses addresses_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.addresses
    ADD CONSTRAINT addresses_pkey PRIMARY KEY (id);


--
-- Name: cloud_pool_selector cloud_pool_selector_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cloud_pool_selector
    ADD CONSTRAINT cloud_pool_selector_pkey PRIMARY KEY (cloud_id);


--
-- Name: gateways gateways_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.gateways
    ADD CONSTRAINT gateways_pkey PRIMARY KEY (id);




--
-- Name: networks networks_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.networks
    ADD CONSTRAINT networks_pkey PRIMARY KEY (id);


--
-- Name: operations operations_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.operations
    ADD CONSTRAINT operations_pkey PRIMARY KEY (id);


--
-- Name: private_endpoints private_endpoints_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.private_endpoints
    ADD CONSTRAINT private_endpoints_pkey PRIMARY KEY (id);


--
-- Name: regions regions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.regions
    ADD CONSTRAINT regions_pkey PRIMARY KEY (id);


--
-- Name: route_tables route_tables_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.route_tables
    ADD CONSTRAINT route_tables_pkey PRIMARY KEY (id);


--
-- Name: security_groups security_groups_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.security_groups
    ADD CONSTRAINT security_groups_pkey PRIMARY KEY (id);


--
-- Name: subnets subnets_no_overlap_v4; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.subnets
    ADD CONSTRAINT subnets_no_overlap_v4 EXCLUDE USING gist (network_id WITH =, v4_cidr_primary inet_ops WITH &&) WHERE ((v4_cidr_primary IS NOT NULL));


--
-- Name: subnets subnets_no_overlap_v6; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.subnets
    ADD CONSTRAINT subnets_no_overlap_v6 EXCLUDE USING gist (network_id WITH =, v6_cidr_primary inet_ops WITH &&) WHERE ((v6_cidr_primary IS NOT NULL));


--
-- Name: subnets subnets_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.subnets
    ADD CONSTRAINT subnets_pkey PRIMARY KEY (id);


--
-- Name: vpc_outbox vpc_outbox_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.vpc_outbox
    ADD CONSTRAINT vpc_outbox_pkey PRIMARY KEY (sequence_no);


--
-- Name: vpc_watch_cursors vpc_watch_cursors_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.vpc_watch_cursors
    ADD CONSTRAINT vpc_watch_cursors_pkey PRIMARY KEY (subscriber_id);


--
-- Name: zones zones_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.zones
    ADD CONSTRAINT zones_pkey PRIMARY KEY (id);


--
-- Name: address_pool_address_override_pool_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX address_pool_address_override_pool_idx ON public.address_pool_address_override USING btree (pool_id);


--
-- Name: address_pool_network_default_pool_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX address_pool_network_default_pool_idx ON public.address_pool_network_default USING btree (pool_id);


--
-- Name: address_pools_selector_labels_gin; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX address_pools_selector_labels_gin ON public.address_pools USING gin (selector_labels jsonb_path_ops) WHERE (selector_labels <> '{}'::jsonb);


--
-- Name: address_pools_zone_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX address_pools_zone_idx ON public.address_pools USING btree (zone_id);


--
-- Name: address_pools_zone_kind_default_uniq; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX address_pools_zone_kind_default_uniq ON public.address_pools USING btree (COALESCE(zone_id, ''::text), kind) WHERE (is_default = true);


--
-- Name: addresses_created_at_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX addresses_created_at_idx ON public.addresses USING btree (created_at);


--
-- Name: addresses_external_ip_uniq; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX addresses_external_ip_uniq ON public.addresses USING btree (((external_ipv4 ->> 'address'::text))) WHERE ((external_ipv4 IS NOT NULL) AND ((external_ipv4 ->> 'address'::text) <> ''::text));


--
-- Name: addresses_external_pool_ip_uniq; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX addresses_external_pool_ip_uniq ON public.addresses USING btree (((external_ipv4 ->> 'address_pool_id'::text)), ((external_ipv4 ->> 'address'::text))) WHERE ((external_ipv4 IS NOT NULL) AND ((external_ipv4 ->> 'address'::text) <> ''::text) AND ((external_ipv4 ->> 'address_pool_id'::text) <> ''::text));


--
-- Name: addresses_folder_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX addresses_folder_idx ON public.addresses USING btree (folder_id);


--
-- Name: addresses_internal_subnet_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX addresses_internal_subnet_idx ON public.addresses USING btree (internal_subnet_id);


--
-- Name: addresses_internal_subnet_ip_uniq; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX addresses_internal_subnet_ip_uniq ON public.addresses USING btree (((internal_ipv4 ->> 'subnet_id'::text)), ((internal_ipv4 ->> 'address'::text))) WHERE ((internal_ipv4 IS NOT NULL) AND ((internal_ipv4 ->> 'address'::text) <> ''::text) AND ((internal_ipv4 ->> 'subnet_id'::text) <> ''::text));


--
-- Name: cloud_pool_selector_gin; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX cloud_pool_selector_gin ON public.cloud_pool_selector USING gin (selector jsonb_path_ops) WHERE (selector <> '{}'::jsonb);


--
-- Name: gateways_created_at_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX gateways_created_at_idx ON public.gateways USING btree (created_at);


--
-- Name: gateways_folder_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX gateways_folder_idx ON public.gateways USING btree (folder_id);


--
-- Name: networks_created_at_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX networks_created_at_idx ON public.networks USING btree (created_at);


--
-- Name: networks_folder_id_name_key; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX networks_folder_id_name_key ON public.networks USING btree (folder_id, name);


--
-- Name: networks_folder_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX networks_folder_idx ON public.networks USING btree (folder_id);


--
-- Name: operations_created_at_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX operations_created_at_idx ON public.operations USING btree (created_at);


--
-- Name: operations_done_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX operations_done_idx ON public.operations USING btree (done);


--
-- Name: operations_resource_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX operations_resource_idx ON public.operations USING btree (resource_id);


--
-- Name: private_endpoints_created_at_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX private_endpoints_created_at_idx ON public.private_endpoints USING btree (created_at);


--
-- Name: private_endpoints_folder_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX private_endpoints_folder_idx ON public.private_endpoints USING btree (folder_id);


--
-- Name: private_endpoints_network_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX private_endpoints_network_idx ON public.private_endpoints USING btree (network_id);


--
-- Name: route_tables_created_at_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX route_tables_created_at_idx ON public.route_tables USING btree (created_at);


--
-- Name: route_tables_folder_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX route_tables_folder_idx ON public.route_tables USING btree (folder_id);


--
-- Name: route_tables_network_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX route_tables_network_idx ON public.route_tables USING btree (network_id);


--
-- Name: sg_created_at_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX sg_created_at_idx ON public.security_groups USING btree (created_at);


--
-- Name: sg_folder_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX sg_folder_idx ON public.security_groups USING btree (folder_id);


--
-- Name: sg_network_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX sg_network_idx ON public.security_groups USING btree (network_id);


--
-- Name: subnets_created_at_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX subnets_created_at_idx ON public.subnets USING btree (created_at);


--
-- Name: subnets_folder_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX subnets_folder_idx ON public.subnets USING btree (folder_id);


--
-- Name: subnets_network_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX subnets_network_idx ON public.subnets USING btree (network_id);


--
-- Name: vpc_outbox_kind_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX vpc_outbox_kind_idx ON public.vpc_outbox USING btree (resource_kind, sequence_no);


--
-- Name: vpc_outbox_seq_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX vpc_outbox_seq_idx ON public.vpc_outbox USING btree (sequence_no);


--
-- Name: zones_region_id_name_key; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX zones_region_id_name_key ON public.zones USING btree (region_id, name) WHERE (name <> ''::text);


--
-- Name: zones_region_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX zones_region_idx ON public.zones USING btree (region_id);


--
-- Name: vpc_outbox vpc_outbox_notify_trg; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER vpc_outbox_notify_trg AFTER INSERT ON public.vpc_outbox FOR EACH ROW EXECUTE FUNCTION public.vpc_outbox_notify();


--
-- Name: address_pool_address_override address_pool_address_override_address_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.address_pool_address_override
    ADD CONSTRAINT address_pool_address_override_address_id_fkey FOREIGN KEY (address_id) REFERENCES public.addresses(id) ON DELETE CASCADE;


--
-- Name: address_pool_address_override address_pool_address_override_pool_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.address_pool_address_override
    ADD CONSTRAINT address_pool_address_override_pool_id_fkey FOREIGN KEY (pool_id) REFERENCES public.address_pools(id) ON DELETE RESTRICT;


--
-- Name: address_pool_network_default address_pool_network_default_network_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.address_pool_network_default
    ADD CONSTRAINT address_pool_network_default_network_id_fkey FOREIGN KEY (network_id) REFERENCES public.networks(id) ON DELETE CASCADE;


--
-- Name: address_pool_network_default address_pool_network_default_pool_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.address_pool_network_default
    ADD CONSTRAINT address_pool_network_default_pool_id_fkey FOREIGN KEY (pool_id) REFERENCES public.address_pools(id) ON DELETE RESTRICT;


--
-- Name: address_pools address_pools_zone_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.address_pools
    ADD CONSTRAINT address_pools_zone_id_fkey FOREIGN KEY (zone_id) REFERENCES public.zones(id) ON DELETE RESTRICT;


--
-- Name: addresses addresses_internal_subnet_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.addresses
    ADD CONSTRAINT addresses_internal_subnet_fkey FOREIGN KEY (internal_subnet_id) REFERENCES public.subnets(id) ON DELETE RESTRICT;


--
-- Name: route_tables route_tables_network_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.route_tables
    ADD CONSTRAINT route_tables_network_id_fkey FOREIGN KEY (network_id) REFERENCES public.networks(id);


--
-- Name: security_groups security_groups_network_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.security_groups
    ADD CONSTRAINT security_groups_network_id_fkey FOREIGN KEY (network_id) REFERENCES public.networks(id) ON DELETE RESTRICT;


--
-- Name: subnets subnets_network_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.subnets
    ADD CONSTRAINT subnets_network_id_fkey FOREIGN KEY (network_id) REFERENCES public.networks(id);


--
-- Name: zones zones_region_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.zones
    ADD CONSTRAINT zones_region_id_fkey FOREIGN KEY (region_id) REFERENCES public.regions(id) ON DELETE RESTRICT;


--
--

-- +goose Down
DROP TABLE IF EXISTS public.vpc_watch_cursors CASCADE;
DROP TABLE IF EXISTS public.vpc_outbox CASCADE;
DROP SEQUENCE IF EXISTS public.vpc_outbox_sequence_no_seq;
-- +goose StatementBegin
DROP FUNCTION IF EXISTS public.vpc_outbox_notify() CASCADE;
-- +goose StatementEnd
DROP TABLE IF EXISTS public.cloud_pool_selector CASCADE;
DROP TABLE IF EXISTS public.address_pool_address_override CASCADE;
DROP TABLE IF EXISTS public.address_pool_network_default CASCADE;
DROP TABLE IF EXISTS public.address_pools CASCADE;
DROP TABLE IF EXISTS public.zones CASCADE;
DROP TABLE IF EXISTS public.regions CASCADE;
DROP TABLE IF EXISTS public.private_endpoints CASCADE;
DROP TABLE IF EXISTS public.gateways CASCADE;
DROP TABLE IF EXISTS public.security_groups CASCADE;
DROP TABLE IF EXISTS public.route_tables CASCADE;
DROP TABLE IF EXISTS public.addresses CASCADE;
DROP TABLE IF EXISTS public.subnets CASCADE;
DROP TABLE IF EXISTS public.networks CASCADE;
DROP TABLE IF EXISTS public.operations CASCADE;
DROP EXTENSION IF EXISTS btree_gist;
