// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package clients

import (
	"sync"
	"time"
)

// existsCache — positive-only TTL-кеш существования cross-domain ресурса
// (region/zone/...). Кешируется ТОЛЬКО положительный результат («существует»)
// на ttl; отрицательный (NotFound) и ошибки НЕ кешируются — ресурс могут
// создать в любой момент, а недоступность owner-сервиса должна fail-closed
// пробрасываться на каждой мутации. Выделен из GeoZoneClient/GeoRegionClient,
// где это bookkeeping был скопирован байт-в-байт (LEAN dedup).
type existsCache struct {
	ttl   time.Duration
	mu    sync.RWMutex
	known map[string]time.Time // id → время, до которого «существует» валидно
}

// newExistsCache создаёт кеш с заданным TTL положительного результата.
func newExistsCache(ttl time.Duration) *existsCache {
	return &existsCache{ttl: ttl, known: make(map[string]time.Time)}
}

// hit сообщает, что id известен как существующий и запись ещё не истекла.
func (c *existsCache) hit(id string) bool {
	c.mu.RLock()
	exp, ok := c.known[id]
	c.mu.RUnlock()
	return ok && time.Now().Before(exp)
}

// remember фиксирует, что id существует, на ttl вперёд.
func (c *existsCache) remember(id string) {
	c.mu.Lock()
	c.known[id] = time.Now().Add(c.ttl)
	c.mu.Unlock()
}
