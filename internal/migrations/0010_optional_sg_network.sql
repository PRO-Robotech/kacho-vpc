-- +goose Up
-- kacho-proto#8: SecurityGroup может быть создана без привязки к сети
-- ("глобальная" / folder-level / unbound SG). Снимаем NOT NULL с
-- security_groups.network_id; пустой network_id в домене хранится как SQL NULL,
-- чтобы FK security_groups_network_id_fkey не срабатывал на ''. Partial index
-- sg_network_idx и фильтр List по network_id='<id>' продолжают работать —
-- они просто не матчат NULL-строки.
ALTER TABLE security_groups ALTER COLUMN network_id DROP NOT NULL;

-- +goose Down
-- Откат: вернуть NOT NULL. ВНИМАНИЕ — если в таблице есть SG с network_id IS NULL
-- (созданные после применения этой миграции как unbound), DROP NOT NULL обратно
-- упадёт. Down здесь best-effort: для чистого rollback такие строки нужно
-- предварительно удалить/переназначить.
ALTER TABLE security_groups ALTER COLUMN network_id SET NOT NULL;
