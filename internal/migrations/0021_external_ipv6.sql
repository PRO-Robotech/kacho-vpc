-- +goose Up
-- +goose StatementBegin

-- KAC-60: поддержка External IPv6 в kacho-vpc.
--
-- Sparse counter-based IPAM (см. дизайн в
-- kacho-workspace/docs/superpowers/specs/2026-05-14-in-cluster-loadtest-hpa-10k-ipam-design.md
-- §9 backlog): материализованный freelist для IPv6 нерабочий — /64 = 18
-- квинтиллионов адресов. Вместо этого:
--   - ipv6_pool_cursors — счётчик "следующего offset" на pool;
--   - ipv6_allocated_ips — реально выданные IP (источник истины);
--   - ipv6_released_offsets — "переиспользуемые" offsets (от Delete).
--
-- Allocate: try pop released → fallback fresh from cursor → ip = pool_base + offset.
-- Delete: push offset в released (ON CONFLICT DO NOTHING — идемпотент).

-- 1) addresses.external_ipv6 JSONB — зеркало external_ipv4. JSON shape:
--    { address: "2001:db8::5", address_pool_id: "apl…",
--      zone_id: "ru-central1-a", requirements: { ddos_protection_provider: "...",
--      outgoing_smtp_capability: "..." } }
ALTER TABLE addresses ADD COLUMN external_ipv6 JSONB;

-- 2) Safety-net UNIQUE: один IPv6 — один Address внутри pool. Запрет #10
--    workspace CLAUDE.md (within-service refs only via DB) — backstop на
--    случай race / direct insert.
CREATE UNIQUE INDEX addresses_external_v6_pool_ip_uniq
    ON addresses ((external_ipv6 ->> 'address_pool_id'),
                  (external_ipv6 ->> 'address'))
    WHERE (external_ipv6 IS NOT NULL)
      AND ((external_ipv6 ->> 'address') <> '')
      AND ((external_ipv6 ->> 'address_pool_id') <> '');

-- 3) ipv6_pool_cursors: per-pool counter. NUMERIC(39,0) вмещает 2^128
--    (max IPv6 offset). Default = 1 (offset 0 — pool_base, сетевой адрес).
CREATE TABLE ipv6_pool_cursors (
    pool_id     TEXT NOT NULL PRIMARY KEY REFERENCES address_pools(id) ON DELETE CASCADE,
    next_offset NUMERIC(39,0) NOT NULL DEFAULT 1
);

-- 4) ipv6_allocated_ips: actually-allocated IPv6 addresses per pool.
--    PRIMARY KEY (pool_id, ip) гарантирует один IP — один Address.
--    UNIQUE (pool_id, offset) гарантирует interference-free allocate.
CREATE TABLE ipv6_allocated_ips (
    pool_id     TEXT NOT NULL REFERENCES address_pools(id) ON DELETE CASCADE,
    ip          INET NOT NULL,
    "offset"    NUMERIC(39,0) NOT NULL,
    address_id  TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (pool_id, ip),
    UNIQUE (pool_id, "offset")
);

CREATE INDEX ipv6_allocated_ips_pool_idx ON ipv6_allocated_ips (pool_id);

-- 5) ipv6_released_offsets: "freed" offsets, готовые к переиспользованию.
--    Allocate сначала пробует pop отсюда (FOR UPDATE SKIP LOCKED) — типичный
--    work-queue pattern, zero-contention.
CREATE TABLE ipv6_released_offsets (
    pool_id     TEXT NOT NULL REFERENCES address_pools(id) ON DELETE CASCADE,
    "offset"    NUMERIC(39,0) NOT NULL,
    PRIMARY KEY (pool_id, "offset")
);

CREATE INDEX ipv6_released_offsets_pool_idx ON ipv6_released_offsets (pool_id);

-- 6) Backfill: для каждого существующего pool с IPv6 CIDR — initialize cursor=1.
--    Существующих v6 pools на момент миграции нет (v4-only freelist в 0015) —
--    INSERT … ON CONFLICT DO NOTHING на всякий случай.
INSERT INTO ipv6_pool_cursors (pool_id, next_offset)
SELECT id, 1
FROM address_pools
WHERE EXISTS (
    SELECT 1 FROM unnest(cidr_blocks) AS c(cidr_str)
    WHERE family(cidr_str::cidr) = 6
)
ON CONFLICT (pool_id) DO NOTHING;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS ipv6_released_offsets;
DROP TABLE IF EXISTS ipv6_allocated_ips;
DROP TABLE IF EXISTS ipv6_pool_cursors;
DROP INDEX IF EXISTS addresses_external_v6_pool_ip_uniq;
ALTER TABLE addresses DROP COLUMN IF EXISTS external_ipv6;
-- +goose StatementEnd
