// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

// Package authzfilter реализует per-object фильтрацию List для kacho-vpc.
//
// Каждый публичный List use-case (Network / Subnet / SecurityGroup / RouteTable /
// Address / Gateway / NetworkInterface) через FGA AuthorizeService.ListObjects
// получает allow-list id ресурсов, видимых вызывающему subject, и сужает SQL до
// этого набора (repo.ListByIDs → WHERE id = ANY). Это дает настоящую per-object
// видимость вместо project-level решения «все или ничего»: видимый набор равен
// Check-allow набору (read==enforce), pagination применяется ПОСЛЕ фильтра, а
// отсутствие гранта означает, что объект пропадает из List и Get отдает NotFound
// (no-leak).
//
// Контракт (Decision / FGAFilter / cache / fail-closed) совпадает с authzfilter
// в kacho-compute. FGA action verb `list`/`get` на стороне сервера резолвится в
// relation `viewer` — ту же tier-relation, что энфорсит per-RPC Check
// (read==enforce). resource_type — FGA object type ("vpc_subnet", …).
package authzfilter

import (
	"container/list"
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	iamv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/iam/v1"
)

// Decision — результат фильтра для конкретного ListObjects-вызова.
//
//   - BypassAll=true: фильтр не применяется (wildcard/all-in-scope scope_grant,
//     cluster-admin, fail-open recovery). repo.List возвращает все project-scoped
//     строки.
//   - Empty=true: subject ничего не разрешено в этом resource_type — use-case
//     возвращает пустой список без обращения к repo (no-leak).
//   - AllowedIDs: explicit-список id, к которым subject имеет access. Используется
//     repo как `WHERE id = ANY($allowed)`.
type Decision struct {
	BypassAll  bool
	Empty      bool
	AllowedIDs []string
	// FromCache — true если ответ получен из cache (observability/tests).
	FromCache bool
	// FailOpen — true если решение принято в degraded-mode (FGA error + fail-open).
	FailOpen bool
}

// Filter — port для use-case. Реализация — *FGAFilter (через AuthorizeService.
// ListObjects) либо BypassFilter (list-filter disabled / dev).
type Filter interface {
	// ListAllowedIDs возвращает Decision для (subject, resourceType, action).
	//   resourceType — FGA object type ("vpc_subnet", "vpc_network", …).
	//   action       — semantic permission ("vpc.subnets.list") — на стороне сервера
	//                  резолвится в FGA relation (list → viewer; read==enforce).
	//   subject      — FGA subject string ("user:usr_alice" / "service_account:sva_x").
	ListAllowedIDs(ctx context.Context, subject, resourceType, action string) (Decision, error)
}

// BypassFilter — заглушка, всегда BypassAll=true (list-filter disabled).
type BypassFilter struct{}

// ListAllowedIDs возвращает BypassAll=true.
func (BypassFilter) ListAllowedIDs(_ context.Context, _, _, _ string) (Decision, error) {
	return Decision{BypassAll: true}, nil
}

// Config — параметры FGAFilter.
type Config struct {
	// Enabled — master-switch. false → ListAllowedIDs возвращает BypassAll=true.
	Enabled bool
	// Timeout — per-request deadline к AuthorizeService.ListObjects.
	Timeout time.Duration
	// CacheTTL — TTL одной записи decision-cache.
	CacheTTL time.Duration
	// CacheMaxEntries — bound для cache size.
	CacheMaxEntries int
	// MaxResults — server-enforced cap набора видимых id (default 10000).
	MaxResults int
	// FailOpen — на FGA error: true → BypassAll=true + warn; false → Unavailable
	// (fail-closed, default — security.md).
	FailOpen bool
}

// DefaultConfig — sane defaults: filter включен, 500ms timeout, 5s TTL, 10000
// entries, fail-closed.
func DefaultConfig() Config {
	return Config{
		Enabled:         true,
		Timeout:         500 * time.Millisecond,
		CacheTTL:        5 * time.Second,
		CacheMaxEntries: 10000,
		MaxResults:      10000,
		FailOpen:        false,
	}
}

// AuthorizeClient — узкий интерфейс к kacho-iam AuthorizeService (тестируемость).
// Сигнатура совпадает с сгенерированным AuthorizeServiceClient.ListObjects —
// NewIAMAuthorizeClient это thin pass-through.
type AuthorizeClient interface {
	ListObjects(ctx context.Context, in *iamv1.ListObjectsRequest, opts ...grpc.CallOption) (*iamv1.ListObjectsResponse, error)
}

// FGAFilter — продакшен-реализация Filter поверх AuthorizeService.ListObjects
// с in-memory TTL+LRU-кешем.
//
// Eviction — LRU (как в internal/clients/project_cache.go): при переполнении
// CacheMaxEntries вытесняется least-recently-used entry (хвост lruLst), а не
// произвольная (Go-map-randomized, возможно горячая) запись. Иначе burst
// distinct-List под нагрузкой трэшил бы кеш (мог выбросить свежую запись, оставив
// вот-вот-протухшую) и гнал бы лишний AuthorizeService.ListObjects QPS в kacho-iam.
type FGAFilter struct {
	cli AuthorizeClient
	cfg Config

	mu     sync.Mutex
	cache  map[string]*list.Element
	lruLst *list.List
}

type cacheEntry struct {
	key      string
	decision Decision
	expires  time.Time
}

