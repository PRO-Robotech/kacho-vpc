package clients

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// stubProjectClient — детерминированный stub upstream-а под unit-тесты.
// Считает вызовы Exists по folder-id; программируется ответом per-folder
// или общим default. -race-safe.
type stubProjectClient struct {
	mu        sync.Mutex
	calls     map[string]int
	answer    map[string]stubAnswer
	defaultRe stubAnswer
}

type stubAnswer struct {
	exists bool
	err    error
}

func newStubProjectClient() *stubProjectClient {
	return &stubProjectClient{
		calls:  make(map[string]int),
		answer: make(map[string]stubAnswer),
	}
}

func (s *stubProjectClient) setAnswer(folderID string, a stubAnswer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.answer[folderID] = a
}

func (s *stubProjectClient) setDefault(a stubAnswer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.defaultRe = a
}

func (s *stubProjectClient) Exists(_ context.Context, folderID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls[folderID]++
	if a, ok := s.answer[folderID]; ok {
		return a.exists, a.err
	}
	return s.defaultRe.exists, s.defaultRe.err
}

func (s *stubProjectClient) GetCloudIDFromProject(_ context.Context, _ string) (string, error) {
	return "", nil
}

func (s *stubProjectClient) callCount(folderID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[folderID]
}

// resetCalls обнуляет счётчик вызовов (для тестов, которые сначала прогревают
// кеш и затем измеряют upstream-нагрузку под параллельной загрузкой).
func (s *stubProjectClient) resetCalls() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = make(map[string]int)
}

// fakeClock — управляемые часы для unit-тестов TTL.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock(t time.Time) *fakeClock { return &fakeClock{now: t} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func TestCachedProjectClient_PositiveCacheHit(t *testing.T) {
	t.Parallel()

	stub := newStubProjectClient()
	stub.setAnswer("f1", stubAnswer{exists: true})
	clk := newFakeClock(time.Unix(1_000_000, 0))

	c := NewCachedProjectClient(stub, ProjectCacheConfig{
		PositiveTTL: 30 * time.Second,
		NegativeTTL: 5 * time.Second,
		MaxSize:     10,
		Clock:       clk.Now,
	})

	// 1-й вызов — miss → ходит в upstream.
	exists, err := c.Exists(context.Background(), "f1")
	if err != nil || !exists {
		t.Fatalf("1st call: got (%v, %v), want (true, nil)", exists, err)
	}
	if got := stub.callCount("f1"); got != 1 {
		t.Fatalf("after 1st: upstream calls = %d, want 1", got)
	}

	// 2-й вызов сразу — должен быть hit (TTL не истёк).
	exists, err = c.Exists(context.Background(), "f1")
	if err != nil || !exists {
		t.Fatalf("2nd call: got (%v, %v), want (true, nil)", exists, err)
	}
	if got := stub.callCount("f1"); got != 1 {
		t.Fatalf("after 2nd: upstream calls = %d, want 1 (cache hit expected)", got)
	}

	// Проматываем время на 29s — всё ещё кеш-hit.
	clk.Advance(29 * time.Second)
	if _, err := c.Exists(context.Background(), "f1"); err != nil {
		t.Fatalf("3rd call: err = %v, want nil", err)
	}
	if got := stub.callCount("f1"); got != 1 {
		t.Fatalf("after 3rd: upstream calls = %d, want 1 (still in TTL)", got)
	}
}

func TestCachedProjectClient_PositiveTTLExpiry(t *testing.T) {
	t.Parallel()

	stub := newStubProjectClient()
	stub.setAnswer("f1", stubAnswer{exists: true})
	clk := newFakeClock(time.Unix(2_000_000, 0))

	c := NewCachedProjectClient(stub, ProjectCacheConfig{
		PositiveTTL: 30 * time.Second,
		NegativeTTL: 5 * time.Second,
		MaxSize:     10,
		Clock:       clk.Now,
	})

	if _, err := c.Exists(context.Background(), "f1"); err != nil {
		t.Fatalf("1st: %v", err)
	}

	// Прокручиваем время за TTL → следующий вызов снова ходит в upstream.
	clk.Advance(31 * time.Second)
	if _, err := c.Exists(context.Background(), "f1"); err != nil {
		t.Fatalf("after TTL: %v", err)
	}
	if got := stub.callCount("f1"); got != 2 {
		t.Fatalf("after TTL expiry: upstream calls = %d, want 2", got)
	}
}

