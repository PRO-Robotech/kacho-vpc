-- KAC-57: follow-up к KAC-56 — backfill subnet.route_table_id для legacy данных.
--
-- Триггеры миграции 0019 (rt_auto_assoc_subnets_trg, subnet_auto_pick_rt_trg)
-- срабатывают только на новые INSERT'ы. Existing-данные, созданные ДО 0019, не
-- получают auto-association — Subnet'ы в сети с RT остаются с
-- `route_table_id IS NULL`. Эта миграция применяет одноразовый backfill: для
-- каждой такой Subnet подставляет самую раннюю по `created_at` RouteTable из
-- её сети (та же логика что в triggers).
--
-- UPDATE триггерит `subnets_outbox_emit_route_table_change_trg` — backfill
-- эмитит `Subnet.UPDATED` в `vpc_outbox` для каждой затронутой Subnet'и (watch-
-- клиенты увидят consistency-event).
--
-- Идемпотентна: WHERE route_table_id IS NULL гарантирует, что повторный
-- прогон ничего не сделает (после первого UPDATE поле уже NOT NULL).
--
-- +goose Up
-- +goose StatementBegin

UPDATE subnets s
   SET route_table_id = (
     SELECT id FROM route_tables r
      WHERE r.network_id = s.network_id
      ORDER BY r.created_at ASC, r.id ASC
      LIMIT 1)
 WHERE s.route_table_id IS NULL
   AND EXISTS (SELECT 1 FROM route_tables r2 WHERE r2.network_id = s.network_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Down — no-op. Откатывать backfill не имеет смысла (нельзя надёжно отличить
-- backfilled-rows от auto-picked при INSERT через триггер). Если нужно
-- ре-применить backfill (например, после restore из снапшота) — UP идемпотентен.
SELECT 1;
-- +goose StatementEnd
