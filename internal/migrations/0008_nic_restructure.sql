-- +goose Up
-- NIC restructure (KAC-7 follow-up): vpc/v1.NetworkInterface drops network_id (8),
-- instance_id (11), index (12) and gains a denormalized used_by reference
-- (kacho.cloud.reference.Reference; e.g. {type:"compute_instance", id:<instance_id>})
-- — mirrors Address.used_by. Set by AttachToInstance, cleared by DetachFromInstance.
-- Storage: one referrer per NIC, flat columns on network_interfaces (like Address
-- uses addresses.used + address_references; here we keep it inline since NIC has
-- exactly one referrer slot). No backfill — NIC resource is brand-new (KAC-7),
-- dev-stand has at most a couple of empty NICs.
DROP INDEX IF EXISTS network_interfaces_instance_idx;
DROP INDEX IF EXISTS network_interfaces_network_idx;
ALTER TABLE network_interfaces
  DROP COLUMN IF EXISTS network_id,
  DROP COLUMN IF EXISTS instance_id,
  DROP COLUMN IF EXISTS ni_index,
  ADD COLUMN used_by_type TEXT NOT NULL DEFAULT '',
  ADD COLUMN used_by_id   TEXT NOT NULL DEFAULT '',
  ADD COLUMN used_by_name TEXT NOT NULL DEFAULT '';
CREATE INDEX network_interfaces_used_by_idx ON network_interfaces (used_by_type, used_by_id) WHERE used_by_id <> '';

-- +goose Down
DROP INDEX IF EXISTS network_interfaces_used_by_idx;
ALTER TABLE network_interfaces
  DROP COLUMN IF EXISTS used_by_type,
  DROP COLUMN IF EXISTS used_by_id,
  DROP COLUMN IF EXISTS used_by_name,
  ADD COLUMN network_id  TEXT NOT NULL DEFAULT '',
  ADD COLUMN instance_id TEXT NOT NULL DEFAULT '',
  ADD COLUMN ni_index    TEXT NOT NULL DEFAULT '';
CREATE INDEX network_interfaces_network_idx  ON network_interfaces (network_id);
CREATE INDEX network_interfaces_instance_idx ON network_interfaces (instance_id) WHERE instance_id <> '';