func TestCachedProjectClient_NegativeCache(t *testing.T) {
	t.Parallel()

	stub := newStubProjectClient()
	stub.setAnswer("missing", stubAnswer{exists: false})
	clk := newFakeClock(time.Unix(3_000_000, 0))

	c := NewCachedProjectClient(stub, ProjectCacheConfig{
		PositiveTTL: 30 * time.Second,
		NegativeTTL: 5 * time.Second,
		MaxSize:     10,
		Clock:       clk.Now,
	})

	// 1-й miss — ходит в upstream, получает (false, nil) → кешируется как negative.
	exists, err := c.Exists(context.Background(), "missing")
	if err != nil || exists {
		t.Fatalf("1st: got (%v, %v), want (false, nil)", exists, err)
	}
	if got := stub.callCount("missing"); got != 1 {
		t.Fatalf("after 1st: upstream calls = %d, want 1", got)
	}

	// Внутри negativeTTL — cache hit.
	clk.Advance(3 * time.Second)
	if exists, _ := c.Exists(context.Background(), "missing"); exists {
		t.Fatalf("within neg TTL: expected false")
	}
	if got := stub.callCount("missing"); got != 1 {
		t.Fatalf("within neg TTL: upstream calls = %d, want 1 (negative cache hit)", got)
	}

	// После negativeTTL — снова ходим в upstream (folder мог появиться).
	clk.Advance(3 * time.Second) // итого 6s > 5s
	if _, _ = c.Exists(context.Background(), "missing"); stub.callCount("missing") != 2 {
		t.Fatalf("after neg TTL expiry: upstream calls = %d, want 2", stub.callCount("missing"))
	}
}

func TestCachedProjectClient_NotFoundErrorCachedAsNegative(t *testing.T) {
	t.Parallel()

	// Если upstream возвращает gRPC codes.NotFound (что normalize-нутый
	// ProjectClient вообще-то не делает, но защищаемся), мы должны
	// маппить в (false, nil) и кешировать как negative.
	stub := newStubProjectClient()
	stub.setAnswer("nf", stubAnswer{err: status.Error(codes.NotFound, "Folder not found")})
	clk := newFakeClock(time.Unix(4_000_000, 0))

	c := NewCachedProjectClient(stub, ProjectCacheConfig{
		PositiveTTL: 30 * time.Second,
		NegativeTTL: 5 * time.Second,
		MaxSize:     10,
		Clock:       clk.Now,
	})

	exists, err := c.Exists(context.Background(), "nf")
	if err != nil || exists {
		t.Fatalf("1st: got (%v, %v), want (false, nil)", exists, err)
	}
	// Должно быть закешировано как negative.
	if _, err := c.Exists(context.Background(), "nf"); err != nil {
		t.Fatalf("2nd: %v", err)
	}
	if got := stub.callCount("nf"); got != 1 {
		t.Fatalf("upstream calls = %d, want 1 (NotFound cached)", got)
	}
}

func TestCachedProjectClient_FailOpenOnUnavailable(t *testing.T) {
	t.Parallel()

	// Unavailable — НЕ кешируется. Каждый вызов идёт в upstream.
	stub := newStubProjectClient()
	stub.setAnswer("rmdown", stubAnswer{err: status.Error(codes.Unavailable, "rm down")})
	clk := newFakeClock(time.Unix(5_000_000, 0))

	c := NewCachedProjectClient(stub, ProjectCacheConfig{
		PositiveTTL: 30 * time.Second,
		NegativeTTL: 5 * time.Second,
		MaxSize:     10,
		Clock:       clk.Now,
	})

	if _, err := c.Exists(context.Background(), "rmdown"); err == nil {
		t.Fatalf("expected error, got nil")
	}
	if _, err := c.Exists(context.Background(), "rmdown"); err == nil {
		t.Fatalf("2nd: expected error, got nil")
	}
	if got := stub.callCount("rmdown"); got != 2 {
		t.Fatalf("upstream calls = %d, want 2 (fail-open, no caching)", got)
	}

	// Когда upstream «выздоравливает» — следующий вызов получает корректный ответ.
	stub.setAnswer("rmdown", stubAnswer{exists: true})
	if exists, err := c.Exists(context.Background(), "rmdown"); err != nil || !exists {
		t.Fatalf("after recovery: got (%v, %v), want (true, nil)", exists, err)
	}
	if got := stub.callCount("rmdown"); got != 3 {
		t.Fatalf("after recovery: upstream calls = %d, want 3", got)
	}
}

