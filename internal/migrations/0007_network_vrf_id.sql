-- Copyright (c) PRO-Robotech
-- SPDX-License-Identifier: BUSL-1.1

-- 0007: аллокация уникального per-network VRF id (SRv6 tenancy-домен data-plane).
-- VRF сети должен быть АВТОРИТЕТНЫМ в control-plane VPC (а не client-side hash),
-- чтобы cilium-датаплейн строил overlap-safe тенантинг на нем. Sequence-backed →
-- уникален по построению; backfill существующих; отдается через
-- InternalNetworkService.GetNetwork (инфра-чувствительное поле, только internal).
-- +goose Up
CREATE SEQUENCE IF NOT EXISTS kacho_vpc.networks_vrf_seq AS bigint START WITH 101 MINVALUE 101;
ALTER TABLE kacho_vpc.networks ADD COLUMN vrf_id bigint;
ALTER TABLE kacho_vpc.networks ALTER COLUMN vrf_id SET DEFAULT nextval('kacho_vpc.networks_vrf_seq');
UPDATE kacho_vpc.networks SET vrf_id = nextval('kacho_vpc.networks_vrf_seq') WHERE vrf_id IS NULL;
ALTER TABLE kacho_vpc.networks ALTER COLUMN vrf_id SET NOT NULL;
ALTER TABLE kacho_vpc.networks ADD CONSTRAINT networks_vrf_id_key UNIQUE (vrf_id);

-- +goose Down
ALTER TABLE kacho_vpc.networks DROP CONSTRAINT IF EXISTS networks_vrf_id_key;
ALTER TABLE kacho_vpc.networks DROP COLUMN IF EXISTS vrf_id;
DROP SEQUENCE IF EXISTS kacho_vpc.networks_vrf_seq;
