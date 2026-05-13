-- +goose Up
-- KAC-33: revert 0011_nic_subnet_cascade.sql. The NIC→Subnet FK was made
-- ON DELETE CASCADE by KAC-31 — that silently cascade-deleted address-less NICs
-- when a subnet was deleted (and transitively let a Network tear-down destroy
-- its resources). Restore ON DELETE RESTRICT: a NIC hard-blocks its subnet.
-- Delete bottom-up: NIC → Address → Subnet → Network. The Address.Delete guard
-- ("address in use by a NIC can't be deleted", KAC-31) stays as-is.
ALTER TABLE network_interfaces DROP CONSTRAINT network_interfaces_subnet_id_fkey;
ALTER TABLE network_interfaces ADD CONSTRAINT network_interfaces_subnet_id_fkey
  FOREIGN KEY (subnet_id) REFERENCES subnets(id) ON DELETE RESTRICT;

-- +goose Down
-- Restore 0011's state (ON DELETE CASCADE).
ALTER TABLE network_interfaces DROP CONSTRAINT network_interfaces_subnet_id_fkey;
ALTER TABLE network_interfaces ADD CONSTRAINT network_interfaces_subnet_id_fkey
  FOREIGN KEY (subnet_id) REFERENCES subnets(id) ON DELETE CASCADE;
