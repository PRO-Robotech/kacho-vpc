// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

// Package clients — единая точка сборки gRPC-клиентских соединений из kacho-vpc к
// peer-сервисам (kacho-iam, kacho-geo) единым паттерном (retries, LB, TLS,
// metrics), без отдельного dial-кода на каждый клиент.
//
// Builder — обертка над corlib `ClientFromAddress(...)` с дефолтами kacho-vpc
// (retries=3, dialTimeout=10s, KeepAlive 30s, userAgent="kacho-vpc"). Client-side
// round_robin LB включается флагом DNSLB: corlib builder не поддерживает
// `grpc.WithDefaultServiceConfig` нативно, поэтому при DNSLB=true используется
// прямой `grpc.NewClient`. Оба пути применяют одни и те же дефолты — retries
// (retry-on-Unavailable), dial-backoff, keepalive, userAgent, creds; на DNSLB-пути
// retry едет через service-config `retryPolicy`, а dial-backoff — через
// `grpc.WithConnectParams` (см. buildDNSLBConn), зеркаля corlib WithMaxRetries /
// WithDialDuration. Общие константы, одинаковый профиль отказоустойчивости.
//
// Возвращает `Conn` — interface `grpc.ClientConnInterface + io.Closer`. Generated
// proto-клиенты (`iamv1.NewProjectServiceClient(conn)` и т.п.) принимают
// `grpc.ClientConnInterface` → работают и с corlib `ClientConn`, и с
// `*grpc.ClientConn`.
package clients

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"strings"
	"time"

	corlibgrpc "github.com/H-BF/corlib/client/grpc"
	"google.golang.org/grpc"
	grpcbackoff "google.golang.org/grpc/backoff"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
	_ "google.golang.org/grpc/resolver/dns" // регистрирует dns:/// resolver (для DNSLB)
)

// Conn — то, что нужно generated proto-клиентам (`grpc.ClientConnInterface`)
// плюс возможность Close. Подходит и corlib `ClientConn`, и `*grpc.ClientConn`.
type Conn interface {
	grpc.ClientConnInterface
	io.Closer
}

// BuildOptions — параметры сборки cross-service gRPC-клиента.
//
// Endpoint — host:port (или `dns:///host:port`, если уже с префиксом).
// TLS=true → credentials.NewTLS(MinVersion=1.2); иначе insecure (dev).
// DNSLB=true → префикс `dns:///` + service-config с round_robin LB.
//
// Retries / DialTimeout / KeepAliveTime — дефолты задаются через withDefaults().
type BuildOptions struct {
	Endpoint      string        // host:port (либо уже dns:///host:port)
	TLS           bool          // true → TLS 1.2+; false → insecure (dev)
	DNSLB         bool          // true → dns:///prefix + round_robin LB
	Retries       uint          // gRPC retries on Unavailable (default 3)
	DialTimeout   time.Duration // dial backoff target (default 10s)
	KeepAliveTime time.Duration // ping every (default 30s)
	UserAgent     string        // gRPC User-Agent (default "kacho-vpc")
}

// defaultBuildOptions — дефолты для kacho-vpc cross-service вызовов
// (retries=3, dial 10s, keepalive 30s). Подбираются под profile peer-сервисов
// (Project.Get / Zone.Get — short calls, цена retry мала; idle longer для
// низкочастотных кешированных путей).
const (
	defaultRetries       = 3
	defaultDialTimeout   = 10 * time.Second
	defaultKeepAliveTime = 30 * time.Second
	defaultUserAgent     = "kacho-vpc"
)

// defaultPeerCallTimeout — per-call deadline на КАЖДЫЙ исходящий peer-gRPC вызов
// cross-service peer-клиентов (geo Zone/Region Get, iam Project Exists). Эти
// вызовы идут в том числе из async Operation-worker'а, чей ctx лишён дедлайна
// (operations baggage.Extract снимает deadline/cancel) и ограничен только грубым
// opTimeout; без собственного per-call дедлайна alive-but-unresponsive peer
// (deadlocked handler / GC-pause / slow query — gRPC keepalive не срабатывает,
// пока stream активен) вешает worker-горутину надолго → исчерпание LRO-слотов
// (DoS-амплификация). Зеркалит sibling SyncRegistrar (5s). См. architecture.md
// «per-call deadline на КАЖДОМ внешнем вызове».
const defaultPeerCallTimeout = 5 * time.Second

// peerCallCtx оборачивает ctx per-call дедлайном (если timeout>0), иначе возвращает
// ctx как есть. Применяется единообразно ко ВСЕМ sibling-методам peer-клиентов —
// не «часть — да, часть — нет». Возвращаемый cancel обязателен к вызову (defer).
func peerCallCtx(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, timeout)
}

func (o BuildOptions) withDefaults() BuildOptions {
	if o.Retries == 0 {
		o.Retries = defaultRetries
	}
	if o.DialTimeout == 0 {
		o.DialTimeout = defaultDialTimeout
	}
	if o.KeepAliveTime == 0 {
		o.KeepAliveTime = defaultKeepAliveTime
	}
	if o.UserAgent == "" {
		o.UserAgent = defaultUserAgent
	}
	return o
}

