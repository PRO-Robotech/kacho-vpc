-- +goose Up
-- Address referrer-tracking (YC-like): кто использует адрес. Surfaced через
-- SubnetService.ListUsedAddresses (UsedAddress.references[]) и Address.used.
-- Один referrer на адрес (PK на address_id) — для control-plane достаточно
-- модели одна-ВМ↔один-адрес (YC допускает несколько references на адрес).
--
-- addresses.used трекается сервис-слоем синхронно: true ровно когда есть
-- referrer-row. При удалении адреса FK CASCADE убирает referrer-row (сам
-- address-row тоже уходит). addresses.used колонка уже есть в 0001_initial.sql.

CREATE TABLE address_references (
    address_id    text        PRIMARY KEY REFERENCES addresses(id) ON DELETE CASCADE,
    referrer_type text        NOT NULL,
    referrer_id   text        NOT NULL,
    referrer_name text        NOT NULL DEFAULT '',
    attached_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX address_references_referrer_idx ON address_references (referrer_type, referrer_id);

-- +goose Down
DROP TABLE IF EXISTS address_references;
