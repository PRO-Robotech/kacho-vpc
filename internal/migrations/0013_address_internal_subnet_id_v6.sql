-- +goose Up
-- KAC-34: the addresses.internal_subnet_id generated column (and the FK
-- addresses_internal_subnet_fkey it backs — ON DELETE RESTRICT to subnets) was
-- derived ONLY from internal_ipv4->>'subnet_id'. So an internal *IPv6* address
-- created "in a subnet" (internal_ipv6_address_spec.subnet_id = X) left
-- internal_subnet_id NULL → the FK never fired → that subnet could be deleted
-- out from under it (a v4 internal address correctly blocked it). Widen the
-- generated expression to also derive from internal_ipv6 (column exists since
-- 0009_address_internal_ipv6.sql), so both v4 and v6 internal addresses block
-- deleting their subnet.
--
-- Postgres 16 has no `ALTER COLUMN ... SET EXPRESSION`, so we drop & recreate
-- the column (and its dependent index + FK constraint) — names verified against
-- 0001_initial.sql: index addresses_internal_subnet_idx, constraint
-- addresses_internal_subnet_fkey. internal_subnet_id is GENERATED, so no Go
-- write-path or SELECT references it — no code change needed beyond this.
ALTER TABLE addresses DROP CONSTRAINT addresses_internal_subnet_fkey;
DROP INDEX addresses_internal_subnet_idx;
ALTER TABLE addresses DROP COLUMN internal_subnet_id;
ALTER TABLE addresses ADD COLUMN internal_subnet_id text GENERATED ALWAYS AS (
  CASE
    WHEN internal_ipv4 IS NOT NULL AND internal_ipv4 ? 'subnet_id' AND length(internal_ipv4->>'subnet_id') > 0 THEN internal_ipv4->>'subnet_id'
    WHEN internal_ipv6 IS NOT NULL AND internal_ipv6 ? 'subnet_id' AND length(internal_ipv6->>'subnet_id') > 0 THEN internal_ipv6->>'subnet_id'
    ELSE NULL
  END
) STORED;
-- Purge addresses whose internal v4/v6 subnet_id points at a subnet that no
-- longer exists — pre-existing dangling rows produced by exactly the bug this
-- migration fixes (a v6 address used to NOT block deleting its subnet, so the
-- subnet got deleted out from under it). Without this the FK below can't be
-- (re)added (SQLSTATE 23503). Such an address can't be referenced by a NIC: a
-- NIC requires the address's subnet == the NIC's own subnet, and (KAC-33) a NIC
-- keeps its subnet from being deleted — so this only removes genuinely orphaned
-- addresses. address_references / address_pool overrides cascade-delete.
DELETE FROM addresses
 WHERE (internal_ipv4 IS NOT NULL AND internal_ipv4 ? 'subnet_id' AND length(internal_ipv4->>'subnet_id') > 0
        AND NOT EXISTS (SELECT 1 FROM subnets s WHERE s.id = internal_ipv4->>'subnet_id'))
    OR (internal_ipv6 IS NOT NULL AND internal_ipv6 ? 'subnet_id' AND length(internal_ipv6->>'subnet_id') > 0
        AND NOT EXISTS (SELECT 1 FROM subnets s WHERE s.id = internal_ipv6->>'subnet_id'));
CREATE INDEX addresses_internal_subnet_idx ON addresses (internal_subnet_id);
ALTER TABLE addresses ADD CONSTRAINT addresses_internal_subnet_fkey FOREIGN KEY (internal_subnet_id) REFERENCES subnets(id) ON DELETE RESTRICT;

-- +goose Down
-- Restore the v4-only generated expression (the 0001_initial.sql form).
ALTER TABLE addresses DROP CONSTRAINT addresses_internal_subnet_fkey;
DROP INDEX addresses_internal_subnet_idx;
ALTER TABLE addresses DROP COLUMN internal_subnet_id;
ALTER TABLE addresses ADD COLUMN internal_subnet_id text GENERATED ALWAYS AS (
  CASE
    WHEN internal_ipv4 IS NOT NULL AND internal_ipv4 ? 'subnet_id' AND length(internal_ipv4->>'subnet_id') > 0 THEN internal_ipv4->>'subnet_id'
    ELSE NULL
  END
) STORED;
CREATE INDEX addresses_internal_subnet_idx ON addresses (internal_subnet_id);
ALTER TABLE addresses ADD CONSTRAINT addresses_internal_subnet_fkey FOREIGN KEY (internal_subnet_id) REFERENCES subnets(id) ON DELETE RESTRICT;
