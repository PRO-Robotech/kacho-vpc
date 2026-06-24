// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package clients

import (
	"container/list"
	"context"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
)

// CachedProjectClient — TTL+LRU декоратор поверх любого repo.ProjectClient
// (port-интерфейс). Убирает gRPC RTT к kacho-iam из hot-path
// Network.Create/Subnet.Create/... при burst-нагрузке: без кеша каждый
// запрос делает hop в kacho-iam и упирает throughput в потолок RTT.
//
// Семантика кеширования Exists:
//   - Положительный результат (Exists=true) кешируется на полный TTL
//     (default 30s). Существование project — стабильное свойство (project
//     редко удаляется), но все-таки кешируем не вечно.
//   - Negative-результат (Exists=false / underlying NotFound) кешируется
//     на короткий negative-TTL (default 5s) — чтобы свеже-созданный
//     project быстро стал виден, но повторные «project не найден» не
//     хаммерили kacho-iam.
//   - Любая другая ошибка (Unavailable, Internal, DeadlineExceeded) —
//     НЕ кешируется, fail-open: следующий запрос попробует снова. Это
//     корректное поведение для transient ошибок kacho-iam.
//
// LRU bounded — защита от unbounded memory growth: при достижении
// MaxSize самый старый (по recency) entry вытесняется. Без bound на
// случайном workload (миллионы уникальных project-id за сессию) кеш мог
// бы дорасти до сотен МБ.
//
// Concurrency: один Mutex защищает map + LRU-list, все операции O(1)
// среднеамортизированно. Goroutine-safe (проверено unit-тестом с -race).
type CachedProjectClient struct {
	upstream repo.ProjectClient
	posTTL   time.Duration
	negTTL   time.Duration
	maxSize  int
	clock    func() time.Time // для тестов; в проде = time.Now

	mu     sync.Mutex
	cache  map[string]*list.Element
	lruLst *list.List
}

// projectCacheEntry — одна запись кеша.
type projectCacheEntry struct {
	projectID string
	exists    bool
	exp       time.Time
}

// Compile-time проверка: CachedProjectClient реализует port-интерфейс.
var _ repo.ProjectClient = (*CachedProjectClient)(nil)

// ProjectCacheConfig — параметры кеша. Все поля опциональны; нулевые
// значения заменяются на дефолты (positiveTTL=30s, negativeTTL=5s,
// maxSize=10000).
type ProjectCacheConfig struct {
	PositiveTTL time.Duration
	NegativeTTL time.Duration
	MaxSize     int
	// Clock — опциональный override таймера (для unit-тестов). Если nil,
	// используется time.Now.
	Clock func() time.Time
}

// NewCachedProjectClient оборачивает upstream ProjectClient TTL+LRU кешем
// для метода Exists.
//
// Применять как drop-in замену projectClient в composition root
// (`cmd/vpc/main.go`):
//
//	rawProjectClient := clients.NewProjectClient(iamConn)
//	projectClient := clients.NewCachedProjectClient(rawProjectClient, clients.ProjectCacheConfig{
//	    PositiveTTL: cfg.ProjectCacheTTL,
//	    NegativeTTL: cfg.ProjectCacheNegativeTTL,
//	    MaxSize:     cfg.ProjectCacheSize,
//	})
func NewCachedProjectClient(upstream repo.ProjectClient, cfg ProjectCacheConfig) *CachedProjectClient {
	if cfg.PositiveTTL <= 0 {
		cfg.PositiveTTL = 30 * time.Second
	}
	if cfg.NegativeTTL <= 0 {
		cfg.NegativeTTL = 5 * time.Second
	}
	if cfg.MaxSize <= 0 {
		cfg.MaxSize = 10000
	}
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	return &CachedProjectClient{
		upstream: upstream,
		posTTL:   cfg.PositiveTTL,
		negTTL:   cfg.NegativeTTL,
		maxSize:  cfg.MaxSize,
		clock:    cfg.Clock,
		cache:    make(map[string]*list.Element, cfg.MaxSize),
		lruLst:   list.New(),
	}
}

// Exists проверяет существование project через кеш + upstream.
func (c *CachedProjectClient) Exists(ctx context.Context, projectID string) (bool, error) {
	// Cache hit?
	if exists, ok := c.lookup(projectID); ok {
		return exists, nil
	}

	// Miss → upstream call.
	exists, err := c.upstream.Exists(ctx, projectID)
	if err != nil {
		// Различаем семантически:
		//   - codes.NotFound внутри err: наш ProjectClient уже маппит
		//     NotFound → (false, nil), поэтому сюда NotFound обычно не
		//     доходит. На всякий случай обработаем — кешируем negative.
		//   - Unavailable / Internal / DeadlineExceeded / любая другая
		//     ошибка — НЕ кешируем (fail-open). Возвращаем err как есть.
		if st, ok := status.FromError(err); ok && st.Code() == codes.NotFound {
			c.store(projectID, false, c.negTTL)
			return false, nil
		}
		return false, err
	}

	ttl := c.posTTL
	if !exists {
		ttl = c.negTTL
	}
	c.store(projectID, exists, ttl)
	return exists, nil
}

// lookup возвращает (exists, true) если кеш hit и не expired, иначе
// (_, false). Также промотирует entry в head LRU.
func (c *CachedProjectClient) lookup(projectID string) (bool, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	el, ok := c.cache[projectID]
	if !ok {
		return false, false
	}
	e := el.Value.(*projectCacheEntry)
	if c.clock().After(e.exp) {
		// Expired → evict.
		c.lruLst.Remove(el)
		delete(c.cache, projectID)
		return false, false
	}
	// LRU touch.
	c.lruLst.MoveToFront(el)
	return e.exists, true
}

// store записывает entry в кеш с указанным TTL; вытесняет LRU-tail
// если перешагнули maxSize.
func (c *CachedProjectClient) store(projectID string, exists bool, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	exp := c.clock().Add(ttl)
	if el, ok := c.cache[projectID]; ok {
		// Обновляем существующую запись.
		e := el.Value.(*projectCacheEntry)
		e.exists = exists
		e.exp = exp
		c.lruLst.MoveToFront(el)
		return
	}

	// Insert new.
	e := &projectCacheEntry{projectID: projectID, exists: exists, exp: exp}
	el := c.lruLst.PushFront(e)
	c.cache[projectID] = el

	// Evict LRU-tail если перешагнули bound.
	for c.lruLst.Len() > c.maxSize {
		tail := c.lruLst.Back()
		if tail == nil {
			break
		}
		te := tail.Value.(*projectCacheEntry)
		c.lruLst.Remove(tail)
		delete(c.cache, te.projectID)
	}
}

// Len возвращает текущее число entries (для тестов / observability).
func (c *CachedProjectClient) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lruLst.Len()
}
