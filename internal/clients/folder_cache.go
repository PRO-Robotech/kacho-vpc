package clients

import (
	"container/list"
	"context"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PRO-Robotech/kacho-vpc/internal/ports"
)

// CachedFolderClient — TTL+LRU декоратор поверх любого
// ports.FolderClient (port-интерфейс). Цель — убрать gRPC RTT к
// resource-manager из hot-path Network.Create/Subnet.Create/... при
// burst-нагрузке (на ~10k RPS Network.Create без кеша каждый запрос
// делал hop в RM → потолок ~3K Create/sec).
//
// Semantics:
//   - Положительный результат (Exists=true) кешируется на полный TTL
//     (default 30s). Существование folder — стабильное свойство (folder
//     редко удаляется), но всё-таки кешируем не вечно.
//   - Negative-результат (Exists=false / underlying NotFound) кешируется
//     на короткий negative-TTL (default 5s) — чтобы свеже-созданный
//     folder быстро стал виден, но повторные «folder не найден» не
//     хаммерили RM при integration-test storms.
//   - Любая другая ошибка (Unavailable, Internal, DeadlineExceeded) —
//     НЕ кешируется, fail-open: следующий запрос попробует снова. Это
//     корректное поведение для transient ошибок RM.
//
// LRU bounded — защита от unbounded memory growth: при достижении
// MaxSize самый старый (по recency) entry вытесняется. Без bound на
// adversarial / случайный workload (миллионы уникальных folder-id за
// сессию) кеш мог бы дорасти до сотен МБ.
//
// Concurrency: один RWMutex защищает map + LRU-list. Все операции —
// O(1) среднеамортизированно. Goroutine-safe; гонок нет (см. unit-тест
// с -race).
//
// GetCloudID — НЕ декорируется здесь (уже есть отдельный TTL-кеш в
// FolderClient под другую TTL-семантику folder→cloud-id immutable
// binding); CachedFolderClient проксирует GetCloudID к upstream без
// доп. кеша.
type CachedFolderClient struct {
	upstream ports.FolderClient
	posTTL   time.Duration
	negTTL   time.Duration
	maxSize  int
	clock    func() time.Time // для тестов; в проде = time.Now

	mu     sync.Mutex
	cache  map[string]*list.Element
	lruLst *list.List
}

// folderCacheEntry — одна запись кеша.
type folderCacheEntry struct {
	folderID string
	exists   bool
	exp      time.Time
}

// Compile-time проверка: CachedFolderClient реализует port-интерфейс.
var _ ports.FolderClient = (*CachedFolderClient)(nil)

// FolderCacheConfig — параметры кеша. Все поля опциональны; нулевые
// значения заменяются на дефолты (positiveTTL=30s, negativeTTL=5s,
// maxSize=10000).
type FolderCacheConfig struct {
	PositiveTTL time.Duration
	NegativeTTL time.Duration
	MaxSize     int
	// Clock — опциональный override таймера (для unit-тестов). Если nil,
	// используется time.Now.
	Clock func() time.Time
}

// NewCachedFolderClient оборачивает upstream FolderClient TTL+LRU кешем
// для метода Exists. GetCloudID проксируется без изменений.
//
// Применять как drop-in замену folderClient в composition root
// (`cmd/vpc/main.go`):
//
//	rawFolderClient := clients.NewFolderClient(rmConn)
//	folderClient := clients.NewCachedFolderClient(rawFolderClient, clients.FolderCacheConfig{
//	    PositiveTTL: cfg.FolderCacheTTL,
//	    NegativeTTL: cfg.FolderCacheNegativeTTL,
//	    MaxSize:     cfg.FolderCacheSize,
//	})
func NewCachedFolderClient(upstream ports.FolderClient, cfg FolderCacheConfig) *CachedFolderClient {
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
	return &CachedFolderClient{
		upstream: upstream,
		posTTL:   cfg.PositiveTTL,
		negTTL:   cfg.NegativeTTL,
		maxSize:  cfg.MaxSize,
		clock:    cfg.Clock,
		cache:    make(map[string]*list.Element, cfg.MaxSize),
		lruLst:   list.New(),
	}
}

// Exists проверяет существование folder через кеш + upstream.
func (c *CachedFolderClient) Exists(ctx context.Context, folderID string) (bool, error) {
	// Cache hit?
	if exists, ok := c.lookup(folderID); ok {
		return exists, nil
	}

	// Miss → upstream call.
	exists, err := c.upstream.Exists(ctx, folderID)
	if err != nil {
		// Различаем семантически:
		//   - codes.NotFound внутри err: но fortсуществующий upstream
		//     (наш FolderClient) уже маппит NotFound → (false, nil),
		//     поэтому сюда NotFound обычно не доходит. На всякий случай
		//     обработаем — кешируем negative.
		//   - Unavailable / Internal / DeadlineExceeded / любая другая
		//     ошибка — НЕ кешируем (fail-open). Возвращаем err как есть.
		if st, ok := status.FromError(err); ok && st.Code() == codes.NotFound {
			c.store(folderID, false, c.negTTL)
			return false, nil
		}
		return false, err
	}

	ttl := c.posTTL
	if !exists {
		ttl = c.negTTL
	}
	c.store(folderID, exists, ttl)
	return exists, nil
}

// GetCloudID проксирует к upstream без дополнительного кеша. Upstream
// FolderClient (см. resourcemanager_client.go) уже держит собственный
// TTL-кеш для cloud-id привязки.
func (c *CachedFolderClient) GetCloudID(ctx context.Context, folderID string) (string, error) {
	return c.upstream.GetCloudID(ctx, folderID)
}

// lookup возвращает (exists, true) если кеш hit и не expired, иначе
// (_, false). Также промотирует entry в head LRU.
func (c *CachedFolderClient) lookup(folderID string) (bool, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	el, ok := c.cache[folderID]
	if !ok {
		return false, false
	}
	e := el.Value.(*folderCacheEntry)
	if c.clock().After(e.exp) {
		// Expired → evict.
		c.lruLst.Remove(el)
		delete(c.cache, folderID)
		return false, false
	}
	// LRU touch.
	c.lruLst.MoveToFront(el)
	return e.exists, true
}

// store записывает entry в кеш с указанным TTL; вытесняет LRU-tail
// если перешагнули maxSize.
func (c *CachedFolderClient) store(folderID string, exists bool, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	exp := c.clock().Add(ttl)
	if el, ok := c.cache[folderID]; ok {
		// Обновляем существующую запись.
		e := el.Value.(*folderCacheEntry)
		e.exists = exists
		e.exp = exp
		c.lruLst.MoveToFront(el)
		return
	}

	// Insert new.
	e := &folderCacheEntry{folderID: folderID, exists: exists, exp: exp}
	el := c.lruLst.PushFront(e)
	c.cache[folderID] = el

	// Evict LRU-tail если перешагнули bound.
	for c.lruLst.Len() > c.maxSize {
		tail := c.lruLst.Back()
		if tail == nil {
			break
		}
		te := tail.Value.(*folderCacheEntry)
		c.lruLst.Remove(tail)
		delete(c.cache, te.folderID)
	}
}

// Len возвращает текущее число entries (для тестов / observability).
func (c *CachedFolderClient) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lruLst.Len()
}
