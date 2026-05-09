-- +goose Up
--
-- Fix: addresses_external_ip_uniq (миграция 0004) был partial UNIQUE без
-- фильтра пустого address — два row с external_ipv4 = `{"address": ""}`
-- (между repo.Insert и allocator.SetIPSpec) ловят UNIQUE-violation.
--
-- Перед inline-IPAM (kacho-vpc-controllers упразднён, allocator живёт inline в
-- worker'е AddressService.doCreate) row с пустым address существует кратко,
-- но это сериализуется через одну UNIQUE-violation на каждый concurrent
-- Insert. Пересоздаём индекс с фильтром пустых строк.

DROP INDEX IF EXISTS addresses_external_ip_uniq;

CREATE UNIQUE INDEX addresses_external_ip_uniq ON addresses ((external_ipv4 ->> 'address'))
  WHERE external_ipv4 IS NOT NULL
    AND external_ipv4 ->> 'address' <> '';

-- +goose Down

DROP INDEX IF EXISTS addresses_external_ip_uniq;
CREATE UNIQUE INDEX addresses_external_ip_uniq ON addresses ((external_ipv4 ->> 'address'))
  WHERE external_ipv4 IS NOT NULL;
