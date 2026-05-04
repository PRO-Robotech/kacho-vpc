-- +goose Up
--
-- vpc_outbox — durable event log для internal Watch API.
-- Service записывает event в одной транзакции с Insert/Update/Delete целевой
-- ресурсной таблицы; controllers читают через gRPC stream subscriber.
--
-- Архитектура: Outbox pattern + LISTEN/NOTIFY wake-up. См. findings/INTERNAL-WATCH-API.md.

CREATE TABLE vpc_outbox (
  sequence_no   BIGSERIAL    PRIMARY KEY,
  resource_kind TEXT         NOT NULL,                -- "Network", "Subnet", "Address", "RouteTable", "SecurityGroup"
  resource_id   TEXT         NOT NULL,
  event_type    TEXT         NOT NULL,                -- "CREATED", "UPDATED", "DELETED"
  payload       JSONB        NOT NULL DEFAULT '{}',
  created_at    TIMESTAMPTZ  NOT NULL DEFAULT now(),
  processed_at  TIMESTAMPTZ                            -- зарезервировано под GC; controllers сейчас не пишут сюда
);

CREATE INDEX vpc_outbox_seq_idx ON vpc_outbox (sequence_no);
CREATE INDEX vpc_outbox_kind_idx ON vpc_outbox (resource_kind, sequence_no);

-- LISTEN/NOTIFY trigger для real-time wake-up subscribers.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION vpc_outbox_notify() RETURNS trigger AS $$
BEGIN
  PERFORM pg_notify('vpc_outbox', NEW.sequence_no::text);
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER vpc_outbox_notify_trg
  AFTER INSERT ON vpc_outbox
  FOR EACH ROW EXECUTE FUNCTION vpc_outbox_notify();

-- Cursor-таблица для stream-subscribers (kacho-vpc-controllers).
-- subscriber_id = "address-allocator" / "default-sg" / etc.
-- last_sequence_no = последнее processed event.
CREATE TABLE vpc_watch_cursors (
  subscriber_id     TEXT         PRIMARY KEY,
  last_sequence_no  BIGINT       NOT NULL DEFAULT 0,
  updated_at        TIMESTAMPTZ  NOT NULL DEFAULT now()
);

-- +goose Down

DROP TABLE IF EXISTS vpc_watch_cursors;
DROP TRIGGER IF EXISTS vpc_outbox_notify_trg ON vpc_outbox;
DROP FUNCTION IF EXISTS vpc_outbox_notify();
DROP TABLE IF EXISTS vpc_outbox;
