-- +goose Up
-- +goose StatementBegin

-- network_interfaces.used_by_id — DB-уровень защиты «один NIC — максимум один
-- attacher». До этой миграции AttachToInstance делал TOCTOU
-- (Get → check cur.UsedByID=="" → unconditional UPDATE), и параллельный
-- worker мог пройти guard, после чего второй UPDATE безусловно перезаписывал
-- ownership. Инцидент 2026-05-14 (KAC-52, см. workspace CLAUDE.md §«Within-service
-- refs — DB-уровень обязателен» / запрет #10): две Compute.Instance.Create указали
-- один existing_network_interface_id, обе прошли software-guard, второй pod
-- висел в ContainerCreating с «no address allocated» от Kube-OVN/multus.
--
-- Partial UNIQUE — последний рубеж: даже если service-слой пропустит race,
-- DB отдаст SQLSTATE 23505, repo маппит в ErrFailedPrecondition. В паре с этим
-- — conditional UPDATE в SetUsedBy (WHERE used_by_id = '' OR used_by_id = $new):
-- безопасно при concurrent attach, без потенциальной потери ownership.

CREATE UNIQUE INDEX network_interfaces_used_by_uniq
    ON network_interfaces (used_by_id)
    WHERE used_by_id <> '';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS network_interfaces_used_by_uniq;
-- +goose StatementEnd
