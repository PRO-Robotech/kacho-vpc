-- +goose Up
-- +goose StatementBegin

-- Откатываем partial UNIQUE network_interfaces_used_by_uniq из миграции 0016
-- (KAC-52). Ошибка дизайна:
--   `UNIQUE (used_by_id) WHERE used_by_id <> ''`
-- семантически означает «каждое значение used_by_id встречается максимум один
-- раз во всей таблице». Это запрещает **multi-NIC instance** — нормальный
-- AWS-ENI use case, когда один Compute.Instance имеет несколько NetworkInterface
-- (used_by_id у всех = id того же instance). На применении к боевому стенду
-- с уже существующим multi-NIC Instance миграция 0016 падала с SQLSTATE 23505,
-- блокируя rollout vpc.
--
-- Race-proof защита уже обеспечивается атомарным conditional UPDATE в
-- NetworkInterfaceRepo.SetUsedBy:
--     UPDATE network_interfaces
--        SET used_by_id=$3, ...
--      WHERE id=$1 AND (used_by_id = '' OR used_by_id = $3)
--     RETURNING ...
-- Это single-statement UPDATE на одной row, защищённый row-level lock-ом
-- Postgres: параллельный writer ждёт commit-а первого, видит обновлённый row,
-- CAS-условие не matches → 0 rows из RETURNING → service.ErrFailedPrecondition.
-- UNIQUE-индекс был **избыточным backstop-ом** и оказался семантически неверным.
--
-- См. workspace CLAUDE.md §«Within-service refs — DB-уровень обязателен» —
-- atomic CAS на одной row сам по себе race-proof; partial UNIQUE подходит для
-- инвариантов «значение поля уникально среди всех row», а не «текущее значение
-- этого row не должно быть перезаписано без условия».

DROP INDEX IF EXISTS network_interfaces_used_by_uniq;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- Down не воссоздаёт индекс — он семантически вредный (см. выше). Если в
-- будущем понадобится partial UNIQUE на (used_by_id), выражение должно быть
-- более узким, например (id, used_by_id) WHERE used_by_id <> '' (тривиально,
-- т.к. id — primary key) или специфичный invariant с CHECK на used_by_type.

-- +goose StatementEnd
