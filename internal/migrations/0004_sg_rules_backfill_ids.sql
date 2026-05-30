-- +goose Up
-- +goose StatementBegin
-- =============================================================================
-- KAC-239 — backfill стабильных id для существующих SG-правил без id.
--
-- Корень: default-SG rules (NewDefaultSecurityGroupRules) и legacy-правила
-- сериализовались в JSONB security_groups.rules БЕЗ ключа `id` (sgRuleJSON.ID
-- имеет json:"id,omitempty"). Без id правило нельзя адресно удалить/изменить —
-- UI дублировал правила и не мог удалить egress (см. kacho-vpc#120).
--
-- Здесь: каждому элементу rules без непустого `id` присваиваем уникальный
-- opaque id формата 'enp' + 17 hex-символов (gen_random_uuid, built-in в pg16).
-- Формат отличается от crockford-base32 рантайм-id, но id правила — opaque
-- строка; для backfill достаточно уникальности и непустоты. Новые правила
-- получают crockford-id через builder/assignRuleIDs (код уже поправлен).
-- =============================================================================

UPDATE kacho_vpc.security_groups sg
SET rules = (
    SELECT jsonb_agg(
        CASE
            WHEN (elem ? 'id') AND (elem->>'id') <> '' THEN elem
            ELSE elem || jsonb_build_object(
                'id', 'enp' || substr(replace(gen_random_uuid()::text, '-', ''), 1, 17)
            )
        END
        ORDER BY ord
    )
    FROM jsonb_array_elements(sg.rules) WITH ORDINALITY AS t(elem, ord)
)
WHERE jsonb_array_length(sg.rules) > 0
  AND EXISTS (
      SELECT 1
      FROM jsonb_array_elements(sg.rules) AS e
      WHERE NOT (e ? 'id') OR (e->>'id') = ''
  );
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Backfill ids необратим (исходные правила были без id, восстановить «какое
-- именно было без id» невозможно). Down — no-op (id остаются; они валидны).
SELECT 1;
-- +goose StatementEnd
