// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package clients

import (
	"sync"
	"time"
)

// valueCache — positive-only TTL-кеш cross-domain ресурса (region/zone/...).
// Кешируется ТОЛЬКО положительный результат (полная read-проекция ресурса) на
// ttl; отрицательный (NotFound) и ошибки НЕ кешируются — ресурс могут создать в
// любой момент, а недоступность owner-сервиса должна fail-closed пробрасываться
// на каждой мутации. Хранит само значение (не только факт существования), чтобы
// cache-hit и cache-miss возвращали идентичную проекцию — иначе caller, читающий
// поля закешированной записи, молча получил бы нулевые поля на весь TTL.
// Выделен из GeoZoneClient/GeoRegionClient, где bookkeeping был скопирован
// байт-в-байт (LEAN dedup).
type valueCache[T any] struct {
	ttl   time.Duration
	mu    sync.RWMutex
	known map[string]cacheEntry[T]
}

// cacheEntry — закешированное значение с моментом истечения.
type cacheEntry[T any] struct {
	val T
	exp time.Time // время, до которого запись валидна
}

// newValueCache создаёт кеш с заданным TTL положительного результата.
func newValueCache[T any](ttl time.Duration) *valueCache[T] {
	return &valueCache[T]{ttl: ttl, known: make(map[string]cacheEntry[T])}
}

// hit возвращает закешированное значение и true, если id известен и запись ещё
// не истекла; иначе — zero-value и false.
func (c *valueCache[T]) hit(id string) (T, bool) {
	c.mu.RLock()
	e, ok := c.known[id]
	c.mu.RUnlock()
	if !ok || !time.Now().Before(e.exp) {
		var zero T
		return zero, false
	}
	return e.val, true
}

// remember фиксирует значение для id на ttl вперёд.
func (c *valueCache[T]) remember(id string, val T) {
	c.mu.Lock()
	c.known[id] = cacheEntry[T]{val: val, exp: time.Now().Add(c.ttl)}
	c.mu.Unlock()
}
