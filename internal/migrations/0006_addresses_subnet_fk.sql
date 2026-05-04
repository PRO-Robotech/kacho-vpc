-- +goose Up
-- SUBNET-DEL-WITH-ADDRESS — добавляет FK addresses.internal_ipv4 → subnets(id)
-- ON DELETE RESTRICT, чтобы Subnet.Delete с зависимым Address упал в repo с
-- SQLSTATE 23503, который handler мапит в FailedPrecondition (как N-DEL-1).
--
-- Поле internal_ipv4 — JSONB { "address": "...", "subnet_id": "..." }; FK не
-- работает на JSONB, поэтому добавляем generated stored-колонку
-- internal_subnet_id, индексируем и кладём FK.

-- +goose StatementBegin
ALTER TABLE addresses
  ADD COLUMN internal_subnet_id UUID
  GENERATED ALWAYS AS (
    CASE WHEN internal_ipv4 IS NOT NULL
         AND internal_ipv4 ? 'subnet_id'
         AND length(internal_ipv4->>'subnet_id') = 36
    THEN (internal_ipv4->>'subnet_id')::UUID
    ELSE NULL
    END
  ) STORED;
-- +goose StatementEnd

CREATE INDEX addresses_internal_subnet_idx ON addresses (internal_subnet_id);

-- Перед добавлением FK — почистить orphans (addresses, ссылающиеся на уже
-- удалённый subnet). В dev-стенде такие могли возникнуть из-за бага до фикса.
DELETE FROM addresses
WHERE internal_subnet_id IS NOT NULL
  AND internal_subnet_id NOT IN (SELECT id FROM subnets);

ALTER TABLE addresses
  ADD CONSTRAINT addresses_internal_subnet_fkey
  FOREIGN KEY (internal_subnet_id) REFERENCES subnets(id) ON DELETE RESTRICT;

-- +goose Down

ALTER TABLE addresses DROP CONSTRAINT IF EXISTS addresses_internal_subnet_fkey;
DROP INDEX IF EXISTS addresses_internal_subnet_idx;
ALTER TABLE addresses DROP COLUMN IF EXISTS internal_subnet_id;
