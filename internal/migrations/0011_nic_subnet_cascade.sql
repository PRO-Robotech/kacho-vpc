-- +goose Up
-- KAC-31: relax the NIC→Subnet FK so a NIC does not *directly* block Subnet
-- (and hence Network) deletion. Dependency now flows NIC → Address → Subnet →
-- Network: a Subnet still can't be deleted while it has internal Address rows
-- (subnets → addresses ON DELETE RESTRICT, 0001_initial.sql), and an Address
-- can't be deleted while a NIC references it (AddressService.Delete guard,
-- KAC-31). A NIC *with* addresses therefore still transitively blocks its
-- subnet; a NIC with *no* addresses doesn't block anything and is cleaned up by
-- this cascade when the subnet is deleted.
--
-- The constraint is the inline `REFERENCES subnets(id) ON DELETE RESTRICT` from
-- 0006_network_interfaces.sql — Postgres auto-named it
-- `network_interfaces_subnet_id_fkey`.
ALTER TABLE network_interfaces DROP CONSTRAINT network_interfaces_subnet_id_fkey;
ALTER TABLE network_interfaces ADD CONSTRAINT network_interfaces_subnet_id_fkey
  FOREIGN KEY (subnet_id) REFERENCES subnets(id) ON DELETE CASCADE;

-- +goose Down
ALTER TABLE network_interfaces DROP CONSTRAINT network_interfaces_subnet_id_fkey;
ALTER TABLE network_interfaces ADD CONSTRAINT network_interfaces_subnet_id_fkey
  FOREIGN KEY (subnet_id) REFERENCES subnets(id) ON DELETE RESTRICT;
