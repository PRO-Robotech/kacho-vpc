-- +goose Up
-- +goose StatementBegin

-- Materialized freelist of available IPv4 addresses per pool.
-- Replaces the random-pick + UNIQUE-retry allocator with an atomic
-- SKIP LOCKED pop:
--   WITH picked AS (SELECT ip FROM address_pool_free_ips
--                   WHERE pool_id=$1 ORDER BY ip LIMIT 1 FOR UPDATE SKIP LOCKED)
--   DELETE … RETURNING ip → UPDATE addresses … in one statement.
-- Concurrent allocators don't contend on the same row.
--
-- Safety-net unique index addresses_external_pool_ip_uniq is already
-- in 0001_initial.sql:511 — no duplicate index needed here.

CREATE TABLE address_pool_free_ips (
    pool_id  TEXT NOT NULL REFERENCES address_pools(id) ON DELETE CASCADE,
    ip       INET NOT NULL,
    PRIMARY KEY (pool_id, ip)
);

CREATE INDEX address_pool_free_ips_pool_idx
    ON address_pool_free_ips (pool_id);

-- +goose StatementEnd

-- +goose StatementBegin

-- Backfill: for each existing pool, populate freelist from its IPv4 CIDR
-- blocks, excluding network/broadcast and any already-allocated IPs.
-- A recursive CTE expands the CIDR — Postgres' generate_series() doesn't
-- accept inet operands, so we step through the range manually.
DO $$
DECLARE
    pool_row RECORD;
    cidr_str TEXT;
BEGIN
    FOR pool_row IN SELECT id, cidr_blocks FROM address_pools LOOP
        FOREACH cidr_str IN ARRAY pool_row.cidr_blocks LOOP
            IF family(cidr_str::cidr) = 4 THEN
                WITH RECURSIVE ips(ip, stop) AS (
                    SELECT (network(cidr_str::cidr) + 1)::inet,
                           broadcast(cidr_str::cidr)::inet
                    UNION ALL
                    SELECT (ip + 1)::inet, stop FROM ips WHERE ip + 1 < stop
                )
                INSERT INTO address_pool_free_ips (pool_id, ip)
                SELECT pool_row.id, ips.ip
                FROM ips
                WHERE NOT EXISTS (
                    SELECT 1 FROM addresses a
                    WHERE (a.external_ipv4 ->> 'address_pool_id') = pool_row.id
                      AND (a.external_ipv4 ->> 'address') = host(ips.ip)
                )
                ON CONFLICT (pool_id, ip) DO NOTHING;
            END IF;
        END LOOP;
    END LOOP;
END $$;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS address_pool_free_ips;
-- +goose StatementEnd
