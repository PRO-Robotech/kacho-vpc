-- Copyright (c) PRO-Robotech
-- SPDX-License-Identifier: BUSL-1.1

-- 0009: additive nullable-денормализация account_id на kacho_vpc.operations.
--
-- ЗАЧЕМ: corelib operations.Repo.CreateWithPrincipal теперь INSERT'ит account_id
-- безусловно (общий LRO-writer, используемый каждой async-мутацией). Без этой
-- колонки INSERT падает (SQLSTATE 42703 undefined_column), ломая КАЖДУЮ vpc
-- Create/Update/Delete → Operation. Колонку обязан добавить каждый сервис со
-- своей operations-таблицей, чтобы общий INSERT проходил.
--
-- ADDITIVE / BACK-COMPAT: nullable, без DEFAULT, без NOT NULL. Для vpc колонка
-- остается NULL: account_id — IAM-only денормализация (vpc operation metadata не
-- содержит account_id, поэтому corelib extractAccountID → "" → SQL NULL).
--
-- partial index (account_id, created_at, id) WHERE account_id IS NOT NULL —
-- ради parity с iam/corelib (account-scoped cursor pagination) и чтобы не
-- индексировать all-NULL vpc-строки (без bloat).

-- +goose Up
ALTER TABLE kacho_vpc.operations
  ADD COLUMN account_id text NULL;

CREATE INDEX operations_account_id_idx
  ON kacho_vpc.operations (account_id, created_at, id)
  WHERE account_id IS NOT NULL;

-- +goose Down

DROP INDEX IF EXISTS kacho_vpc.operations_account_id_idx;

ALTER TABLE kacho_vpc.operations
  DROP COLUMN account_id;
