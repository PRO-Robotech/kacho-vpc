-- +goose Up
--
-- AddressPool становится глобальным infrastructure-ресурсом (как Region/Zone).
-- Пулы общие на всю инсталляцию, не привязаны к Org/Cloud/Folder.
-- Управляются админом через kachoctl-ipam (InternalAddressPoolService gRPC).
--
-- Это убирает folder_id и связанный индекс. Folder validation в service-слое
-- становится не нужна (admin-only resource).

DROP INDEX IF EXISTS address_pools_folder_idx;
ALTER TABLE address_pools DROP COLUMN IF EXISTS folder_id;

-- +goose Down

ALTER TABLE address_pools ADD COLUMN folder_id TEXT NOT NULL DEFAULT '';
CREATE INDEX address_pools_folder_idx ON address_pools (folder_id);
