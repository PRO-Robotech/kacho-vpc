-- +goose Up
-- KAC-48: mac_address на NetworkInterface — cloud-wide UNIQUE, output-only,
-- стабильное поле NIC (по образцу AWS-ENI: MAC принадлежит интерфейсу-ресурсу,
-- не инстансу). Значение присваивается на NetworkInterface.Create (service-слой
-- генерирует, retry'ит на collision); миграция выполняет backfill для уже
-- существующих rows, потому что UNIQUE NOT NULL.
--
-- Префикс `0e:` (locally-administered, unicast) зарезервирован под Kachō.
-- Backfill использует md5(random()||id) — стабильный, без зависимости от
-- pgcrypto. После backfill ставим NOT NULL и UNIQUE INDEX.

ALTER TABLE network_interfaces ADD COLUMN mac_address TEXT;

UPDATE network_interfaces SET mac_address =
    '0e:'
    || substring(md5(random()::text || id) FROM 1 FOR 2) || ':'
    || substring(md5(random()::text || id) FROM 3 FOR 2) || ':'
    || substring(md5(random()::text || id) FROM 5 FOR 2) || ':'
    || substring(md5(random()::text || id) FROM 7 FOR 2) || ':'
    || substring(md5(random()::text || id) FROM 9 FOR 2)
WHERE mac_address IS NULL;

ALTER TABLE network_interfaces ALTER COLUMN mac_address SET NOT NULL;

CREATE UNIQUE INDEX network_interfaces_mac_address_key
    ON network_interfaces (mac_address);

-- +goose Down
DROP INDEX IF EXISTS network_interfaces_mac_address_key;
ALTER TABLE network_interfaces DROP COLUMN IF EXISTS mac_address;
