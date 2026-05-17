-- +goose Up

-- KAC-113 (subtask KAC-105 / epic KAC-104 IAM DoD #4):
-- Раскатка corelib/migrations/common/0002_operations_principal.sql в kacho-vpc.
--
-- Добавляет principal-поля в kacho_vpc.operations. На E0/без auth заполняется
-- stub'ом 'system'/'bootstrap'/'System' через DEFAULT — это же значение
-- использует operations.SystemPrincipal() и operations.PrincipalFromContext(emptyCtx).
-- На E2+ auth-interceptor api-gateway пробрасывает реального principal'а через
-- operations.WithPrincipal -> use-case -> repo.CreateWithPrincipal.
--
-- NOT NULL DEFAULT работает на ALTER TABLE — Postgres back-fill'ит существующие
-- строки атомарно (Postgres 11+).

SET search_path TO kacho_vpc, public;

ALTER TABLE kacho_vpc.operations
  ADD COLUMN principal_type         TEXT NOT NULL DEFAULT 'system',
  ADD COLUMN principal_id           TEXT NOT NULL DEFAULT 'bootstrap',
  ADD COLUMN principal_display_name TEXT NOT NULL DEFAULT 'System';

-- +goose Down

SET search_path TO kacho_vpc, public;

ALTER TABLE kacho_vpc.operations
  DROP COLUMN principal_type,
  DROP COLUMN principal_id,
  DROP COLUMN principal_display_name;
