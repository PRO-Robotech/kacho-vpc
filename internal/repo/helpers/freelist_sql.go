package helpers

// AllocateFromFreelistSQL — PG-native v4 freelist allocator. Используется в
// AllocateIPFromFreelist:
//
//   - pop one free IP из address_pool_free_ips для pool_id (FOR UPDATE SKIP
//     LOCKED — параллельные аллокаторы получают разные row без contention);
//   - DELETE из address_pool_free_ips (атомарно с pop'ом);
//   - UPDATE addresses.external_ipv4{address, address_pool_id} для address_id;
//
// один SQL-statement, нулевая contention между параллельными аллокаторами.
// Возвращает host(r.ip)::text — assigned IP в host-нотации (без mask).
// ErrPoolExhausted — если ни одного free IP нет (Scan вернёт pgx.ErrNoRows).
const AllocateFromFreelistSQL = `
WITH picked AS (
    SELECT ip FROM address_pool_free_ips
    WHERE pool_id = $1
    ORDER BY ip
    LIMIT 1 FOR UPDATE SKIP LOCKED
), removed AS (
    DELETE FROM address_pool_free_ips f
    USING picked p
    WHERE f.pool_id = $1 AND f.ip = p.ip
    RETURNING f.ip
)
UPDATE addresses a
SET external_ipv4 = jsonb_set(
    jsonb_set(COALESCE(a.external_ipv4, '{}'::jsonb), '{address}', to_jsonb(host(r.ip))),
    '{address_pool_id}', to_jsonb($1::text)
)
FROM removed r
WHERE a.id = $2
RETURNING host(r.ip)::text;
`
