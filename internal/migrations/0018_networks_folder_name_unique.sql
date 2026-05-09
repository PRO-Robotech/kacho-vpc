-- +goose Up
--
-- Re-introduce UNIQUE (folder_id, name) на networks.
--
-- В upstream YC дублирующиеся имена внутри одного folder допускаются (probe
-- против real API подтверждал 200 OK на дубль). Мы сознательно расходимся
-- с YC parity ради архитектурной cleanliness: имя network должно идентифицировать
-- его в пределах folder, иначе observability/management ломается.
--
-- Тесты Newman, проверяющие YC-behaviour (200 OK на дубль), вынесены в
-- newman/collections/kacho-vpc-yc-diff.postman_collection.json — их прогон
-- фиксирует расхождение с YC, но **не** требуется для kacho-vpc CI.
--
-- Repo-mapping: 23505 на этом индексе уже превращается в gRPC AlreadyExists
-- (см. internal/repo/network_repo.go::Insert и unique.go::isUniqueViolation).
--
-- При наличии исторических дубликатов в БД миграция упадёт; в dev-стенде
-- БД зачищена (TRUNCATE), в prod применяется ручной cleanup перед migrate.

CREATE UNIQUE INDEX IF NOT EXISTS networks_folder_id_name_key
  ON networks (folder_id, name);

-- +goose Down

DROP INDEX IF EXISTS networks_folder_id_name_key;