// NewFGAFilter создает фильтр. cli == nil → всегда BypassAll (graceful start без iam).
func NewFGAFilter(cli AuthorizeClient, cfg Config) *FGAFilter {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 500 * time.Millisecond
	}
	if cfg.CacheTTL <= 0 {
		cfg.CacheTTL = 5 * time.Second
	}
	if cfg.CacheMaxEntries <= 0 {
		cfg.CacheMaxEntries = 10000
	}
	if cfg.MaxResults <= 0 {
		cfg.MaxResults = 10000
	}
	return &FGAFilter{
		cli:    cli,
		cfg:    cfg,
		cache:  make(map[string]*list.Element, cfg.CacheMaxEntries),
		lruLst: list.New(),
	}
}

// ListAllowedIDs — основной entry-point.
func (f *FGAFilter) ListAllowedIDs(ctx context.Context, subject, resourceType, action string) (Decision, error) {
	if !f.cfg.Enabled || f.cli == nil {
		return Decision{BypassAll: true}, nil
	}
	if subject == "" {
		// Anonymous caller — fail-closed (use-case передает subject из metadata).
		return Decision{}, status.Error(codes.Unauthenticated, "list filter: subject required")
	}
	if resourceType == "" || action == "" {
		return Decision{}, fmt.Errorf("authzfilter: resourceType and action required")
	}

	key := cacheKey(subject, resourceType, action)
	if d, ok := f.getCache(key); ok {
		d.FromCache = true
		return d, nil
	}

	callCtx, cancel := context.WithTimeout(ctx, f.cfg.Timeout)
	defer cancel()

	resp, err := f.cli.ListObjects(callCtx, &iamv1.ListObjectsRequest{
		Subject:      subject,
		ResourceType: resourceType,
		Action:       action,
		MaxResults:   int64(f.cfg.MaxResults),
	})
	if err != nil {
		return f.handleErr(err)
	}

	// WildcardGrant — subject имеет unbounded reach (scope_grant на весь
	// account/project). repo возвращает все project-scoped строки.
	if resp.GetWildcardGrant() {
		d := Decision{BypassAll: true}
		f.putCache(key, d)
		return d, nil
	}

	ids := append([]string(nil), resp.GetResourceIds()...)
	sort.Strings(ids) // deterministic ordering for stable pagination

	d := Decision{
		AllowedIDs: ids,
		Empty:      len(ids) == 0,
	}
	f.putCache(key, d)
	return d, nil
}

// handleErr — реакция по fail-open / fail-closed.
func (f *FGAFilter) handleErr(err error) (Decision, error) {
	if f.cfg.FailOpen {
		return Decision{BypassAll: true, FailOpen: true}, nil
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return Decision{}, status.Errorf(codes.Unavailable, "list filter: AuthorizeService.ListObjects deadline exceeded after %s", f.cfg.Timeout)
	}
	if s, ok := status.FromError(err); ok && s.Code() != codes.OK && s.Code() != codes.Unknown {
		return Decision{}, status.Errorf(codes.Unavailable, "list filter: AuthorizeService.ListObjects %s: %s", s.Code(), s.Message())
	}
	return Decision{}, status.Errorf(codes.Unavailable, "list filter: AuthorizeService.ListObjects: %v", err)
}

func cacheKey(subject, resourceType, action string) string {
	return subject + "|" + resourceType + "|" + action
}

func (f *FGAFilter) getCache(key string) (Decision, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	el, ok := f.cache[key]
	if !ok {
		return Decision{}, false
	}
	e := el.Value.(*cacheEntry)
	if time.Now().After(e.expires) {
		f.lruLst.Remove(el)
		delete(f.cache, key)
		return Decision{}, false
	}
	f.lruLst.MoveToFront(el) // LRU touch
	d := e.decision
	if len(d.AllowedIDs) > 0 {
		idsCopy := make([]string, len(d.AllowedIDs))
		copy(idsCopy, d.AllowedIDs)
		d.AllowedIDs = idsCopy
	}
	return d, true
}

func (f *FGAFilter) putCache(key string, d Decision) {
	f.mu.Lock()
	defer f.mu.Unlock()
	exp := time.Now().Add(f.cfg.CacheTTL)
	if el, ok := f.cache[key]; ok {
		e := el.Value.(*cacheEntry)
		e.decision = d
		e.expires = exp
		f.lruLst.MoveToFront(el)
		return
	}
	el := f.lruLst.PushFront(&cacheEntry{key: key, decision: d, expires: exp})
	f.cache[key] = el
	// Вытеснить LRU-tail пока перешагиваем bound.
	for f.lruLst.Len() > f.cfg.CacheMaxEntries {
		tail := f.lruLst.Back()
		if tail == nil {
			break
		}
		te := tail.Value.(*cacheEntry)
		f.lruLst.Remove(tail)
		delete(f.cache, te.key)
	}
}

// Size — текущий размер cache (observability/tests).
func (f *FGAFilter) Size() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lruLst.Len()
}

// Invalidate — удаляет записи subject'а из cache (LISTEN/NOTIFY-driven inval).
func (f *FGAFilter) Invalidate(subject string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	prefix := subject + "|"
	for k, el := range f.cache {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			f.lruLst.Remove(el)
			delete(f.cache, k)
		}
	}
}