func TestCachedProjectClient_FailOpenOnGenericError(t *testing.T) {
	t.Parallel()

	// Обычная (не grpc-status) ошибка — тоже не кешируется.
	stub := newStubProjectClient()
	stub.setAnswer("err1", stubAnswer{err: errors.New("network is unreachable")})

	c := NewCachedProjectClient(stub, ProjectCacheConfig{
		PositiveTTL: 30 * time.Second,
		NegativeTTL: 5 * time.Second,
		MaxSize:     10,
	})

	if _, err := c.Exists(context.Background(), "err1"); err == nil {
		t.Fatal("expected err, got nil")
	}
	if _, err := c.Exists(context.Background(), "err1"); err == nil {
		t.Fatal("expected err, got nil")
	}
	if got := stub.callCount("err1"); got != 2 {
		t.Fatalf("upstream calls = %d, want 2", got)
	}
}

func TestCachedProjectClient_LRUEviction(t *testing.T) {
	t.Parallel()

	stub := newStubProjectClient()
	stub.setDefault(stubAnswer{exists: true})

	c := NewCachedProjectClient(stub, ProjectCacheConfig{
		PositiveTTL: 30 * time.Second,
		NegativeTTL: 5 * time.Second,
		MaxSize:     3,
	})

	// Заполняем кеш до bound.
	for _, id := range []string{"a", "b", "c"} {
		if _, err := c.Exists(context.Background(), id); err != nil {
			t.Fatalf("warmup %q: %v", id, err)
		}
	}
	if c.Len() != 3 {
		t.Fatalf("Len = %d, want 3", c.Len())
	}

	// Touch "a" — теперь LRU-tail = "b".
	if _, err := c.Exists(context.Background(), "a"); err != nil {
		t.Fatal(err)
	}

	// Добавляем "d" — должен вытеснить "b" (oldest).
	if _, err := c.Exists(context.Background(), "d"); err != nil {
		t.Fatal(err)
	}
	if c.Len() != 3 {
		t.Fatalf("Len after eviction = %d, want 3", c.Len())
	}

	// LRU state at this point: d(front), a, c(back). "b" должен быть выселен.

	// "a", "c", "d" — всё ещё в кеше; не должны делать upstream call.
	beforeA := stub.callCount("a")
	beforeC := stub.callCount("c")
	beforeD := stub.callCount("d")
	for _, id := range []string{"a", "c", "d"} {
		if _, err := c.Exists(context.Background(), id); err != nil {
			t.Fatal(err)
		}
	}
	if stub.callCount("a") != beforeA {
		t.Errorf("\"a\" leaked upstream call (was %d, now %d)", beforeA, stub.callCount("a"))
	}
	if stub.callCount("c") != beforeC {
		t.Errorf("\"c\" leaked upstream call (was %d, now %d)", beforeC, stub.callCount("c"))
	}
	if stub.callCount("d") != beforeD {
		t.Errorf("\"d\" leaked upstream call (was %d, now %d)", beforeD, stub.callCount("d"))
	}

	// "b" должен быть выселен — следующий запрос пойдёт в upstream.
	// (Делаем эту проверку ПОСЛЕ touch a/c/d, чтобы их кеш-state не зависел
	// от subsequent eviction'ов.)
	beforeB := stub.callCount("b")
	if _, err := c.Exists(context.Background(), "b"); err != nil {
		t.Fatal(err)
	}
	if got := stub.callCount("b"); got != beforeB+1 {
		t.Fatalf("\"b\" upstream calls: before=%d, after=%d; want +1 (was evicted)", beforeB, got)
	}
}

func TestCachedProjectClient_DefaultsApplied(t *testing.T) {
	t.Parallel()

	// Все zero-fields → дефолты (30s / 5s / 10000).
	stub := newStubProjectClient()
	stub.setDefault(stubAnswer{exists: true})

	c := NewCachedProjectClient(stub, ProjectCacheConfig{})

	if c.posTTL != 30*time.Second {
		t.Errorf("posTTL = %v, want 30s", c.posTTL)
	}
	if c.negTTL != 5*time.Second {
		t.Errorf("negTTL = %v, want 5s", c.negTTL)
	}
	if c.maxSize != 10000 {
		t.Errorf("maxSize = %d, want 10000", c.maxSize)
	}
	if c.clock == nil {
		t.Error("clock = nil, want non-nil")
	}
}