// Build открывает gRPC-клиентское соединение по BuildOptions.
//
// Поведение по флагам:
//   - DNSLB=false (default): corlib builder с retries / dialDuration / keepalive
//     / TLS / userAgent. Стандартный паттерн для cross-service.
//   - DNSLB=true: `grpc.NewClient` с dns:///prefix, `loadBalancingConfig:
//     round_robin` + retry/dial-backoff-дефолты вручную (corlib builder
//     serviceConfig не поддерживает; параметры зеркалят corlib-путь).
//
// Возвращает `Conn` — interface с grpc.ClientConnInterface + io.Closer.
// Подходит для передачи в generated `xxxv1.NewXxxServiceClient(conn)`.
func Build(ctx context.Context, opts BuildOptions) (Conn, error) {
	if strings.TrimSpace(opts.Endpoint) == "" {
		return nil, fmt.Errorf("clients.Build: empty Endpoint")
	}
	opts = opts.withDefaults()

	creds := buildCreds(opts.TLS)

	if opts.DNSLB {
		return buildDNSLBConn(opts, creds)
	}
	return buildCorlibConn(ctx, opts, creds)
}

// buildCreds — единый source-of-truth TLS / insecure для всех cross-service
// клиентов; TLS MinVersion=1.2 верифицирует server-сертификат по системному
// trust store (production-strict mode требует TLS, см. validateAuthMode).
func buildCreds(useTLS bool) credentials.TransportCredentials {
	if useTLS {
		return credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12})
	}
	return insecure.NewCredentials()
}

// buildCorlibConn — default path. corlib `ClientFromAddress` собирает dial-options
// (retries on Unavailable, backoff из dialDuration, keepalive params, user-agent,
// hostname-propagator interceptor).
func buildCorlibConn(ctx context.Context, opts BuildOptions, creds credentials.TransportCredentials) (Conn, error) {
	cc, err := corlibgrpc.ClientFromAddress(opts.Endpoint).
		WithCreds(creds).
		WithDialDuration(opts.DialTimeout).
		WithMaxRetries(opts.Retries).
		WithUserAgent(opts.UserAgent).
		WithKeepAlive(keepalive.ClientParameters{
			Time:                opts.KeepAliveTime,
			Timeout:             opts.KeepAliveTime / 3, // ack within 1/3 of ping interval
			PermitWithoutStream: false,
		}).
		New(ctx)
	if err != nil {
		return nil, fmt.Errorf("clients.Build: corlib dial %q: %w", opts.Endpoint, err)
	}
	return cc, nil
}

// dnslbServiceConfigJSON — service-config JSON для DNSLB-пути: client-side
// round_robin LB + retry-on-Unavailable policy, зеркаля corlib WithMaxRetries.
//   - round_robin: с dns:///prefix grpc резолвит все A/AAAA Headless Service и
//     распределяет RPC между ними (без этого — pick_first, 1 backend per addr).
//   - retryPolicy: maxAttempts = retries+1 (service-config считает исходную
//     попытку), retryableStatusCodes = UNAVAILABLE (тот же код-сет, что corlib
//     grpc_retry.WithCodes(codes.Unavailable)). Backoff — короткая экспонента:
//     service-config требует положительный backoff, поэтому в отличие от
//     corlib-immediate-retry между попытками вставляется небольшая задержка.
func dnslbServiceConfigJSON(retries uint) string {
	maxAttempts := retries + 1
	return fmt.Sprintf(`{"loadBalancingConfig":[{"round_robin":{}}],`+
		`"methodConfig":[{"name":[{}],"retryPolicy":{`+
		`"maxAttempts":%d,"initialBackoff":"0.1s","maxBackoff":"1s",`+
		`"backoffMultiplier":2.0,"retryableStatusCodes":["UNAVAILABLE"]}}]}`, maxAttempts)
}

// dnslbConnectParams — dial-backoff для DNSLB-пути, зеркалит corlib
// WithDialDuration (grpcBackoff.DefaultConfig с BaseDelay=d/10, Multiplier=1.01,
// Jitter=0.1, MaxDelay=d, MinConnectTimeout=d/10; d поднимается до >=1s).
func dnslbConnectParams(dialTimeout time.Duration) grpc.ConnectParams {
	d := dialTimeout
	if d < time.Second {
		d = time.Second
	}
	bk := grpcbackoff.DefaultConfig
	bk.BaseDelay = d / 10
	bk.Multiplier = 1.01
	bk.Jitter = 0.1
	bk.MaxDelay = d
	return grpc.ConnectParams{Backoff: bk, MinConnectTimeout: d / 10}
}

// buildDNSLBConn — путь DNSLB. corlib builder не экспонирует
// `grpc.WithDefaultServiceConfig`, поэтому здесь собираем те же defaults через
// прямой `grpc.NewClient`: retry (service-config retryPolicy) + dial-backoff
// (`WithConnectParams`) + keepalive + creds + userAgent — паритет с corlib-путём.
//
// Префикс `dns:///` добавляется автоматически (если addr им не начинается) —
// gRPC dns resolver требует его для multi-IP резолва Headless Service.
func buildDNSLBConn(opts BuildOptions, creds credentials.TransportCredentials) (Conn, error) {
	addr := opts.Endpoint
	if !strings.HasPrefix(addr, "dns:///") {
		addr = "dns:///" + addr
	}
	dialOpts := []grpc.DialOption{
		grpc.WithTransportCredentials(creds),
		grpc.WithUserAgent(opts.UserAgent),
		grpc.WithDefaultServiceConfig(dnslbServiceConfigJSON(opts.Retries)),
		grpc.WithConnectParams(dnslbConnectParams(opts.DialTimeout)),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                opts.KeepAliveTime,
			Timeout:             opts.KeepAliveTime / 3,
			PermitWithoutStream: false,
		}),
	}
	cc, err := grpc.NewClient(addr, dialOpts...)
	if err != nil {
		return nil, fmt.Errorf("clients.Build: grpc.NewClient %q (DNSLB): %w", addr, err)
	}
	return cc, nil
}
