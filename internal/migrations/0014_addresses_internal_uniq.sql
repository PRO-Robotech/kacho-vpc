-- +goose Up
--
-- Internal IPAM: гарантия, что один и тот же internal IP не может быть
-- выдан дважды в рамках одной Subnet.
--
-- (Cross-subnet collision разрешён — в разных Network'ах CIDR может
--  пересекаться, и тот же IP в другой subnet — это другой IP; а внутри
--  одной subnet UNIQUE по (subnet_id, address) — invariant IPAM.)
--
-- Реализация — partial UNIQUE INDEX на JSONB-extracted полях. Применяется
-- только для address-rows с заполненным internal_ipv4.address (т.е. после
-- успешной allocation).

CREATE UNIQUE INDEX IF NOT EXISTS addresses_internal_subnet_ip_uniq
  ON addresses ((internal_ipv4->>'subnet_id'), (internal_ipv4->>'address'))
  WHERE internal_ipv4 IS NOT NULL
    AND internal_ipv4->>'address' <> ''
    AND internal_ipv4->>'subnet_id' <> '';

-- +goose Down

DROP INDEX IF EXISTS addresses_internal_subnet_ip_uniq;
