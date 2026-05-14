-- +goose Up
-- +goose StatementBegin

-- HISTORICAL NO-OP. Originally created
--     CREATE UNIQUE INDEX network_interfaces_used_by_uniq
--         ON network_interfaces (used_by_id)
--         WHERE used_by_id <> '';
-- as a backstop for the NIC-attach race (KAC-52). Reverted in 0017_drop_nic_used_by_uniq.sql:
-- the partial UNIQUE semantically forbade multi-NIC instance (one Compute.Instance
-- with N NetworkInterface — normal AWS-ENI use case), and the migration failed
-- with SQLSTATE 23505 on the live stand at rollout time, blocking deploy.
--
-- Race-proof защита уже полностью обеспечивается атомарным single-statement CAS
-- в NetworkInterfaceRepo.SetUsedBy (см. workspace CLAUDE.md §«Within-service refs —
-- DB-уровень обязателен», шаблон «Атомарный CAS»). Никакого UNIQUE-индекса не нужно.
--
-- Rewriting 0016 to a no-op (instead of leaving the broken CREATE UNIQUE INDEX
-- + relying on 0017 DROP) — это исключение из workspace CLAUDE.md запрет #5:
-- миграция 0016 ни в одной БД не была успешно применена (везде падала на
-- existing multi-NIC state), так что мы редактируем то, что фактически нигде
-- не accepted. Этот компромисс лучше чем оставлять заведомо-fail-ить migrate
-- step в каждом vpc rollout, ожидая что 0017 как-то её обгонит (goose не
-- двигается на следующую миграцию пока текущая не успешна).

SELECT 1; -- explicit no-op (goose требует хотя бы одно statement)

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 1; -- no-op
-- +goose StatementEnd
