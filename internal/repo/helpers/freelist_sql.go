// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package helpers

// AllocateFromFreelistSQL — PG-native v4 freelist allocator. Используется в
// AllocateIPFromFreelist:
//
//   - target: row-lock адреса (`FOR UPDATE`) ТОЛЬКО если у него еще нет
//     external_ipv4.address — идемпотентность + сериализация конкурентных
//     allocate для одного address_id;
//   - pop one free IP из address_pool_free_ips для pool_id (FOR UPDATE SKIP
//     LOCKED — параллельные аллокаторы по РАЗНЫМ адресам получают разные row
//     без contention), но только если target существует (EXISTS) — иначе пул
//     не трогаем;
//   - DELETE из address_pool_free_ips (атомарно с pop'ом);
//   - UPDATE addresses.external_ipv4{address, address_pool_id} для address_id;
//
// Зачем target-guard: без него pop+DELETE происходил
// безусловно, а UPDATE мог не сматчиться (адрес уже имеет IP / не существует) —
// тогда IP вынут из freelist, но никому не присвоен (утечка). А два конкурентных
// allocate на один address_id оба попали бы в pop → второй перезаписывал бы
// external_ipv4, теряя первый IP. Теперь второй блокируется на target-lock,
// после commit первого видит непустой external_ipv4 → target пуст → pop не
// выполняется → 0 строк (ErrNoRows). Один SQL-statement.
// Возвращает host(r.ip)::text — assigned IP в host-нотации (без mask).
// Scan вернет pgx.ErrNoRows и когда free IP нет, и когда адрес уже имеет IP/не
// существует (target-guard отсёк pop). Различение — в repo-caller
// AllocateIPFromFreelist: он re-read'ит address FOR UPDATE и на непустом
// external_ipv4 возвращает существующий IP идемпотентно (зеркало
// AllocateExternalIPv6), иначе — ErrPoolExhausted (реальный exhausted).
const AllocateFromFreelistSQL = `
WITH pool_lock AS (
    -- FOR SHARE: совместим с другими allocate, конфликтует с AddressPool.Delete
    -- (FOR UPDATE) → Delete сериализуется против in-flight allocate (находка #15).
    -- Пул удален → pool_lock пуст → ничего не popаем.
    SELECT 1 FROM address_pools WHERE id = $1 FOR SHARE
), target AS (
    SELECT id FROM addresses
    WHERE id = $2
      AND COALESCE(external_ipv4 ->> 'address', '') = ''
    FOR UPDATE
), picked AS (
    SELECT ip FROM address_pool_free_ips
    WHERE pool_id = $1
      AND EXISTS (SELECT 1 FROM pool_lock)
      AND EXISTS (SELECT 1 FROM target)
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