func TestCachedProjectClient_ParallelAccess(t *testing.T) {
	t.Parallel()

	// -race-проверка под высокой конкуренцией: после прогрева кеша параллельная
	// нагрузка должна давать ровно 0 upstream-вызовов (cache-hit на всё),
	// а сам кеш не должен race'ить (фиксируется через -race + детерминированный
	// инвариант на счётчик).
	//
	// Раньше тест запускал параллельный sweep **без** прогрева и допускал
	// "небольшой thundering herd" с порогом 64. Под нагрузкой CI (GOMAXPROCS=2,
	// race-detector overhead) thundering herd подскакивал выше порога (>=68 в
	// failing CI run 26358985805, KAC-177 на 46cc37a) — тест flaky. Прогрев
	// устраняет thundering herd как переменную (свойство кеша уже проверено в
	// TestCachedProjectClient_PositiveCacheHit; здесь — race-safety map+LRU).
	folders := []string{"a", "b", "c", "d", "e"}
	stub := newStubProjectClient()
	stub.setDefault(stubAnswer{exists: true})

	c := NewCachedProjectClient(stub, ProjectCacheConfig{
		PositiveTTL: 30 * time.Second,
		NegativeTTL: 5 * time.Second,
		MaxSize:     1000,
	})

	// Phase 1: warmup — последовательно прогреваем кеш по всем folder'ам.
	// Гарантирует ровно по одному upstream-вызову на folder, без гонок.
	for _, id := range folders {
		exists, err := c.Exists(context.Background(), id)
		if err != nil || !exists {
			t.Fatalf("warmup folder=%q: exists=%v err=%v", id, exists, err)
		}
	}
	// Sanity: после прогрева — ровно 5 upstream-вызовов (по 1 на folder).
	warmupCalls := 0
	for _, id := range folders {
		warmupCalls += stub.callCount(id)
	}
	if warmupCalls != len(folders) {
		t.Fatalf("warmup upstream calls = %d, want %d", warmupCalls, len(folders))
	}
	stub.resetCalls()

	// Phase 2: parallel load. Все cache-hit'ы → 0 upstream-вызовов.
	const goroutines = 64
	const iter = 500

	var (
		wg         sync.WaitGroup
		errorCount atomic.Int64
	)

	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(seed int) {
			defer wg.Done()
			for i := 0; i < iter; i++ {
				// Несколько разных folder'ов чтобы LRU-list был под нагрузкой
				// (touch разных entries через MoveToFront → race-target).
				folder := folders[(seed+i)%len(folders)]
				exists, err := c.Exists(context.Background(), folder)
				if err != nil || !exists {
					errorCount.Add(1)
				}
			}
		}(g)
	}
	wg.Wait()
	if errorCount.Load() != 0 {
		t.Fatalf("got %d errors under concurrent load", errorCount.Load())
	}
	// После прогрева ни одного upstream-вызова быть не должно — всё cache-hit.
	// Без кеша было бы 64*500 = 32_000 вызовов.
	totalCalls := 0
	for _, id := range folders {
		totalCalls += stub.callCount(id)
	}
	if totalCalls != 0 {
		t.Errorf("upstream calls under load (after warmup) = %d; want 0 (cache miss / leak?)", totalCalls)
	}
	t.Logf("upstream calls = %d (down from %d without cache)", totalCalls, goroutines*iter)
}

func TestCachedProjectClient_GetCloudIDPassthrough(t *testing.T) {
	t.Parallel()

	// GetCloudID должен просто проксировать.
	stub := &cloudIDStub{cloud: "cl-123"}
	c := NewCachedProjectClient(stub, ProjectCacheConfig{})

	cloudID, err := c.GetCloudIDFromProject(context.Background(), "f1")
	if err != nil || cloudID != "cl-123" {
		t.Fatalf("GetCloudID: got (%q, %v), want (cl-123, nil)", cloudID, err)
	}
	if stub.cloudCalls != 1 {
		t.Errorf("upstream GetCloudID calls = %d, want 1", stub.cloudCalls)
	}
}

type cloudIDStub struct {
	cloud      string
	cloudCalls int
}

func (s *cloudIDStub) Exists(_ context.Context, _ string) (bool, error) { return true, nil }
func (s *cloudIDStub) GetCloudIDFromProject(_ context.Context, _ string) (string, error) {
	s.cloudCalls++
	return s.cloud, nil
}
