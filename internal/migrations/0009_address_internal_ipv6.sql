-- +goose Up
-- IPv6 vertical: Address gains internal_ipv6 (oneof field 22 in vpc/v1.Address —
-- {address, subnet_id}). Stored as a jsonb column, mirroring internal_ipv4.
-- A partial UNIQUE index enforces "one Address per (subnet_id, ipv6 address)",
-- mirroring addresses_internal_subnet_ip_uniq — this also doubles as the
-- conflict target for the AllocateInternalIPv6 random-pick-and-retry allocator.
ALTER TABLE addresses ADD COLUMN internal_ipv6 jsonb;
CREATE UNIQUE INDEX addresses_internal_subnet_ipv6_uniq
  ON addresses (((internal_ipv6 ->> 'subnet_id')), ((internal_ipv6 ->> 'address')))
  WHERE internal_ipv6 IS NOT NULL
    AND (internal_ipv6 ->> 'address') <> ''
    AND (internal_ipv6 ->> 'subnet_id') <> '';

-- +goose Down
DROP INDEX IF EXISTS addresses_internal_subnet_ipv6_uniq;
ALTER TABLE addresses DROP COLUMN IF EXISTS internal_ipv6;
