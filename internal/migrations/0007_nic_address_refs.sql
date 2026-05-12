-- +goose Up
-- KAC-7 follow-up (epic KAC-2): NIC ссылается на Address-ресурсы по id вместо
-- сырых IP-строк. Публичный VPC API NIC'а несёт v4_address_ids/v6_address_ids
-- (refs на addresses); резолвленные IP уходят в data-plane через internal-
-- проекцию (InternalNetworkInterface.v4_addresses/v6_addresses).
--
-- Хранение: jsonb-массивы id прямо на строке network_interfaces (без join-таблицы).
-- Уникальность «Address ≤ 1 NIC» обеспечивается на уровне сервиса через
-- addresses.used (Create/Update проверяют used, выставляют used=true + referrer-row;
-- Delete снимает used + referrer). Бэкфилла нет — NIC-ресурс новый (KAC-7,
-- замержен сегодня), production-данных и NIC'ов на dev-стенде нет.
DROP INDEX IF EXISTS network_interfaces_subnet_addr_key;
ALTER TABLE network_interfaces
  DROP COLUMN primary_v4_address,
  DROP COLUMN secondary_v4_addresses,
  DROP COLUMN v6_addresses,
  ADD COLUMN v4_address_ids jsonb NOT NULL DEFAULT '[]'::jsonb,
  ADD COLUMN v6_address_ids jsonb NOT NULL DEFAULT '[]'::jsonb;

-- +goose Down
ALTER TABLE network_interfaces
  DROP COLUMN v4_address_ids,
  DROP COLUMN v6_address_ids,
  ADD COLUMN primary_v4_address     TEXT  NOT NULL DEFAULT '',
  ADD COLUMN secondary_v4_addresses jsonb NOT NULL DEFAULT '[]'::jsonb,
  ADD COLUMN v6_addresses           jsonb NOT NULL DEFAULT '[]'::jsonb;
CREATE UNIQUE INDEX network_interfaces_subnet_addr_key ON network_interfaces (subnet_id, primary_v4_address);
